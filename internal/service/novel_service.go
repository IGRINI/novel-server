package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"novel-server/internal/model"
	"novel-server/internal/repository"
	"novel-server/pkg/ai"
	"novel-server/pkg/taskmanager"
)

// NovelService представляет сервис для работы с новеллами
type NovelService struct {
	repo        *repository.NovelRepository
	aiClient    *ai.Client
	taskManager taskmanager.ITaskManager
	wsNotifier  WebSocketNotifier
}

// WebSocketNotifier интерфейс для отправки уведомлений через WebSocket
type WebSocketNotifier interface {
	SendToUser(userID, messageType, topic string, payload interface{})
	Broadcast(messageType, topic string, payload interface{})
}

// NewNovelService создает новый экземпляр сервиса новелл
func NewNovelService(repo *repository.NovelRepository, aiClient *ai.Client, taskManager taskmanager.ITaskManager, notifier WebSocketNotifier) *NovelService {
	// Устанавливаем WebSocket нотификатор для TaskManager
	if tm, ok := taskManager.(*taskmanager.TaskManager); ok {
		tm.SetWebSocketNotifier(notifier)
	}

	return &NovelService{
		repo:        repo,
		aiClient:    aiClient,
		taskManager: taskManager,
		wsNotifier:  notifier,
	}
}

// CreateNovel создает новую новеллу
func (s *NovelService) CreateNovel(ctx context.Context, novel model.Novel) (model.Novel, error) {
	return s.repo.Create(ctx, novel)
}

// GetNovelByID получает новеллу по ID
func (s *NovelService) GetNovelByID(ctx context.Context, id uuid.UUID) (model.Novel, error) {
	return s.repo.GetByID(ctx, id)
}

// GetNovelsByAuthorID получает все новеллы автора
func (s *NovelService) GetNovelsByAuthorID(ctx context.Context, authorID uuid.UUID) ([]model.Novel, error) {
	return s.repo.GetByAuthorID(ctx, authorID)
}

// UpdateNovel обновляет новеллу
func (s *NovelService) UpdateNovel(ctx context.Context, novel model.Novel) (model.Novel, error) {
	// Проверяем, существует ли новелла
	_, err := s.repo.GetByID(ctx, novel.ID)
	if err != nil {
		return model.Novel{}, fmt.Errorf("novel not found: %w", err)
	}

	return s.repo.Update(ctx, novel)
}

// DeleteNovel удаляет новеллу
func (s *NovelService) DeleteNovel(ctx context.Context, id uuid.UUID) error {
	// Проверяем, существует ли новелла
	_, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("novel not found: %w", err)
	}

	return s.repo.Delete(ctx, id)
}

// ListPublicNovels получает список публичных новелл
func (s *NovelService) ListPublicNovels(ctx context.Context, limit, offset int) ([]model.Novel, error) {
	return s.repo.ListPublic(ctx, limit, offset)
}

// GenerateNovelDraftAsync асинхронно генерирует драфт новеллы через нарратор
func (s *NovelService) GenerateNovelDraftAsync(ctx context.Context, userID uuid.UUID, request model.NarratorPromptRequest) (uuid.UUID, error) {
	// Создаем новый контекст для задачи
	taskCtx := context.Background()

	// Создаем параметры задачи, включая userID
	taskParams := struct {
		UserID  uuid.UUID
		Request model.NarratorPromptRequest
	}{
		UserID:  userID,
		Request: request,
	}

	// Создаем задачу с указанием владельца
	taskID, err := s.taskManager.SubmitTaskWithOwner(
		taskCtx,
		s.generateNovelDraftTask,
		taskParams,
		userID.String(), // Передаем ID пользователя как владельца задачи
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("ошибка при создании задачи: %w", err)
	}

	return taskID, nil
}

// cleanAIResponse очищает ответ от AI от markdown-разметки и проверяет баланс скобок
func cleanAIResponse(response string) string {
	cleanedResponse := strings.TrimSpace(response)

	// Удаляем markdown-разметку
	cleanedResponse = strings.TrimPrefix(cleanedResponse, "```json\n")
	cleanedResponse = strings.TrimPrefix(cleanedResponse, "```json")
	cleanedResponse = strings.TrimSuffix(cleanedResponse, "\n```")
	cleanedResponse = strings.TrimSuffix(cleanedResponse, "```")
	cleanedResponse = strings.TrimSpace(cleanedResponse)

	// Проверяем и исправляем баланс фигурных скобок
	openCount := strings.Count(cleanedResponse, "{")
	closeCount := strings.Count(cleanedResponse, "}")

	// Если есть лишние закрывающие скобки в конце, удаляем их
	if closeCount > openCount {
		excess := closeCount - openCount
		lastIndex := len(cleanedResponse) - 1
		for i := 0; i < excess; i++ {
			if lastIndex >= 0 && cleanedResponse[lastIndex] == '}' {
				cleanedResponse = cleanedResponse[:lastIndex]
				lastIndex--
			}
		}
		cleanedResponse = strings.TrimSpace(cleanedResponse)
	}

	return cleanedResponse
}

