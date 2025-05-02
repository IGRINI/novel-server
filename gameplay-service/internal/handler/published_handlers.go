package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"novel-server/gameplay-service/internal/service" // Добавляем импорт сервиса
	sharedModels "novel-server/shared/models"
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
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return
	}

	limitStr := c.Query("limit")
	cursor := c.Query("cursor")

	limit := 10
	if limitStr != "" {
		parsedLimit, err := strconv.Atoi(limitStr)
		if err != nil || parsedLimit <= 0 {
			h.logger.Warn("Invalid limit parameter received", zap.String("limit", limitStr), zap.Error(err))
			handleServiceError(c, fmt.Errorf("%w: invalid 'limit' parameter", sharedModels.ErrBadRequest), h.logger)
			return
		}
		if parsedLimit > 100 {
			parsedLimit = 100
		}
		limit = parsedLimit
	}

	log := h.logger.With(
		zap.String("userID", userID.String()),
		zap.Int("limit", limit),
		zap.String("cursor", cursor),
	)
	log.Debug("Fetching my published stories")

	// Вызываем метод сервиса, который возвращает []*PublishedStoryDetailWithProgressDTO
	storiesDTO, nextCursor, err := h.service.ListMyPublishedStories(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		log.Error("Error listing my published stories", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	// <<< ДОБАВЛЕНО: Конвертация из DTO сервиса в sharedModels >>>
	storySummaries := make([]sharedModels.PublishedStorySummaryWithProgress, len(storiesDTO))
	for i, dto := range storiesDTO {
		if dto == nil {
			log.Warn("Received nil PublishedStoryDetailWithProgressDTO in listMyPublishedStories")
			continue
		}
		title := dto.Title
		description := dto.ShortDescription

		storySummaries[i] = sharedModels.PublishedStorySummaryWithProgress{
			PublishedStorySummary: sharedModels.PublishedStorySummary{
				ID:               dto.ID,
				Title:            title,
				ShortDescription: description,
				AuthorID:         dto.AuthorID,
				AuthorName:       dto.AuthorName,
				PublishedAt:      dto.PublishedAt,
				IsAdultContent:   dto.IsAdultContent,
				LikesCount:       int64(dto.LikesCount),
				IsLiked:          dto.IsLiked,
				Status:           dto.Status,
			},
			HasPlayerProgress: dto.HasPlayerProgress,
			IsPublic:          dto.IsPublic,
		}
	}

	resp := PaginatedResponse{
		Data:       storySummaries,
		NextCursor: nextCursor,
	}

	log.Debug("Successfully fetched my published stories",
		zap.Int("count", len(storySummaries)),
		zap.Bool("hasNext", nextCursor != ""),
	)
	c.JSON(http.StatusOK, resp)
}

// listPublicPublishedStories получает список публичных опубликованных историй.
func (h *GameplayHandler) listPublicPublishedStories(c *gin.Context) {
	userID, _ := getUserIDFromContext(c) // Опционально для проверки лайков и прогресса

	limitStr := c.Query("limit")
	cursor := c.Query("cursor")

	limit := 20
	if limitStr != "" {
		parsedLimit, err := strconv.Atoi(limitStr)
		if err != nil || parsedLimit <= 0 {
			h.logger.Warn("Invalid limit parameter received", zap.String("limit", limitStr), zap.Error(err))
			handleServiceError(c, fmt.Errorf("%w: invalid 'limit' parameter", sharedModels.ErrBadRequest), h.logger)
			return
		}
		if parsedLimit > 100 {
			parsedLimit = 100
		}
		limit = parsedLimit
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
	gameStateIdStr := c.Param("game_state_id") // <<< ИЗМЕНЕНО: Получаем game_state_id
	gameStateID, err := uuid.Parse(gameStateIdStr)
	if err != nil {
		h.logger.Warn("Invalid game state ID format in getPublishedStoryScene", zap.String("gameStateId", gameStateIdStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid game state ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	// <<< ДОБАВЛЕНО: Получаем userID для проверки прав (хотя сам сервис может это делать) >>>
	userID, err := getUserIDFromContext(c)
	if err != nil {
		// Ошибка уже обработана
		return
	}

	log := h.logger.With(zap.String("gameStateID", gameStateID.String()), zap.Stringer("userID", userID)) // Логируем gameStateID и userID
	log.Info("getPublishedStoryScene called")

	// <<< ИЗМЕНЕНО: Вызываем GetStoryScene с userID и gameStateID >>>
	scene, err := h.service.GetStoryScene(c.Request.Context(), userID, gameStateID)
	if err != nil {
		log.Error("Error calling GetStoryScene service", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	// Парсим json.RawMessage из scene.Content
	var rawContent map[string]json.RawMessage // Используем map для гибкого парсинга
	if len(scene.Content) == 0 || string(scene.Content) == "null" {
		log.Error("Scene content is empty or null", zap.String("sceneID", scene.ID.String()))
		handleServiceError(c, fmt.Errorf("internal error: scene content is missing"), h.logger)
		return
	}
	if err := json.Unmarshal(scene.Content, &rawContent); err != nil {
		log.Error("Failed to unmarshal scene content", zap.String("sceneID", scene.ID.String()), zap.Error(err))
		handleServiceError(c, fmt.Errorf("internal error: failed to parse scene content"), h.logger)
		return
	}

	// <<< ИЗМЕНЕНО: Получаем прогресс по userID и gameStateID >>>
	playerProgress, err := h.service.GetPlayerProgress(c.Request.Context(), userID, gameStateID)
	if err != nil {
		// Ошибка здесь означает реальную проблему
		h.logger.Error("Failed to get player progress for game state", zap.String("gameStateID", gameStateID.String()), zap.Error(err))
		handleServiceError(c, fmt.Errorf("internal error: failed to get player progress data: %w", err), h.logger)
		return
	}

	// Формируем DTO ответа (используем GameSceneResponseDTO из dto.go)
	responseDTO := GameSceneResponseDTO{
		ID:               scene.ID,
		PublishedStoryID: scene.PublishedStoryID, // Оставляем PublishedStoryID для контекста на клиенте
		GameStateID:      gameStateID,            // <<< ИЗМЕНЕНО/ДОБАВЛЕНО: Передаем ID состояния игры (убедитесь, что поле есть в DTO)
		// CurrentStats будет заполнено ниже
	}

	// Заполнение CurrentStats
	responseDTO.CurrentStats = make(map[string]int)
	if playerProgress != nil && playerProgress.CoreStats != nil {
		for statName, statValue := range playerProgress.CoreStats {
			responseDTO.CurrentStats[statName] = statValue
		}
	}

	// Парсим и добавляем блоки выборов ('ch') или данные продолжения/концовки
	if chJSON, ok := rawContent["ch"]; ok {
		parseChoicesBlock(chJSON, &responseDTO, scene.ID.String(), log)
	} else if _, ok := rawContent["et"]; ok { // etJSON -> _
		// parseEndingBlock(etJSON, &responseDTO, log) // <<< ЗАКОММЕНТИРОВАНО: Функция не определена
		log.Warn("Ending block 'et' found but parseEndingBlock is commented out", zap.String("sceneID", scene.ID.String())) // Добавим лог
	} else if _, ok := rawContent["npd"]; ok { // npdJSON -> _
		// parseContinuationBlock(rawContent, &responseDTO, log) // <<< ЗАКОММЕНТИРОВАНО: Функция не определена
		log.Warn("Continuation block 'npd' found but parseContinuationBlock is commented out", zap.String("sceneID", scene.ID.String())) // Добавим лог
	} else {
		log.Warn("Scene content does not match expected types (choices, ending, continuation)", zap.String("sceneID", scene.ID.String()))
	}

	log.Info("Successfully prepared scene response")
	c.JSON(http.StatusOK, responseDTO)
}

// <<< ДОБАВЛЕНО: Вспомогательная функция для парсинга блока 'ch' >>>
func parseChoicesBlock(chJSON json.RawMessage, responseDTO *GameSceneResponseDTO, sceneID string, log *zap.Logger) {
	// Временная структура для парсинга блока 'ch'
	type rawChoiceBlock struct {
		Char        string `json:"char"` // <<< ПОЛЕ ДЛЯ ПАРСИНГА
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
			CharacterName: rawChoice.Char, // <<< КОПИРУЕМ ИМЯ ПЕРСОНАЖА
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
						var statChanges map[string]int
						if errUnmarshal := json.Unmarshal(csChgJSON, &statChanges); errUnmarshal == nil && len(statChanges) > 0 {
							// Используем поле StatChanges из ConsequencesDTO
							preview.StatChanges = statChanges
							hasData = true
						} else if errUnmarshal != nil {
							log.Warn("Failed to unmarshal cs content", zap.String("sceneID", sceneID), zap.Error(errUnmarshal))
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
	gameStateIdStr := c.Param("game_state_id") // <<< ИЗМЕНЕНО: Получаем game_state_id
	gameStateID, err := uuid.Parse(gameStateIdStr)
	if err != nil {
		h.logger.Warn("Invalid game state ID format in makeChoice", zap.String("gameStateId", gameStateIdStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid game state ID format", sharedModels.ErrBadRequest), h.logger)
		return
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
	userID, err := getUserIDFromContext(c) // <<< Меняем 'ok' на 'err'
	if err != nil {                        // <<< Проверяем 'err != nil'
		// Ошибка уже обработана в getUserIDFromContext
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in likeStory", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
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

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in unlikeStory", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	log := h.logger.With(
		zap.String("storyID", id.String()),
		zap.String("userID", userID.String()),
	)
	log.Info("Unliking story")

	err = h.service.UnlikeStory(c.Request.Context(), userID, id)
	if err != nil {
		if !errors.Is(err, sharedModels.ErrNotFound) && !errors.Is(err, service.ErrStoryNotFound) {
			// Ошибка 'лайк не найден' также игнорируется при логировании ошибки
			if !errors.Is(err, service.ErrNotLikedYet) {
				log.Error("Error unliking story", zap.Error(err))
			}
		}
		// Возвращаем 204 даже если лайка не было (цель достигнута - лайка нет)
		if errors.Is(err, sharedModels.ErrNotFound) || errors.Is(err, service.ErrStoryNotFound) || errors.Is(err, service.ErrNotLikedYet) {
			log.Info("Story or like not found, unliking skipped (considered success)")
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

	limitStr := c.Query("limit")
	cursor := c.Query("cursor")

	limit := 10
	if limitStr != "" {
		parsedLimit, err := strconv.Atoi(limitStr)
		if err != nil || parsedLimit <= 0 {
			h.logger.Warn("Invalid limit parameter received", zap.String("limit", limitStr), zap.Error(err))
			handleServiceError(c, fmt.Errorf("%w: invalid 'limit' parameter", sharedModels.ErrBadRequest), h.logger)
			return
		}
		if parsedLimit > 100 {
			parsedLimit = 100
		}
		limit = parsedLimit
	}

	log := h.logger.With(
		zap.String("userID", userID.String()),
		zap.Int("limit", limit),
		zap.String("cursor", cursor),
	)
	log.Debug("Fetching liked stories")

	// <<< ИЗМЕНЕНО: Вызываем обновленный сервис, получаем DTO - теперь это []*service.LikedStoryDetailDTO >>>
	// Важно: ListLikedStories *не* возвращает []sharedModels.PublishedStorySummaryWithProgress
	// Он возвращает []*service.LikedStoryDetailDTO, который нужно преобразовать в sharedModels.PublishedStorySummaryWithProgress
	likedStoriesDetails, nextCursor, err := h.service.ListLikedStories(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		log.Error("Error listing liked stories", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	// <<< ОСТАВЛЯЕМ ПРЕОБРАЗОВАНИЕ, т.к. сервис возвращает другой тип DTO >>>
	storySummaries := make([]sharedModels.PublishedStorySummaryWithProgress, len(likedStoriesDetails))
	for i, detail := range likedStoriesDetails {
		if detail == nil { // Safety check
			log.Warn("Received nil LikedStoryDetailDTO in listLikedStories")
			continue
		}
		title := ""
		if detail.Title != nil {
			title = *detail.Title
		}
		description := ""
		if detail.Description != nil {
			description = *detail.Description
		}
		storySummaries[i] = sharedModels.PublishedStorySummaryWithProgress{
			PublishedStorySummary: sharedModels.PublishedStorySummary{
				ID:               detail.ID,
				Title:            title,
				ShortDescription: description, // Map description to ShortDescription
				AuthorID:         detail.UserID,
				AuthorName:       detail.AuthorName, // <-- Исправлено: Используем поле из DTO
				PublishedAt:      detail.CreatedAt,
				IsAdultContent:   detail.IsAdultContent, // Assuming this field exists in LikedStoryDetailDTO
				LikesCount:       detail.LikesCount,     // Assuming this field exists
				IsLiked:          true,                  // Always true for liked stories endpoint
			},
			HasPlayerProgress: detail.HasPlayerProgress, // Assuming this field exists
			IsPublic:          detail.IsPublic,          // <<< ИСПРАВЛЕНО: Присваиваем поле на правильном уровне
		}
	}

	resp := PaginatedResponse{
		Data:       storySummaries, // Используем преобразованные данные
		NextCursor: nextCursor,
	}

	log.Debug("Successfully fetched liked stories",
		zap.Int("count", len(storySummaries)),
		zap.Bool("hasNext", nextCursor != ""),
	)
	c.JSON(http.StatusOK, resp)
}

// setStoryVisibility обрабатывает запрос на изменение видимости опубликованной истории.
func (h *GameplayHandler) setStoryVisibility(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return // Ошибка уже обработана
	}

	idStr := c.Param("id")
	storyID, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in setStoryVisibility", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	var req SetStoryVisibilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid request body for setStoryVisibility", zap.String("storyID", storyID.String()), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid request body: %v", sharedModels.ErrBadRequest, err), h.logger)
		return
	}

	log := h.logger.With(
		zap.String("storyID", storyID.String()),
		zap.Stringer("userID", userID),
		zap.Bool("isPublic", req.IsPublic),
	)
	log.Info("Setting story visibility")

	err = h.service.SetStoryVisibility(c.Request.Context(), storyID, userID, req.IsPublic)
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

	idStr := c.Param("id")
	storyID, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in retryPublishedStoryGeneration", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	log := h.logger.With(zap.String("userID", userID.String()), zap.String("storyID", storyID.String()))
	log.Info("Handling retry published story generation request")

	// <<< ИЗМЕНЕНО: Проверка лимита активных генераций через publishedStoryRepo >>>
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

	// Вызываем метод сервиса
	err = h.service.RetryStoryGeneration(c.Request.Context(), storyID, userID)
	if err != nil {
		log.Error("Error retrying published story generation", zap.Error(err))
		// Используем handleServiceError для стандартизации
		handleServiceError(c, err, h.logger)
		return
	}

	log.Info("Published story retry request accepted")
	c.Status(http.StatusAccepted)
}

// <<<<< НАЧАЛО ОБРАБОТЧИКА УДАЛЕНИЯ ОПУБЛИКОВАННОЙ ИСТОРИИ >>>>>
func (h *GameplayHandler) deletePublishedStory(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return // Abort already called
	}

	idStr := c.Param("id")
	storyID, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid published story ID format in deletePublishedStory", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	// Вызываем метод сервиса (GameplayService)
	err = h.service.DeletePublishedStory(c.Request.Context(), storyID, userID)
	if err != nil {
		h.logger.Error("Error deleting published story", zap.String("userID", userID.String()), zap.String("storyID", storyID.String()), zap.Error(err))
		handleServiceError(c, err, h.logger) // Обрабатываем стандартные ошибки, включая ErrNotFound и ErrForbidden
		return
	}

	// При успехе возвращаем 204 No Content
	c.Status(http.StatusNoContent)
}

// <<<<< КОНЕЦ ОБРАБОТЧИКА УДАЛЕНИЯ ОПУБЛИКОВАННОЙ ИСТОРИИ >>>>>

// listStoriesWithProgress возвращает список опубликованных историй, в которых у пользователя есть прогресс.
// GET /published-stories/me/progress?limit=10&cursor=...
func (h *GameplayHandler) listStoriesWithProgress(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		h.logger.Warn("Failed to get user ID from context", zap.Error(err))
		c.JSON(http.StatusUnauthorized, APIError{Message: "Unauthorized: " + err.Error()})
		return
	}

	// Парсим параметры пагинации
	limitStr := c.DefaultQuery("limit", "10")
	cursor := c.Query("cursor")

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 || limit > 100 {
		h.logger.Warn("Invalid limit parameter for stories with progress", zap.String("limit", limitStr))
		c.JSON(http.StatusBadRequest, APIError{Message: "Invalid limit parameter. Must be between 1 and 100."})
		return
	}

	// Вызываем метод сервиса для получения историй с прогрессом
	stories, nextCursor, serviceErr := h.service.GetStoriesWithProgress(c.Request.Context(), userID, limit, cursor)
	if serviceErr != nil {
		h.logger.Error("Failed to get stories with progress", zap.String("userID", userID.String()), zap.Error(serviceErr))
		// Используем handleServiceError, который умеет обрабатывать ErrNotFound и другие стандартные ошибки
		handleServiceError(c, serviceErr, h.logger)
		return
	}

	// Формируем ответ
	response := PaginatedResponse{
		Data:       stories,
		NextCursor: nextCursor,
	}

	c.JSON(http.StatusOK, response)
}

// --- НОВЫЕ ОБРАБОТЧИКИ ДЛЯ УПРАВЛЕНИЯ СОСТОЯНИЯМИ ИГРЫ ---

// listGameStates возвращает список состояний игры (слотов сохранений) для пользователя и истории.
// GET /published-stories/{storyId}/gamestates
func (h *GameplayHandler) listGameStates(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return // Ошибка уже обработана
	}

	storyIdStr := c.Param("storyId") // Получаем ID истории из пути
	storyID, err := uuid.Parse(storyIdStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in listGameStates", zap.String("storyId", storyIdStr), zap.Error(err))
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
