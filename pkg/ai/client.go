package ai

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"novel-server/internal/model"

	"github.com/rs/zerolog"
	"github.com/sashabaranov/go-openai"
)

var log = zerolog.New(zerolog.NewConsoleWriter()).With().Timestamp().Logger()

const (
	promptsDir                  = "promts"
	narratorPromptFile          = "narrator.md"
	setupPromptFile             = "novel_setup.md"
	firstSceneCreatorPromptFile = "novel_first_scene_creator.md"
	creatorPromptFile           = "novel_creator.md"
)

// Client предоставляет интерфейс для работы с API нейросети
type Client struct {
	client               *openai.Client
	modelName            string
	timeout              time.Duration
	maxRetries           int
	narratorSystemPrompt string
	setupSystemPrompt    string
	creatorSystemPrompt  string
}

// Config содержит конфигурацию для клиента нейросети
type Config struct {
	APIKey     string
	ModelName  string
	Timeout    int
	MaxRetries int
}

// loadPromptFromFile reads the content of a prompt file.
func loadPromptFromFile(filename string) (string, error) {
	filePath := filepath.Join(promptsDir, filename)
	content, err := os.ReadFile(filePath)
	if err != nil {
		log.Error().Err(err).Str("file", filePath).Msg("Failed to read prompt file")
		return "", fmt.Errorf("failed to read prompt file %s: %w", filePath, err)
	}
	return string(content), nil
}

// New создает новый экземпляр клиента нейросети
func New(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("не указан API ключ для OpenRouter")
	}

	if cfg.ModelName == "" {
		cfg.ModelName = "deepseek/deepseek-chat-v3-0324:free"
	}

	if cfg.Timeout <= 0 {
		cfg.Timeout = 120
	}

	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}

	narratorPrompt, err := loadPromptFromFile(narratorPromptFile)
	if err != nil {
		return nil, err
	}
	setupPrompt, err := loadPromptFromFile(setupPromptFile)
	if err != nil {
		return nil, err
	}
	creatorPrompt, err := loadPromptFromFile(creatorPromptFile)
	if err != nil {
		return nil, err
	}

	config := openai.DefaultConfig(cfg.APIKey)
	config.BaseURL = "https://openrouter.ai/api/v1"

	client := openai.NewClientWithConfig(config)

	return &Client{
		client:               client,
		modelName:            cfg.ModelName,
		timeout:              time.Duration(cfg.Timeout) * time.Second,
		maxRetries:           cfg.MaxRetries,
		narratorSystemPrompt: narratorPrompt,
		setupSystemPrompt:    setupPrompt,
		creatorSystemPrompt:  creatorPrompt,
	}, nil
}

// GenerateNovelConfig генерирует конфигурацию новеллы на основе промпта пользователя
func (c *Client) GenerateNovelConfig(ctx context.Context, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	systemPromptForConfig := "Ты опытный писатель, который создает интерактивные новеллы. Ты создаешь конфигурацию для новой интерактивной новеллы на основе запроса пользователя. Ответ должен быть в формате JSON, содержащий заголовок, описание, сеттинг, жанр, темы и основных персонажей."

	attempts := 0
	for attempts < c.maxRetries {
		attempts++

		req := openai.ChatCompletionRequest{
			Model: c.modelName,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: systemPromptForConfig,
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: prompt,
				},
			},
			Temperature: 0.7,
			MaxTokens:   1500,
			TopP:        0.95,
		}

		resp, err := c.client.CreateChatCompletion(ctx, req)
		if err != nil {
			if attempts >= c.maxRetries {
				return "", fmt.Errorf("ошибка при генерации конфигурации новеллы: %w", err)
			}
			continue
		}

		if len(resp.Choices) == 0 {
			if attempts >= c.maxRetries {
				return "", errors.New("пустой ответ от API: не получены варианты")
			}
			continue
		}

		return resp.Choices[0].Message.Content, nil
	}

	return "", errors.New("не удалось получить ответ от API после нескольких попыток")
}

// GenerateWithNarrator генерирует драфт новеллы через нарратор
func (c *Client) GenerateWithNarrator(ctx context.Context, request model.NarratorPromptRequest) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	userPrompt := request.UserPrompt

	if request.PrevConfig != nil {
		configAsContext := fmt.Sprintf("Предыдущая конфигурация: %+v\n\nПользователь хочет внести изменения: %s", *request.PrevConfig, userPrompt)
		userPrompt = configAsContext
	}

	attempts := 0
	for attempts < c.maxRetries {
		attempts++

		req := openai.ChatCompletionRequest{
			Model: c.modelName,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: c.narratorSystemPrompt,
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: userPrompt,
				},
			},
			Temperature: 0.7,
			MaxTokens:   15000,
			TopP:        0.95,
		}

		resp, err := c.client.CreateChatCompletion(ctx, req)
		if err != nil {
			if attempts >= c.maxRetries {
				return "", fmt.Errorf("ошибка при генерации драфта новеллы: %w", err)
			}
			continue
		}

		if len(resp.Choices) == 0 {
			if attempts >= c.maxRetries {
				return "", errors.New("пустой ответ от API: не получены варианты")
			}
			continue
		}

		return resp.Choices[0].Message.Content, nil
	}

	return "", errors.New("не удалось получить ответ от API после нескольких попыток")
}

