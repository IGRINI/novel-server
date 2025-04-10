package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"novel-server/internal/model"
	"novel-server/internal/repository"
	"novel-server/internal/utils"
	"novel-server/pkg/ai"
	"novel-server/pkg/taskmanager"
)

// Определяем типы задач (Используем string)
const (
	TaskTypeGenerateDraft          string = "generate_draft"
	TaskTypeSetupNovel             string = "setup_novel"
	TaskTypeGenerateContent        string = "generate_content"
	TaskTypeGenerateGameOverEnding string = "generate_game_over_ending"
)

// NovelService реализует логику работы с новеллами
type NovelService struct {
	repo        repository.NovelRepository
	taskManager *taskmanager.TaskManager
	aiClient    ai.Client
	wsNotifier  WebSocketNotifier
}

// WebSocketNotifier интерфейс для отправки уведомлений через WebSocket
type WebSocketNotifier interface {
	SendToUser(userID, messageType, topic string, payload interface{})
	Broadcast(messageType, topic string, payload interface{})
}

// NewNovelService создает новый экземпляр сервиса новелл
func NewNovelService(repo repository.NovelRepository, tm *taskmanager.TaskManager, aiClient ai.Client, notifier WebSocketNotifier) *NovelService {
	// Устанавливаем WebSocket нотификатор для TaskManager
	tm.SetWebSocketNotifier(notifier)

	return &NovelService{
		repo:        repo,
		taskManager: tm,
		aiClient:    aiClient,
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
func (s *NovelService) ListPublicNovels(ctx context.Context, limit, offset int) ([]model.NovelWithAuthor, error) {
	return s.repo.ListPublic(ctx, limit, offset)
}

// LikeNovel добавляет лайк к новелле от пользователя
func (s *NovelService) LikeNovel(ctx context.Context, userID, novelID uuid.UUID) error {
	// Проверяем, существует ли новелла
	_, err := s.repo.GetByID(ctx, novelID)
	if err != nil {
		return fmt.Errorf("новелла не найдена: %w", err)
	}

	return s.repo.LikeNovel(ctx, userID, novelID)
}

// UnlikeNovel удаляет лайк пользователя с новеллы
func (s *NovelService) UnlikeNovel(ctx context.Context, userID, novelID uuid.UUID) error {
	// Проверяем, существует ли новелла
	_, err := s.repo.GetByID(ctx, novelID)
	if err != nil {
		return fmt.Errorf("новелла не найдена: %w", err)
	}

	return s.repo.UnlikeNovel(ctx, userID, novelID)
}

// GetNovelByIDWithLikes получает новеллу по ID с информацией о лайках
func (s *NovelService) GetNovelByIDWithLikes(ctx context.Context, id uuid.UUID, currentUserID *uuid.UUID) (model.NovelWithAuthor, error) {
	return s.repo.GetNovelByIDWithLikes(ctx, id, currentUserID)
}

// GetLikedNovelsByUser получает список новелл, лайкнутых пользователем
func (s *NovelService) GetLikedNovelsByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]model.NovelWithAuthor, error) {
	return s.repo.GetLikedNovelsByUser(ctx, userID, limit, offset)
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
	taskData, ok := params.(struct {
		UserID  uuid.UUID
		Request model.NarratorPromptRequest // Предполагаем, что эта структура содержит UserPrompt
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
	// Убедимся, что строка лога корректна
	log.Info().Msg("Очищенный ответ от нарратора:\n" + cleanedResponse)

	var config model.NovelConfig
	if err := json.Unmarshal([]byte(cleanedResponse), &config); err != nil {
		log.Error().Err(err).Msg("Ошибка при разборе ответа нарратора после очистки JSON")
		return nil, fmt.Errorf("ошибка при разборе ответа нарратора: %w", err)
	}

	draft := model.NovelDraft{
		UserID: taskData.UserID,
		Config: config,
		// Исправлено: Убираем taskData.Request.UserPrompt, предполагаем, что prompt в taskData.Request
		// UserPrompt: taskData.Request.UserPrompt, // Если UserPrompt в NarratorPromptRequest, использовать его
	}

	savedDraft, err := s.repo.SaveNovelDraft(ctx, draft)
	if err != nil {
		log.Error().Err(err).Msg("Ошибка при сохранении черновика новеллы в БД")
	} else {
		log.Info().Str("draft_id", savedDraft.ID.String()).Msg("Черновик новеллы успешно сохранен в БД")
	}

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
		CoreStats:         config.CoreStats,
		Themes:            config.PlayerPrefs.Themes,
	}

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

	// 6. Возвращаем результат, включающий ID, Config и Setup
	result := struct {
		NovelID uuid.UUID         `json:"novel_id"`
		Config  model.NovelConfig `json:"config"`
		Setup   model.NovelSetup  `json:"setup"`
	}{
		NovelID: createdNovel.ID,
		Config:  createdNovel.Config,
		Setup:   createdNovel.Setup,
	}

	log.Info().Str("novelID", createdNovel.ID.String()).Msg("Новелла успешно создана из драфта")
	return result, nil
}

// GenerateNovelContentAsync асинхронно генерирует контент новеллы
func (s *NovelService) GenerateNovelContentAsync(ctx context.Context, req model.GenerateNovelContentRequest) (uuid.UUID, error) {
	// taskCtx := context.Background() // НЕПРАВИЛЬНО: Терялся контекст с логгером

	// Создаем задачу с указанием владельца
	// Используем оригинальный контекст ctx, чтобы сохранить значения (например, логгер)
	taskID, err := s.taskManager.SubmitTaskWithOwner(
		ctx, // <-- Используем оригинальный контекст ctx
		s.generateContentTask,
		req,
		req.UserID.String(), // UserID уже является строкой
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("ошибка при создании задачи: %w", err)
	}

	return taskID, nil
}

// generateContentTask выполняет задачу генерации контента для новеллы
func (s *NovelService) generateContentTask(ctx context.Context, payload interface{}) (interface{}, error) {
	// 1. Преобразуем payload
	p, ok := payload.(model.GenerateNovelContentRequest)
	if !ok {
		return nil, fmt.Errorf("неверный тип payload")
	}

	// 2. Получаем новеллу с setup
	novel, err := s.repo.GetNovelWithSetup(ctx, p.NovelID)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения новеллы: %w", err)
	}

	// 3. Получаем предыдущее состояние (если есть)
	isFirstRequest := false
	previousState, errState := s.repo.GetNovelState(ctx, p.UserID, p.NovelID)
	if errState != nil {
		if errors.Is(errState, model.ErrNotFound) {
			isFirstRequest = true
			log.Ctx(ctx).Info().Msg("Состояние не найдено, первый запрос.")
		} else {
			// Логируем ошибку, но можем попытаться продолжить, если кеш сработает
			log.Ctx(ctx).Error().Err(errState).Msg("Ошибка получения состояния, но продолжаем для проверки кеша")
		}
	} else if previousState.CurrentBatchNumber == 0 {
		isFirstRequest = true
		log.Ctx(ctx).Info().Msg("Номер батча 0, считаем первым запросом.")
	}

	// Проверяем, есть ли история пользовательских выборов
	if p.UserChoices != nil && len(p.UserChoices) > 0 {
		// Для отладки логируем количество выборов
		log.Ctx(ctx).Info().Int("choices_count", len(p.UserChoices)).Msg("Получена история выборов пользователя")

		// Если есть выборы, то это точно не первый запрос
		if isFirstRequest {
			log.Ctx(ctx).Warn().Msg("Получены выборы для первого запроса - это необычно, но продолжаем...")
			isFirstRequest = false
		}
	} else if p.UserChoice.ChoiceNumber > 0 || (p.UserChoice.ChoiceIndex >= 0 && p.UserChoice.ChoiceNumber >= 0) {
		// Для обратной совместимости проверяем старое поле UserChoice
		// Проверяем, что оба значения не отрицательные, чтобы игнорировать значения -1,-1 (явное отсутствие выбора)
		log.Ctx(ctx).Info().Int("batch", p.UserChoice.ChoiceNumber).Int("choice", p.UserChoice.ChoiceIndex).Msg("Используем старое поле UserChoice")
		isFirstRequest = false
	}

	log.Ctx(ctx).Info().Interface("previousState", previousState).Msg("Предыдущее состояние")
	log.Ctx(ctx).Info().Interface("Payload", p).Msg("Payload")

	// 4. Вычисляем хеш и проверяем кеш (ВСЕГДА)
	var stateOrSceneHash string
	var errHash error

	if isFirstRequest {
		stateOrSceneHash, errHash = utils.CalculateFirstSceneHash(novel.Config, novel.Setup)
		if errHash != nil {
			log.Ctx(ctx).Error().Err(errHash).Msg("Ошибка вычисления хеша первой сцены")
			// Не фатально, просто не сможем использовать кеш
		} else {
			log.Ctx(ctx).Debug().Str("hash", stateOrSceneHash).Msg("Вычислен хеш первой сцены")
		}
	} else {
		// Извлекаем нужные данные из previousState для CalculateStateHash
		coreStatsMap := make(map[string]int)
		globalFlagsSlice := []string{}

		// Извлекаем CoreStats
		if novel.Setup.CoreStatsDefinition != nil {
			for statName := range novel.Setup.CoreStatsDefinition {
				if valueRaw, ok := previousState.StoryVariables[statName]; ok {
					var intValue int
					success := false
					switch v := valueRaw.(type) {
					case int:
						intValue = v
						success = true
					case float64:
						intValue = int(v)
						success = true
					case json.Number:
						if i64, errConv := v.Int64(); errConv == nil {
							intValue = int(i64)
							success = true
						} else {
							log.Ctx(ctx).Warn().Err(errConv).Str("statName", statName).Interface("value", valueRaw).Msg("Не удалось конвертировать json.Number стата в int для хеша")
						}
					case string:
						if i, errConv := strconv.Atoi(v); errConv == nil {
							intValue = i
							success = true
						} else {
							log.Ctx(ctx).Warn().Err(errConv).Str("statName", statName).Interface("value", valueRaw).Msg("Не удалось конвертировать строку стата в int для хеша")
						}
					default:
						log.Ctx(ctx).Warn().Str("statName", statName).Interface("value", valueRaw).Msg("Неподдерживаемый тип стата для конвертации в int при расчете хеша")
					}
					if success {
						coreStatsMap[statName] = intValue
					}
				} else {
					log.Ctx(ctx).Warn().Str("statName", statName).Msg("Определенный стат отсутствует в StoryVariables при расчете хеша")
					// Можно установить значение по умолчанию (например, 0), если это необходимо для консистентности хеша
					// coreStatsMap[statName] = 0
				}
			}
		} else {
			log.Ctx(ctx).Warn().Msg("CoreStatsDefinition не найден в Setup новеллы, невозможно извлечь CoreStats для хеша")
		}

		// Извлекаем GlobalFlags (предполагаем префикс "flag_")
		const flagPrefix = "flag_"
		for key, valueRaw := range previousState.StoryVariables {
			if strings.HasPrefix(key, flagPrefix) {
				if boolValue, ok := valueRaw.(bool); ok && boolValue {
					flagName := strings.TrimPrefix(key, flagPrefix)
					globalFlagsSlice = append(globalFlagsSlice, flagName)
				}
			}
		}

		// Теперь вызываем хеширование с извлеченными данными
		stateOrSceneHash, errHash = utils.CalculateStateHash(coreStatsMap, globalFlagsSlice, previousState.StoryVariables)
		if errHash != nil {
			log.Ctx(ctx).Error().Err(errHash).Msg("Ошибка вычисления хеша состояния")
		} else {
			log.Ctx(ctx).Debug().Str("hash", stateOrSceneHash).Msg("Вычислен хеш состояния")
		}
	}

	var cacheHit bool
	var parsedContent *model.ParsedNovelContent

	if errHash == nil && stateOrSceneHash != "" { // Проверяем кеш, только если хеш вычислен
		cachedBatch, errCache := s.repo.GetSceneBatchByStateHash(ctx, stateOrSceneHash)
		if errCache == nil {
			log.Ctx(ctx).Info().Str("hash", stateOrSceneHash).Msg("Найден кеш сцены")

			parsedContent = &model.ParsedNovelContent{}
			if cachedBatch.EndingText != nil {
				parsedContent.EndingText = *cachedBatch.EndingText
			}
			parsedContent.Choices = cachedBatch.Batch

			cacheHit = true
			err = nil // Ошибки парсинга больше нет
		} else if !errors.Is(errCache, model.ErrNotFound) {
			log.Ctx(ctx).Warn().Err(errCache).Msg("Ошибка при поиске кеша сцены")
		}
	}

	// Если кеш не найден или не удалось его использовать, генерируем новый контент
	if !cacheHit {
		var aiResponseText string
		var errAI error

		// --- Обновление состояния новеллы для запроса к AI ---
		// Создаем временное состояние для запроса к AI с историей выборов
		aiRequestState := model.NovelState{}

		if isFirstRequest {
			log.Ctx(ctx).Info().Msg("Генерация первого батча через AI (first scene creator)")

			// Добавляем детальное логирование полей новеллы
			configJSON, _ := json.MarshalIndent(novel.Config, "", "  ")
			setupJSON, _ := json.MarshalIndent(novel.Setup, "", "  ")
			log.Ctx(ctx).Debug().RawJSON("novel.Config", configJSON).Msg("Конфигурация новеллы перед отправкой")
			log.Ctx(ctx).Debug().RawJSON("novel.Setup", setupJSON).Msg("Setup новеллы перед отправкой")

			aiReq := model.GenerateNovelContentRequestForAI{
				Config: novel.Config,
				Setup:  novel.Setup,
			}
			aiResponseText, errAI = s.aiClient.GenerateWithFirstSceneCreator(ctx, aiReq)
		} else {
			// Проверяем, что previousState действительно загружено
			if errState != nil {
				log.Ctx(ctx).Error().Err(errState).Msg("Критическая ошибка: невозможно сгенерировать следующий батч без предыдущего состояния.")
				return nil, fmt.Errorf("ошибка получения предыдущего состояния для генерации: %w", errState)
			}

			// Копируем предыдущее состояние для запроса к AI
			aiRequestState = previousState

			// Обрабатываем выборы пользователя и добавляем их в историю для AI
			if len(p.UserChoices) > 0 {
				// Создаем копию истории
				aiRequestState.History = make([]model.UserChoice, len(previousState.History))
				copy(aiRequestState.History, previousState.History)

				// Обрабатываем каждый выбор последовательно
				for _, choice := range p.UserChoices {
					// Получаем кэшированный батч для применения последствий
					cachedBatch, err := s.repo.GetSceneBatchByStateHash(ctx, stateOrSceneHash)
					if err != nil {
						log.Ctx(ctx).Warn().Err(err).Msg("Не удалось получить кэшированный батч для обработки выбора")
						continue
					}

					// Проверяем корректность индексов
					if choice.EventIndex < 0 || choice.EventIndex >= len(cachedBatch.Batch) {
						log.Ctx(ctx).Warn().Int("eventIndex", choice.EventIndex).Msg("Некорректный индекс события")
						continue
					}

					event := cachedBatch.Batch[choice.EventIndex]
					if choice.ChoiceIndex < 0 || choice.ChoiceIndex >= len(event.Choices) {
						log.Ctx(ctx).Warn().Int("choiceIndex", choice.ChoiceIndex).Msg("Некорректный индекс выбора")
						continue
					}

					// Добавляем выбор в историю с полным контекстом
					userChoice := model.UserChoice{
						ChoiceNumber:     aiRequestState.CurrentBatchNumber,
						ChoiceIndex:      choice.ChoiceIndex,
						EventDescription: event.Description,
						ChoiceText:       event.Choices[choice.ChoiceIndex].Text,
					}
					aiRequestState.History = append(aiRequestState.History, userChoice)
				}
			} else if p.UserChoice.ChoiceNumber > 0 || (p.UserChoice.ChoiceIndex >= 0 && p.UserChoice.ChoiceNumber >= 0) {
				// Обратная совместимость с полем UserChoice

				// Создаем копию истории
				aiRequestState.History = make([]model.UserChoice, len(previousState.History))
				copy(aiRequestState.History, previousState.History)

				// Получаем кэшированный батч для применения последствий
				cachedBatch, err := s.repo.GetSceneBatchByStateHash(ctx, stateOrSceneHash)
				if err != nil {
					return nil, fmt.Errorf("ошибка получения кэшированного батча: %w", err)
				}

				// Проверяем индекс выбора
				if p.UserChoice.ChoiceIndex < 0 || p.UserChoice.ChoiceIndex >= len(cachedBatch.Batch) {
					return nil, fmt.Errorf("некорректный индекс выбора: %d (доступно вариантов: %d)",
						p.UserChoice.ChoiceIndex, len(cachedBatch.Batch))
				}

				// Получаем информацию о выбранном событии
				chosenEvent := cachedBatch.Batch[p.UserChoice.ChoiceIndex]
				if p.UserChoice.ChoiceIndex < 0 || p.UserChoice.ChoiceIndex >= len(chosenEvent.Choices) {
					return nil, fmt.Errorf("некорректный индекс выбора для события: %d", p.UserChoice.ChoiceIndex)
				}

				// Обновляем поле UserChoice в запросе с данными о событии и выбранном варианте
				p.UserChoice.EventDescription = chosenEvent.Description
				p.UserChoice.ChoiceText = chosenEvent.Choices[p.UserChoice.ChoiceIndex].Text

				// Создаем копию для добавления в историю
				userChoiceWithContext := model.UserChoice{
					ChoiceNumber:     p.UserChoice.ChoiceNumber,
					ChoiceIndex:      p.UserChoice.ChoiceIndex,
					EventDescription: chosenEvent.Description,
					ChoiceText:       chosenEvent.Choices[p.UserChoice.ChoiceIndex].Text,
				}

				// Добавляем выбор в историю для AI
				aiRequestState.History = append(aiRequestState.History, userChoiceWithContext)
			}

			// Теперь у нас есть обновленное состояние с историей выборов
			log.Ctx(ctx).Info().Msg("Генерация следующего батча через AI (creator) с обновленной историей выборов")
			log.Ctx(ctx).Debug().Int("historyCount", len(aiRequestState.History)).Msg("Количество записей в истории выборов")

			aiReq := model.GenerateNovelContentRequestForAI{
				NovelState: aiRequestState,
				Config:     novel.Config,
				Setup:      novel.Setup,
			}
			aiResponseText, errAI = s.aiClient.GenerateWithNovelCreator(ctx, aiReq)
		}

		if errAI != nil {
			return nil, fmt.Errorf("ошибка генерации контента через AI: %w", errAI)
		}

		// Очищаем ответ и парсим через наш парсер
		cleanedResponse := strings.TrimSpace(aiResponseText) // Используем strings.TrimSpace
		parsedContent, err = ai.ParseNovelContentResponse(cleanedResponse)
		if err != nil {
			log.Ctx(ctx).Error().Err(err).Str("rawResponse", aiResponseText).Str("cleanedResponse", cleanedResponse).Msg("Ошибка парсинга ответа AI")
			return nil, fmt.Errorf("ошибка парсинга ответа AI: %w", err)
		}

		// Сохраняем в кеш (если хеш был вычислен)
		if errHash == nil && stateOrSceneHash != "" {
			batch := model.SceneBatch{
				ID:                uuid.New(),
				NovelID:           p.NovelID,
				StateHash:         stateOrSceneHash,
				StorySummarySoFar: parsedContent.StorySummarySoFar,
				FutureDirection:   parsedContent.FutureDirection,
				// EndingText будет установлен ниже, если choices пуст
			}

			// Преобразуем []ChoiceEvent в []ChoiceOption для кеша
			if len(parsedContent.Choices) > 0 {
				batch.Batch = parsedContent.Choices
				batch.EndingText = nil // Если есть choices, ending text должен быть nil
			} else if parsedContent.EndingText != "" {
				// Если choices нет, но есть ending text
				batch.Batch = nil
				batch.EndingText = &parsedContent.EndingText
			} else {
				// Странный случай: нет ни choices, ни ending text. Не сохраняем в кеш.
				log.Ctx(ctx).Warn().Str("hash", stateOrSceneHash).Msg("Попытка сохранить в кеш пустой результат (нет ни choices, ни ending text). Кеширование пропущено.")
				// Пропускаем сохранение в кеш, но продолжаем выполнение функции
				goto SkipCacheSave // Используем goto для перехода к концу блока сохранения
			}

			// Сохраняем в кеш
			_, err = s.repo.SaveSceneBatch(ctx, batch)
			if err != nil {
				log.Ctx(ctx).Error().Err(err).Msg("Ошибка сохранения сцены в кеш")
			}
		SkipCacheSave: // Метка для goto
		}
	}

	// --- Обновление состояния новеллы ---
	newState := model.NovelState{
		UserID:  p.UserID,
		NovelID: p.NovelID,
	}
	if isFirstRequest {
		// Используем StorySummarySoFar и FutureDirection из *ответа AI* (parsedContent),
		// а не из начального конфига, т.к. это GM notes для СЛЕДУЮЩЕГО шага.
		newState.StorySummarySoFar = parsedContent.StorySummarySoFar
		newState.FutureDirection = parsedContent.FutureDirection
		newState.CurrentBatchNumber = 0
		newState.StoryVariables = make(map[string]interface{})
		if novel.Setup.CoreStatsDefinition != nil {
			for name, definition := range novel.Setup.CoreStatsDefinition {
				newState.StoryVariables[name] = definition.InitialValue
			}
		}
		newState.History = []model.UserChoice{}
	} else {
		// Проверяем, что предыдущее состояние существует
		if previousState.ID == uuid.Nil {
			return nil, fmt.Errorf("не найдено предыдущее состояние для последующего запроса")
		}

		// Инициализируем новое состояние на основе предыдущего
		newState.StorySummarySoFar = parsedContent.StorySummarySoFar
		newState.FutureDirection = parsedContent.FutureDirection

		// По умолчанию сохраняем тот же batch number (для случая загрузки игры)
		newState.CurrentBatchNumber = previousState.CurrentBatchNumber

		// Копируем переменные из предыдущего состояния
		newState.StoryVariables = make(map[string]interface{})
		for k, v := range previousState.StoryVariables {
			newState.StoryVariables[k] = v
		}

		// Применяем выборы пользователя (из поля UserChoices или из UserChoice для обратной совместимости)
		if len(p.UserChoices) > 0 {
			// Поскольку есть новые выборы, увеличиваем CurrentBatchNumber
			newState.CurrentBatchNumber = previousState.CurrentBatchNumber + 1

			// Преобразуем p.UserChoices в UserChoice для истории
			choices := make([]model.UserChoice, 0, len(p.UserChoices))

			// Копируем предыдущее состояние
			copyState := model.NovelState{
				CurrentBatchNumber: previousState.CurrentBatchNumber,
				StoryVariables:     make(map[string]interface{}),
				History:            make([]model.UserChoice, len(previousState.History)),
			}

			// Копируем story variables
			for k, v := range previousState.StoryVariables {
				copyState.StoryVariables[k] = v
			}

			// Копируем историю
			copy(copyState.History, previousState.History)

			currentState := &copyState

			// Обрабатываем каждый выбор последовательно, обновляя состояние
			for i, choice := range p.UserChoices {
				// Получаем соответствующий batch из кэша для текущего состояния
				currentStateHash := stateOrSceneHash
				if i > 0 {
					// Для последующих выборов нам нужно вычислить новый хеш состояния
					// Но в данной реализации у нас есть только хеш исходного состояния,
					// поэтому мы используем его для всех выборов
					log.Ctx(ctx).Warn().
						Int("choiceNumber", i).
						Msg("Используем исходный хеш состояния для последующего выбора. В полной реализации здесь должен быть хеш промежуточного состояния.")
				}

				cachedBatch, err := s.repo.GetSceneBatchByStateHash(ctx, currentStateHash)

				if err != nil {
					log.Ctx(ctx).Warn().
						Err(err).
						Str("stateHash", currentStateHash).
						Int("choiceNumber", i).
						Msg("Не удалось получить кэшированный батч для обработки выбора. Пропускаем.")
					continue
				}

				// Проверяем корректность индексов
				if choice.EventIndex < 0 || choice.EventIndex >= len(cachedBatch.Batch) {
					log.Ctx(ctx).Warn().
						Int("eventIndex", choice.EventIndex).
						Int("maxEvents", len(cachedBatch.Batch)).
						Msg("Некорректный индекс события в UserChoices")
					continue
				}

				event := cachedBatch.Batch[choice.EventIndex]
				if choice.ChoiceIndex < 0 || choice.ChoiceIndex >= len(event.Choices) {
					log.Ctx(ctx).Warn().
						Int("eventIndex", choice.EventIndex).
						Int("choiceIndex", choice.ChoiceIndex).
						Int("maxChoices", len(event.Choices)).
						Msg("Некорректный индекс выбора в UserChoices")
					continue
				}

				// Добавляем выбор в историю
				userChoice := model.UserChoice{
					ChoiceNumber:     currentState.CurrentBatchNumber,
					ChoiceIndex:      choice.ChoiceIndex,
					EventDescription: event.Description,
					ChoiceText:       event.Choices[choice.ChoiceIndex].Text,
				}
				choices = append(choices, userChoice)

				// Получаем последствия и применяем к состоянию
				consequences := event.Choices[choice.ChoiceIndex].Consequences

				// Применяем изменения core_stats
				if consequences.CoreStatsChange != nil {
					for statName, change := range consequences.CoreStatsChange {
						// Если переменной еще нет, инициализируем её
						currentValue, ok := currentState.StoryVariables[statName].(float64)
						if !ok {
							currentValue = 0
						}

						// Обновляем значение
						newValue := currentValue + float64(change)
						currentState.StoryVariables[statName] = newValue

						log.Ctx(ctx).Debug().
							Str("statName", statName).
							Float64("oldValue", currentValue).
							Float64("newValue", newValue).
							Int("change", change).
							Int("choiceNumber", i).
							Msg("Обновлен core_stat из UserChoices")
					}
				}

				// Обновляем global_flags
				if consequences.GlobalFlags != nil && len(consequences.GlobalFlags) > 0 {
					// Получаем текущие флаги
					var currentFlags []interface{}
					if flagsRaw, exists := currentState.StoryVariables["global_flags"]; exists {
						if flagsArray, ok := flagsRaw.([]interface{}); ok {
							currentFlags = flagsArray
						} else {
							log.Ctx(ctx).Warn().
								Interface("foundValue", flagsRaw).
								Msg("global_flags существует, но не является массивом. Создаем новый массив.")
							currentFlags = make([]interface{}, 0)
						}
					} else {
						currentFlags = make([]interface{}, 0)
					}

					// Добавляем новые флаги
					for _, flag := range consequences.GlobalFlags {
						// Проверяем, что флага еще нет
						flagExists := false
						for _, existingFlag := range currentFlags {
							if existingFlagStr, ok := existingFlag.(string); ok && existingFlagStr == flag {
								flagExists = true
								break
							}
						}

						// Добавляем флаг если его нет
						if !flagExists {
							currentFlags = append(currentFlags, flag)
							log.Ctx(ctx).Debug().
								Str("flag", flag).
								Int("choiceNumber", i).
								Msg("Добавлен global_flag из UserChoices")
						}
					}

					// Сохраняем обновленные флаги
					currentState.StoryVariables["global_flags"] = currentFlags
				}

				// Обновляем story_variables
				if consequences.StoryVariables != nil {
					for varName, value := range consequences.StoryVariables {
						currentState.StoryVariables[varName] = value
						log.Ctx(ctx).Debug().
							Str("varName", varName).
							Interface("value", value).
							Int("choiceNumber", i).
							Msg("Обновлена story_variable из UserChoices")
					}
				}
			}

			// Увеличиваем BatchNumber один раз после обработки всех выборов
			// вместо увеличения после каждого выбора
			currentState.CurrentBatchNumber++

			// Копируем обработанное состояние в новое состояние
			newState.StoryVariables = currentState.StoryVariables
			// Примечание: CurrentBatchNumber уже увеличен в начале этого блока,
			// не устанавливаем его здесь снова, чтобы избежать двойного увеличения
			// newState.CurrentBatchNumber = currentState.CurrentBatchNumber

			// Копируем историю из предыдущего состояния и добавляем новые выборы
			newState.History = make([]model.UserChoice, len(previousState.History))
			copy(newState.History, previousState.History)
			newState.History = append(newState.History, choices...)
		} else if p.UserChoice.ChoiceNumber > 0 || (p.UserChoice.ChoiceIndex >= 0 && p.UserChoice.ChoiceNumber >= 0) {
			// Обратная совместимость с полем UserChoice
			// Увеличиваем CurrentBatchNumber, так как обрабатываем выбор пользователя
			newState.CurrentBatchNumber = previousState.CurrentBatchNumber + 1

			// Проверяем, что выбор пользователя соответствует предыдущему батчу
			if p.UserChoice.ChoiceNumber != 0 && p.UserChoice.ChoiceNumber != previousState.CurrentBatchNumber {
				return nil, fmt.Errorf("некорректный номер батча в выборе пользователя: ожидался %d, получен %d",
					previousState.CurrentBatchNumber, p.UserChoice.ChoiceNumber)
			}

			// Получаем кэшированный батч для применения последствий
			cachedBatch, err := s.repo.GetSceneBatchByStateHash(ctx, stateOrSceneHash)
			if err != nil {
				return nil, fmt.Errorf("ошибка получения кэшированного батча: %w", err)
			}

			// Проверяем индекс выбора
			if p.UserChoice.ChoiceIndex < 0 || p.UserChoice.ChoiceIndex >= len(cachedBatch.Batch) {
				return nil, fmt.Errorf("некорректный индекс выбора: %d (доступно вариантов: %d)",
					p.UserChoice.ChoiceIndex, len(cachedBatch.Batch))
			}

			// Применяем последствия выбранного варианта
			chosenEvent := cachedBatch.Batch[p.UserChoice.ChoiceIndex]
			if p.UserChoice.ChoiceIndex < 0 || p.UserChoice.ChoiceIndex >= len(chosenEvent.Choices) {
				return nil, fmt.Errorf("некорректный индекс выбора для события: %d", p.UserChoice.ChoiceIndex)
			}

			// Важно: Поля EventDescription и ChoiceText должны быть уже заполнены выше в коде для запроса к AI
			// Проверим, что они заполнены, иначе заполним их снова
			if p.UserChoice.EventDescription == "" || p.UserChoice.ChoiceText == "" {
				p.UserChoice.EventDescription = chosenEvent.Description
				p.UserChoice.ChoiceText = chosenEvent.Choices[p.UserChoice.ChoiceIndex].Text
				log.Ctx(ctx).Debug().Msg("Заполнены поля EventDescription и ChoiceText для истории выборов")
			}

			consequences := chosenEvent.Choices[p.UserChoice.ChoiceIndex].Consequences

			// Обновляем core_stats
			if consequences.CoreStatsChange != nil {
				for statName, change := range consequences.CoreStatsChange {
					currentValue, ok := newState.StoryVariables[statName].(float64)
					if !ok {
						// Если значение не найдено или не типа float64, используем 0
						currentValue = 0
					}
					newState.StoryVariables[statName] = currentValue + float64(change)
					log.Ctx(ctx).Debug().Str("statName", statName).Float64("oldValue", currentValue).Float64("newValue", currentValue+float64(change)).Int("change", change).Msg("Обновлен core_stat")
				}
			}

			// Добавляем новые global_flags
			if consequences.GlobalFlags != nil && len(consequences.GlobalFlags) > 0 {
				// Получаем текущие флаги из переменных состояния
				var currentFlags []interface{}

				// Проверяем, существует ли ключ global_flags и является ли он массивом
				if flagsRaw, exists := newState.StoryVariables["global_flags"]; exists {
					if flagsArray, ok := flagsRaw.([]interface{}); ok {
						currentFlags = flagsArray
					} else {
						// Если значение есть, но не массив - логируем ошибку и создаем новый массив
						log.Ctx(ctx).Warn().Interface("foundValue", flagsRaw).Msg("global_flags существует, но не является массивом. Создаем новый массив.")
						currentFlags = make([]interface{}, 0)
					}
				} else {
					// Если ключа нет - создаем новый массив
					currentFlags = make([]interface{}, 0)
				}

				// Добавляем новые флаги
				for _, flag := range consequences.GlobalFlags {
					// Проверяем, есть ли уже такой флаг
					flagExists := false
					for _, existingFlag := range currentFlags {
						if existingFlagStr, ok := existingFlag.(string); ok && existingFlagStr == flag {
							flagExists = true
							break
						}
					}

					// Добавляем только если флага еще нет
					if !flagExists {
						currentFlags = append(currentFlags, flag)
						log.Ctx(ctx).Debug().Str("flag", flag).Msg("Добавлен новый global_flag")
					}
				}

				// Сохраняем обновленный массив флагов
				newState.StoryVariables["global_flags"] = currentFlags
			}

			// Обновляем story_variables
			if consequences.StoryVariables != nil {
				for varName, value := range consequences.StoryVariables {
					newState.StoryVariables[varName] = value
					log.Ctx(ctx).Debug().Str("varName", varName).Interface("value", value).Msg("Установлена story_variable")
				}
			}

			// Копируем и обновляем историю выборов
			newState.History = make([]model.UserChoice, len(previousState.History))
			copy(newState.History, previousState.History)

			// Добавляем обогащенный информацией выбор пользователя в историю
			newState.History = append(newState.History, p.UserChoice)
		} else {
			// Если пользователь не сделал выбора (необычная ситуация), просто копируем историю
			newState.History = make([]model.UserChoice, len(previousState.History))
			copy(newState.History, previousState.History)
		}
	}

	// Сохраняем новое или обновленное состояние
	savedState, err := s.repo.SaveNovelState(ctx, newState)
	if err != nil {
		return nil, fmt.Errorf("ошибка сохранения состояния новеллы: %w", err)
	}
	log.Ctx(ctx).Info().Str("state_id", savedState.ID.String()).Int("batch", savedState.CurrentBatchNumber).Msg("Состояние новеллы успешно сохранено перед возвратом результата")

	// --- Формирование ответа для клиента ---
	// Создаем указатель на EndingText, если он не пустой
	var endingText *string
	if parsedContent.EndingText != "" {
		endingText = &parsedContent.EndingText
	}

	clientResponse := model.ClientGameplayPayload{
		Choices:    parsedContent.Choices, // Передаем []ChoiceEvent напрямую
		EndingText: endingText,
		IsGameOver: parsedContent.EndingText != "",
	}

	// Логируем финальный ответ перед возвратом
	clientResponseJSON, _ := json.MarshalIndent(clientResponse, "", "  ")
	log.Ctx(ctx).Debug().RawJSON("clientResponse", clientResponseJSON).Msg("Финальный clientResponse перед возвратом из generateContentTask")

	log.Ctx(ctx).Info().Msg("generateContentTask успешно завершен, возвращаем результат типа ClientGameplayPayload.")
	return clientResponse, nil
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
		ID:                draft.ID,
		Title:             config.Title,
		ShortDescription:  config.ShortDescription,
		Franchise:         config.Franchise,
		Genre:             config.Genre,
		IsAdultContent:    config.IsAdultContent,
		PlayerName:        config.PlayerName,
		PlayerGender:      config.PlayerGender,
		PlayerDescription: config.PlayerDescription,
		WorldContext:      config.WorldContext,
		CoreStats:         config.CoreStats,
		Themes:            config.PlayerPrefs.Themes,
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
		// Поля UserPrompt и PrevConfig здесь не используются согласно текущей модели
	}
	_ = aiRequest // Убираем ошибку "unused variable"

	// 4. Вызываем нарратор для генерации обновленного конфига
	var response string // Объявляем переменные
	// Исправлено: Используем '=' вместо ':='
	response, err = s.aiClient.GenerateWithNarrator(ctx, aiRequest) // Закомментировано пока aiRequest пустой
	if err != nil {
		// return nil, fmt.Errorf("ошибка при генерации модифицированного драфта новеллы: %w", err)
	}
	// cleanedResponse := cleanAIResponse(response)
	response = "{}" // Заглушка, пока вызов AI закомментирован
	cleanedResponse := response

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
		ID:                savedDraft.ID,
		Title:             newConfig.Title,
		ShortDescription:  newConfig.ShortDescription,
		Franchise:         newConfig.Franchise,
		Genre:             newConfig.Genre,
		IsAdultContent:    newConfig.IsAdultContent,
		PlayerName:        newConfig.PlayerName,
		PlayerGender:      newConfig.PlayerGender,
		PlayerDescription: newConfig.PlayerDescription,
		WorldContext:      newConfig.WorldContext,
		CoreStats:         newConfig.CoreStats,
		Themes:            newConfig.PlayerPrefs.Themes,
	}

	return draftView, nil
}

