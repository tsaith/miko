package backendclient

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"miko/internal/audio"
	backendproto "miko/internal/backendclient/proto"
	"miko/internal/config"
	"miko/internal/debuglog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Manager struct {
	mu      sync.Mutex
	cfg     config.BackendConfig
	logger  *debuglog.Logger
	cmd     *exec.Cmd
	running bool
	mode    string
	target  string
	service string
	version string
	status  string
	lastErr string

	convMu        sync.Mutex
	convConn      *grpc.ClientConn
	convAudio     grpc.ClientStreamingClient[backendproto.AudioChunk, backendproto.Ack]
	convCancel    context.CancelFunc
	convSessionID string
}

type ConversationCallbacks struct {
	OnTranscript   func(kind string, text string)
	OnChat         func(role string, content string, status string)
	OnTTSLifecycle func(state string)
	OnTTSChunk     func(pcm []byte, sampleRate uint32, channels uint32)
	OnError        func(error)
}

func NewManager(cfg config.BackendConfig, logger *debuglog.Logger) *Manager {
	return &Manager{
		cfg:    cfg,
		logger: logger,
	}
}

func (m *Manager) Start(ctx context.Context) error {
	if !m.cfg.Enabled {
		return nil
	}

	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	command, err := m.buildCommand(ctx)
	if err != nil {
		m.setError(err)
		return err
	}

	stdout, err := command.StdoutPipe()
	if err != nil {
		m.setError(err)
		return fmt.Errorf("backend stdout pipe: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		m.setError(err)
		return fmt.Errorf("backend stderr pipe: %w", err)
	}

	if err := command.Start(); err != nil {
		m.setError(err)
		return fmt.Errorf("backend start: %w", err)
	}

	m.mu.Lock()
	m.cmd = command
	m.running = true
	m.lastErr = ""
	m.service = ""
	m.version = ""
	m.status = ""
	m.mu.Unlock()

	m.logger.Infof("backend", "sidecar started mode=%s pid=%d endpoint=%s", m.mode, command.Process.Pid, m.endpoint())
	go m.streamLogs("stdout", stdout)
	go m.streamLogs("stderr", stderr)
	go m.wait()

	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := m.WaitUntilReady(readyCtx); err != nil {
		m.setError(err)
		m.logger.Warnf("backend", "sidecar ping failed: %v", err)
		m.Close()
		return fmt.Errorf("backend ready check: %w", err)
	}

	return nil
}

func (m *Manager) Close() {
	_ = m.StopConversation("")

	m.mu.Lock()
	cmd := m.cmd
	m.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (m *Manager) StartConversation(ctx context.Context, sessionID string, callbacks ConversationCallbacks) error {
	conn, err := grpc.NewClient(
		m.endpoint(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("grpc dial: %w", err)
	}

	convClient := backendproto.NewConversationServiceClient(conn)
	if _, err := convClient.StartSession(ctx, &backendproto.StartSessionRequest{SessionId: sessionID}); err != nil {
		_ = conn.Close()
		return fmt.Errorf("start session: %w", err)
	}

	audioStream, err := convClient.StreamAudio(ctx)
	if err != nil {
		_, _ = convClient.StopSession(context.Background(), &backendproto.StopSessionRequest{SessionId: sessionID})
		_ = conn.Close()
		return fmt.Errorf("stream audio: %w", err)
	}

	eventStream, err := convClient.StreamEvents(ctx, &backendproto.StreamEventsRequest{SessionId: sessionID})
	if err != nil {
		_, _ = audioStream.CloseAndRecv()
		_, _ = convClient.StopSession(context.Background(), &backendproto.StopSessionRequest{SessionId: sessionID})
		_ = conn.Close()
		return fmt.Errorf("stream events: %w", err)
	}

	streamCtx, cancel := context.WithCancel(context.Background())

	m.convMu.Lock()
	m.convConn = conn
	m.convAudio = audioStream
	m.convCancel = cancel
	m.convSessionID = sessionID
	m.convMu.Unlock()

	go func() {
		defer func() {
			_ = m.StopConversation(sessionID)
		}()

		for {
			event, err := eventStream.Recv()
			if err != nil {
				if streamCtx.Err() != nil || ctx.Err() != nil {
					return
				}
				m.logger.Warnf("backend", "conversation stream closed session=%s err=%v", sessionID, err)
				if callbacks.OnError != nil {
					callbacks.OnError(fmt.Errorf("conversation stream closed: %w", err))
				}
				return
			}

			if transcript := event.GetTranscript(); transcript != nil {
				if callbacks.OnTranscript != nil {
					callbacks.OnTranscript(transcript.GetKind(), transcript.GetText())
				}
				continue
			}
			if chat := event.GetChat(); chat != nil {
				if callbacks.OnChat != nil {
					callbacks.OnChat(chat.GetRole(), chat.GetContent(), chat.GetStatus())
				}
				continue
			}
			if lifecycle := event.GetTtsLifecycle(); lifecycle != nil {
				if callbacks.OnTTSLifecycle != nil {
					callbacks.OnTTSLifecycle(lifecycle.GetState())
				}
				continue
			}
			if chunk := event.GetTtsChunk(); chunk != nil {
				if callbacks.OnTTSChunk != nil {
					callbacks.OnTTSChunk(chunk.GetPcmS16Le(), chunk.GetSampleRate(), chunk.GetChannels())
				}
				continue
			}
			if evtErr := event.GetError(); evtErr != nil {
				m.logger.Warnf("backend", "conversation event error session=%s message=%s", sessionID, evtErr.GetMessage())
				if callbacks.OnError != nil {
					callbacks.OnError(errors.New(evtErr.GetMessage()))
				}
			}
		}
	}()

	m.logger.Infof("backend", "conversation started session=%s", sessionID)
	return nil
}

func (m *Manager) SendAudio(sessionID string, chunk []byte) error {
	if len(chunk) == 0 {
		return nil
	}

	m.convMu.Lock()
	audioStream := m.convAudio
	activeSessionID := m.convSessionID
	m.convMu.Unlock()

	if audioStream == nil || activeSessionID == "" {
		return errors.New("conversation stream is not active")
	}
	if sessionID != activeSessionID {
		return fmt.Errorf("conversation session mismatch: want %s got %s", activeSessionID, sessionID)
	}

	return audioStream.Send(&backendproto.AudioChunk{
		SessionId:  sessionID,
		PcmS16Le:   chunk,
		SampleRate: uint32(audio.DefaultSampleRate),
		Channels:   uint32(audio.DefaultChannels),
	})
}

func (m *Manager) StopConversation(sessionID string) error {
	m.convMu.Lock()
	conn := m.convConn
	audioStream := m.convAudio
	cancel := m.convCancel
	activeSessionID := m.convSessionID
	if activeSessionID == "" || (sessionID != "" && sessionID != activeSessionID) {
		m.convMu.Unlock()
		return nil
	}
	m.convConn = nil
	m.convAudio = nil
	m.convCancel = nil
	m.convSessionID = ""
	m.convMu.Unlock()

	if cancel != nil {
		cancel()
	}

	var firstErr error
	if audioStream != nil {
		if _, err := audioStream.CloseAndRecv(); err != nil {
			firstErr = err
		}
	}
	if conn != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		convClient := backendproto.NewConversationServiceClient(conn)
		if _, err := convClient.StopSession(stopCtx, &backendproto.StopSessionRequest{SessionId: activeSessionID}); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if firstErr == nil {
		m.logger.Infof("backend", "conversation stopped session=%s", activeSessionID)
	}
	return firstErr
}

func (m *Manager) Status() config.BackendStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return config.BackendStatus{
		Enabled:   m.cfg.Enabled,
		Running:   m.running,
		Mode:      m.mode,
		Transport: "unix",
		Endpoint:  m.endpoint(),
		Service:   m.service,
		Version:   m.version,
		Status:    m.status,
		LastError: m.lastErr,
	}
}

func (m *Manager) endpoint() string {
	if m.target != "" {
		return m.target
	}
	return unixTarget(m.cfg.SocketPath)
}

func (m *Manager) WaitUntilReady(ctx context.Context) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		resp, err := m.Ping(ctx)
		if err == nil {
			m.mu.Lock()
			m.service = resp.GetService()
			m.version = resp.GetVersion()
			m.status = resp.GetStatus()
			m.lastErr = ""
			m.target = m.endpoint()
			m.mu.Unlock()
			m.logger.Infof("backend", "sidecar ready service=%s version=%s status=%s", resp.GetService(), resp.GetVersion(), resp.GetStatus())
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *Manager) Ping(ctx context.Context) (*backendproto.PingResponse, error) {
	conn, err := grpc.NewClient(
		m.endpoint(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("grpc dial: %w", err)
	}
	defer conn.Close()

	client := backendproto.NewHealthServiceClient(conn)
	resp, err := client.Ping(ctx, &backendproto.PingRequest{})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (m *Manager) StartVisionStream(ctx context.Context, onFace func(x float64, y float64, present bool)) error {
	conn, err := grpc.NewClient(
		m.endpoint(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("grpc dial: %w", err)
	}

	visionClient := backendproto.NewVisionServiceClient(conn)
	convClient := backendproto.NewConversationServiceClient(conn)

	ack, err := visionClient.StartVision(ctx, &backendproto.StartVisionRequest{})
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("start vision: %w", err)
	}
	if !ack.GetOk() {
		_ = conn.Close()
		return fmt.Errorf("start vision rejected: %s", ack.GetMessage())
	}

	stream, err := convClient.StreamEvents(ctx, &backendproto.StreamEventsRequest{})
	if err != nil {
		_, _ = visionClient.StopVision(context.Background(), &backendproto.StopVisionRequest{})
		_ = conn.Close()
		return fmt.Errorf("stream events: %w", err)
	}

	go func() {
		defer conn.Close()
		defer func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = visionClient.StopVision(stopCtx, &backendproto.StopVisionRequest{})
		}()

		for {
			event, err := stream.Recv()
			if err != nil {
				m.logger.Warnf("backend", "vision stream closed: %v", err)
				onFace(0, 0, false)
				return
			}

			face := event.GetFaceTarget()
			if face == nil {
				continue
			}
			onFace(float64(face.GetX()), float64(face.GetY()), face.GetPresent())
		}
	}()

	m.logger.Info("backend", "vision stream started")
	return nil
}

func (m *Manager) wait() {
	m.mu.Lock()
	cmd := m.cmd
	m.mu.Unlock()
	if cmd == nil {
		return
	}

	err := cmd.Wait()

	m.mu.Lock()
	if m.cmd == cmd {
		m.cmd = nil
	}
	m.running = false
	if err != nil {
		m.lastErr = err.Error()
	}
	m.target = ""
	m.service = ""
	m.version = ""
	m.status = ""
	m.mu.Unlock()

	if err != nil {
		m.logger.Warnf("backend", "sidecar exited: %v", err)
		return
	}
	m.logger.Info("backend", "sidecar exited")
}

func (m *Manager) streamLogs(stream string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		m.logger.Infof("backend."+stream, "%s", line)
	}
	if err := scanner.Err(); err != nil {
		m.logger.Warnf("backend."+stream, "log stream closed: %v", err)
	}
}

func (m *Manager) setError(err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastErr = err.Error()
}

func (m *Manager) buildCommand(ctx context.Context) (*exec.Cmd, error) {
	backendDir, err := resolveBackendDir()
	if err != nil {
		return nil, err
	}

	if err := m.prepareTransport(); err != nil {
		return nil, err
	}

	mode := m.cfg.LaunchMode
	if mode == "" {
		mode = "auto"
	}

	switch mode {
	case "binary":
		path, err := resolveBackendBinary(backendDir)
		if err != nil {
			return nil, err
		}
		m.mode = "binary"
		cmd := exec.CommandContext(ctx, path)
		cmd.Env = append(os.Environ(), backendEnv(m.cfg)...)
		return cmd, nil
	case "uv":
		return m.uvCommand(ctx, backendDir)
	case "auto":
		if path, err := resolveBackendBinary(backendDir); err == nil {
			m.mode = "binary"
			cmd := exec.CommandContext(ctx, path)
			cmd.Env = append(os.Environ(), backendEnv(m.cfg)...)
			return cmd, nil
		}
		return m.uvCommand(ctx, backendDir)
	default:
		return nil, fmt.Errorf("unsupported backend launch mode %q", mode)
	}
}

func (m *Manager) uvCommand(ctx context.Context, backendDir string) (*exec.Cmd, error) {
	uvPath, err := exec.LookPath("uv")
	if err != nil {
		return nil, errors.New("cannot find uv in PATH")
	}
	m.mode = "uv"
	cmd := exec.CommandContext(ctx, uvPath, "run", "python", "-m", m.cfg.PythonModule)
	cmd.Dir = backendDir
	cmd.Env = append(os.Environ(), backendEnv(m.cfg)...)
	return cmd, nil
}

func backendEnv(cfg config.BackendConfig) []string {
	return []string{
		fmt.Sprintf("MIKO_BACKEND_SOCKET=%s", cfg.SocketPath),
		fmt.Sprintf("MIKO_CONFIG_PATH=%s", configPathForEnv()),
		fmt.Sprintf("MIKO_WORKSPACE_DIR=%s", workspaceDirForEnv()),
		fmt.Sprintf("MIKO_MODELS_DIR=%s", modelsDirForEnv()),
		"PYTHONUNBUFFERED=1",
	}
}

func (m *Manager) prepareTransport() error {
	return m.ensureSocketPath()
}

func configPathForEnv() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".miko", "config.yaml")
}

