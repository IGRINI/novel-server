package deepseek

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/sashabaranov/go-openai"
)

const defaultTimeout = 300 * time.Second

// Message представляет сообщение в диалоге.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request представляет тело запроса к API.
type Request struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	// Дополнительные параметры, такие как temperature, max_tokens, можно добавить сюда
	// Temperature float64 `json:"temperature,omitempty"`
	// MaxTokens   int     `json:"max_tokens,omitempty"`
}

// ResponseChoice представляет один вариант ответа от модели.
type ResponseChoice struct {
	Index   int     `json:"index"`
	Message Message `json:"message"`
	// FinishReason string `json:"finish_reason"`
}

// UsageInfo содержит информацию об использовании токенов.
type UsageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Response представляет тело ответа от API.
type Response struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int64            `json:"created"`
	Model   string           `json:"model"`
	Choices []ResponseChoice `json:"choices"`
	Usage   UsageInfo        `json:"usage"`
	// Error *ErrorDetail `json:"error,omitempty"` // Можно добавить для обработки ошибок API
}

// Client представляет клиент для взаимодействия с API DeepSeek через OpenRouter.
type Client struct {
	openaiClient *openai.Client
	modelName    string
}

// NewClient создает новый экземпляр клиента.
// apiKey - ваш ключ API от OpenRouter.
// model - имя модели, которую вы хотите использовать (например, "deepseek/deepseek-chat-v3-0324:free").
func NewClient(apiKey string, model string) *Client {
	config := openai.DefaultConfig(apiKey)
	config.BaseURL = "https://openrouter.ai/api/v1"

	// Настраиваем таймаут для HTTP клиента
	config.HTTPClient = &http.Client{
		Timeout: defaultTimeout,
	}

	return &Client{
		openaiClient: openai.NewClientWithConfig(config),
		modelName:    model,
	}
}

// ChatCompletion отправляет запрос на завершение чата к API.
// Возвращает ответ модели или ошибку.
func (c *Client) ChatCompletion(ctx context.Context, messages []openai.ChatCompletionMessage) (string, error) {
	if len(messages) == 0 {
		return "", fmt.Errorf("messages cannot be empty")
	}

	resp, err := c.openaiClient.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model:    c.modelName,
			Messages: messages,
			// Дополнительные параметры:
			// Temperature: 0.7,
			// MaxTokens:   150,
		},
	)

	if err != nil {
		return "", fmt.Errorf("openai chat completion failed: %w", err)
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("received empty response from API")
	}

	return resp.Choices[0].Message.Content, nil
}

// ChatCompletionWithOptions отправляет запрос на завершение чата с дополнительными опциями.
func (c *Client) ChatCompletionWithOptions(ctx context.Context, request openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	if len(request.Messages) == 0 {
		return openai.ChatCompletionResponse{}, fmt.Errorf("messages cannot be empty")
	}

	// Устанавливаем модель, если не указана
	if request.Model == "" {
		request.Model = c.modelName
	}

	return c.openaiClient.CreateChatCompletion(ctx, request)
}

// SetSystemPrompt создает новый запрос с системным промптом в начале.
// Удобная функция для задания "личности" модели.
func SetSystemPrompt(messages []openai.ChatCompletionMessage, systemPrompt string) []openai.ChatCompletionMessage {
	// Если список пуст или первое сообщение не системное, добавляем системное сообщение в начало
	if len(messages) == 0 || messages[0].Role != "system" {
		return append([]openai.ChatCompletionMessage{
			{
				Role:    "system",
				Content: systemPrompt,
			},
		}, messages...)
	}

	// Если первое сообщение уже системное, заменяем его контент
	result := make([]openai.ChatCompletionMessage, len(messages))
	copy(result, messages)
	result[0].Content = systemPrompt
	return result
}