// generateNovelDraftTask обрабатывает задачу генерации драфта новеллы
func (s *NovelService) generateNovelDraftTask(ctx context.Context, params interface{}) (interface{}, error) {
	// Преобразуем параметры задачи
	taskData, ok := params.(struct {
		UserID  uuid.UUID
		Request model.NarratorPromptRequest
	})
	if !ok {
		return nil, errors.New("неверный тип параметров для generateNovelDraftTask")
	}

	// Вызываем нарратор для генерации драфта
	response, err := s.aiClient.GenerateWithNarrator(ctx, taskData.Request)
	if err != nil {
		return nil, fmt.Errorf("ошибка при генерации драфта новеллы: %w", err)
	}

	cleanedResponse := cleanAIResponse(response)
	log.Info().Msg("Очищенный ответ от нарратора:\n" + cleanedResponse)

	// Парсим JSON-ответ в структуру NovelConfig
	var config model.NovelConfig
	if err := json.Unmarshal([]byte(cleanedResponse), &config); err != nil {
		// Логируем ТОЛЬКО саму ошибку парсинга, без полного ответа
		log.Error().Err(err).Msg("Ошибка при разборе ответа нарратора после очистки JSON")
		return nil, fmt.Errorf("ошибка при разборе ответа нарратора: %w", err)
	}

	// Создаем черновик для сохранения в БД (полный конфиг)
	draft := model.NovelDraft{
		UserID:     taskData.UserID,
		Config:     config,
		UserPrompt: taskData.Request.UserPrompt, // Save the original user prompt
	}

	// Сохраняем черновик в БД
	savedDraft, err := s.repo.SaveNovelDraft(ctx, draft)
	if err != nil {
		// Логируем ошибку, но не прерываем задачу, т.к. конфиг уже сгенерирован
		log.Error().Err(err).Msg("Ошибка при сохранении черновика новеллы в БД")
	} else {
		log.Info().Str("draft_id", savedDraft.ID.String()).Msg("Черновик новеллы успешно сохранен в БД")
	}

	// Создаем урезанную версию конфига для отправки клиенту
	draftView := model.NovelDraftView{
		ID:                savedDraft.ID,
		Title:             config.Title,
		ShortDescription:  config.ShortDescription,
		Franchise:         config.Franchise,
		Genre:             config.Genre,
		IsAdultContent:    config.IsAdultContent,
		PlayerName:        config.PlayerName,
		PlayerGender:      config.PlayerGender,
		PlayerDescription: config.PlayerDescription,
		WorldContext:      config.WorldContext,
		CoreStats:         make(map[string]model.CoreStatView), // Инициализируем мапу
		Themes:            config.PlayerPrefs.Themes,           // Берем темы из PlayerPrefs
	}

	// Копируем данные CoreStats
	for name, stat := range config.CoreStats {
		draftView.CoreStats[name] = stat.ToView()
	}

	// Возвращаем урезанную версию как результат задачи
	return draftView, nil
}

// SetupNovelAsync асинхронно настраивает новеллу из драфта
func (s *NovelService) SetupNovelAsync(ctx context.Context, draftID uuid.UUID, authorID uuid.UUID) (uuid.UUID, error) {
	taskCtx := context.Background()

	taskParams := struct {
		DraftID  uuid.UUID
		AuthorID uuid.UUID
	}{
		DraftID:  draftID,
		AuthorID: authorID,
	}

	// Создаем задачу с указанием владельца
	taskID, err := s.taskManager.SubmitTaskWithOwner(
		taskCtx,
		s.setupNovelTask,
		taskParams,
		authorID.String(), // Передаем ID автора как владельца задачи
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("ошибка при создании задачи: %w", err)
	}

	return taskID, nil
}

