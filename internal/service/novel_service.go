package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"novel-server/internal/auth"
	"novel-server/internal/deepseek"
	"novel-server/internal/domain"
	"novel-server/internal/repository"
	"os"
	"strings"

	"github.com/google/uuid"
	// "github.com/jackc/pgx/v5/pgxpool" // Убираем зависимость от pgxpool

	"github.com/sashabaranov/go-openai"
)

// NovelService предоставляет функциональность для работы с новеллами и их черновиками
type NovelService struct {
	deepseekClient      *deepseek.Client
	novelRepo           repository.NovelRepository  // Используем интерфейс репозитория для новелл
	draftRepo           domain.NovelDraftRepository // Исправлено: используем интерфейс из domain
	systemPrompt        string
	novelContentService *NovelContentService // Добавлен сервис для генерации контента
}

// NewNovelService создает новый экземпляр сервиса
func NewNovelService(deepseekClient *deepseek.Client, novelRepo repository.NovelRepository, draftRepo domain.NovelDraftRepository, novelContentService *NovelContentService) (*NovelService, error) {
	// Загружаем системный промпт для генерации новеллы
	promptBytes, err := os.ReadFile("promts/narrator.md")
	if err != nil {
		return nil, fmt.Errorf("failed to read narrator prompt: %w", err)
	}

	return &NovelService{
		deepseekClient:      deepseekClient,
		novelRepo:           novelRepo,
		draftRepo:           draftRepo, // Инициализируем draftRepo
		systemPrompt:        string(promptBytes),
		novelContentService: novelContentService, // Инициализируем сервис для генерации контента
	}, nil
}

// CreateDraft генерирует конфигурацию новеллы и сохраняет её как черновик.
func (s *NovelService) CreateDraft(ctx context.Context, userID string, request domain.NovelGenerationRequest) (uuid.UUID, *domain.NovelConfig, error) {
	log.Printf("[NovelService] CreateDraft called for UserID: %s", userID)
	if userID == "" {
		log.Println("[NovelService] CreateDraft - Error: userID is empty")
		return uuid.Nil, nil, fmt.Errorf("userID cannot be empty")
	}

	// 1. Создаем сообщения для отправки в DeepSeek (или другой ИИ)
	messages := []openai.ChatCompletionMessage{
		{
			Role: openai.ChatMessageRoleUser,
			// TODO: Возможно, нужно будет объединять request.UserPrompt с существующим конфигом, если это уточнение?
			// Пока считаем, что это всегда новый черновик или полное переписывание.
			Content: request.UserPrompt,
		},
	}

	// Устанавливаем системный промпт
	messages = deepseek.SetSystemPrompt(messages, s.systemPrompt)

	// 2. Отправляем запрос к ИИ-нарратору
	response, err := s.deepseekClient.ChatCompletion(ctx, messages)
	if err != nil {
		log.Printf("[NovelService] CreateDraft - Error from AI Narrator: %v", err)
		return uuid.Nil, nil, fmt.Errorf("failed to get response from AI Narrator: %w", err)
	}

	// 3. Извлекаем JSON из ответа модели
	jsonStr, err := extractJSONFromResponse(response)
	if err != nil {
		log.Printf("[NovelService] CreateDraft - Error extracting JSON: %v", err)
		return uuid.Nil, nil, fmt.Errorf("failed to extract JSON from response: %w\nResponse: %s", err, response)
	}

	// Проверяем и исправляем JSON для дополнительной безопасности
	jsonStr = FixJSON(jsonStr)

	// 4. Парсим JSON в структуру NovelConfig
	var config domain.NovelConfig
	err = json.Unmarshal([]byte(jsonStr), &config)
	if err != nil {
		log.Printf("[NovelService] CreateDraft - Error parsing JSON: %v\nJSON String: %s", err, jsonStr)
		return uuid.Nil, nil, fmt.Errorf("failed to parse JSON config: %w", err)
	}

	// 5. Валидируем конфигурацию
	if err = config.Validate(); err != nil {
		log.Printf("[NovelService] CreateDraft - Invalid config generated: %v", err)
		return uuid.Nil, nil, fmt.Errorf("invalid configuration generated: %w", err)
	}
	log.Printf("[NovelService] CreateDraft - Successfully generated and validated config for UserID: %s, Title: %s", userID, config.Title)

	// 6. Генерируем новый DraftID
	draftID := uuid.New()

	// 7. Сериализуем конфиг обратно в JSON для сохранения в БД
	configJSON, err := json.Marshal(config)
	if err != nil {
		log.Printf("[NovelService] CreateDraft - Error marshaling config to JSON: %v", err)
		return uuid.Nil, nil, fmt.Errorf("failed to marshal config to JSON: %w", err)
	}

	// 8. Сохраняем черновик в репозитории черновиков
	err = s.draftRepo.SaveDraft(ctx, userID, draftID, configJSON)
	if err != nil {
		log.Printf("[NovelService] CreateDraft - Error saving draft to repository: %v", err)
		return uuid.Nil, nil, fmt.Errorf("failed to save novel draft: %w", err)
	}
	log.Printf("[NovelService] CreateDraft - Successfully saved draft with ID: %s for UserID: %s", draftID, userID)

	// Возвращаем ID черновика и саму конфигурацию
	return draftID, &config, nil
}