func workspaceDirForEnv() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".miko", "workspace")
}

func modelsDirForEnv() string {
	if cwd, err := os.Getwd(); err == nil {
		path := filepath.Join(cwd, "models")
		if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
			return path
		}
	}
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates := []string{
			filepath.Join(exeDir, "models"),
			filepath.Join(exeDir, "..", "models"),
			filepath.Join(exeDir, "..", "Resources", "models"),
			filepath.Join(exeDir, "..", "..", "Resources", "models"),
		}
		for _, candidate := range candidates {
			info, statErr := os.Stat(candidate)
			if statErr == nil && info.IsDir() {
				return candidate
			}
		}
	}
	return ""
}

func (m *Manager) ensureSocketPath() error {
	path := strings.TrimSpace(m.cfg.SocketPath)
	if path == "" {
		path = filepath.Join(workspaceDirForEnv(), "backend", "miko-backend.sock")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve backend socket path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return fmt.Errorf("create backend socket dir: %w", err)
	}
	if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale backend socket: %w", err)
	}
	m.cfg.SocketPath = absPath
	return nil
}

func unixTarget(socketPath string) string {
	return "unix://" + socketPath
}

func resolveBackendDir() (string, error) {
	var candidates []string
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "backend"))
	}
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			filepath.Join(exeDir, "backend"),
			filepath.Join(exeDir, "..", "backend"),
			filepath.Join(exeDir, "..", "Resources", "backend"),
			filepath.Join(exeDir, "..", "..", "Resources", "backend"),
		)
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return filepath.Abs(candidate)
		}
	}
	return "", errors.New("cannot locate backend directory")
}

func resolveBackendBinary(backendDir string) (string, error) {
	candidates := []string{
		filepath.Join(backendDir, "bin", "miko-backend"),
		filepath.Join(backendDir, "dist", "miko-backend"),
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("cannot locate backend binary")
}