// setupNovelTask обрабатывает задачу настройки новеллы
func (s *NovelService) setupNovelTask(ctx context.Context, params interface{}) (interface{}, error) {
	// Преобразуем параметры
	p, ok := params.(struct {
		DraftID  uuid.UUID
		AuthorID uuid.UUID
	})
	if !ok {
		return nil, errors.New("неверный тип параметров для setupNovelTask")
	}

	// 1. Получаем полный черновик из БД по ID
	draft, err := s.repo.GetDraftByID(ctx, p.DraftID)
	if err != nil {
		log.Error().Err(err).Str("draftID", p.DraftID.String()).Msg("Не удалось получить драфт для настройки новеллы")
		return nil, fmt.Errorf("драфт с ID %s не найден: %w", p.DraftID.String(), err)
	}

	// 2. Проверяем, что автор совпадает с тем, кто запустил настройку
	if draft.UserID != p.AuthorID {
		log.Warn().Str("draftID", p.DraftID.String()).Str("requestAuthorID", p.AuthorID.String()).Str("draftOwnerID", draft.UserID.String()).Msg("Попытка настройки новеллы из чужого драфта")
		return nil, fmt.Errorf("доступ запрещен: вы не являетесь владельцем этого драфта")
	}

	// 3. Вызываем сетап для настройки новеллы, используя конфиг из загруженного драфта
	response, err := s.aiClient.GenerateWithNovelSetup(ctx, draft.Config)
	if err != nil {
		return nil, fmt.Errorf("ошибка при настройке новеллы: %w", err)
	}

	cleanedResponse := cleanAIResponse(response)

	// Парсим JSON-ответ в структуру NovelSetup
	var setup model.NovelSetup
	if err := json.Unmarshal([]byte(cleanedResponse), &setup); err != nil {
		log.Error().Err(err).Str("rawResponse", response).Msg("Ошибка при разборе JSON ответа сетапа")
		return nil, fmt.Errorf("ошибка при разборе ответа сетапа: %w, response: %s", err, response)
	}

	// 4. Создаем новеллу в БД
	novel := model.Novel{
		ID:          uuid.New(),
		Title:       draft.Config.Title,            // Берем из конфига драфта
		Description: draft.Config.ShortDescription, // Берем из конфига драфта
		AuthorID:    p.AuthorID,                    // ID пользователя, запустившего настройку
		IsPublic:    false,
		Tags:        draft.Config.PlayerPrefs.Themes, // Берем из конфига драфта
		Config:      draft.Config,                    // Сохраняем полный конфиг из драфта
		Setup:       setup,                           // Сохраняем полученный сетап
	}

	createdNovel, err := s.repo.Create(ctx, novel)
	if err != nil {
		return nil, fmt.Errorf("ошибка при создании новеллы: %w", err)
	}

	// 5. Удаляем драфт после успешного создания новеллы
	if err := s.repo.DeleteDraft(ctx, p.DraftID); err != nil {
		log.Error().Err(err).Str("draftID", p.DraftID.String()).Msg("Ошибка при удалении драфта после создания новеллы")
		// Не возвращаем ошибку, т.к. новелла уже создана, но логируем проблему
	}

	// Результат содержит ID новеллы и настройку
	result := struct {
		NovelID uuid.UUID        `json:"novel_id"`
		Setup   model.NovelSetup `json:"setup"`
	}{
		NovelID: createdNovel.ID,
		Setup:   setup,
	}

	return result, nil
}

// GenerateNovelContentAsync асинхронно генерирует контент новеллы
func (s *NovelService) GenerateNovelContentAsync(ctx context.Context, req model.GenerateNovelContentRequest) (uuid.UUID, error) {
	taskCtx := context.Background()

	// Создаем задачу с указанием владельца
	taskID, err := s.taskManager.SubmitTaskWithOwner(
		taskCtx,
		s.generateContentTask,
		req,
		req.UserID, // UserID уже является строкой
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("ошибка при создании задачи: %w", err)
	}

	return taskID, nil
}

