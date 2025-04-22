package client

import (
	"context"
	"io"
)

// GenerationParams содержит параметры для запроса генерации.
// Используем указатели, чтобы легко отличать неустановленное значение (nil)
// от значения по умолчанию (например, 0 для int/float).
type GenerationParams struct {
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
}

// StoryGeneratorClient определяет интерфейс для взаимодействия с сервисом генерации историй.
type StoryGeneratorClient interface {
	// GenerateStream отправляет промпты и параметры сервису генерации
	// и возвращает io.ReadCloser для чтения потокового ответа.
	GenerateStream(ctx context.Context, systemPrompt, userPrompt string, params GenerationParams) (io.ReadCloser, error)

	// GenerateText отправляет промпты и параметры и возвращает полный сгенерированный текст.
	GenerateText(ctx context.Context, systemPrompt, userPrompt string, params GenerationParams) (string, error)

	// SetInterServiceToken устанавливает межсервисный токен для клиента.
	SetInterServiceToken(token string)
}
