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
	"miko/internal/config"
	"miko/internal/debuglog"
	"miko/internal/services/brain"
	"miko/internal/services/llm"
	"miko/internal/services/stt"
	"miko/internal/services/tts"
)

type EventEmitter func(event string, payload any)

type Service struct {
	settings   config.Settings
	config     config.Config
	configPath string
	emit       EventEmitter

	audio  *audio.Service
	stt    *stt.DeepgramService
	brain  *brain.Service
	tts    *tts.CartesiaService
	motion *avatar.MotionController
	logger *debuglog.Logger

	mu                sync.Mutex
	sessionCtx        context.Context
	sessionCancel     context.CancelFunc
	sessionID         string
	sendQueue         chan []byte
	isListening       bool
	isProcessing      bool
	assistantSpeaking bool
	lastSpeechState   bool
	reconnectingSTT   bool
	closed            bool

	rootCtx    context.Context
	rootCancel context.CancelFunc
}

func NewService(settings config.Settings, cfg config.Config, cfgPath string, emit EventEmitter, logger *debuglog.Logger) (*Service, error) {
	audioService, err := audio.NewService(logger)
	if err != nil {
		return nil, err
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	service := &Service{
		settings:   settings,
		config:     cfg,
		configPath: cfgPath,
		emit:       emit,
		logger:     logger,
		audio:      audioService,
		stt:        stt.NewDeepgramService(cfg.APIKeys.Deepgram, cfg.STT.Deepgram, logger),
		brain:      brain.NewService(llm.NewOpenAIService(cfg.APIKeys.OpenAI, cfg.LLM.OpenAI, logger), logger),
		tts:        tts.NewCartesiaService(cfg.APIKeys.Cartesia, cfg.TTS.Cartesia, logger),
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
	s.sessionCancel = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	s.logger.Info("assistant", "service closing")
	s.stt.Finish()
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
	s.motion.Reset()
	s.brain.Reset()
	s.mu.Unlock()
	s.logger.SessionInfof(sessionID, "assistant", "start listening")

	if err := s.connectSTT(sessionCtx, sessionID); err != nil {
		s.logger.SessionErrorf(sessionID, "assistant", "stt connect failed: %v", err)
		s.resetSession()
		return err
	}

	go s.sendLoop(sessionCtx, s.sendQueue)
	if err := s.audio.StartCapture(s.handleCaptureChunk); err != nil {
		s.stt.Finish()
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
	s.stt.Finish()
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
			_ = s.stt.SendAudio(s.currentSessionID(), chunk)
		}
	}
}

func (s *Service) handleUtterance(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	if !s.beginProcessing() {
		return
	}
	defer s.endProcessing()

	ctx := s.currentSessionContext()
	sessionID := s.currentSessionID()
	if ctx == nil {
		return
	}
	s.logger.SessionInfof(sessionID, "assistant", "utterance received chars=%d", len([]rune(text)))

	s.emit("chat_update", map[string]string{
		"role":    "user",
		"content": text,
	})
	s.emit("chat_status", map[string]string{"status": "thinking"})

	reply, err := s.brain.ProcessMessage(ctx, sessionID, text)
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			s.emit("chat_status", map[string]string{"status": "idle"})
			return
		}
		s.emit("chat_update", map[string]string{
			"role":    "assistant",
			"content": "抱歉，我剛剛連不上雲端服務，請稍後再試。",
		})
		s.emit("chat_status", map[string]string{"status": "idle"})
		return
	}

	s.emit("chat_update", map[string]string{
		"role":    "assistant",
		"content": reply,
	})

	audioData, err := s.tts.Synthesize(ctx, sessionID, reply)
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			s.emit("chat_status", map[string]string{"status": "idle"})
			return
		}
		s.emitError(fmt.Sprintf("TTS 生成失敗：%v", err))
		s.emit("chat_status", map[string]string{"status": "idle"})
		return
	}

	s.setAssistantSpeaking(true)
	s.emit("tts_start", map[string]any{})
	playErr := s.audio.PlayPCM(sessionID, audioData, func(level float64) {
		s.motion.SetAudioLevel(level)
	})
	s.motion.SetAudioLevel(0)
	s.emit("tts_end", map[string]any{})
	s.setAssistantSpeaking(false)
	if playErr != nil && !errors.Is(playErr, context.Canceled) && ctx.Err() == nil {
		s.emitError(fmt.Sprintf("語音播放失敗：%v", playErr))
	}
	s.emit("chat_status", map[string]string{"status": "idle"})
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

func (s *Service) beginProcessing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.isListening || s.isProcessing || s.assistantSpeaking {
		return false
	}
	s.isProcessing = true
	return true
}

func (s *Service) endProcessing() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.isProcessing = false
}

func (s *Service) setAssistantSpeaking(value bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.assistantSpeaking = value
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
	s.reconnectingSTT = false
}

func (s *Service) emitError(message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	sessionID := s.currentSessionID()
	s.logger.SessionErrorf(sessionID, "assistant", "%s", message)
	s.emit("app_error", map[string]string{"message": message})
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

func (s *Service) connectSTT(ctx context.Context, sessionID string) error {
	return s.stt.Connect(ctx, sessionID, func(text string) {
		go s.handleUtterance(text)
	}, func(err error) {
		go s.handleSTTDisconnect(sessionID, err)
	})
}

func (s *Service) handleSTTDisconnect(sessionID string, cause error) {
	if cause == nil {
		return
	}

	s.mu.Lock()
	if s.closed || !s.isListening || s.sessionID != sessionID || s.sessionCtx == nil || s.sessionCtx.Err() != nil {
		s.mu.Unlock()
		return
	}
	if s.reconnectingSTT {
		s.mu.Unlock()
		return
	}
	s.reconnectingSTT = true
	ctx := s.sessionCtx
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		if s.sessionID == sessionID {
			s.reconnectingSTT = false
		}
		s.mu.Unlock()
	}()

	s.logger.SessionWarnf(sessionID, "assistant", "stt disconnected, reconnecting: %v", cause)
	for attempt := 1; attempt <= 3; attempt++ {
		if ctx.Err() != nil {
			return
		}
		if attempt > 1 {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
		if err := s.connectSTT(ctx, sessionID); err == nil {
			s.logger.SessionInfof(sessionID, "assistant", "stt reconnected attempt=%d", attempt)
			return
		} else {
			s.logger.SessionWarnf(sessionID, "assistant", "stt reconnect failed attempt=%d err=%v", attempt, err)
		}
	}

	s.emitError("語音辨識連線中斷，請停止交談後重新開始。")
}