// generateContentTask обрабатывает задачу генерации контента новеллы
func (s *NovelService) generateContentTask(ctx context.Context, params interface{}) (interface{}, error) {
	// Преобразуем параметры задачи
	p, ok := params.(model.GenerateNovelContentRequest)
	if !ok {
		return nil, errors.New("неверный тип параметров для generateContentTask")
	}
	log.Info().Str("novelID", p.NovelID.String()).Msg("Начало задачи генерации контента")

	// Загружаем новеллу (config и setup)
	novel, err := s.repo.GetByID(ctx, p.NovelID)
	if err != nil {
		return nil, fmt.Errorf("ошибка при загрузке новеллы %s: %w", p.NovelID, err)
	}

	novelConfig := novel.Config
	novelSetup := novel.Setup

	// Получаем текущее состояние ИЛИ определяем, что это первый запрос
	var currentState model.NovelState
	var isFirstRequest bool
	userID, err := uuid.Parse(p.UserID)
	if err != nil {
		return nil, fmt.Errorf("неверный формат UserID: %w", err)
	}
	currentState, err = s.repo.GetNovelState(ctx, userID, p.NovelID)
	if err != nil {
		if strings.Contains(err.Error(), "не найдено") { // Используем Contains для проверки ошибки "не найдено"
			isFirstRequest = true
			log.Info().Str("novelID", p.NovelID.String()).Str("userID", p.UserID).Msg("Состояние новеллы не найдено, это первый запрос.")
		} else {
			// Другая ошибка при получении состояния
			return nil, fmt.Errorf("ошибка при получении состояния новеллы: %w", err)
		}
	} else {
		// Состояние найдено, это не первый запрос
		isFirstRequest = false
		log.Info().Str("novelID", p.NovelID.String()).Str("userID", p.UserID).Int("currentBatch", currentState.CurrentBatchNumber).Msg("Состояние новеллы найдено.")
		// Обновляем состояние на основе данных от клиента
		currentState.CoreStats = p.ClientState.CoreStats
		currentState.GlobalFlags = p.ClientState.GlobalFlags
		currentState.StoryVariables = p.ClientState.StoryVariables
		// CurrentBatchNumber будет обновлен после генерации следующего батча
	}

	// Определяем номер следующего батча
	nextBatchNumber := 1
	if !isFirstRequest {
		nextBatchNumber = currentState.CurrentBatchNumber + 1
	}
	log.Info().Str("novelID", p.NovelID.String()).Int("nextBatchNumber", nextBatchNumber).Msg("Определение номера следующего батча")

	var responseJSON string
	var choicesForClient []model.ChoiceEvent
	var storySummary, futureDirection string
	var cachedBatch model.SceneBatch

	// --- Проверка кеша в БД ---
	cachedBatch, err = s.repo.GetSceneBatchByNumber(ctx, p.NovelID, nextBatchNumber)
	if err == nil {
		// Кеш найден!
		log.Info().Str("novelID", p.NovelID.String()).Int("batchNumber", nextBatchNumber).Msg("Найден кешированный батч сцены.")
		responseJSON = string(cachedBatch.Choices) // Используем кешированный JSON
		storySummary = cachedBatch.StorySummarySoFar
		futureDirection = cachedBatch.FutureDirection

		// Парсим кешированные choices для клиента
		// Структура кешированного JSON должна соответствовать []ChoiceDetails
		var cachedChoicesDetails []model.ChoiceDetails
		if err := json.Unmarshal(cachedBatch.Choices, &cachedChoicesDetails); err != nil {
			log.Error().Err(err).Msg("Ошибка при разборе кешированного JSON choices")
			return nil, fmt.Errorf("ошибка при разборе кешированного JSON: %w", err)
		}

		// Конвертируем []ChoiceDetails в []ChoiceEvent для клиента
		choicesForClient = make([]model.ChoiceEvent, 0, len(cachedChoicesDetails))
		for _, details := range cachedChoicesDetails {
			if len(details.Choices) == 2 {
				choicesForClient = append(choicesForClient, model.ChoiceEvent{
					Description: details.Description,
					Choices:     details.Choices,
					Shuffleable: details.Shuffleable,
				})
			} else {
				log.Warn().Msgf("Пропущен кешированный ChoiceDetails с %d опциями (ожидалось 2)", len(details.Choices))
			}
		}

	} else if errors.Is(err, model.ErrNotFound) {
		// Кеш НЕ найден, генерируем через AI
		log.Info().Str("novelID", p.NovelID.String()).Int("batchNumber", nextBatchNumber).Msg("Кеш не найден, генерация через AI.")

		if isFirstRequest {
			// --- Генерация ПЕРВОГО батча ---
			log.Info().Str("novelID", p.NovelID.String()).Msg("Запрос генерации ПЕРВОГО батча контента через AI")
			configForAI := novelConfig
			configForAI.CoreStats = nil // Удаляем статы перед отправкой AI

			firstSceneReq := model.GenerateFirstSceneRequest{
				Config: configForAI,
				Setup:  novelSetup,
			}
			responseJSON, err = s.aiClient.GenerateFirstScene(ctx, firstSceneReq)
			if err != nil {
				return nil, fmt.Errorf("ошибка при генерации первого батча AI: %w", err)
			}

			cleanedResponse := cleanAIResponse(responseJSON)
			log.Debug().Str("cleanedResponse", cleanedResponse).Msg("Очищенный JSON первого батча AI")

			// Парсим ПОЛНЫЙ ответ AI для внутреннего использования и кеширования
			var firstSceneFullResponse model.FirstSceneResponse
			if err := json.Unmarshal([]byte(cleanedResponse), &firstSceneFullResponse); err != nil {
				log.Error().Err(err).Str("rawResponse", cleanedResponse).Msg("Ошибка при разборе JSON ответа генератора первого батча AI")
				return nil, fmt.Errorf("ошибка при разборе ответа генератора первого батча AI: %w", err)
			}
			storySummary = firstSceneFullResponse.StorySummarySoFar
			futureDirection = firstSceneFullResponse.FutureDirection

			// Конвертируем []ChoiceDetails в []ChoiceEvent для клиента
			choicesForClient = make([]model.ChoiceEvent, 0, len(firstSceneFullResponse.Choices))
			for _, details := range firstSceneFullResponse.Choices {
				if len(details.Choices) == 2 {
					choicesForClient = append(choicesForClient, model.ChoiceEvent{
						Description: details.Description,
						Choices:     details.Choices,
						Shuffleable: details.Shuffleable,
					})
				} else {
					log.Warn().Msgf("Пропущен AI ChoiceDetails с %d опциями (ожидалось 2)", len(details.Choices))
				}
			}

			// Сохраняем батч в кеш БД
			choicesJSON, _ := json.Marshal(firstSceneFullResponse.Choices) // Сериализуем обратно для кеша
			newBatch := model.SceneBatch{
				NovelID:           p.NovelID,
				BatchNumber:       nextBatchNumber,
				StorySummarySoFar: storySummary,
				FutureDirection:   futureDirection,
				Choices:           json.RawMessage(choicesJSON),
			}
			if _, err := s.repo.SaveSceneBatch(ctx, newBatch); err != nil {
				log.Error().Err(err).Msg("Ошибка при сохранении первого батча в кеш")
				// Не возвращаем ошибку, кеширование - не критично
			}

			// --- Сохранение начального состояния NovelState в БД (только при первом запросе) ---
			initialCoreStats := make(map[string]int)
			for name, definition := range novelSetup.CoreStatsDefinition {
				initialCoreStats[name] = definition.InitialValue
			}
			initialState := model.NovelState{
				UserID:             userID,
				NovelID:            p.NovelID,
				CurrentBatchNumber: nextBatchNumber,              // Сохраняем номер сгенерированного батча
				StorySummarySoFar:  storySummary,                 // Сохраняем для AI
				FutureDirection:    futureDirection,              // Сохраняем для AI
				CoreStats:          initialCoreStats,             // Сохраняем начальные статы
				GlobalFlags:        []string{},                   // Инициализируем пустым слайсом
				StoryVariables:     make(map[string]interface{}), // Инициализируем пустой мапой
				History:            []uuid.UUID{},                // Инициализируем пустым слайсом UUID
			}
			if _, err := s.repo.SaveNovelState(ctx, initialState); err != nil {
				log.Error().Err(err).Msg("Ошибка при сохранении начального состояния новеллы")
				// Решаем, возвращать ли ошибку или продолжать
			}
			log.Info().Msg("Первый батч сгенерирован и сохранен, начальное состояние сохранено.")

		} else {
			// --- Генерация СЛЕДУЮЩЕГО батча ---
			log.Info().Str("novelID", p.NovelID.String()).Int("currentBatch", currentState.CurrentBatchNumber).Msg("Запрос генерации СЛЕДУЮЩЕГО батча контента через AI")

			// Готовим запрос для AI
			contentReqForAI := model.GenerateNovelContentRequestForAI{
				NovelState: currentState, // Передаем обновленное состояние
				Config:     novelConfig,
				Setup:      novelSetup,
			}

			responseJSON, err = s.aiClient.GenerateWithNovelCreator(ctx, contentReqForAI)
			if err != nil {
				return nil, fmt.Errorf("ошибка при генерации следующего батча AI: %w", err)
			}

			cleanedResponse := cleanAIResponse(responseJSON)
			log.Debug().Str("cleanedResponse", cleanedResponse).Msg("Очищенный JSON следующего батча AI")

			// Парсим ПОЛНЫЙ ответ AI
			var nextBatchFullResponse model.NextBatchResponse // Используем новую структуру
			if err := json.Unmarshal([]byte(cleanedResponse), &nextBatchFullResponse); err != nil {
				log.Error().Err(err).Str("rawResponse", cleanedResponse).Msg("Ошибка при разборе JSON ответа генератора следующего батча AI")
				return nil, fmt.Errorf("ошибка при разборе ответа генератора следующего батча AI: %w", err)
			}
			storySummary = nextBatchFullResponse.StorySummarySoFar
			futureDirection = nextBatchFullResponse.FutureDirection

			// Конвертируем []ChoiceDetails в []ChoiceEvent для клиента
			choicesForClient = make([]model.ChoiceEvent, 0, len(nextBatchFullResponse.Choices))
			for _, details := range nextBatchFullResponse.Choices {
				if len(details.Choices) == 2 {
					choicesForClient = append(choicesForClient, model.ChoiceEvent{
						Description: details.Description,
						Choices:     details.Choices,
						Shuffleable: details.Shuffleable,
					})
				} else {
					log.Warn().Msgf("Пропущен AI ChoiceDetails с %d опциями (ожидалось 2)", len(details.Choices))
				}
			}

			// Сохраняем батч в кеш БД
			choicesJSON, _ := json.Marshal(nextBatchFullResponse.Choices) // Сериализуем обратно для кеша
			newBatch := model.SceneBatch{
				NovelID:           p.NovelID,
				BatchNumber:       nextBatchNumber,
				StorySummarySoFar: storySummary,
				FutureDirection:   futureDirection,
				Choices:           json.RawMessage(choicesJSON),
			}
			if _, err := s.repo.SaveSceneBatch(ctx, newBatch); err != nil {
				log.Error().Err(err).Msg("Ошибка при сохранении следующего батча в кеш")
				// Не возвращаем ошибку, кеширование - не критично
			}

			// --- Обновляем NovelState в БД (после генерации не-первого батча) ---
			currentState.CurrentBatchNumber = nextBatchNumber // Обновляем номер текущего батча
			currentState.StorySummarySoFar = storySummary     // Обновляем для следующего вызова AI
			currentState.FutureDirection = futureDirection    // Обновляем для следующего вызова AI
			if _, err := s.repo.SaveNovelState(ctx, currentState); err != nil {
				log.Error().Err(err).Msg("Ошибка при обновлении состояния новеллы после генерации батча")
				// Решаем, возвращать ли ошибку или продолжать
			}
			log.Info().Msgf("Следующий батч (%d) сгенерирован и сохранен, состояние обновлено.", nextBatchNumber)
		}

	} else {
		// Другая ошибка при получении кеша
		return nil, fmt.Errorf("ошибка при получении кеша батча %d: %w", nextBatchNumber, err)
	}

	// --- Создаем ответ ДЛЯ КЛИЕНТА ---
	clientPayload := model.ClientGameplayPayload{
		Choices: choicesForClient,
	}

	// Добавляем начальные статы и определения ТОЛЬКО для первого ответа
	if isFirstRequest {
		initialCoreStats := make(map[string]int)
		for name, definition := range novelSetup.CoreStatsDefinition {
			initialCoreStats[name] = definition.InitialValue
		}
		clientPayload.CoreStats = initialCoreStats
		clientPayload.CoreStatsDefinition = novelSetup.CoreStatsDefinition
	}

	// Возвращаем ClientGameplayPayload как результат задачи
	log.Info().Str("novelID", p.NovelID.String()).Int("batchNumber", nextBatchNumber).Msg("Задача генерации контента успешно завершена.")
	return clientPayload, nil
}

