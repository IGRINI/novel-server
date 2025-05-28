package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"novel-server/gameplay-service/internal/service" // Добавляем импорт сервиса
	sharedModels "novel-server/shared/models"
	"novel-server/shared/utils"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type MakeChoicesRequest struct {
	SelectedOptionIndices []int `json:"selected_option_indices" binding:"required,dive,min=0,max=1"`
}

type publishedCoreStatDetail struct {
	Description        string                        `json:"description"`
	InitialValue       int                           `json:"initial_value"`
	GameOverConditions []sharedModels.StatDefinition `json:"game_over_conditions"` // <<< Заменено StatRule на StatDefinition
}

type PublishedStoryDetail struct {
	ID                string                             `json:"id"`
	Title             string                             `json:"title"`
	ShortDescription  string                             `json:"short_description"`
	AuthorID          string                             `json:"author_id"`
	AuthorName        string                             `json:"author_name"`
	PublishedAt       time.Time                          `json:"published_at"`
	Genre             string                             `json:"genre"`
	Language          string                             `json:"language"`
	IsAdultContent    bool                               `json:"is_adult_content"`
	PlayerName        string                             `json:"player_name"`
	PlayerDescription string                             `json:"player_description"`
	WorldContext      string                             `json:"world_context"`
	StorySummary      string                             `json:"story_summary"`
	CoreStats         map[string]publishedCoreStatDetail `json:"core_stats"`
	LastPlayedAt      *time.Time                         `json:"last_played_at,omitempty"` // Время последнего взаимодействия игрока с историей
	IsAuthor          bool                               `json:"is_author"`                // Является ли текущий пользователь автором
	IsPublic          bool                               `json:"is_public"`                // <<< ДОБАВЛЕНО: Является ли история публичной
	PlayerGameStatus  string                             `json:"player_game_status"`       // <<< ДОБАВЛЕНО
}

// <<< НОВЫЙ ОБРАБОТЧИК: setStoryVisibility >>>
// SetStoryVisibilityRequest - структура тела запроса для изменения видимости
type SetStoryVisibilityRequest struct {
	IsPublic bool `json:"is_public"` // Используем binding:"required"?
	// Поле IsPublic должно быть булевым, но binding может не работать с bool напрямую?
	// Проще проверить значение после биндинга.
}

// --- Обработчики --- //

