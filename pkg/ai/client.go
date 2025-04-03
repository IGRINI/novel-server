package ai

import (
	"context"
	"encoding/json"
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

// GenerateWithNovelCreator вызывает LLM с промптом novel_creator.md
// Принимает GenerateNovelContentRequestForAI для последующих запросов
func (c *Client) GenerateWithNovelCreator(ctx context.Context, request model.GenerateNovelContentRequestForAI) (string, error) {
	// Преобразуем запрос (который теперь содержит NovelState) в map для передачи в generate
	// Это data, которая будет передана в шаблон внутри generate
	data := map[string]interface{}{
		"NovelState": request.NovelState,
		"Config":     request.Config,
		"Setup":      request.Setup,
	}

	// Генерируем ответ, передавая имя файла промпта и данные
	promptFile := "promts/novel_creator.md"
	log.Info().Str("model", c.modelName).Str("promptFile", promptFile).Msg("Отправка запроса на генерацию контента новеллы (следующий батч)")

	response, err := c.generate(ctx, promptFile, data) // Передаем promptFile и data
	if err != nil {
		return "", err // Ошибка уже обработана в generate
	}

	return response, nil
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
	// Загружаем основной промпт (инструкции для AI)
	instructions, err := loadPromptFromFile(promptFile)
	if err != nil {
		return "", fmt.Errorf("не удалось загрузить инструкции из %s: %w", promptFile, err)
	}

	// Сериализуем входные данные в JSON
	inputJSONBytes, err := json.MarshalIndent(inputData, "", "  ") // Используем MarshalIndent для читаемости в логах
	if err != nil {
		return "", fmt.Errorf("ошибка при сериализации входных данных в JSON: %w", err)
	}
	inputJSONString := string(inputJSONBytes)

	// Формируем системный промпт, включающий инструкции
	// Теперь системный промпт содержит ТОЛЬКО инструкции из файла
	systemPrompt := instructions

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	attempts := 0
	for attempts < c.maxRetries {
		attempts++

		log.Debug().Str("promptFile", promptFile).Int("attempt", attempts).Msg("Отправка запроса к AI")
		// Логируем входной JSON для отладки (можно убрать или изменить уровень на Debug)
		// log.Trace().Str("inputJson", inputJSONString).Msg("Входные данные для AI")

		req := openai.ChatCompletionRequest{
			Model: c.modelName,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: systemPrompt, // Передаем инструкции как системный промпт
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: inputJSONString, // Передаем входные данные как JSON
				},
			},
			Temperature: 0.7,   // Можно настроить
			MaxTokens:   15000, // Убедитесь, что лимит достаточен
			TopP:        0.95,  // Можно настроить
			// ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatJSON}, // Можно раскомментировать, если модель поддерживает JSON mode
		}

		resp, err := c.client.CreateChatCompletion(ctx, req)
		if err != nil {
			log.Error().Err(err).Int("attempt", attempts).Msg("Ошибка при вызове CreateChatCompletion")
			if attempts >= c.maxRetries {
				return "", fmt.Errorf("ошибка AI после %d попыток: %w", attempts, err)
			}
			time.Sleep(time.Duration(attempts) * time.Second) // Экспоненциальная задержка
			continue
		}

		if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
			log.Warn().Int("attempt", attempts).Msg("Пустой ответ от AI")
			if attempts >= c.maxRetries {
				return "", errors.New("пустой ответ от API после нескольких попыток")
			}
			time.Sleep(time.Duration(attempts) * time.Second)
			continue
		}

		responseContent := resp.Choices[0].Message.Content
		// Логируем ответ
		log.Info().
			Str("model", c.modelName).
			Str("promptFile", promptFile).
			Int("attempt", attempts).
			Msg("Получен ответ от API:\n" + responseContent)

		// Проверка, является ли ответ валидным JSON (опционально, но полезно)
		var js json.RawMessage
		if json.Unmarshal([]byte(responseContent), &js) != nil {
			log.Warn().Int("attempt", attempts).Msg("Ответ AI не является валидным JSON, пробуем снова...")
			if attempts >= c.maxRetries {
				return "", fmt.Errorf("ответ AI не является валидным JSON после %d попыток", attempts)
			}
			time.Sleep(time.Duration(attempts) * time.Second)
			continue // Пробуем снова, если ответ не JSON
		}

		return responseContent, nil
	}

	return "", errors.New("не удалось получить валидный ответ от API после нескольких попыток")
}