// safeGetString безопасно извлекает строку из map[string]interface{}
func safeGetString(data map[string]interface{}, key string) string {
	if val, ok := data[key]; ok {
		if strVal, ok := val.(string); ok {
			return strVal
		}
	}
	return ""
}

// GetTaskStatus получает статус задачи
func (s *NovelService) GetTaskStatus(ctx context.Context, taskID uuid.UUID) (*model.TaskStatus, error) {
	task, err := s.taskManager.GetTask(taskID)
	if err != nil {
		return nil, fmt.Errorf("ошибка при получении задачи: %w", err)
	}

	status := &model.TaskStatus{
		ID:        task.ID,
		Status:    string(task.Status),
		Progress:  task.Progress,
		Message:   task.Message,
		Result:    task.Result,
		CreatedAt: task.CreatedAt,
		UpdatedAt: task.UpdatedAt,
	}

	return status, nil
}

// PublishNovel публикует новеллу, делая ее доступной для других игроков
func (s *NovelService) PublishNovel(ctx context.Context, novelID uuid.UUID) error {
	novel, err := s.repo.GetByID(ctx, novelID)
	if err != nil {
		return fmt.Errorf("новелла не найдена: %w", err)
	}

	novel.IsPublic = true
	_, err = s.repo.Update(ctx, novel)
	if err != nil {
		return fmt.Errorf("ошибка при публикации новеллы: %w", err)
	}

	// Отправляем оповещение о новой публичной новелле
	s.wsNotifier.Broadcast("notification", "novel.published", map[string]interface{}{
		"novel_id":     novelID.String(),
		"title":        novel.Title,
		"description":  novel.Description,
		"author_id":    novel.AuthorID.String(),
		"published_at": novel.PublishedAt,
	})

	return nil
}