// GenerateNovel - ЭТА ФУНКЦИЯ ТЕПЕРЬ НЕ ИСПОЛЬЗУЕТСЯ НАПРЯМУЮ ИЗ ХЭНДЛЕРА
// Ее логика будет частью процесса подтверждения черновика (ConfirmDraft)
// Пока оставим ее здесь, возможно, переименуем и адаптируем позже.
func (s *NovelService) GenerateNovel(ctx context.Context, userID string, request domain.NovelGenerationRequest) (uuid.UUID, *domain.NovelConfig, error) {
	log.Println("[NovelService] GenerateNovel - DEPRECATED: Use CreateDraft and ConfirmDraft instead.")
	return uuid.Nil, nil, fmt.Errorf("GenerateNovel is deprecated, use CreateDraft")
	// TODO: Перенести логику сохранения в novelRepo в метод ConfirmDraft
}

// extractJSONFromResponse извлекает JSON из ответа модели
func extractJSONFromResponse(response string) (string, error) {
	// Очищаем ответ от возможных префиксов/суффиксов
	response = strings.TrimSpace(response)

	// Проверяем, начинается ли ответ с ```json
	jsonBlockPrefix := "```json"
	if strings.HasPrefix(response, jsonBlockPrefix) {
		response = strings.TrimPrefix(response, jsonBlockPrefix)
		// Ищем закрывающий ```
		jsonEndMarker := "```"
		if endIndex := strings.LastIndex(response, jsonEndMarker); endIndex != -1 {
			response = response[:endIndex]
		}
	}
	response = strings.TrimSpace(response)

	// Проверяем, начинается ли ответ с открывающей фигурной скобки
	jsonStartIndex := strings.Index(response, "{")
	if jsonStartIndex == -1 {
		return "", fmt.Errorf("no JSON object start found in response")
	}

	// Проверяем, заканчивается ли ответ закрывающей фигурной скобкой
	jsonEndIndex := strings.LastIndex(response, "}")
	if jsonEndIndex == -1 {
		return "", fmt.Errorf("no JSON object end found in response")
	}

	// Извлекаем JSON
	jsonStr := response[jsonStartIndex : jsonEndIndex+1]

	// Исправляем потенциально некорректный JSON
	jsonStr = FixJSON(jsonStr)

	// Валидация JSON
	var js json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &js); err != nil {
		return "", fmt.Errorf("invalid JSON extracted: %w", err)
	}

	return jsonStr, nil
}

