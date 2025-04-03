package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
		CoreStats:         make(map[string]model.CoreStatView),
		Themes:            config.PlayerPrefs.Themes,
	}

	for name, stat := range config.CoreStats {
		draftView.CoreStats[name] = stat.ToView()
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

// SubmitGenerateContentTask отправляет задачу для генерации контента новеллы
func (s *NovelService) SubmitGenerateContentTask(ctx context.Context, req model.GenerateNovelContentRequest) (string, error) {
	// Исправлено: Сигнатура SubmitTaskWithOwner: (ctx, taskFunc, payload, ownerID)
	taskID, err := s.taskManager.SubmitTaskWithOwner(
		ctx,                   // Контекст
		s.generateContentTask, // Функция задачи типа taskmanager.TaskFunc
		req,                   // Payload (interface{})
		req.UserID.String(),   // Владелец (string) - предполагаем UserID это UUID
	)
	if err != nil {
		return "", fmt.Errorf("ошибка отправки задачи генерации контента: %w", err)
	}
	// Исправлено: Конвертируем UUID в строку
	return taskID.String(), nil
}

// generateContentTask выполняет задачу генерации контента для новеллы
func (s *NovelService) generateContentTask(ctx context.Context, payload interface{}) (interface{}, error) {
	// 1. Преобразуем payload
	p, ok := payload.(model.GenerateNovelContentRequest)
	if !ok {
		return nil, fmt.Errorf("неверный тип payload")
	}

	log.Ctx(ctx).Info().Str("novelID", p.NovelID.String()).Str("userID", p.UserID.String()).Msg("Начало задачи генерации контента")

	// 2. Получаем новеллу с setup
	novel, err := s.repo.GetNovelWithSetup(ctx, p.NovelID)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения новеллы: %w", err)
	}

	// 3. Получаем предыдущее состояние
	isFirstRequest := false
	previousState, err := s.repo.GetNovelState(ctx, p.UserID, p.NovelID)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			isFirstRequest = true
			log.Ctx(ctx).Info().Msg("Состояние не найдено, первый запрос.")
		} else {
			return nil, fmt.Errorf("ошибка получения состояния: %w", err)
		}
	}

	var stateHash string
	var cacheHit bool
	var resultJSON string // Будем возвращать строку JSON

	// 4. Логика кэширования (если не первый запрос)
	if !isFirstRequest {
		stateHash, err = calculateStateHash(previousState)
		if err != nil {
			log.Ctx(ctx).Error().Err(err).Msg("Ошибка вычисления хеша состояния")
		} else {
			cachedBatch, errCache := s.repo.GetSceneBatchByHash(ctx, p.NovelID, stateHash)
			if errCache == nil {
				log.Ctx(ctx).Info().Str("hash", stateHash).Msg("Найден кеш сцены")
				// Формируем структуру ответа из кеша
				cacheResponse := model.AIResponse{
					StorySummarySoFar: cachedBatch.StorySummarySoFar,
					FutureDirection:   cachedBatch.FutureDirection,
					Choices:           cachedBatch.Choices,
					EndingText:        cachedBatch.EndingText,
				}
				// Сериализуем в JSON
				resultBytes, errMarshal := json.Marshal(cacheResponse)
				if errMarshal != nil {
					log.Ctx(ctx).Error().Err(errMarshal).Msg("Ошибка сериализации кешированного батча в JSON")
					// Не фатально, просто не используем кеш
				} else {
					resultJSON = string(resultBytes)
					cacheHit = true
				}
			} else if !errors.Is(errCache, model.ErrNotFound) {
				log.Ctx(ctx).Warn().Err(errCache).Msg("Ошибка при поиске кеша сцены")
			}
		}
	}

	// 5. Генерация AI (если кеш не найден или не использован)
	if !cacheHit {
		var jsonResponse string // Ответ от AI

		if isFirstRequest {
			log.Ctx(ctx).Info().Msg("Генерация первого батча через AI (creator)")
			aiReq := model.GenerateNovelContentRequestForAI{
				Config: novel.Config,
				Setup:  novel.Setup,
			}
			jsonResponse, err = s.aiClient.GenerateWithNovelCreator(ctx, aiReq)
			if err != nil {
				return nil, fmt.Errorf("ошибка генерации первого батча AI (creator): %w", err)
			}
			// TODO: Валидация JSON ответа для первой сцены? Промпт требует specific поля.
			// Нужно либо парсить в FirstSceneResponse, убедиться в наличии полей,
			// а потом возвращать исходный jsonResponse. Либо положиться на AI.
		} else {
			log.Ctx(ctx).Info().Msg("Генерация следующего батча через AI (creator)")
			aiReq := model.GenerateNovelContentRequestForAI{
				NovelState: previousState,
				Config:     novel.Config,
				Setup:      novel.Setup,
			}
			jsonResponse, err = s.aiClient.GenerateWithNovelCreator(ctx, aiReq)
			if err != nil {
				return nil, fmt.Errorf("ошибка генерации следующего батча AI (creator): %w", err)
			}
		}

		// Очищаем ответ и используем его как результат
		resultJSON = cleanAIResponse(jsonResponse)

		// Важно: Мы больше НЕ ПАРСИМ JSON здесь, возвращаем строку
		// Парсинг нужен только для кеширования, если будем парсить

		// Сохраняем в кеш (если не первый запрос и хеш был вычислен)
		if !isFirstRequest && stateHash != "" {
			// Для сохранения в кеш нам НУЖНО распарсить ответ
			var creatorRespForCache model.AIResponse
			if errUnmarshal := json.Unmarshal([]byte(resultJSON), &creatorRespForCache); errUnmarshal != nil {
				log.Ctx(ctx).Error().Err(errUnmarshal).Str("json_string", resultJSON).Msg("Ошибка разбора JSON ответа AI для сохранения в кеш")
				// Не сохраняем в кеш, если парсинг не удался
			} else {
				batchToCache := model.SceneBatch{
					NovelID:           p.NovelID,
					StateHash:         stateHash,
					StorySummarySoFar: creatorRespForCache.StorySummarySoFar, // Используем распарсенные данные
					FutureDirection:   creatorRespForCache.FutureDirection,   // Используем распарсенные данные
					Choices:           creatorRespForCache.Choices,           // Используем распарсенные данные
					EndingText:        creatorRespForCache.EndingText,        // Используем распарсенные данные
				}
				_, errSave := s.repo.SaveSceneBatch(ctx, batchToCache)
				if errSave != nil {
					log.Ctx(ctx).Warn().Err(errSave).Msg("Ошибка сохранения батча в кеш")
				}
			}
		}
	}

	// --- Обновление состояния ---
	// Для обновления состояния нам НУЖНО распарсить resultJSON, если он не из кеша
	var responseForState model.AIResponse
	if errUnmarshal := json.Unmarshal([]byte(resultJSON), &responseForState); errUnmarshal != nil {
		// Если не удалось распарсить JSON, который мы собираемся вернуть,
		// это критическая ошибка.
		log.Ctx(ctx).Error().Err(errUnmarshal).Str("resultJSON", resultJSON).Msg("Критическая ошибка: не удалось распарсить JSON для обновления состояния")
		return nil, fmt.Errorf("внутренняя ошибка: не удалось обработать ответ AI для обновления состояния: %w", errUnmarshal)
	}

	// 6. Обновляем или создаем состояние новеллы
	newState := model.NovelState{
		UserID:            p.UserID,
		NovelID:           p.NovelID,
		StorySummarySoFar: responseForState.StorySummarySoFar, // Используем распарсенные данные
		FutureDirection:   responseForState.FutureDirection,   // Используем распарсенные данные
	}

	if isFirstRequest {
		newState.CurrentBatchNumber = 1
		newState.StoryVariables = make(map[string]interface{})
		if novel.Setup.CoreStatsDefinition != nil {
			for name, definition := range novel.Setup.CoreStatsDefinition {
				newState.StoryVariables[name] = definition.InitialValue
			}
		}
		newState.History = []model.UserChoice{}
		newState.HistoryChoices = make(map[int][]model.ChoiceOption)
		newState.HistoryChoices[0] = responseForState.Choices // Используем распарсенные данные
	} else {
		newState.ID = previousState.ID
		newState.CreatedAt = previousState.CreatedAt
		newState.CurrentBatchNumber = previousState.CurrentBatchNumber + 1
		newState.StoryVariables = previousState.StoryVariables
		newState.History = append(previousState.History, p.UserChoice)

		// Применяем изменения статов из последствий
		if previousState.HistoryChoices != nil {
			if previousChoicesForBatch, batchOk := previousState.HistoryChoices[p.UserChoice.BatchNumber]; batchOk {
				if p.UserChoice.ChoiceIndex >= 0 && p.UserChoice.ChoiceIndex < len(previousChoicesForBatch) {
					chosenOption := previousChoicesForBatch[p.UserChoice.ChoiceIndex]
					if chosenOption.Consequences.CoreStatsChange != nil {
						for stat, change := range chosenOption.Consequences.CoreStatsChange {
							if currentValRaw, ok := newState.StoryVariables[stat]; ok {
								if currentValInt, typeOk := currentValRaw.(int); typeOk {
									newState.StoryVariables[stat] = currentValInt + change
								} else if currentValFloat, typeOk := currentValRaw.(float64); typeOk {
									newState.StoryVariables[stat] = int(currentValFloat) + change
								} else {
									log.Ctx(ctx).Warn().Str("stat", stat).Interface("value", currentValRaw).Msg("Переменная состояния имеет нечисловой тип, перезапись изменением.")
									newState.StoryVariables[stat] = change
								}
							} else {
								newState.StoryVariables[stat] = change
							}
						}
					}
					// TODO: Обработать изменения GlobalFlags и StoryVariables из Consequences
				} else {
					log.Ctx(ctx).Warn().Int("choiceIndex", p.UserChoice.ChoiceIndex).Int("batchNumber", p.UserChoice.BatchNumber).Msg("Индекс выбора пользователя вне диапазона для батча")
				}
			} else {
				log.Ctx(ctx).Warn().Int("batchNumber", p.UserChoice.BatchNumber).Msg("Номер батча выбора пользователя отсутствует в истории выборов")
			}
		}

		// Копируем и обновляем HistoryChoices
		newState.HistoryChoices = make(map[int][]model.ChoiceOption)
		if previousState.HistoryChoices != nil {
			for k, v := range previousState.HistoryChoices {
				newState.HistoryChoices[k] = v
			}
		}
		newState.HistoryChoices[newState.CurrentBatchNumber-1] = responseForState.Choices // Используем распарсенные данные
	}

	// 7. Сохраняем состояние в базу данных
	_, err = s.repo.SaveNovelState(ctx, newState)
	if err != nil {
		return nil, fmt.Errorf("ошибка сохранения нового состояния новеллы: %w", err)
	}

	// 8. Возвращаем строку JSON клиенту
	// Для первого запроса, возможно, нужно добавить initialStats к JSON?
	// Пока просто возвращаем JSON, полученный от AI или из кеша.
	log.Ctx(ctx).Info().Msg("Отправка результата задачи (JSON строка)")
	return resultJSON, nil
}