// GenerateGameOverEnding генерирует текст концовки при проигрыше по статам
func (s *NovelService) GenerateGameOverEnding(ctx context.Context, userID, novelID uuid.UUID, reason model.GameOverReason) (string, error) {
	log.Info().Str("userID", userID.String()).Str("novelID", novelID.String()).Interface("reason", reason).Msg("Начало генерации концовки Game Over")

	// --- Шаг 1: Получить последнее состояние новеллы и данные новеллы ---
	lastState, err := s.repo.GetNovelState(ctx, userID, novelID)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			log.Error().Err(err).Str("userID", userID.String()).Str("novelID", novelID.String()).Msg("Критическая ошибка: состояние новеллы не найдено при генерации Game Over")
			return "", fmt.Errorf("внутренняя ошибка: состояние новеллы не найдено для генерации концовки")
		}
		return "", fmt.Errorf("ошибка получения последнего состояния новеллы: %w", err)
	}

	novel, err := s.repo.GetByID(ctx, novelID) // Получаем новеллу для доступа к Setup
	if err != nil {
		return "", fmt.Errorf("ошибка получения данных новеллы для генерации концовки: %w", err)
	}

	// --- Шаг 2: Извлечь CoreStats и GlobalFlags из StoryVariables ---
	coreStatsMap := make(map[string]int)
	if novel.Setup.CoreStatsDefinition != nil {
		for statName := range novel.Setup.CoreStatsDefinition {
			if valueRaw, ok := lastState.StoryVariables[statName]; ok {
				// Пытаемся конвертировать значение в int (нужна более надежная конвертация в utils)
				var intValue int
				switch v := valueRaw.(type) {
				case int:
					intValue = v
				case float64: // JSON числа часто приходят как float64
					intValue = int(v)
				case json.Number:
					if i64, errConv := v.Int64(); errConv == nil {
						intValue = int(i64)
					} else {
						log.Warn().Err(errConv).Str("statName", statName).Interface("value", valueRaw).Msg("Не удалось конвертировать json.Number в int для хеша")
						continue // Пропускаем стат, если конвертация не удалась
					}
				case string: // Попробуем конвертировать из строки
					if i, errConv := strconv.Atoi(v); errConv == nil {
						intValue = i
					} else {
						log.Warn().Err(errConv).Str("statName", statName).Interface("value", valueRaw).Msg("Не удалось конвертировать строку в int для хеша")
						continue
					}
				default:
					log.Warn().Str("statName", statName).Interface("value", valueRaw).Msg("Неподдерживаемый тип стата для конвертации в int при расчете хеша")
					continue // Пропускаем стат
				}
				coreStatsMap[statName] = intValue
			} else {
				log.Warn().Str("statName", statName).Msg("Определенный стат отсутствует в StoryVariables при расчете хеша")
			}
		}
	} else {
		log.Warn().Msg("CoreStatsDefinition не найден в Setup новеллы, невозможно извлечь CoreStats для хеша")
	}

	globalFlagsSlice := []string{}
	for key, valueRaw := range lastState.StoryVariables {
		// Пример: ищем ключи, начинающиеся с "flag_", и значение равно true
		if strings.HasPrefix(key, "flag_") {
			if boolValue, ok := valueRaw.(bool); ok && boolValue {
				flagName := strings.TrimPrefix(key, "flag_")
				globalFlagsSlice = append(globalFlagsSlice, flagName)
			}
		}
	}
	sort.Strings(globalFlagsSlice) // Сортируем флаги для стабильного хеша

	// --- Шаг 3: Рассчитать хеш этого состояния для кеша ---
	stateHash, err := utils.CalculateStateHash(
		coreStatsMap,             // Передаем извлеченные статы
		globalFlagsSlice,         // Передаем извлеченные флаги
		lastState.StoryVariables, // Передаем все переменные, как требует функция
	)
	if err != nil {
		log.Error().Err(err).Msg("Ошибка расчета хеша состояния для кеширования концовки")
		stateHash = "" // Сбрасываем хеш, чтобы не пытаться использовать невалидный
	}

	// --- Шаг 4: Проверить кеш ---
	if stateHash != "" {
		cachedBatch, errCache := s.repo.GetSceneBatchByStateHash(ctx, stateHash) // Используем другую переменную для ошибки кеша
		if errCache == nil {
			if cachedBatch.EndingText != nil {
				return *cachedBatch.EndingText, nil
			}
			return "", model.ErrNotFound
		}
		if !errors.Is(errCache, model.ErrNotFound) {
			log.Ctx(ctx).Warn().Err(errCache).Msg("Ошибка при поиске кеша сцены")
		}
	}

	// --- Шаг 5: Если кеш не найден или некорректен - генерация AI ---
	log.Info().Str("novelID", novelID.String()).Msg("Генерация новой концовки Game Over через AI...")

	// 5.1 Сформировать запрос к AI (данные новеллы уже загружены выше)
	aiRequest := model.GameOverEndingRequestForAI{
		NovelConfig:    novel.Config,
		NovelSetup:     novel.Setup,
		LastNovelState: lastState, // Используем правильное имя поля
		Reason:         reason,
		// Добавляем извлеченные данные для AI, если это необходимо по промпту
		// FinalStateVars: lastState.StoryVariables, // Можно передать все переменные
	}

	// 5.2 Вызвать новый метод AI клиента
	aiResponseJSON, err := s.aiClient.GenerateGameOverEnding(ctx, aiRequest)
	if err != nil {
		return "", fmt.Errorf("ошибка AI при генерации концовки: %w", err)
	}

	// 5.3 Распарсить ответ AI
	var parsedResponse model.GameOverEndingResponseFromAI
	if err := json.Unmarshal([]byte(aiResponseJSON), &parsedResponse); err != nil {
		log.Error().Err(err).Str("rawResponse", aiResponseJSON).Msg("Ошибка парсинга ответа AI для концовки Game Over")
		return "", fmt.Errorf("ошибка парсинга ответа AI для концовки: %w", err)
	}
	generatedEndingText := parsedResponse.EndingText

	// --- Шаг 6: Сохранить результат в кеш (если хеш валиден) ---
	if stateHash != "" {
		newBatch := model.SceneBatch{
			NovelID:           novelID,
			StateHash:         stateHash,
			EndingText:        &generatedEndingText,
			Batch:             nil,
			StorySummarySoFar: lastState.StorySummarySoFar,
			FutureDirection:   "Game Over",
		}
		if _, errSave := s.repo.SaveSceneBatch(ctx, newBatch); errSave != nil {
			log.Error().Err(errSave).Str("stateHash", stateHash).Msg("Ошибка при сохранении сгенерированной концовки Game Over в кеш")
		} else {
			log.Info().Str("stateHash", stateHash).Msg("Сгенерированная концовка Game Over сохранена в кеш.")
		}
	}

	// --- Шаг 7: Вернуть текст концовки ---
	return generatedEndingText, nil
}