// GenerateWithNovelSetup вызывает AI для генерации конфигурации новеллы на основе драфта
func (c *Client) GenerateWithNovelSetup(ctx context.Context, config model.NovelConfig) (string, error) {
	return c.generate(ctx, setupPromptFile, config)
}

// GenerateFirstScene вызывает AI для генерации первой сцены новеллы
func (c *Client) GenerateFirstScene(ctx context.Context, req model.GenerateFirstSceneRequest) (string, error) {
	return c.generate(ctx, firstSceneCreatorPromptFile, req)
}

// GenerateWithNovelCreator вызывает AI для генерации контента новеллы
func (c *Client) GenerateWithNovelCreator(ctx context.Context, req model.GenerateNovelContentRequest) (string, error) {
	return c.generate(ctx, creatorPromptFile, req)
}

// GenerateSceneContent генерирует содержимое сцены новеллы
func (c *Client) GenerateSceneContent(ctx context.Context, novelConfig, currentState, userAction string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	systemPromptForScene := "Ты опытный писатель, который создает интерактивные новеллы. Твоя задача - генерировать содержимое сцен на основе конфигурации новеллы, текущего состояния и действий пользователя. Ответ должен быть в формате JSON, содержащий: заголовок сцены, описание, основное содержание и массив доступных вариантов выбора (каждый с текстом и ID)."

	attempts := 0
	for attempts < c.maxRetries {
		attempts++

		req := openai.ChatCompletionRequest{
			Model: c.modelName,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: systemPromptForScene,
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: fmt.Sprintf("Конфигурация новеллы: %s\nТекущее состояние: %s\nДействие пользователя: %s", novelConfig, currentState, userAction),
				},
			},
			Temperature: 0.8,
			MaxTokens:   15000,
			TopP:        0.95,
		}

		resp, err := c.client.CreateChatCompletion(ctx, req)
		if err != nil {
			if attempts >= c.maxRetries {
				return "", fmt.Errorf("ошибка при генерации содержимого сцены: %w", err)
			}
			continue
		}

		if len(resp.Choices) == 0 {
			if attempts >= c.maxRetries {
				return "", errors.New("пустой ответ от API: не получены варианты")
			}
			continue
		}

		responseContent := resp.Choices[0].Message.Content
		// Логируем ответ, встраивая его в сообщение
		log.Info().
			Str("model", c.modelName).
			Int("attempt", attempts).
			Msg("Получен ответ от API (Creator):\n" + responseContent)

		return responseContent, nil
	}

	return "", errors.New("не удалось получить ответ от API после нескольких попыток")
}

// generate is a helper function to generate content based on a prompt file and input data
func (c *Client) generate(ctx context.Context, promptFile string, inputData interface{}) (string, error) {
	content, err := loadPromptFromFile(promptFile)
	if err != nil {
		return "", err
	}

	systemPrompt := fmt.Sprintf("Ты опытный писатель, который создает интерактивные новеллы. Твоя задача - генерировать содержимое сцен на основе конфигурации новеллы, текущего состояния и действий пользователя. Ответ должен быть в формате JSON, содержащий: заголовок сцены, описание, основное содержание и массив доступных вариантов выбора (каждый с текстом и ID). Конфигурация новеллы: %s", content)

	attempts := 0
	for attempts < c.maxRetries {
		attempts++

		req := openai.ChatCompletionRequest{
			Model: c.modelName,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: systemPrompt,
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: fmt.Sprintf("%+v", inputData),
				},
			},
			Temperature: 0.7,
			MaxTokens:   15000,
			TopP:        0.95,
		}

		resp, err := c.client.CreateChatCompletion(ctx, req)
		if err != nil {
			if attempts >= c.maxRetries {
				return "", fmt.Errorf("ошибка при генерации содержимого сцены: %w", err)
			}
			continue
		}

		if len(resp.Choices) == 0 {
			if attempts >= c.maxRetries {
				return "", errors.New("пустой ответ от API: не получены варианты")
			}
			continue
		}

		responseContent := resp.Choices[0].Message.Content
		// Логируем ответ, встраивая его в сообщение
		log.Info().
			Str("model", c.modelName).
			Int("attempt", attempts).
			Msg("Получен ответ от API (Creator):\n" + responseContent)

		return responseContent, nil
	}

	return "", errors.New("не удалось получить ответ от API после нескольких попыток")
}
