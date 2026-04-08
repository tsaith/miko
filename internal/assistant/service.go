package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"miko/internal/audio"
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
	logger  *debuglog.Logger

	mu                  sync.Mutex
	sessionCtx          context.Context
	sessionCancel       context.CancelFunc
	sessionID           string
	sendQueue           chan []byte
	isListening         bool
	isProcessing        bool
	assistantSpeaking   bool
	lastSpeechState     bool
	pendingTTS          []byte
	playbackInterrupted bool
	interruptStreaming  bool
	bargeInCandidate    bool
	bargeInConfirming   bool
	micHistory          []byte
	speechBytes         int
	playbackLevel       float64
	closed              bool

	rootCtx    context.Context
	rootCancel context.CancelFunc
}

const (
	bargeInPlaybackRatioGate = 1.35
	bargeInMinOverPlayback   = 0.03
)

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
		rootCtx:    rootCtx,
		rootCancel: rootCancel,
	}

	logger.Infof("assistant", "service initialized config=%s", cfgPath)
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
	s.playbackInterrupted = false
	s.interruptStreaming = false
	s.bargeInCandidate = false
	s.bargeInConfirming = false
	s.micHistory = nil
	s.speechBytes = 0
	s.playbackLevel = 0
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
	s.mu.Lock()
	s.playbackInterrupted = true
	s.interruptStreaming = false
	s.bargeInCandidate = false
	s.bargeInConfirming = false
	s.mu.Unlock()
	s.audio.StopPlayback()
	s.audio.StopCapture()
	if s.backend != nil {
		_ = s.backend.StopConversation(sessionID)
	}
	s.resetSession()

	s.emit("listening_state", map[string]bool{"is_listening": false})
	s.emit("vad_status", map[string]bool{"is_speech": false})
	s.emit("chat_status", map[string]string{"status": "idle"})
	s.emit("tts_end", map[string]any{})
	return nil
}

func (s *Service) handleCaptureChunk(chunk []byte) {
	micLevel := audio.NormalizedRMS(chunk)
	isSpeech := micLevel >= 0.02
	shouldSend := false
	var preRoll []byte
	var triggerInterrupt bool
	var candidateActivated bool
	var speechMs int
	var confirmRequested bool
	var confirmPayload []byte

	s.mu.Lock()
	if s.isListening {
		s.micHistory = appendBounded(s.micHistory, chunk, s.bargeInPreRollBytes())
	}
	if s.isListening && s.lastSpeechState != isSpeech {
		s.lastSpeechState = isSpeech
		go s.emit("vad_status", map[string]bool{"is_speech": isSpeech})
	}
	if s.isListening && s.assistantSpeaking && s.sendQueue != nil {
		if s.config.BargeIn.Enabled {
			var confirmed bool
			confirmed, candidateActivated, confirmRequested, speechMs = s.updateBargeInStateLocked(len(chunk), micLevel, isSpeech)
			if confirmRequested {
				confirmPayload = append([]byte(nil), s.micHistory...)
			}
			if confirmed {
				s.interruptStreaming = true
				s.playbackInterrupted = true
				s.bargeInCandidate = false
				s.bargeInConfirming = false
				s.speechBytes = 0
				preRoll = append([]byte(nil), s.micHistory...)
				triggerInterrupt = true
				shouldSend = false
			} else if s.interruptStreaming {
				shouldSend = true
			}
		} else {
			s.resetBargeInStateLocked()
		}
	} else {
		s.resetBargeInStateLocked()
		if s.isListening && ((!s.assistantSpeaking && !s.isProcessing) || s.interruptStreaming) && s.sendQueue != nil {
			shouldSend = true
		}
	}
	sendQueue := s.sendQueue
	sessionID := s.sessionID
	s.mu.Unlock()

	if candidateActivated {
		s.logger.SessionInfof(
			sessionID,
			"assistant",
			"barge-in candidate speech_ms=%d mic_level=%.3f playback_level=%.3f",
			speechMs,
			micLevel,
			s.currentPlaybackLevel(),
		)
	}

	if confirmRequested && len(confirmPayload) > 0 {
		go s.confirmBargeIn(sessionID, confirmPayload, sendQueue)
	}

	if triggerInterrupt {
		s.triggerBargeIn(sessionID, micLevel, speechMs, preRoll, sendQueue)
		return
	}

	if !shouldSend {
		return
	}

	s.queueAudioChunk(sessionID, sendQueue, chunk, "live")
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
		s.playbackInterrupted = false
		s.interruptStreaming = false
		s.bargeInCandidate = false
		s.bargeInConfirming = false
		s.speechBytes = 0
		s.playbackLevel = 0
		s.micHistory = nil
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

	if s.backend != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := s.backend.NotifyPlaybackState(ctx, sessionID, "started"); err != nil {
			s.logger.SessionWarnf(sessionID, "assistant", "notify playback start failed: %v", err)
		}
		cancel()
	}

	err := s.audio.PlayPCM(sessionID, pcm, func(level float64) {
		s.mu.Lock()
		s.playbackLevel = level
		s.mu.Unlock()
	})
	s.finishPlayback(err)
}