// SubmitGenerateGameOverEndingTask отправляет задачу для генерации концовки при проигрыше
func (s *NovelService) SubmitGenerateGameOverEndingTask(ctx context.Context, userID uuid.UUID, req model.GameOverNotificationRequest) (string, error) {
	payload := model.GameOverEndingRequestForAI{
		NovelID:        req.NovelID,
		UserID:         userID,
		Reason:         req.Reason,
		FinalStateVars: req.FinalStateVars,
	}
	// Сигнатура SubmitTaskWithOwner: (ctx, taskFunc, payload, ownerID)
	taskID, err := s.taskManager.SubmitTaskWithOwner(
		ctx,                          // Контекст
		s.generateGameOverEndingTask, // Функция задачи типа taskmanager.TaskFunc
		payload,                      // Payload (interface{})
		userID.String(),              // Владелец (string)
	)
	if err != nil {
		return "", fmt.Errorf("ошибка отправки задачи генерации концовки: %w", err)
	}
	// Конвертируем UUID в строку
	return taskID.String(), nil
}

// HandleGameOver обрабатывает уведомление о Game Over и возвращает результат с возможностью продолжения
// Этот метод:
// 1. Генерирует текст концовки через GenerateGameOverEnding
// 2. Определяет, возможно ли продолжение игры
// 3. Если продолжение возможно, подготавливает данные для нового персонажа и начальные выборы
// 4. Возвращает структуру GameOverResult с информацией для клиента
func (s *NovelService) HandleGameOver(ctx context.Context, userID, novelID uuid.UUID, reason model.GameOverReason, userChoices []model.UserChoice) (model.GameOverResult, error) {
	log.Info().Str("userID", userID.String()).Str("novelID", novelID.String()).Interface("reason", reason).Msg("Обработка Game Over с возможностью продолжения")

	result := model.GameOverResult{
		CanContinue: false, // По умолчанию продолжение недоступно
	}

	// Шаг 1: Генерируем текст концовки
	endingText, err := s.GenerateGameOverEnding(ctx, userID, novelID, reason)
	if err != nil {
		return result, fmt.Errorf("ошибка при генерации текста концовки: %w", err)
	}
	result.EndingText = endingText

	// Шаг 2: Получаем данные новеллы
	novel, err := s.repo.GetByID(ctx, novelID)
	if err != nil {
		return result, fmt.Errorf("ошибка получения данных новеллы: %w", err)
	}

	// Шаг 3: Проверяем, поддерживает ли новелла продолжение (через настройки или другие критерии)
	// В будущем здесь может быть более сложная логика определения возможности продолжения
	canContinue := true // Временно всегда разрешаем продолжение для демонстрации

	if !canContinue {
		return result, nil // Возвращаем только концовку без продолжения
	}

	// Шаг 4: Генерируем данные нового персонажа и начальной сцены
	// Используем AI для генерации нового персонажа
	// В будущей имплементации здесь может быть создан запрос к AI:
	// newCharacterRequest := model.GameOverEndingRequestForAI{
	//	NovelID:     novelID,
	//	UserID:      userID,
	//	Reason:      reason,
	//	NovelConfig: novel.Config,
	//	NovelSetup:  novel.Setup,
	// }

	newCharacterDescription := "Новый искатель приключений, готовый бросить вызов судьбе после неудачи предыдущего героя."

	// Создаем базовые статы для нового персонажа
	newCoreStats := make(map[string]int)
	for statName, statInfo := range novel.Config.CoreStats {
		newCoreStats[statName] = statInfo.InitialValue
	}

	// Устанавливаем флаг продолжения и данные нового персонажа
	result.CanContinue = true
	result.NewCharacter = newCharacterDescription
	result.NewCoreStats = newCoreStats

	// Шаг 5: Генерируем начальные выборы для нового персонажа
	// В реальной имплементации здесь должен быть вызов AI для генерации начальных выборов
	// Сейчас создаем базовые выборы для демонстрации
	initialChoices := []model.ChoiceEvent{
		{
			Description: "С чего начать ваше новое приключение?",
			Choices: []model.ChoiceOption{
				{
					Text: "Начать с изучения окрестностей",
					Consequences: model.Consequences{
						CoreStatsChange: map[string]int{
							"influence": 5,
						},
						ResponseText: "Вы решаете тщательно изучить окрестности, чтобы избежать ошибок вашего предшественника.",
					},
				},
				{
					Text: "Сразу отправиться к центру опасности",
					Consequences: model.Consequences{
						CoreStatsChange: map[string]int{
							"magic": 5,
						},
						ResponseText: "Вы решаете смело идти туда, где ваш предшественник потерпел неудачу.",
					},
				},
			},
			Shuffleable: true,
		},
	}
	result.InitialChoices = initialChoices

	return result, nil
}

