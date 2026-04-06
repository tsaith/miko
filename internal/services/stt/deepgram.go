package stt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"miko/internal/config"
	"miko/internal/debuglog"
)

type DeepgramService struct {
	apiKey string
	config config.DeepgramConfig
	logger *debuglog.Logger

	mu              sync.Mutex
	writeMu         sync.Mutex
	conn            *websocket.Conn
	closeOnce       sync.Once
	receiveDone     chan struct{}
	pendingSegments []string
}

func NewDeepgramService(apiKey string, cfg config.DeepgramConfig, logger *debuglog.Logger) *DeepgramService {
	if cfg.Model == "" {
		cfg.Model = config.DefaultDeepgramModel
	}
	if cfg.Language == "" {
		cfg.Language = config.DefaultDeepgramLang
	}
	if cfg.EndpointingMs <= 0 {
		cfg.EndpointingMs = 300
	}
	if cfg.UtteranceEndMs <= 0 {
		cfg.UtteranceEndMs = 1200
	}
	return &DeepgramService{
		apiKey: apiKey,
		config: cfg,
		logger: logger,
	}
}

func (s *DeepgramService) Connect(ctx context.Context, sessionID string, onUtterance func(string), onDisconnect func(error)) error {
	query := url.Values{}
	query.Set("model", s.config.Model)
	query.Set("language", s.config.Language)
	query.Set("smart_format", boolString(s.config.SmartFormat))
	query.Set("encoding", "linear16")
	query.Set("channels", "1")
	query.Set("sample_rate", "16000")
	query.Set("interim_results", boolString(s.config.InterimResults))
	query.Set("vad_events", boolString(s.config.VadEvents))
	query.Set("endpointing", fmt.Sprintf("%d", s.config.EndpointingMs))
	query.Set("utterance_end_ms", fmt.Sprintf("%d", s.config.UtteranceEndMs))

	u := url.URL{
		Scheme:   "wss",
		Host:     "api.deepgram.com",
		Path:     "/v1/listen",
		RawQuery: query.Encode(),
	}

	header := http.Header{}
	header.Set("Authorization", "Token "+s.apiKey)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), header)
	if err != nil {
		s.logger.SessionErrorf(sessionID, "stt.deepgram", "websocket connect failed: %v", err)
		return fmt.Errorf("connect deepgram websocket: %w", err)
	}
	s.logger.SessionInfof(sessionID, "stt.deepgram", "connected model=%s language=%s", s.config.Model, s.config.Language)

	s.mu.Lock()
	if s.conn != nil {
		s.mu.Unlock()
		s.Finish()
		s.mu.Lock()
	}
	receiveDone := make(chan struct{})
	s.conn = conn
	s.receiveDone = receiveDone
	s.pendingSegments = nil
	s.closeOnce = sync.Once{}
	s.mu.Unlock()

	go s.readLoop(ctx, sessionID, conn, receiveDone, onUtterance, onDisconnect)
	go s.keepAliveLoop(ctx, sessionID, conn)
	return nil
}

func (s *DeepgramService) SendAudio(sessionID string, audio []byte) error {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil || len(audio) == 0 {
		return nil
	}
	if err := s.writeMessage(conn, websocket.BinaryMessage, audio); err != nil {
		s.logger.SessionWarnf(sessionID, "stt.deepgram", "send audio failed: %v", err)
		return err
	}
	return nil
}

func (s *DeepgramService) Finish() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		conn := s.conn
		done := s.receiveDone
		s.conn = nil
		s.mu.Unlock()

		if conn != nil {
			_ = s.writeJSON(conn, map[string]string{"type": "Finalize"})
			_ = s.writeJSON(conn, map[string]string{"type": "CloseStream"})
			_ = conn.Close()
		}

		if done != nil {
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
		}
	})
}

func (s *DeepgramService) readLoop(ctx context.Context, sessionID string, conn *websocket.Conn, receiveDone chan struct{}, onUtterance func(string), onDisconnect func(error)) {
	cleanedUp := false
	defer func() {
		if !cleanedUp {
			s.cleanupConnection(conn, receiveDone)
		}
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			s.logger.SessionDebugf(sessionID, "stt.deepgram", "read loop closed: %v", err)
			s.cleanupConnection(conn, receiveDone)
			cleanedUp = true
			if ctx.Err() == nil && onDisconnect != nil {
				onDisconnect(err)
			}
			return
		}

		var message deepgramMessage
		if err := json.Unmarshal(data, &message); err != nil {
			continue
		}

		switch message.Type {
		case "Results":
			s.handleResults(sessionID, message, onUtterance)
		case "UtteranceEnd":
			s.flushPending(sessionID, onUtterance)
		}
	}
}

func (s *DeepgramService) keepAliveLoop(ctx context.Context, sessionID string, conn *websocket.Conn) {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.writeJSON(conn, map[string]string{"type": "KeepAlive"}); err != nil {
				s.logger.SessionDebugf(sessionID, "stt.deepgram", "keepalive stopped: %v", err)
				return
			}
		}
	}
}

func (s *DeepgramService) handleResults(sessionID string, message deepgramMessage, onUtterance func(string)) {
	transcript := strings.TrimSpace(message.Channel.Alternatives.FirstTranscript())
	if message.IsFinal && transcript != "" {
		s.mu.Lock()
		s.pendingSegments = append(s.pendingSegments, transcript)
		s.mu.Unlock()
		s.logger.SessionDebugf(sessionID, "stt.deepgram", "final segment chars=%d", len([]rune(transcript)))
	}
	if message.SpeechFinal {
		s.flushPending(sessionID, onUtterance)
	}
}

func (s *DeepgramService) flushPending(sessionID string, onUtterance func(string)) {
	s.mu.Lock()
	parts := append([]string(nil), s.pendingSegments...)
	s.pendingSegments = nil
	s.mu.Unlock()

	if len(parts) == 0 {
		return
	}
	text := strings.TrimSpace(strings.Join(parts, " "))
	if text != "" {
		s.logger.SessionInfof(sessionID, "stt.deepgram", "utterance ready chars=%d", len([]rune(text)))
		onUtterance(text)
	}
}

func (s *DeepgramService) cleanupConnection(conn *websocket.Conn, receiveDone chan struct{}) {
	s.mu.Lock()
	if s.conn == conn {
		s.conn = nil
	}
	if s.receiveDone == receiveDone {
		close(receiveDone)
		s.receiveDone = nil
	}
	s.mu.Unlock()
}

func (s *DeepgramService) writeJSON(conn *websocket.Conn, payload any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return conn.WriteJSON(payload)
}

func (s *DeepgramService) writeMessage(conn *websocket.Conn, messageType int, data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return conn.WriteMessage(messageType, data)
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

type deepgramMessage struct {
	Type        string `json:"type"`
	IsFinal     bool   `json:"is_final"`
	SpeechFinal bool   `json:"speech_final"`
	Channel     struct {
		Alternatives deepgramAlternatives `json:"alternatives"`
	} `json:"channel"`
}

type deepgramAlternatives []struct {
	Transcript string `json:"transcript"`
}

func (a deepgramAlternatives) FirstTranscript() string {
	if len(a) == 0 {
		return ""
	}
	return a[0].Transcript
}
