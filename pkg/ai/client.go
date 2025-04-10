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
	openai "github.com/sashabaranov/go-openai"
)

var log = zerolog.New(zerolog.NewConsoleWriter()).With().Timestamp().Logger()

const (
	promptsDir                  = "promts"
	narratorPromptFile          = "narrator.md"
	setupPromptFile             = "novel_setup.md"
	firstSceneCreatorPromptFile = "novel_first_scene_creator.md"
	creatorPromptFile           = "novel_creator.md"
	gameOverCreatorPromptFile   = "novel_gameover_creator.md"
	storyVarDefinitionsMarker   = "Story Variable Definitions:"
	choiceMarker                = "Choice:"
	coreStatsResetMarker        = "Core Stats Reset:"
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

// GenerateWithNarrator генерирует конфигурацию новеллы через нарратор
func (c *Client) GenerateWithNarrator(ctx context.Context, request model.NarratorPromptRequest) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	userContent := ""
	if request.UserPrompt == "" {
		return "", errors.New("UserPrompt is required for Narrator") // Промпт обязателен
	}

	if request.PrevConfig != nil {
		// Формируем контент для запроса на МОДИФИКАЦИЮ
		configBytes, _ := json.MarshalIndent(request.PrevConfig, "", "  ") // Сериализуем для удобства чтения AI
		configAsContext := fmt.Sprintf("Предыдущая конфигурация:\n```json\n%s\n```\n\nПользователь хочет внести изменения:\n%s", string(configBytes), request.UserPrompt)
		userContent = configAsContext
	} else {
		// Используем чистый UserPrompt для СОЗДАНИЯ драфта
		userContent = request.UserPrompt
	}

	req := openai.ChatCompletionRequest{
		Model: c.modelName,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: c.narratorSystemPrompt, // Системный промпт нарратора
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: userContent, // Передаем UserPrompt или данные для модификации
			},
		},
		Temperature: 0.7,
		MaxTokens:   15000,
		TopP:        0.95,
	}

	resp, err := c.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("ошибка при вызове CreateChatCompletion: %w", err)
	}

	response := resp.Choices[0].Message.Content
	// Парсим ответ для проверки и установки InitialState
	// Используем функцию из parser.go
	var configForInitialState model.NovelConfig // Промежуточная переменная для парсинга всего конфига
	if err := json.Unmarshal([]byte(response), &configForInitialState); err != nil {
		// Попытаться распарсить только начальные поля, если полный парсинг не удался?
		// Пока что возвращаем ошибку, если весь конфиг не парсится.
		log.Error().Err(err).Str("response", response).Msg("Ошибка парсинга JSON ответа нарратора для извлечения InitialState")
		return "", fmt.Errorf("ошибка парсинга JSON ответа нарратора: %w", err)
	}

	// Создаем структуру NovelConfig (хотя она уже есть в configForInitialState)
	var config model.NovelConfig
	if err := json.Unmarshal([]byte(response), &config); err != nil {
		return "", fmt.Errorf("ошибка парсинга конфига: %w", err)
	}

	// Устанавливаем InitialState из распарсенного конфига
	config.InitialState = configForInitialState.InitialState

	// Сериализуем обновленный конфиг обратно в JSON
	updatedResponse, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("ошибка сериализации обновленного конфига: %w", err)
	}

	return string(updatedResponse), nil
}

// GenerateWithNovelSetup вызывает AI для генерации конфигурации новеллы на основе драфта
func (c *Client) GenerateWithNovelSetup(ctx context.Context, config model.NovelConfig) (string, error) {
	return c.generate(ctx, setupPromptFile, config)
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

	log.Info().
		Str("model", c.modelName).
		Str("promptFile", creatorPromptFile).
		Msg("Отправка запроса на генерацию контента новеллы (следующий батч)")

	return c.generate(ctx, creatorPromptFile, data)
}

// GenerateWithFirstSceneCreator вызывает LLM с промптом novel_first_scene_creator.md
// Принимает GenerateNovelContentRequestForAI (хотя NovelState будет nil/пустым)
func (c *Client) GenerateWithFirstSceneCreator(ctx context.Context, request model.GenerateNovelContentRequestForAI) (string, error) {
	// Преобразуем запрос в map для передачи в generate
	// NovelState должен быть пустым или отсутствовать для первого запроса
	data := map[string]interface{}{
		"Config": request.Config,
		"Setup":  request.Setup,
	}

	log.Info().Str("model", c.modelName).Str("promptFile", firstSceneCreatorPromptFile).Msg("Отправка запроса на генерацию контента новеллы (первый батч)")

	return c.generate(ctx, firstSceneCreatorPromptFile, data)
}

// GenerateGameOverEnding вызывает AI для генерации концовки игры
func (c *Client) GenerateGameOverEnding(ctx context.Context, request model.GameOverEndingRequestForAI) (string, error) {
	log.Info().Str("model", c.modelName).Str("promptFile", gameOverCreatorPromptFile).Msg("Отправка запроса на генерацию концовки Game Over")
	return c.generate(ctx, gameOverCreatorPromptFile, request)
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
		log.Info().Msgf("inputJSONString: %v", inputJSONString)
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
			// ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatJSON}, // Оставляем закомментированным
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
			Msg("Получен ответ от API (текстовый формат): " + responseContent)

		return responseContent, nil // Возвращаем сырой текстовый ответ
	}

	return "", errors.New("не удалось получить валидный ответ от API после нескольких попыток")
}
