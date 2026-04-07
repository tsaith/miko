package assistant

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"miko/internal/audio"
	"miko/internal/avatar"
	"miko/internal/backendclient"
	"miko/internal/config"
	"miko/internal/debuglog"
)

type EventEmitter func(event string, payload any)

type Service struct {
	settings   config.Settings
	config     config.Config
	configPath string
	emit       EventEmitter

	backend *backendclient.Manager
	audio   *audio.Service
	motion  *avatar.MotionController
	logger  *debuglog.Logger

	mu                sync.Mutex
	sessionCtx        context.Context
	sessionCancel     context.CancelFunc
	sessionID         string
	sendQueue         chan []byte
	isListening       bool
	isProcessing      bool
	assistantSpeaking bool
	lastSpeechState   bool
	pendingTTS        []byte
	closed            bool

	rootCtx    context.Context
	rootCancel context.CancelFunc
}

func NewService(settings config.Settings, cfg config.Config, cfgPath string, backend *backendclient.Manager, emit EventEmitter, logger *debuglog.Logger) (*Service, error) {
	audioService, err := audio.NewService(logger)
	if err != nil {
		return nil, err
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	service := &Service{
		settings:   settings,
		config:     cfg,
		configPath: cfgPath,
		backend:    backend,
		emit:       emit,
		logger:     logger,
		audio:      audioService,
		motion:     avatar.NewMotionController(),
		rootCtx:    rootCtx,
		rootCancel: rootCancel,
	}

	logger.Infof("assistant", "service initialized config=%s", cfgPath)
	go service.motionLoop(rootCtx)
	return service, nil
}

func (s *Service) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	cancel := s.sessionCancel
	sessionID := s.sessionID
	s.sessionCancel = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	s.logger.Info("assistant", "service closing")
	if s.backend != nil {
		_ = s.backend.StopConversation(sessionID)
	}
	s.audio.StopPlayback()
	s.audio.StopCapture()
	s.audio.Close()
	s.rootCancel()
}

func (s *Service) BootstrapState() config.RuntimeState {
	return config.RuntimeState{
		AppName:      s.settings.AppName,
		ConfigPath:   s.configPath,
		LogPath:      loggerPath(s.logger),
		LogDirectory: loggerDir(s.logger),
		Config:       s.config,
		Providers: config.ProviderStatus{
			OpenAIConfigured:   s.config.APIKeys.OpenAI != "",
			DeepgramConfigured: s.config.APIKeys.Deepgram != "",
			CartesiaConfigured: s.config.APIKeys.Cartesia != "",
		},
		PlatformNote: "支援 macOS 與 Linux ARM64，音訊 I/O 依賴系統原生裝置。",
	}
}

func (s *Service) StartListening() error {
	if err := s.ensureConfigured(); err != nil {
		return err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("assistant is closed")
	}
	if s.isListening {
		s.mu.Unlock()
		return nil
	}

	sessionCtx, sessionCancel := context.WithCancel(s.rootCtx)
	sessionID := newSessionID()
	s.sessionCtx = sessionCtx
	s.sessionCancel = sessionCancel
	s.sessionID = sessionID
	s.sendQueue = make(chan []byte, 64)
	s.isListening = true
	s.isProcessing = false
	s.assistantSpeaking = false
	s.lastSpeechState = false
	s.pendingTTS = nil
	s.motion.Reset()
	s.mu.Unlock()
	s.logger.SessionInfof(sessionID, "assistant", "start listening")

	if s.backend == nil || !s.backend.Status().Running {
		s.resetSession()
		return errors.New("python backend sidecar is not ready")
	}
	if err := s.backend.StartConversation(sessionCtx, sessionID, backendclient.ConversationCallbacks{
		OnTranscript:   s.handleTranscriptEvent,
		OnChat:         s.handleChatEvent,
		OnTTSLifecycle: s.handleTTSLifecycle,
		OnTTSChunk:     s.handleTTSChunk,
		OnError:        s.handleConversationError,
	}); err != nil {
		s.logger.SessionErrorf(sessionID, "assistant", "backend conversation start failed: %v", err)
		s.resetSession()
		return err
	}

	go s.sendLoop(sessionCtx, s.sendQueue)
	if err := s.audio.StartCapture(s.handleCaptureChunk); err != nil {
		_ = s.backend.StopConversation(sessionID)
		s.resetSession()
		return err
	}

	s.emit("listening_state", map[string]bool{"is_listening": true})
	s.emit("vad_status", map[string]bool{"is_speech": false})
	return nil
}

func (s *Service) StopListening() error {
	s.mu.Lock()
	if !s.isListening {
		s.mu.Unlock()
		return nil
	}
	cancel := s.sessionCancel
	s.mu.Unlock()
	sessionID := s.currentSessionID()

	if cancel != nil {
		cancel()
	}
	s.logger.SessionInfof(sessionID, "assistant", "stop listening")
	s.audio.StopPlayback()
	s.audio.StopCapture()
	if s.backend != nil {
		_ = s.backend.StopConversation(sessionID)
	}
	s.motion.SetAudioLevel(0)
	s.resetSession()

	s.emit("listening_state", map[string]bool{"is_listening": false})
	s.emit("vad_status", map[string]bool{"is_speech": false})
	s.emit("chat_status", map[string]string{"status": "idle"})
	s.emit("tts_end", map[string]any{})
	return nil
}