// GetNovelState получает состояние новеллы для пользователя
func (s *NovelService) GetNovelState(ctx context.Context, userID, novelID uuid.UUID) (model.NovelState, error) {
	return s.repo.GetNovelState(ctx, userID, novelID)
}

// SaveNovelState сохраняет состояние новеллы
func (s *NovelService) SaveNovelState(ctx context.Context, state model.NovelState) (model.NovelState, error) {
	return s.repo.SaveNovelState(ctx, state)
}

// DeleteNovelState удаляет состояние новеллы (сохранение) для пользователя
func (s *NovelService) DeleteNovelState(ctx context.Context, userID, novelID uuid.UUID) error {
	return s.repo.DeleteNovelState(ctx, userID, novelID)
}

// ModifyNovelDraftAsync асинхронно модифицирует существующий драфт новеллы
func (s *NovelService) ModifyNovelDraftAsync(ctx context.Context, draftID, userID uuid.UUID, modificationPrompt string) (uuid.UUID, error) {
	taskCtx := context.Background()

	taskParams := struct {
		DraftID            uuid.UUID
		UserID             uuid.UUID
		ModificationPrompt string
	}{
		DraftID:            draftID,
		UserID:             userID,
		ModificationPrompt: modificationPrompt,
	}

	// Создаем задачу с указанием владельца
	taskID, err := s.taskManager.SubmitTaskWithOwner(
		taskCtx,
		s.modifyNovelDraftTask,
		taskParams,
		userID.String(), // Передаем ID пользователя как владельца задачи
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("ошибка при создании задачи модификации: %w", err)
	}

	return taskID, nil
}