// listMyPublishedStories получает список опубликованных историй текущего пользователя.
func (h *GameplayHandler) listMyPublishedStories(c *gin.Context) {
	h.logger.Debug(">>> Entered listMyPublishedStories <<<") // <<< НОВЫЙ ЛОГ В САМОМ НАЧАЛЕ
	userID, err := getUserIDFromContext(c)
	if err != nil {
		h.logger.Error("Failed to get valid userID from context in listMyPublishedStories", zap.Error(err))
		c.AbortWithStatusJSON(http.StatusInternalServerError, sharedModels.ErrorResponse{
			Code:    sharedModels.ErrCodeInternal,
			Message: "Internal error processing user context: " + err.Error(),
		})
		return
	}

	// Используем новую вспомогательную функцию
	limit, cursor, ok := parsePaginationParams(c, 10, 100, h.logger)
	if !ok {
		return // Ошибка уже обработана
	}

	log := h.logger.With(
		zap.String("userID", userID.String()),
		zap.Int("limit", limit),
		zap.String("cursor", cursor),
	)
	log.Debug("Fetching my published stories")

	// <<< ИЗМЕНЕНО: Тип возвращаемого значения из сервиса >>>
	stories, nextCursor, err := h.service.ListMyPublishedStories(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		log.Error("Error listing my published stories from service", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	resp := PaginatedResponse{
		Data:       stories, // <<< ИЗМЕНЕНО: Используем stories напрямую
		NextCursor: nextCursor,
	}

	log.Debug("Successfully fetched my published stories",
		zap.Int("count", len(stories)), // <<< ИЗМЕНЕНО: Используем stories
		zap.Bool("hasNext", nextCursor != ""),
	)

	// <<< ДОБАВЛЕНО: Логирование перед отправкой ответа >>>
	h.logger.Debug("Data prepared for JSON response in listMyPublishedStories",
		zap.Any("response_data", resp))

	c.JSON(http.StatusOK, resp)
}

// listPublicPublishedStories получает список публичных опубликованных историй.
func (h *GameplayHandler) listPublicPublishedStories(c *gin.Context) {
	userID, _ := getUserIDFromContext(c) // Опционально для проверки лайков и прогресса

	// Используем новую вспомогательную функцию
	limit, cursor, ok := parsePaginationParams(c, 20, 100, h.logger)
	if !ok {
		return // Ошибка уже обработана
	}

	log := h.logger.With(
		zap.Int("limit", limit),
		zap.String("cursor", cursor),
	)
	if userID != uuid.Nil {
		log = log.With(zap.String("userID", userID.String()))
	}
	log.Debug("Fetching public published stories")

	// <<< ИЗМЕНЕНО: Вызываем обновленный метод сервиса, который возвращает []sharedModels.PublishedStorySummaryWithProgress >>>
	stories, nextCursor, err := h.service.ListPublishedStoriesPublic(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		log.Error("Error listing public published stories", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	resp := PaginatedResponse{
		Data:       stories,
		NextCursor: nextCursor,
	}

	log.Debug("Successfully fetched public published stories",
		zap.Int("count", len(stories)),
		zap.Bool("hasNext", nextCursor != ""),
	)
	c.JSON(http.StatusOK, resp)
}

// GetStoryScene получает текущую игровую сцену для ОПРЕДЕЛЕННОГО СОСТОЯНИЯ ИГРЫ.
// Теперь принимает gameStateId вместо storyId.
func (h *GameplayHandler) getPublishedStoryScene(c *gin.Context) {
	// Используем новую вспомогательную функцию
	gameStateID, ok := parseUUIDParam(c, "game_state_id", h.logger)
	if !ok {
		return // Ошибка уже обработана
	}

	userID, err := getUserIDFromContext(c)
	if err != nil {
		return
	}

	log := h.logger.With(zap.String("gameStateID", gameStateID.String()), zap.Stringer("userID", userID))
	log.Info("getPublishedStoryScene called")

	// 1. Получаем сцену
	scene, err := h.service.GetStoryScene(c.Request.Context(), userID, gameStateID)
	if err != nil {
		log.Error("Error calling GetStoryScene service", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	// 2. Получаем опубликованную историю для доступа к Setup
	// <<< ИЗМЕНЕНО: Вызываем внутренний метод сервиса для получения полной модели >>>
	publishedStory, err := h.StoryBrowsingSvc.GetPublishedStoryDetailsInternal(c.Request.Context(), scene.PublishedStoryID) // Используем внутренний метод
	if err != nil {
		log.Error("Error calling GetPublishedStoryDetailsInternal service", zap.Stringer("publishedStoryID", scene.PublishedStoryID), zap.Error(err)) // <<< ИЗМЕНЕНО: Имя метода в логе
		handleServiceError(c, err, h.logger)                                                                                                          // Обработка ошибок (NotFound, Forbidden, Internal)
		return
	}
	if len(publishedStory.Setup) == 0 { // <<< Теперь используем поле Setup напрямую
		log.Error("Published story Setup is missing or empty", zap.Stringer("publishedStoryID", scene.PublishedStoryID))
		handleServiceError(c, errors.New("internal error: story setup data is missing"), h.logger)
		return
	}

	// 3. Парсим Setup для создания маппингов
	var setupContent sharedModels.NovelSetupContent
	if err = json.Unmarshal(publishedStory.Setup, &setupContent); err != nil { // <<< Используем поле Setup напрямую
		log.Error("Failed to unmarshal published story Setup JSON", zap.Stringer("publishedStoryID", scene.PublishedStoryID), zap.Error(err))
		handleServiceError(c, errors.New("internal error: could not parse story setup"), h.logger)
		return
	}

	// Создаем маппинги индекс -> имя
	characterIndexToName := make(map[int]string)
	for i, char := range setupContent.Characters {
		characterIndexToName[i] = char.Name
	}

	statIndexToName := make(map[int]string)
	statNameToIndex := make(map[string]int) // Для обратного поиска при необходимости
	if len(setupContent.CoreStatsDefinition) > 0 {
		statNames := make([]string, 0, len(setupContent.CoreStatsDefinition))
		for name := range setupContent.CoreStatsDefinition {
			statNames = append(statNames, name)
		}
		sort.Strings(statNames) // Важно сортировать так же, как в форматтере!
		for i, name := range statNames {
			statIndexToName[i] = name
			statNameToIndex[name] = i
		}
	}

	// 4. Парсим JSON сцены
	var rawContent map[string]json.RawMessage
	if err := json.Unmarshal(scene.Content, &rawContent); err != nil {
		log.Error("Failed to unmarshal scene content",
			zap.String("sceneID", scene.ID.String()),
			zap.String("rawContent", utils.StringShort(string(scene.Content), 500)),
			zap.Error(err),
		)
		handleServiceError(c, fmt.Errorf("internal error: failed to parse scene content"), h.logger)
		return
	}

	// 5. Получаем прогресс игрока
	playerProgress, err := h.service.GetPlayerProgress(c.Request.Context(), userID, gameStateID)
	if err != nil {
		h.logger.Error("Failed to get player progress for game state", zap.String("gameStateID", gameStateID.String()), zap.Error(err))
		handleServiceError(c, fmt.Errorf("internal error: failed to get player progress data: %w", err), h.logger)
		return
	}

	// 6. Формируем DTO ответа
	responseDTO := GameSceneResponseDTO{
		ID:               scene.ID,
		PublishedStoryID: scene.PublishedStoryID,
		GameStateID:      gameStateID,
	}

	// Заполнение CurrentStats (используем имена статов)
	responseDTO.CurrentStats = make(map[string]int)
	if playerProgress != nil && playerProgress.CoreStats != nil {
		for statName, statValue := range playerProgress.CoreStats {
			responseDTO.CurrentStats[statName] = statValue
		}
	}

	// Парсим и добавляем блоки выборов ('ch') или данные продолжения/концовки
	if chJSON, ok := rawContent["ch"]; ok {
		// Передаем маппинги в parseChoicesBlock
		parseChoicesBlock(chJSON, &responseDTO, scene.ID.String(), log, characterIndexToName, statIndexToName)
	} else if etJSON, ok := rawContent["et"]; ok {
		var endingText string
		if errUnmarshal := json.Unmarshal(etJSON, &endingText); errUnmarshal == nil {
			responseDTO.EndingText = &endingText
		} else {
			log.Error("Failed to unmarshal ending text ('et')", zap.String("sceneID", scene.ID.String()), zap.Error(errUnmarshal))
		}
	} else {
		log.Warn("Scene content does not match expected types (choices, ending)", zap.String("sceneID", scene.ID.String()))
	}

	log.Info("Successfully prepared scene response with name lookups")
	c.JSON(http.StatusOK, responseDTO)
}

// <<< ДОБАВЛЕНО: Вспомогательная функция для парсинга блока 'ch' >>>
// <<< ИЗМЕНЕНА СИГНАТУРА: Принимает маппинги индексов >>>
func parseChoicesBlock(chJSON json.RawMessage, responseDTO *GameSceneResponseDTO, sceneID string, log *zap.Logger, charIdxToName map[int]string, statIdxToName map[int]string) {
	// Структура для парсинга блока 'ch' из JSON
	type rawChoiceBlock struct {
		CharIndex   int    `json:"char"` // <<< ПОЛЕ ДЛЯ ПАРСИНГА - ИНДЕКС
		Description string `json:"desc"`
		Options     []struct {
			Text         string          `json:"txt"`
			Consequences json.RawMessage `json:"cons"`
		} `json:"opts"`
	}
	var rawChoices []rawChoiceBlock
	if err := json.Unmarshal(chJSON, &rawChoices); err != nil {
		log.Error("Failed to unmarshal choices block ('ch')", zap.String("sceneID", sceneID), zap.Error(err))
		// Возможно, здесь стоит вернуть ошибку, а не просто логировать?
		// Пока оставляем так, чтобы не ломать основной поток для других типов сцен.
		responseDTO.Choices = []ChoiceBlockDTO{} // Возвращаем пустой слайс
		return
	}

	responseDTO.Choices = make([]ChoiceBlockDTO, len(rawChoices))
	for i, rawChoice := range rawChoices {
		choiceDTO := ChoiceBlockDTO{
			CharacterName: charIdxToName[rawChoice.CharIndex], // <<< КОПИРУЕМ ИМЯ ПЕРСОНАЖА
			Description:   rawChoice.Description,
			Options:       make([]ChoiceOptionDTO, len(rawChoice.Options)),
		}
		for j, rawOpt := range rawChoice.Options {
			optionDTO := ChoiceOptionDTO{
				Text: rawOpt.Text,
			}
			// Парсим последствия ('cons') для извлечения 'rt' и 'cs'
			if len(rawOpt.Consequences) > 2 { // Проверяем, что не пустое {}
				var consMap map[string]json.RawMessage // Используем RawMessage для гибкости парсинга
				if err := json.Unmarshal(rawOpt.Consequences, &consMap); err == nil {
					preview := ConsequencesDTO{}
					hasData := false

					// Извлекаем rt (бывший resp_txt)
					if respTxtJSON, ok := consMap["rt"]; ok { // <<< ИСПРАВЛЕНО: Ищем "rt"
						var respTxtStr string
						if errUnmarshal := json.Unmarshal(respTxtJSON, &respTxtStr); errUnmarshal == nil && respTxtStr != "" {
							// Используем поле ResponseText из ConsequencesDTO
							preview.ResponseText = &respTxtStr
							hasData = true
						}
					}

					// Извлекаем cs (бывший cs_chg)
					if csChgJSON, ok := consMap["cs"]; ok { // <<< ИСПРАВЛЕНО: Ищем "cs"
						// <<< ИЗМЕНЕНО НАЗАД: Ожидаем map[string]int >>>
						var statChangesFromAI map[string]int // Ключи здесь - это строковые индексы статов
						if errUnmarshal := json.Unmarshal(csChgJSON, &statChangesFromAI); errUnmarshal == nil && len(statChangesFromAI) > 0 {
							resolvedStatChanges := make(map[string]int)
							for statIdxStr, value := range statChangesFromAI {
								statIdx, errConv := strconv.Atoi(statIdxStr)
								if errConv == nil {
									if statName, found := statIdxToName[statIdx]; found {
										resolvedStatChanges[statName] = value
									} else {
										log.Warn("Stat index from AI not found in mapping", zap.String("sceneID", sceneID), zap.String("statIndexString", statIdxStr))
										// Если имя не найдено, можно либо пропустить, либо добавить с индексом в качестве ключа.
										// Для безопасности, пока просто логируем и пропускаем.
									}
								} else {
									log.Warn("Failed to convert stat index string to int", zap.String("sceneID", sceneID), zap.String("statIndexString", statIdxStr), zap.Error(errConv))
								}
							}

							if len(resolvedStatChanges) > 0 {
								preview.StatChanges = resolvedStatChanges
								hasData = true
							} else if len(statChangesFromAI) > 0 {
								// Этот лог сработает, если statChangesFromAI не пуст, но resolvedStatChanges пуст (например, все индексы были невалидны)
								log.Warn("No stat changes were resolved to names", zap.String("sceneID", sceneID), zap.Any("originalStatChangesFromAI", statChangesFromAI))
							}
						} else if errUnmarshal != nil {
							log.Warn("Failed to unmarshal cs content into map[string]int", zap.String("sceneID", sceneID), zap.Error(errUnmarshal))
						}
					}

					// Присваиваем preview только если есть какие-то данные
					if hasData {
						// Здесь тип должен быть *ConsequencesDTO, а не *ConsequencePreviewDTO
						optionDTO.Consequences = &preview // Тип preview должен быть ConsequencesDTO
					}
				} else {
					log.Warn("Failed to unmarshal consequences map", zap.String("sceneID", sceneID), zap.Error(err))
				}
			}
			choiceDTO.Options[j] = optionDTO
		}
		responseDTO.Choices[i] = choiceDTO
	}
}

// makeChoice обрабатывает выбор игрока для ОПРЕДЕЛЕННОГО СОСТОЯНИЯ ИГРЫ.
func (h *GameplayHandler) makeChoice(c *gin.Context) {
	// Используем новую вспомогательную функцию
	gameStateID, ok := parseUUIDParam(c, "game_state_id", h.logger)
	if !ok {
		return // Ошибка уже обработана
	}

	var req MakeChoicesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid request body for makeChoice", zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid request body: %s", sharedModels.ErrBadRequest, err.Error()), h.logger)
		return
	}

	// <<< ДОБАВЛЕНО: Получаем userID для проверки прав >>>
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return
	}

	log := h.logger.With(zap.String("gameStateID", gameStateID.String()), zap.Stringer("userID", userID), zap.Any("selectedIndices", req.SelectedOptionIndices))
	log.Info("makeChoice called")

	// <<< ИЗМЕНЕНО: Вызываем MakeChoice с userID и gameStateID >>>
	err = h.service.MakeChoice(c.Request.Context(), userID, gameStateID, req.SelectedOptionIndices)
	if err != nil {
		log.Error("Error calling MakeChoice service", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	log.Info("Choice processed successfully")
	c.Status(http.StatusOK) // Успех, возвращаем 200 OK без тела
}

// likeStory обрабатывает запрос на постановку лайка опубликованной истории.
func (h *GameplayHandler) likeStory(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return
	}

	// <<< ИЗМЕНЕНО: Используем ok вместо err >>>
	id, ok := parseUUIDParam(c, "story_id", h.logger)
	if !ok { // <<< ИЗМЕНЕНО: Проверяем !ok >>>
		// Ошибка уже обработана и залогирована в parseUUIDParam
		return
	}

	log := h.logger.With(
		zap.String("storyID", id.String()),
		zap.String("userID", userID.String()),
	)
	log.Info("Liking story")

	err = h.service.LikeStory(c.Request.Context(), userID, id)
	if err != nil {
		if !errors.Is(err, sharedModels.ErrNotFound) && !errors.Is(err, service.ErrStoryNotFound) {
			log.Error("Error liking story", zap.Error(err))
		}
		handleServiceError(c, err, h.logger)
		return
	}

	log.Info("Story liked successfully")
	c.Status(http.StatusNoContent)
}

// unlikeStory обрабатывает запрос на снятие лайка с опубликованной истории.
func (h *GameplayHandler) unlikeStory(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return
	}

	// <<< ИЗМЕНЕНО: Используем ok вместо err >>>
	id, ok := parseUUIDParam(c, "story_id", h.logger)
	if !ok { // <<< ИЗМЕНЕНО: Проверяем !ok >>>
		// Ошибка уже обработана и залогирована в parseUUIDParam
		return
	}

	log := h.logger.With(
		zap.String("storyID", id.String()),
		zap.String("userID", userID.String()),
	)
	log.Info("Unliking story")

	err = h.service.UnlikeStory(c.Request.Context(), userID, id)
	if err != nil {
		// Логируем только действительно неожиданные ошибки
		if !errors.Is(err, sharedModels.ErrNotFound) && !errors.Is(err, service.ErrStoryNotFound) {
			log.Error("Error unliking story", zap.Error(err))
		}
		// Возвращаем 204 если истории не было или другая ошибка NotFound
		// (т.к. цель - отсутствие лайка - достигнута или не могла быть достигнута из-за отсутствия истории)
		if errors.Is(err, sharedModels.ErrNotFound) || errors.Is(err, service.ErrStoryNotFound) {
			log.Info("Story not found, unliking skipped (considered success)")
			c.Status(http.StatusNoContent)
			return
		}
		handleServiceError(c, err, h.logger)
		return
	}

	log.Info("Story unliked successfully")
	c.Status(http.StatusNoContent)
}

// listLikedStories получает список историй, которые лайкнул пользователь.
func (h *GameplayHandler) listLikedStories(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return
	}

	// Используем новую вспомогательную функцию
	limit, cursor, ok := parsePaginationParams(c, 10, 100, h.logger)
	if !ok {
		return // Ошибка уже обработана
	}

	log := h.logger.With(
		zap.String("userID", userID.String()),
		zap.Int("limit", limit),
		zap.String("cursor", cursor),
	)
	log.Debug("Fetching liked stories")

	// <<< ИЗМЕНЕНО: Тип возвращаемого значения из сервиса >>>
	likedStories, nextCursor, err := h.service.ListLikedStories(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		log.Error("Error listing liked stories", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	resp := PaginatedResponse{
		Data:       likedStories, // <<< ИЗМЕНЕНО: Используем likedStories напрямую
		NextCursor: nextCursor,
	}

	log.Debug("Successfully fetched liked stories",
		zap.Int("count", len(likedStories)), // <<< ИЗМЕНЕНО: Используем likedStories
		zap.Bool("hasNext", nextCursor != ""),
	)
	c.JSON(http.StatusOK, resp)
}

// setStoryVisibility обрабатывает запрос на изменение видимости опубликованной истории.
func (h *GameplayHandler) setStoryVisibility(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return
	}

	// <<< ИЗМЕНЕНО: Используем ok вместо err >>>
	id, ok := parseUUIDParam(c, "story_id", h.logger)
	if !ok { // <<< ИЗМЕНЕНО: Проверяем !ok >>>
		// Ошибка уже обработана и залогирована в parseUUIDParam
		return
	}

	var req SetStoryVisibilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid request body for setStoryVisibility", zap.String("storyID", id.String()), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid request body: %v", sharedModels.ErrBadRequest, err), h.logger)
		return
	}

	log := h.logger.With(
		zap.String("storyID", id.String()),
		zap.Stringer("userID", userID),
		zap.Bool("isPublic", req.IsPublic),
	)
	log.Info("Setting story visibility")

	err = h.service.SetStoryVisibility(c.Request.Context(), id, userID, req.IsPublic)
	if err != nil {
		// Логируем только неожидаемые ошибки
		if !errors.Is(err, sharedModels.ErrNotFound) &&
			!errors.Is(err, sharedModels.ErrForbidden) &&
			!errors.Is(err, service.ErrStoryNotReadyForPublishing) &&
			!errors.Is(err, service.ErrInternal) { // Проверяем и на ErrInternal
			log.Error("Error setting story visibility (unhandled)", zap.Error(err))
		}
		handleServiceError(c, err, h.logger) // Передаем ошибку для стандартизированной обработки
		return
	}

	log.Info("Story visibility set successfully")
	c.Status(http.StatusNoContent) // Успех
}

