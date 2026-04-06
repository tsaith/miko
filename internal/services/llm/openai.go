package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"miko/internal/config"
	"miko/internal/debuglog"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

type Message struct {
	Role    string
	Content string
}

type OpenAIService struct {
	apiKey      string
	model       string
	maxTokens   int
	temperature float64
	client      openai.Client
	logger      *debuglog.Logger
}

func NewOpenAIService(apiKey string, cfg config.OpenAIConfig, logger *debuglog.Logger) *OpenAIService {
	model := cfg.Model
	if model == "" {
		model = config.DefaultOpenAIModel
	}
	maxTokens := cfg.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = 250
	}
	temperature := cfg.Temperature
	if temperature == 0 {
		temperature = 0.7
	}

	return &OpenAIService{
		apiKey:      apiKey,
		model:       model,
		maxTokens:   maxTokens,
		temperature: temperature,
		logger:      logger,
		client: openai.NewClient(
			option.WithAPIKey(apiKey),
		),
	}
}

func (s *OpenAIService) GenerateResponse(ctx context.Context, sessionID string, messages []Message) (string, error) {
	input := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		switch msg.Role {
		case "system":
			input = append(input, openai.SystemMessage(content))
		case "assistant":
			input = append(input, openai.AssistantMessage(content))
		default:
			input = append(input, openai.UserMessage(content))
		}
	}

	payload := openai.ChatCompletionNewParams{
		Model:       openai.ChatModel(s.model),
		Messages:    input,
		MaxTokens:   openai.Int(int64(s.maxTokens)),
		Temperature: openai.Float(s.temperature),
	}

	started := time.Now()
	s.logger.SessionInfof(sessionID, "llm.openai", "request start model=%s messages=%d", s.model, len(messages))
	var rawResp *http.Response
	resp, err := s.client.Chat.Completions.New(
		ctx,
		payload,
		option.WithResponseInto(&rawResp),
	)
	if err != nil {
		status := 0
		if rawResp != nil {
			status = rawResp.StatusCode
		}
		s.logger.SessionErrorf(sessionID, "llm.openai", "request failed status=%d after=%s err=%v", status, time.Since(started).Round(time.Millisecond), err)
		return "", fmt.Errorf("openai request failed: %w", err)
	}

	if len(resp.Choices) > 0 {
		text := strings.TrimSpace(resp.Choices[0].Message.Content)
		if text != "" {
			s.logger.SessionInfof(sessionID, "llm.openai", "request done after=%s chars=%d", time.Since(started).Round(time.Millisecond), len([]rune(text)))
			return text, nil
		}
	}
	s.logger.SessionWarnf(sessionID, "llm.openai", "response missing output text after=%s", time.Since(started).Round(time.Millisecond))
	return "", fmt.Errorf("openai response did not contain output text")
}