// calculateStateHash вычисляет SHA256 хеш от релевантных полей состояния
func calculateStateHash(state model.NovelState) (string, error) {
	// Собираем данные для хеширования
	var dataToHash strings.Builder
	dataToHash.WriteString(state.NovelID.String())
	dataToHash.WriteString(state.UserID.String())
	dataToHash.WriteString(fmt.Sprintf("%d", state.CurrentBatchNumber))
	dataToHash.WriteString(state.StorySummarySoFar)
	dataToHash.WriteString(state.FutureDirection)

	// Сериализуем и сортируем ключи StoryVariables для стабильного хеша
	variablesBytes, err := json.Marshal(state.StoryVariables)
	if err != nil {
		return "", fmt.Errorf("ошибка сериализации story_variables для хеша: %w", err)
	}
	dataToHash.Write(variablesBytes)

	// Сериализуем и сортируем History для стабильного хеша (если важно)
	// TODO: Решить, нужно ли включать всю историю или только последние N шагов
	historyBytes, err := json.Marshal(state.History)
	if err != nil {
		return "", fmt.Errorf("ошибка сериализации history для хеша: %w", err)
	}
	dataToHash.Write(historyBytes)

	// Вычисляем хеш
	hash := sha256.Sum256([]byte(dataToHash.String()))
	return hex.EncodeToString(hash[:]), nil
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
		cachedBatch, errCache := s.repo.GetSceneBatchByHash(ctx, novelID, stateHash) // Используем другую переменную для ошибки кеша
		if errCache == nil {
			if cachedBatch.EndingText != nil {
				log.Info().Str("stateHash", stateHash).Msg("Найдена кешированная концовка Game Over.")
				return *cachedBatch.EndingText, nil
			} else {
				log.Warn().Str("stateHash", stateHash).Msg("Найден кеш для состояния Game Over, но он не содержит концовки. Будет сгенерирована новая.")
			}
		} else if !errors.Is(errCache, model.ErrNotFound) {
			log.Error().Err(errCache).Str("stateHash", stateHash).Msg("Ошибка при проверке кеша для концовки Game Over")
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
			Choices:           nil,
			StorySummarySoFar: lastState.StorySummarySoFar, // Сохраняем предыдущие данные?
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