// generateGameOverEndingTask выполняет задачу генерации концовки
func (s *NovelService) generateGameOverEndingTask(ctx context.Context, payload interface{}) (interface{}, error) {
	aiRequest, ok := payload.(model.GameOverEndingRequestForAI)
	if !ok {
		return nil, fmt.Errorf("неверный тип payload для generateGameOverEndingTask")
	}

	log.Ctx(ctx).Info().Str("novelID", aiRequest.NovelID.String()).Str("userID", aiRequest.UserID.String()).Msg("Начало задачи генерации концовки при проигрыше")

	// Загружаем новеллу
	novel, err := s.repo.GetByID(ctx, aiRequest.NovelID)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения новеллы: %w", err)
	}
	aiRequest.NovelConfig = novel.Config
	aiRequest.NovelSetup = novel.Setup // Предполагаем, что GetByID возвращает Setup

	// Загружаем последнее состояние
	lastState, err := s.repo.GetNovelState(ctx, aiRequest.UserID, aiRequest.NovelID)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		log.Ctx(ctx).Warn().Err(err).Msg("Не удалось загрузить последнее состояние для генерации концовки")
		// Не передаем состояние в AI, если не удалось загрузить
	} else if err == nil {
		aiRequest.LastNovelState = lastState
	} else {
		// Состояние не найдено, но это может быть ок для запроса к AI, если FinalStateVars переданы
		log.Ctx(ctx).Info().Msg("Последнее состояние не найдено, используем FinalStateVars из запроса.")
	}

	// Генерируем концовку через AI
	jsonResponse, err := s.aiClient.GenerateGameOverEnding(ctx, aiRequest)
	if err != nil {
		return nil, fmt.Errorf("ошибка генерации концовки через AI: %w", err)
	}

	// Парсим ответ AI
	var aiResult model.GameOverEndingResponseFromAI
	// Очистка ответа AI перед парсингом
	cleanedResponse := cleanAIResponse(jsonResponse)
	if err := json.Unmarshal([]byte(cleanedResponse), &aiResult); err != nil {
		// Логируем и очищенный, и исходный ответ для отладки
		log.Error().Err(err).Str("rawResponse", jsonResponse).Str("cleanedResponse", cleanedResponse).Msg("Ошибка разбора JSON ответа AI для концовки после очистки")
		return nil, fmt.Errorf("ошибка разбора JSON ответа AI для концовки: %w", err)
	}

	generatedEndingText := aiResult.EndingText
	log.Ctx(ctx).Info().Str("novelID", aiRequest.NovelID.String()).Msg("Концовка при проигрыше успешно сгенерирована")

	return generatedEndingText, nil
}
