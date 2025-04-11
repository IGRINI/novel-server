package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"novel-server/story-generator/internal/config"
	"time"

	openaigo "github.com/sashabaranov/go-openai"
)

// ErrAIGenerationFailed - ошибка при генерации текста AI
var ErrAIGenerationFailed = errors.New("ошибка генерации текста AI")

// AIClient интерфейс для взаимодействия с AI API
type AIClient interface {
	GenerateText(ctx context.Context, systemPrompt string, userInput string) (string, error)
}

// openAIClient реализует AIClient с использованием go-openai
type openAIClient struct {
	client *openaigo.Client
	model  string
}

// NewAIClient создает новый клиент для взаимодействия с AI
func NewAIClient(cfg *config.Config) AIClient {
	openaiConfig := openaigo.DefaultConfig(cfg.AIAPIKey)
	openaiConfig.BaseURL = cfg.AIBaseURL
	// Можно добавить кастомный HTTP клиент с таймаутами, если нужно больше контроля
	// httpClient := &http.Client{
	// 	 Timeout: cfg.AITimeout,
	// }
	// openaiConfig.HTTPClient = httpClient

	client := openaigo.NewClientWithConfig(openaiConfig)

	log.Printf("AI Клиент создан. BaseURL: %s, Model: %s", cfg.AIBaseURL, cfg.AIModel)

	return &openAIClient{
		client: client,
		model:  cfg.AIModel,
	}
}

// GenerateText генерирует текст на основе системного промта и ввода пользователя
func (c *openAIClient) GenerateText(ctx context.Context, systemPrompt string, userInput string) (string, error) {
	messages := []openaigo.ChatCompletionMessage{
		{
			Role:    openaigo.ChatMessageRoleSystem,
			Content: systemPrompt,
		},
	}
	// Добавляем ввод пользователя, если он есть
	if userInput != "" {
		messages = append(messages, openaigo.ChatCompletionMessage{
			Role:    openaigo.ChatMessageRoleUser,
			Content: userInput,
		})
	}

	startTime := time.Now()
	log.Printf("Отправка запроса к AI: Model=%s, SystemPrompt=%d bytes, UserInput=%d bytes",
		c.model, len(systemPrompt), len(userInput))

	resp, err := c.client.CreateChatCompletion(
		ctx,
		openaigo.ChatCompletionRequest{
			Model:    c.model,
			Messages: messages,
		},
	)

	duration := time.Since(startTime)

	if err != nil {
		log.Printf("Ошибка от AI API за %v: %v", duration, err)
		return "", fmt.Errorf("%w: %v", ErrAIGenerationFailed, err)
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		log.Printf("AI API вернул пустой ответ за %v", duration)
		return "", fmt.Errorf("%w: получен пустой ответ", ErrAIGenerationFailed)
	}

	generatedText := resp.Choices[0].Message.Content
	log.Printf("Ответ от AI API получен за %v. Длина ответа: %d символов.", duration, len(generatedText))

	// Дополнительно можно логировать Usage info: resp.Usage
	if resp.Usage.TotalTokens > 0 {
		log.Printf("AI Usage: PromptTokens=%d, CompletionTokens=%d, TotalTokens=%d",
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	}

	return generatedText, nil
}