// LoadPromptFromFile загружает текст системного промпта из файла
func LoadPromptFromFile(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

// ListNovels возвращает список новелл с пагинацией
// Обновлено: теперь принимает UserID из запроса для получения прогресса
func (s *NovelService) ListNovels(ctx context.Context, request domain.ListNovelsRequest) (*domain.ListNovelsResponse, error) {
	log.Printf("[Service] ListNovels called with request: %+v", request)

	// Получаем UserID из контекста
	userID, ok := ctx.Value(auth.UserIDKey).(string)
	if !ok || userID == "" {
		log.Println("[Service] ListNovels - Error: UserID not found in context or is empty.")
		// В зависимости от логики: можно возвращать список без прогресса или ошибку.
		// Пока возвращаем ошибку, т.к. репозиторий теперь требует UserID.
		return nil, fmt.Errorf("user authentication required to list novels with progress")
	}

	// Получаем список новелл из репозитория с поддержкой пагинации и UserID
	novels, total, nextCursor, err := s.novelRepo.ListNovels(ctx, userID, request.Limit, request.Cursor)
	if err != nil {
		log.Printf("[Service] ListNovels - Error from repository: %v", err)
		return nil, fmt.Errorf("failed to list novels: %w", err)
	}

	// Фильтруем новеллы - оставляем только те, у которых был выполнен сетап
	// (Эта фильтрация может быть уже не нужна, если репозиторий возвращает только засетапленные,
	// но оставим пока для ясности, если логика репозитория изменится)
	setupedNovels := []domain.NovelListItem{}
	for _, novel := range novels {
		if novel.IsSetuped {
			setupedNovels = append(setupedNovels, novel)
		}
	}

	// Если после фильтрации не осталось новелл и есть следующая страница,
	// можно рекурсивно запросить следующую. Важно передать тот же UserID.
	if len(setupedNovels) == 0 && nextCursor != nil {
		// Создаем новый контекст с тем же UserID для рекурсивного вызова
		nextCtx := context.WithValue(ctx, auth.UserIDKey, userID)
		nextRequest := domain.ListNovelsRequest{
			Limit:  request.Limit,
			Cursor: nextCursor,
			// UserID теперь берется из контекста в начале функции
		}
		log.Printf("[Service] ListNovels - No setuped novels found, recursively calling for next page with cursor %s", *nextCursor)
		return s.ListNovels(nextCtx, nextRequest)
	}

	// Формируем ответ
	response := &domain.ListNovelsResponse{
		Novels:       setupedNovels, // Возвращаем отфильтрованный список
		TotalResults: total,         // Общее количество (по данным из репозитория)
		NextCursor:   nextCursor,
		HasMore:      nextCursor != nil, // Определяется репозиторием
	}

	log.Printf("[Service] ListNovels - Successfully retrieved %d novels for UserID %s (after filtering)", len(setupedNovels), userID)
	return response, nil
}

// GetNovelDetails возвращает детальную информацию о новелле
func (s *NovelService) GetNovelDetails(ctx context.Context, novelID uuid.UUID) (*domain.NovelDetailsResponse, error) {
	log.Printf("[Service] GetNovelDetails called for NovelID: %s", novelID)

	// Получаем детальную информацию о новелле из репозитория
	details, err := s.novelRepo.GetNovelDetails(ctx, novelID)
	if err != nil {
		log.Printf("[Service] GetNovelDetails - Error from repository: %v", err)
		return nil, err // Возвращаем ошибку как есть, включая "novel not setuped"
	}

	log.Printf("[Service] GetNovelDetails - Successfully retrieved details for NovelID: %s", novelID)
	return details, nil
}

// ConfirmDraft подтверждает черновик, создает новеллу и запускает ее сетап
func (s *NovelService) ConfirmDraft(ctx context.Context, userID string, draftID uuid.UUID) (uuid.UUID, error) {
	log.Printf("[NovelService] ConfirmDraft called for UserID: %s, DraftID: %s", userID, draftID)

	// 1. Получаем конфиг черновика из репозитория
	configJSON, err := s.draftRepo.GetDraftConfigJSON(ctx, userID, draftID)
	if err != nil {
		log.Printf("[NovelService] ConfirmDraft - Error getting draft: %v", err)
		return uuid.Nil, fmt.Errorf("failed to get draft: %w", err)
	}

	// 2. Десериализуем JSON в структуру NovelConfig
	var config domain.NovelConfig
	err = json.Unmarshal(configJSON, &config)
	if err != nil {
		log.Printf("[NovelService] ConfirmDraft - Error unmarshaling config: %v", err)
		return uuid.Nil, fmt.Errorf("failed to parse draft config: %w", err)
	}

	// 3. Повторно валидируем конфигурацию (на всякий случай)
	if err = config.Validate(); err != nil {
		log.Printf("[NovelService] ConfirmDraft - Invalid config: %v", err)
		return uuid.Nil, fmt.Errorf("invalid configuration in draft: %w", err)
	}

	// 4. Сохраняем новеллу в основной репозиторий
	novelID, err := s.novelRepo.CreateNovel(ctx, userID, &config)
	if err != nil {
		log.Printf("[NovelService] ConfirmDraft - Error creating novel: %v", err)
		return uuid.Nil, fmt.Errorf("failed to create novel: %w", err)
	}
	log.Printf("[NovelService] ConfirmDraft - Successfully created novel with ID: %s from draft: %s", novelID, draftID)

	// 5. Удаляем черновик
	err = s.draftRepo.DeleteDraft(ctx, userID, draftID)
	if err != nil {
		// Не возвращаем ошибку, если не смогли удалить черновик, просто логируем
		log.Printf("[NovelService] ConfirmDraft - Warning: failed to delete draft after confirmation: %v", err)
	}

	// 6. Автоматически запускаем генерацию начального сетапа новеллы
	// Подготавливаем запрос для генерации контента
	contentRequest := domain.NovelContentRequest{
		NovelID: novelID,
		UserID:  userID,
	}

	// Запускаем асинхронную генерацию сетапа новеллы
	go func() {
		// Создаем новый контекст для асинхронной операции
		asyncCtx := context.Background()

		log.Printf("[NovelService] ConfirmDraft - Starting async setup generation for NovelID: %s", novelID)
		_, genErr := s.novelContentService.GenerateNovelContent(asyncCtx, contentRequest)
		if genErr != nil {
			log.Printf("[NovelService] ConfirmDraft - Error generating setup asynchronously: %v", genErr)
		} else {
			log.Printf("[NovelService] ConfirmDraft - Successfully generated setup for NovelID: %s", novelID)
		}
	}()

	log.Printf("[NovelService] ConfirmDraft - Returning NovelID: %s. Setup generation will continue asynchronously.", novelID)
	return novelID, nil
}

// RefineDraft уточняет черновик новеллы с помощью дополнительного пользовательского промпта
func (s *NovelService) RefineDraft(ctx context.Context, userID string, draftID uuid.UUID, additionalPrompt string) (*domain.NovelConfig, error) {
	log.Printf("[NovelService] RefineDraft called for UserID: %s, DraftID: %s", userID, draftID)

	// 1. Получаем конфиг черновика из репозитория
	configJSON, err := s.draftRepo.GetDraftConfigJSON(ctx, userID, draftID)
	if err != nil {
		log.Printf("[NovelService] RefineDraft - Error getting draft: %v", err)
		return nil, fmt.Errorf("failed to get draft: %w", err)
	}

	// 2. Десериализуем JSON в структуру NovelConfig
	var existingConfig domain.NovelConfig
	err = json.Unmarshal(configJSON, &existingConfig)
	if err != nil {
		log.Printf("[NovelService] RefineDraft - Error unmarshaling config: %v", err)
		return nil, fmt.Errorf("failed to parse draft config: %w", err)
	}

	// 3. Создаем составной промпт, включая текущую конфигурацию и новый запрос пользователя
	combinedPrompt := fmt.Sprintf("Current configuration: Title: %s, Genre: %s, Summary: %s\n\nAdditional request: %s",
		existingConfig.Title,
		existingConfig.Genre,
		existingConfig.StorySummary,
		additionalPrompt)

	// 4. Создаем сообщения для отправки в ИИ-нарратор
	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleUser,
			Content: combinedPrompt,
		},
	}

	messages = deepseek.SetSystemPrompt(messages, s.systemPrompt)

	// 5. Отправляем запрос к ИИ-нарратору
	response, err := s.deepseekClient.ChatCompletion(ctx, messages)
	if err != nil {
		log.Printf("[NovelService] RefineDraft - Error from AI Narrator: %v", err)
		return nil, fmt.Errorf("failed to get response from AI Narrator: %w", err)
	}

	// 6. Извлекаем JSON из ответа модели
	jsonStr, err := extractJSONFromResponse(response)
	if err != nil {
		log.Printf("[NovelService] RefineDraft - Error extracting JSON: %v", err)
		return nil, fmt.Errorf("failed to extract JSON from response: %w\nResponse: %s", err, response)
	}

	// Проверяем и исправляем JSON для дополнительной безопасности
	jsonStr = FixJSON(jsonStr)

	// 7. Парсим JSON в структуру NovelConfig
	var updatedConfig domain.NovelConfig
	err = json.Unmarshal([]byte(jsonStr), &updatedConfig)
	if err != nil {
		log.Printf("[NovelService] RefineDraft - Error parsing JSON: %v\nJSON String: %s", err, jsonStr)
		return nil, fmt.Errorf("failed to parse JSON config: %w", err)
	}

	// 8. Валидируем обновленную конфигурацию
	if err = updatedConfig.Validate(); err != nil {
		log.Printf("[NovelService] RefineDraft - Invalid updated config: %v", err)
		return nil, fmt.Errorf("invalid updated configuration: %w", err)
	}

	// 9. Сериализуем обновленный конфиг обратно в JSON для сохранения
	updatedConfigJSON, err := json.Marshal(updatedConfig)
	if err != nil {
		log.Printf("[NovelService] RefineDraft - Error marshaling updated config: %v", err)
		return nil, fmt.Errorf("failed to marshal updated config: %w", err)
	}

	// 10. Обновляем черновик в репозитории
	err = s.draftRepo.UpdateDraftConfigJSON(ctx, userID, draftID, updatedConfigJSON)
	if err != nil {
		log.Printf("[NovelService] RefineDraft - Error updating draft: %v", err)
		return nil, fmt.Errorf("failed to update draft: %w", err)
	}

	log.Printf("[NovelService] RefineDraft - Successfully refined draft with ID: %s", draftID)
	return &updatedConfig, nil
}