// GetDraftsByUserID получает все черновики пользователя
func (s *NovelService) GetDraftsByUserID(ctx context.Context, userID uuid.UUID) ([]model.NovelDraft, error) {
	return s.repo.GetDraftsByUserID(ctx, userID)
}

// GetDraftViewByID получает детальную информацию о черновике в виде NovelDraftView
func (s *NovelService) GetDraftViewByID(ctx context.Context, draftID, userID uuid.UUID) (model.NovelDraftView, error) {
	// Получаем полный драфт из репозитория
	draft, err := s.repo.GetDraftByID(ctx, draftID)
	if err != nil {
		log.Error().Err(err).Str("draftID", draftID.String()).Msg("Не удалось получить драфт для просмотра деталей")
		return model.NovelDraftView{}, fmt.Errorf("черновик с ID %s не найден: %w", draftID.String(), err)
	}

	// Проверяем, что пользователь является владельцем драфта
	if draft.UserID != userID {
		log.Warn().Str("draftID", draftID.String()).Str("requestUserID", userID.String()).Str("ownerUserID", draft.UserID.String()).Msg("Попытка просмотра деталей чужого драфта")
		return model.NovelDraftView{}, fmt.Errorf("доступ запрещен: вы не являетесь владельцем этого драфта")
	}

	// Конвертируем Config в NovelDraftView
	config := draft.Config
	draftView := model.NovelDraftView{
		ID:                draft.ID, // Используем ID драфта
		Title:             config.Title,
		ShortDescription:  config.ShortDescription,
		Franchise:         config.Franchise,
		Genre:             config.Genre,
		IsAdultContent:    config.IsAdultContent,
		PlayerName:        config.PlayerName,
		PlayerGender:      config.PlayerGender,
		PlayerDescription: config.PlayerDescription,
		WorldContext:      config.WorldContext,
		CoreStats:         make(map[string]model.CoreStatView),
		Themes:            config.PlayerPrefs.Themes,
	}

	for name, stat := range config.CoreStats {
		draftView.CoreStats[name] = stat.ToView()
	}

	return draftView, nil
}