func (s *Service) handleCaptureChunk(chunk []byte) {
	isSpeech := audio.SpeechActive(chunk, 0.02)
	shouldSend := false

	s.mu.Lock()
	if s.isListening && s.lastSpeechState != isSpeech {
		s.lastSpeechState = isSpeech
		go s.emit("vad_status", map[string]bool{"is_speech": isSpeech})
	}
	if s.isListening && !s.assistantSpeaking && !s.isProcessing && s.sendQueue != nil {
		shouldSend = true
	}
	sendQueue := s.sendQueue
	s.mu.Unlock()

	if !shouldSend {
		return
	}

	select {
	case sendQueue <- chunk:
	default:
	}
}

func (s *Service) sendLoop(ctx context.Context, sendQueue <-chan []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-sendQueue:
			if !ok {
				return
			}
			if s.backend != nil {
				if err := s.backend.SendAudio(s.currentSessionID(), chunk); err != nil && ctx.Err() == nil {
					s.logger.SessionWarnf(s.currentSessionID(), "assistant", "send audio to backend failed: %v", err)
				}
			}
		}
	}
}

func (s *Service) handleTranscriptEvent(kind string, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if kind == "final" {
		s.setProcessing(true)
	}
}

func (s *Service) handleChatEvent(role string, content string, status string) {
	content = strings.TrimSpace(content)
	if role != "" && content != "" {
		s.emit("chat_update", map[string]string{
			"role":    role,
			"content": content,
		})
	}
	if status != "" {
		if status == "thinking" {
			s.setProcessing(true)
		}
		if status == "idle" && !s.isCurrentlySpeaking() {
			s.setProcessing(false)
		}
		s.emit("chat_status", map[string]string{"status": status})
	}
}

func (s *Service) handleTTSLifecycle(state string) {
	switch strings.TrimSpace(state) {
	case "start":
		s.mu.Lock()
		s.pendingTTS = nil
		s.assistantSpeaking = true
		s.mu.Unlock()
		s.emit("tts_start", map[string]any{})
	case "end":
		audioData := s.consumePendingTTS()
		go s.playBackendAudio(audioData)
	}
}

func (s *Service) handleTTSChunk(pcm []byte, _ uint32, _ uint32) {
	if len(pcm) == 0 {
		return
	}
	s.mu.Lock()
	s.pendingTTS = append(s.pendingTTS, pcm...)
	s.mu.Unlock()
}

func (s *Service) playBackendAudio(pcm []byte) {
	sessionID := s.currentSessionID()
	if len(pcm) == 0 {
		s.finishPlayback(nil)
		return
	}

	err := s.audio.PlayPCM(sessionID, pcm, func(level float64) {
		s.motion.SetAudioLevel(level)
	})
	s.finishPlayback(err)
}

func (s *Service) finishPlayback(playErr error) {
	s.motion.SetAudioLevel(0)
	s.setAssistantSpeaking(false)
	s.setProcessing(false)
	s.emit("tts_end", map[string]any{})
	s.emit("chat_status", map[string]string{"status": "idle"})

	if playErr != nil && !errors.Is(playErr, context.Canceled) {
		s.emitError(fmt.Sprintf("語音播放失敗：%v", playErr))
	}
}

func (s *Service) consumePendingTTS() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	audioData := append([]byte(nil), s.pendingTTS...)
	s.pendingTTS = nil
	return audioData
}

func (s *Service) motionLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second / 30)
	defer ticker.Stop()

	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			delta := now.Sub(last).Seconds()
			if delta > 0.1 {
				delta = 0.1
			}
			last = now
			s.emit("avatar_motion", s.motion.Tick(delta))
		}
	}
}

func (s *Service) ensureConfigured() error {
	var missing []string
	if strings.TrimSpace(s.config.APIKeys.OpenAI) == "" {
		missing = append(missing, "api_keys.openai")
	}
	if strings.TrimSpace(s.config.APIKeys.Deepgram) == "" {
		missing = append(missing, "api_keys.deepgram")
	}
	if strings.TrimSpace(s.config.APIKeys.Cartesia) == "" {
		missing = append(missing, "api_keys.cartesia")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("missing required fields in ~/.miko/config.yaml: %s", strings.Join(missing, ", "))
}

func (s *Service) setProcessing(value bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.isProcessing = value
}

func (s *Service) setAssistantSpeaking(value bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.assistantSpeaking = value
}

func (s *Service) isCurrentlySpeaking() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.assistantSpeaking
}

func (s *Service) currentSessionContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionCtx
}

func (s *Service) currentSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

func (s *Service) resetSession() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sendQueue = nil
	s.sessionCtx = nil
	s.sessionCancel = nil
	s.sessionID = ""
	s.isListening = false
	s.isProcessing = false
	s.assistantSpeaking = false
	s.lastSpeechState = false
	s.pendingTTS = nil
}

func (s *Service) emitError(message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	sessionID := s.currentSessionID()
	s.logger.SessionErrorf(sessionID, "assistant", "%s", message)
	s.emit("app_error", map[string]string{"message": message})
}

func (s *Service) handleConversationError(err error) {
	if err == nil {
		return
	}
	if ctx := s.currentSessionContext(); ctx != nil && ctx.Err() != nil {
		return
	}
	s.setProcessing(false)
	s.setAssistantSpeaking(false)
	s.emit("chat_status", map[string]string{"status": "idle"})
	s.emitError(fmt.Sprintf("語音服務中斷：%v", err))
}

func (s *Service) SetFaceTarget(x float64, y float64, present bool) {
	s.motion.SetFaceTarget(x, y, present)
}

func newSessionID() string {
	return fmt.Sprintf("%08x", rand.Uint32())
}

func loggerPath(logger *debuglog.Logger) string {
	if logger == nil {
		return ""
	}
	return logger.Path()
}

func loggerDir(logger *debuglog.Logger) string {
	if logger == nil {
		return ""
	}
	return logger.Directory()
}