func (s *Service) finishPlayback(playErr error) {
	sessionID := s.currentSessionID()
	state := "ended"
	s.mu.Lock()
	if s.playbackInterrupted {
		state = "interrupted"
	}
	s.playbackInterrupted = false
	s.interruptStreaming = false
	s.bargeInCandidate = false
	s.bargeInConfirming = false
	s.playbackLevel = 0
	s.mu.Unlock()
	if s.backend != nil && sessionID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := s.backend.NotifyPlaybackState(ctx, sessionID, state); err != nil {
			s.logger.SessionWarnf(sessionID, "assistant", "notify playback %s failed: %v", state, err)
		}
		cancel()
	}
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
	s.playbackInterrupted = false
	s.interruptStreaming = false
	s.bargeInCandidate = false
	s.bargeInConfirming = false
	s.micHistory = nil
	s.speechBytes = 0
	s.playbackLevel = 0
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

func (s *Service) HandleAvatarMotion(raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		s.logger.Warnf("assistant", "avatar motion decode failed: %v", err)
		return
	}
	s.emit("avatar_motion", payload)
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

func (s *Service) updateBargeInStateLocked(chunkBytes int, micLevel float64, isSpeech bool) (confirmed bool, candidateActivated bool, confirmRequested bool, speechMs int) {
	if !isSpeech {
		s.resetBargeInStateLocked()
		return false, false, false, 0
	}

	levelThreshold := s.config.BargeIn.MinMicLevel
	if candidate := s.playbackLevel + s.config.BargeIn.PlaybackLevelPadding; candidate > levelThreshold {
		levelThreshold = candidate
	}
	if candidate := s.playbackLevel * bargeInPlaybackRatioGate; candidate > levelThreshold {
		levelThreshold = candidate
	}
	if candidate := s.playbackLevel + bargeInMinOverPlayback; candidate > levelThreshold {
		levelThreshold = candidate
	}
	if micLevel < levelThreshold {
		s.resetBargeInStateLocked()
		return false, false, false, 0
	}

	s.speechBytes += chunkBytes
	speechMs = bytesToMilliseconds(s.speechBytes)
	if !s.bargeInCandidate && speechMs >= s.config.BargeIn.CandidateSpeechMs {
		s.bargeInCandidate = true
		candidateActivated = true
	}
	if s.bargeInCandidate && !s.bargeInConfirming {
		s.bargeInConfirming = true
		confirmRequested = true
	}
	return false, candidateActivated, confirmRequested, speechMs
}