// modifyNovelDraftTask обрабатывает задачу модификации драфта новеллы
func (s *NovelService) modifyNovelDraftTask(ctx context.Context, params interface{}) (interface{}, error) {
	// Преобразуем параметры задачи
	taskData, ok := params.(struct {
		DraftID            uuid.UUID
		UserID             uuid.UUID
		ModificationPrompt string
	})
	if !ok {
		return nil, errors.New("неверный тип параметров для modifyNovelDraftTask")
	}

	// 1. Получаем существующий драфт из БД
	currentDraft, err := s.repo.GetDraftByID(ctx, taskData.DraftID) // Вам нужно реализовать этот метод в репозитории
	if err != nil {
		log.Error().Err(err).Str("draftID", taskData.DraftID.String()).Msg("Не удалось получить драфт для модификации")
		return nil, fmt.Errorf("драфт с ID %s не найден: %w", taskData.DraftID.String(), err)
	}

	// 2. Проверяем владельца драфта
	if currentDraft.UserID != taskData.UserID {
		log.Warn().Str("draftID", taskData.DraftID.String()).Str("requestedUserID", taskData.UserID.String()).Str("ownerUserID", currentDraft.UserID.String()).Msg("Попытка модификации чужого драфта")
		return nil, fmt.Errorf("доступ запрещен: вы не являетесь владельцем этого драфта")
	}

	// 3. Формируем запрос к AI
	aiRequest := model.NarratorPromptRequest{
		UserPrompt: taskData.ModificationPrompt, // Используем текст модификации как основной промпт для AI
		PrevConfig: &currentDraft.Config,        // Передаем текущую конфигурацию
	}

	// 4. Вызываем нарратор для генерации обновленного конфига
	response, err := s.aiClient.GenerateWithNarrator(ctx, aiRequest)
	if err != nil {
		return nil, fmt.Errorf("ошибка при генерации модифицированного драфта новеллы: %w", err)
	}

	cleanedResponse := cleanAIResponse(response)

	// 5. Парсим JSON-ответ в новую структуру NovelConfig
	var newConfig model.NovelConfig
	if err := json.Unmarshal([]byte(cleanedResponse), &newConfig); err != nil {
		log.Error().Err(err).Str("cleanedResponse", cleanedResponse).Msg("Ошибка при разборе ответа нарратора при модификации")
		return nil, fmt.Errorf("ошибка при разборе ответа нарратора при модификации: %w", err)
	}

	// 6. Обновляем драфт в БД
	updatedDraft := currentDraft                          // Копируем существующий драфт
	updatedDraft.Config = newConfig                       // Заменяем конфиг на новый
	updatedDraft.UserPrompt = taskData.ModificationPrompt // Опционально: можно сохранить последнюю модификацию

	// Используем UpdateNovelDraft - вам нужно реализовать этот метод
	savedDraft, err := s.repo.UpdateNovelDraft(ctx, updatedDraft)
	if err != nil {
		log.Error().Err(err).Str("draftID", taskData.DraftID.String()).Msg("Ошибка при обновлении модифицированного черновика новеллы в БД")
		return nil, fmt.Errorf("ошибка при обновлении драфта: %w", err)
	}

	log.Info().Str("draft_id", savedDraft.ID.String()).Msg("Модифицированный черновик новеллы успешно обновлен в БД")

	// 7. Создаем урезанную версию для ответа клиенту
	draftView := model.NovelDraftView{
		ID:                savedDraft.ID, // ID остается тем же!
		Title:             newConfig.Title,
		ShortDescription:  newConfig.ShortDescription,
		Franchise:         newConfig.Franchise,
		Genre:             newConfig.Genre,
		IsAdultContent:    newConfig.IsAdultContent,
		PlayerName:        newConfig.PlayerName,
		PlayerGender:      newConfig.PlayerGender,
		PlayerDescription: newConfig.PlayerDescription,
		WorldContext:      newConfig.WorldContext,
		CoreStats:         make(map[string]model.CoreStatView),
		Themes:            newConfig.PlayerPrefs.Themes,
	}

	for name, stat := range newConfig.CoreStats {
		draftView.CoreStats[name] = stat.ToView()
	}

	// Возвращаем урезанную версию как результат задачи
	return draftView, nil
}