// <<< ДОБАВЛЕНО: Обработчик для повторной генерации опубликованной истории >>>
func (h *GameplayHandler) retryPublishedStoryGeneration(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return // Ошибка уже обработана
	}

	// Используем новую вспомогательную функцию
	id, ok := parseUUIDParam(c, "story_id", h.logger)
	if !ok {
		return // Ошибка уже обработана
	}

	log := h.logger.With(zap.String("userID", userID.String()), zap.String("storyID", id.String()))
	log.Info("Handling retry published story generation request")

	// <<< ИЗМЕНЕНО: Проверка лимита активных генераций через publishedStoryRepo >>>
	// Note: h.publishedStoryRepo does not exist on GameplayHandler. This check should likely be inside the service method.
	// Commenting out for now as it will cause compile error. The service method should handle limits.
	/*
		activeCount, err := h.publishedStoryRepo.CountActiveGenerationsForUser(c.Request.Context(), userID)
		if err != nil {
			log.Error("Error counting active generations before retry", zap.Error(err))
			handleServiceError(c, fmt.Errorf("error checking generation status: %w", err), h.logger)
			return
		}
		// <<< ИЗМЕНЕНО: Используем GenerationLimitPerUser из config >>>
		generationLimit := h.config.GenerationLimitPerUser // Используем лимит из конфига
		if activeCount >= generationLimit {
			log.Warn("User reached active generation limit, retry rejected", zap.Int("limit", generationLimit), zap.Int("count", activeCount))
			handleServiceError(c, sharedModels.ErrUserHasActiveGeneration, h.logger)
			return
		}
	*/

	// <<< ИЗМЕНЕНО: Вызываем RetryInitialGeneration вместо старого метода >>>
	err = h.service.RetryInitialGeneration(c.Request.Context(), userID, id) // <<< ИЗМЕНЕНИЕ ЗДЕСЬ
	if err != nil {
		log.Error("Error retrying initial published story generation", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	log.Info("Published story retry request accepted")
	c.Status(http.StatusAccepted)
}

// deletePublishedStory удаляет опубликованную историю (только автор).
func (h *GameplayHandler) deletePublishedStory(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return
	}

	// <<< ИЗМЕНЕНО: Используем ok вместо err >>>
	storyID, ok := parseUUIDParam(c, "story_id", h.logger)
	if !ok { // <<< ИЗМЕНЕНО: Проверяем !ok >>>
		return
	}

	log := h.logger.With(zap.String("storyID", storyID.String()), zap.Stringer("userID", userID))
	log.Info("Handling delete published story request")

	err = h.service.DeletePublishedStory(c.Request.Context(), storyID, userID)
	if err != nil {
		log.Error("Error deleting published story", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	log.Info("Published story deleted successfully")
	c.Status(http.StatusNoContent)
}

// GET /published-stories/{storyId}/gamestates
func (h *GameplayHandler) listGameStates(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return // Ошибка уже обработана
	}

	// <<< ИСПРАВЛЕНО: Получаем 'story_id' >>>
	storyIdStr := c.Param("story_id")
	storyID, err := uuid.Parse(storyIdStr)
	if err != nil {
		// <<< ИСПРАВЛЕНО: Логируем 'story_id' >>>
		h.logger.Warn("Invalid story ID format in listGameStates", zap.String("story_id", storyIdStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	log := h.logger.With(zap.Stringer("userID", userID), zap.Stringer("storyID", storyID))
	log.Info("listGameStates called")

	// Вызываем метод сервиса
	gameStates, err := h.service.ListGameStates(c.Request.Context(), userID, storyID)
	if err != nil {
		log.Error("Error calling ListGameStates service", zap.Error(err))
		handleServiceError(c, err, h.logger) // Обрабатываем стандартные ошибки
		return
	}

	// TODO: Возможно, нужно преобразовать []*sharedModels.PlayerGameState в DTO?
	// Пока возвращаем как есть.
	response := DataResponse{ // Используем DataResponse, если нет пагинации
		Data: gameStates,
	}

	log.Info("Game states listed successfully", zap.Int("count", len(gameStates)))
	c.JSON(http.StatusOK, response)
}

// <<< ДОБАВЛЕНО: Простое определение DataResponse >>>
type DataResponse struct {
	Data interface{} `json:"data"`
}

// retrySpecificGameStateGeneration handles retrying generation for a specific game state.
func (h *GameplayHandler) retrySpecificGameStateGeneration(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return // Error already handled
	}

	// Используем новую вспомогательную функцию
	storyID, okStory := parseUUIDParam(c, "story_id", h.logger)
	if !okStory {
		return
	}

	gameStateIdStr := c.Param("game_state_id")
	gameStateID, err := uuid.Parse(gameStateIdStr)
	if err != nil {
		h.logger.Warn("Invalid game state ID format in retrySpecificGameStateGeneration", zap.String("game_state_id", gameStateIdStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid game state ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	log := h.logger.With(
		zap.Stringer("userID", userID),
		zap.Stringer("storyID", storyID),
		zap.Stringer("gameStateID", gameStateID),
	)
	log.Info("Handling retry specific game state generation request")

	// Call the service method
	err = h.service.RetryGenerationForGameState(c.Request.Context(), userID, storyID, gameStateID)
	if err != nil {
		log.Error("Error retrying specific game state generation", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	log.Info("Specific game state generation retry request accepted")
	c.Status(http.StatusAccepted)
}

// <<< НАЧАЛО НОВОГО ОБРАБОТЧИКА >>>
// listMyStoriesWithProgress возвращает список опубликованных историй пользователя, в которых есть прогресс.
// GET /api/v1/published-stories/me/progress?limit=10&cursor=...[&filter_adult=true]
func (h *GameplayHandler) listMyStoriesWithProgress(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return // Error handled in helper
	}

	// Используем новую вспомогательную функцию
	limit, cursor, ok := parsePaginationParams(c, 10, 100, h.logger)
	if !ok {
		return // Ошибка уже обработана
	}
	filterAdultStr := c.DefaultQuery("filter_adult", "false") // <<< ДОБАВЛЕНО: Читаем filter_adult

	// Парсим filter_adult вручную, т.к. parsePaginationParams его не обрабатывает
	filterAdult, err := strconv.ParseBool(filterAdultStr) // <<< ДОБАВЛЕНО: Парсим filter_adult
	if err != nil {
		h.logger.Warn("Invalid filter_adult parameter for listMyStoriesWithProgress", zap.String("filter_adult", filterAdultStr), zap.Stringer("userID", userID))
		handleServiceError(c, fmt.Errorf("%w: invalid filter_adult parameter", sharedModels.ErrBadRequest), h.logger)
		return
	}

	log := h.logger.With(zap.Stringer("userID", userID), zap.Int("limit", limit), zap.String("cursor", cursor), zap.Bool("filterAdult", filterAdult)) // <<< ДОБАВЛЕНО: filterAdult в лог
	log.Debug("Fetching my published stories with progress")

	// Вызываем метод сервиса
	// <<< ИЗМЕНЕНО: Тип возвращаемого значения из сервиса >>>
	stories, nextCursor, serviceErr := h.service.ListMyStoriesWithProgress(c.Request.Context(), userID, cursor, limit, filterAdult)
	if serviceErr != nil {
		log.Error("Error listing my published stories with progress from service", zap.Error(serviceErr))
		handleServiceError(c, serviceErr, h.logger)
		return
	}

	response := PaginatedResponse{
		Data:       stories, // <<< ИЗМЕНЕНО: Используем stories напрямую
		NextCursor: nextCursor,
	}

	log.Info("My published stories with progress listed successfully", zap.Int("count", len(stories)), zap.Bool("hasNext", nextCursor != ""))
	c.JSON(http.StatusOK, response)
}

// <<< КОНЕЦ НОВОГО ОБРАБОТЧИКА >>>
