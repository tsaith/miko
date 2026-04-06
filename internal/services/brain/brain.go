package brain

import (
	"context"
	"strings"

	"miko/internal/debuglog"
	"miko/internal/services/llm"
)

type Service struct {
	llm          *llm.OpenAIService
	systemPrompt string
	history      []llm.Message
	logger       *debuglog.Logger
}

func NewService(service *llm.OpenAIService, logger *debuglog.Logger) *Service {
	prompt := strings.TrimSpace(`
你是一位名叫 Miko 的桌上型語音智能助理。
你會像真人助理一樣和使用者自然交談，不要說自己是 AI 或語言模型。
請主要使用繁體中文回覆，語氣自然、簡潔、有親和力。
你的文字會直接交給 TTS 唸出，所以請避免使用不適合口語的格式、清單符號或舞台動作描述。
如果使用者問題不明確，優先用一句簡短問題確認。
`)

	return &Service{
		llm:          service,
		systemPrompt: prompt,
		logger:       logger,
		history: []llm.Message{
			{
				Role:    "system",
				Content: prompt,
			},
		},
	}
}

func (s *Service) Reset() {
	s.history = []llm.Message{
		{
			Role:    "system",
			Content: s.systemPrompt,
		},
	}
}

func (s *Service) ProcessMessage(ctx context.Context, sessionID string, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}

	s.history = append(s.history, llm.Message{Role: "user", Content: text})
	if len(s.history) > 11 {
		s.history = append([]llm.Message{s.history[0]}, s.history[len(s.history)-10:]...)
	}
	s.logger.SessionDebugf(sessionID, "brain", "history size=%d", len(s.history))

	reply, err := s.llm.GenerateResponse(ctx, sessionID, s.history)
	if err != nil {
		return "", err
	}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		reply = "抱歉，我剛剛沒有整理好回覆，請再說一次。"
	}

	s.history = append(s.history, llm.Message{Role: "assistant", Content: reply})
	s.logger.SessionDebugf(sessionID, "brain", "assistant reply chars=%d", len([]rune(reply)))
	return reply, nil
}
