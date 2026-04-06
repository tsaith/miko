package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"miko/internal/config"
	"miko/internal/debuglog"
)

type CartesiaService struct {
	apiKey     string
	httpClient *http.Client
	config     config.CartesiaConfig
	logger     *debuglog.Logger
}

func NewCartesiaService(apiKey string, cfg config.CartesiaConfig, logger *debuglog.Logger) *CartesiaService {
	if cfg.VoiceID == "" {
		cfg.VoiceID = config.DefaultCartesiaVoiceID
	}
	if cfg.ModelID == "" {
		cfg.ModelID = config.DefaultCartesiaModelID
	}
	if cfg.Language == "" {
		cfg.Language = "zh"
	}
	if cfg.SampleRate <= 0 {
		cfg.SampleRate = 16000
	}
	if cfg.APIVersion == "" {
		cfg.APIVersion = config.DefaultCartesiaVersion
	}

	return &CartesiaService{
		apiKey: apiKey,
		config: cfg,
		logger: logger,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (s *CartesiaService) Synthesize(ctx context.Context, sessionID string, text string) ([]byte, error) {
	payload := cartesiaRequest{
		ModelID:    s.config.ModelID,
		Transcript: text,
		Voice: cartesiaVoice{
			Mode: "id",
			ID:   s.config.VoiceID,
		},
		Language: s.config.Language,
		OutputFormat: cartesiaOutputFormat{
			Container:  "raw",
			Encoding:   "pcm_s16le",
			SampleRate: s.config.SampleRate,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal cartesia request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.cartesia.ai/tts/bytes", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create cartesia request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cartesia-Version", s.config.APIVersion)
	req.Header.Set("X-API-Key", s.apiKey)
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	started := time.Now()
	s.logger.SessionInfof(sessionID, "tts.cartesia", "request start model=%s voice=%s chars=%d", s.config.ModelID, s.config.VoiceID, len([]rune(text)))
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.logger.SessionErrorf(sessionID, "tts.cartesia", "request failed after=%s err=%v", time.Since(started).Round(time.Millisecond), err)
		return nil, fmt.Errorf("cartesia request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		s.logger.SessionErrorf(sessionID, "tts.cartesia", "api error status=%d after=%s body=%s", resp.StatusCode, time.Since(started).Round(time.Millisecond), strings.TrimSpace(string(data)))
		return nil, fmt.Errorf("cartesia error: %s", strings.TrimSpace(string(data)))
	}

	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.SessionErrorf(sessionID, "tts.cartesia", "read failed after=%s err=%v", time.Since(started).Round(time.Millisecond), err)
		return nil, fmt.Errorf("read cartesia response: %w", err)
	}
	if len(audio) == 0 {
		s.logger.SessionWarnf(sessionID, "tts.cartesia", "empty audio after=%s", time.Since(started).Round(time.Millisecond))
		return nil, fmt.Errorf("cartesia returned empty audio")
	}
	s.logger.SessionInfof(sessionID, "tts.cartesia", "request done after=%s bytes=%d sample_rate=%d", time.Since(started).Round(time.Millisecond), len(audio), s.config.SampleRate)
	return audio, nil
}

type cartesiaRequest struct {
	ModelID      string               `json:"model_id"`
	Transcript   string               `json:"transcript"`
	Voice        cartesiaVoice        `json:"voice"`
	Language     string               `json:"language,omitempty"`
	OutputFormat cartesiaOutputFormat `json:"output_format"`
}

type cartesiaVoice struct {
	Mode string `json:"mode"`
	ID   string `json:"id"`
}

type cartesiaOutputFormat struct {
	Container  string `json:"container"`
	Encoding   string `json:"encoding"`
	SampleRate int    `json:"sample_rate"`
}