func (s *Service) triggerBargeIn(sessionID string, micLevel float64, speechMs int, preRoll []byte, sendQueue chan []byte) {
	s.logger.SessionInfof(
		sessionID,
		"assistant",
		"barge-in confirmed speech_ms=%d preroll_bytes=%d mic_level=%.3f",
		speechMs,
		len(preRoll),
		micLevel,
	)
	s.audio.StopPlayback()
	if len(preRoll) > 0 {
		s.queueAudioChunk(sessionID, sendQueue, preRoll, "barge-in-preroll")
	}
}

func (s *Service) queueAudioChunk(sessionID string, sendQueue chan []byte, chunk []byte, source string) {
	if len(chunk) == 0 || sendQueue == nil {
		return
	}
	select {
	case sendQueue <- chunk:
	default:
		s.logger.SessionWarnf(sessionID, "assistant", "audio chunk dropped source=%s bytes=%d", source, len(chunk))
	}
}

func (s *Service) resetBargeInStateLocked() {
	s.bargeInCandidate = false
	s.bargeInConfirming = false
	s.speechBytes = 0
}

func (s *Service) currentPlaybackLevel() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.playbackLevel
}

func (s *Service) confirmBargeIn(sessionID string, payload []byte, sendQueue chan []byte) {
	if s.backend == nil || sessionID == "" || len(payload) == 0 {
		s.clearBargeInConfirming()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	confirmed, speechMs, frames, err := s.backend.ConfirmBargeIn(ctx, sessionID, payload, s.config.BargeIn.ConfirmSpeechMs)
	cancel()
	if err != nil {
		s.logger.SessionWarnf(sessionID, "assistant", "barge-in confirm failed: %v", err)
		s.clearBargeInConfirming()
		return
	}
	if !confirmed {
		s.logger.SessionInfof(sessionID, "assistant", "barge-in rejected speech_ms=%d frames=%d", speechMs, frames)
		s.clearBargeInConfirming()
		return
	}

	s.mu.Lock()
	if !s.isListening || !s.assistantSpeaking || s.interruptStreaming {
		s.bargeInConfirming = false
		s.mu.Unlock()
		return
	}
	s.interruptStreaming = true
	s.playbackInterrupted = true
	s.bargeInCandidate = false
	s.bargeInConfirming = false
	s.speechBytes = 0
	preRoll := append([]byte(nil), s.micHistory...)
	micLevel := s.playbackLevel
	s.mu.Unlock()

	s.logger.SessionInfof(sessionID, "assistant", "barge-in Silero confirmed speech_ms=%d frames=%d", speechMs, frames)
	s.triggerBargeIn(sessionID, micLevel, speechMs, preRoll, sendQueue)
}

func (s *Service) clearBargeInConfirming() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bargeInConfirming = false
}

func appendBounded(dst []byte, chunk []byte, limit int) []byte {
	if limit <= 0 {
		return nil
	}
	if len(chunk) >= limit {
		return append([]byte(nil), chunk[len(chunk)-limit:]...)
	}
	next := append(dst, chunk...)
	if len(next) <= limit {
		return next
	}
	overflow := len(next) - limit
	trimmed := make([]byte, limit)
	copy(trimmed, next[overflow:])
	return trimmed
}

func (s *Service) bargeInPreRollBytes() int {
	ms := s.config.BargeIn.PreRollMs
	if ms <= 0 {
		ms = 450
	}
	return millisecondsToBytes(ms)
}

func bytesToMilliseconds(totalBytes int) int {
	if totalBytes <= 0 {
		return 0
	}
	return roundDiv(totalBytes*1000, audio.DefaultSampleRate*audio.DefaultChannels*2)
}

func millisecondsToBytes(ms int) int {
	if ms <= 0 {
		return 0
	}
	return roundDiv(ms*audio.DefaultSampleRate*audio.DefaultChannels*2, 1000)
}

func roundDiv(value int, divisor int) int {
	if divisor <= 0 {
		return 0
	}
	return (value + divisor/2) / divisor
}
