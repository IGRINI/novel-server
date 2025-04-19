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

// --- Структуры для ответов и запросов --- //

// <<< НОВЫЕ СТРУКТУРЫ DTO ДЛЯ ОТВЕТА СЦЕНЫ КЛИЕНТУ >>>

// GameSceneResponse - структура ответа для сцены, отправляемая клиенту
type GameSceneResponse struct {
	ID               string           `json:"id"`               // ID сцены
	PublishedStoryID string           `json:"publishedStoryId"` // ID истории
	Content          GameSceneContent `json:"content"`          // Содержимое сцены для клиента
}

// GameSceneContent - Содержимое сцены, отправляемое клиенту
type GameSceneContent struct {
	Type    string            `json:"type"`         // Тип контента ("choices", "game_over", "continuation")
	Choices []GameChoiceBlock `json:"ch,omitempty"` // Блоки выбора (только для type="choices" или "continuation")
	// Поля для концовок (копируем из SceneContent, т.к. они нужны клиенту)
	EndingText             string         `json:"et,omitempty"`  // Текст стандартной концовки
	NewPlayerDescription   string         `json:"npd,omitempty"` // Описание нового персонажа (для продолжения)
	CoreStatsReset         map[string]int `json:"csr,omitempty"` // Новые статы (для продолжения)
	EndingTextPreviousChar string         `json:"etp,omitempty"` // Текст концовки пред. персонажа (для продолжения)
}

// GameChoiceBlock - Блок выбора для клиента
type GameChoiceBlock struct {
	Shuffleable int               `json:"sh"`   // Можно ли перемешивать
	Description string            `json:"desc"` // Описание ситуации
	Options     []GameSceneOption `json:"opts"` // Опции выбора
}

// GameSceneOption - Опция выбора для клиента
type GameSceneOption struct {
	Text         string           `json:"txt"`  // Текст опции
	Consequences GameConsequences `json:"cons"` // Последствия (только нужные клиенту)
}

// GameConsequences - Последствия выбора для клиента
type GameConsequences struct {
	CoreStatsChange map[string]int `json:"cs_chg,omitempty"`   // Изменения статов (показываем игроку)
	ResponseText    string         `json:"resp_txt,omitempty"` // Текст-реакция на выбор (если есть)
}

// <<< КОНЕЦ НОВЫХ СТРУКТУР DTO >>>

// <<< ДОБАВЛЕНО: Определение запроса для makeChoice
type MakeChoicesRequest struct {
	SelectedOptionIndices []int `json:"selected_option_indices" binding:"required,dive,min=0,max=1"`
}

// PublishedStorySummary представляет базовую информацию об опубликованной истории для списков.
// !!! Поля AuthorName, Genre, Language, LastPlayedAt УБРАНЫ, т.к. их нет в основной модели PublishedStory !!!
type PublishedStorySummary struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	AuthorID    string    `json:"author_id"` // Это UserID из модели
	PublishedAt time.Time `json:"published_at"`
	LikesCount  int64     `json:"likes_count"`
	IsLiked     bool      `json:"is_liked"` // Лайкнул ли текущий пользователь
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

	stories, nextCursor, err := h.service.ListMyPublishedStories(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		log.Error("Error listing my published stories", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	storySummaries := make([]PublishedStorySummary, len(stories))
	for i, story := range stories {
		title := ""
		if story.Title != nil {
			title = *story.Title // <<< Разыменовываем указатель
		}
		description := ""
		if story.Description != nil {
			description = *story.Description // <<< Разыменовываем указатель
		}
		storySummaries[i] = PublishedStorySummary{
			ID:          story.ID.String(),
			Title:       title,
			Description: description,
			AuthorID:    story.UserID.String(), // <<< Используем UserID как AuthorID
			PublishedAt: story.CreatedAt,       // <<< Используем CreatedAt как PublishedAt
			LikesCount:  story.LikesCount,
			IsLiked:     story.IsLiked,
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
	userID, _ := getUserIDFromContext(c) // Опционально для проверки лайков

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

	stories, nextCursor, err := h.service.ListPublicStories(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		log.Error("Error listing public published stories", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	storySummaries := make([]PublishedStorySummary, len(stories))
	for i, story := range stories {
		title := ""
		if story.Title != nil {
			title = *story.Title
		}
		description := ""
		if story.Description != nil {
			description = *story.Description
		}
		storySummaries[i] = PublishedStorySummary{
			ID:          story.ID.String(),
			Title:       title,
			Description: description,
			AuthorID:    story.UserID.String(),
			PublishedAt: story.CreatedAt,
			LikesCount:  story.LikesCount,
			IsLiked:     story.IsLiked,
		}
	}

	resp := PaginatedResponse{
		Data:       storySummaries,
		NextCursor: nextCursor,
	}

	log.Debug("Successfully fetched public published stories",
		zap.Int("count", len(storySummaries)),
		zap.Bool("hasNext", nextCursor != ""),
	)
	c.JSON(http.StatusOK, resp)
}

// getPublishedStoryDetails получает детальную информацию об опубликованной истории.
func (h *GameplayHandler) getPublishedStoryDetails(c *gin.Context) {
	userID, _ := getUserIDFromContext(c) // Для проверки IsAuthor и LastPlayedAt

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in getPublishedStoryDetails", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	log := h.logger.With(zap.String("storyID", id.String()))
	if userID != uuid.Nil {
		log = log.With(zap.String("userID", userID.String()))
	}
	log.Info("Fetching published story details")

	// Сервис возвращает *service.PublishedStoryDetailDTO
	storyDTO, err := h.service.GetPublishedStoryDetails(c.Request.Context(), id, userID)
	if err != nil {
		log.Error("Error getting published story details", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	// Преобразование *service.PublishedStoryDetailDTO в handler.PublishedStoryDetail
	resp := PublishedStoryDetail{
		ID:                storyDTO.ID.String(),
		Title:             storyDTO.Title,
		ShortDescription:  storyDTO.ShortDescription,
		AuthorID:          storyDTO.AuthorID.String(),
		AuthorName:        storyDTO.AuthorName,
		PublishedAt:       storyDTO.PublishedAt,
		Genre:             storyDTO.Genre,
		Language:          storyDTO.Language,
		IsAdultContent:    storyDTO.IsAdultContent,
		PlayerName:        storyDTO.PlayerName,
		PlayerDescription: storyDTO.PlayerDescription,
		WorldContext:      storyDTO.WorldContext,
		StorySummary:      storyDTO.StorySummary,
		CoreStats:         make(map[string]publishedCoreStatDetail, len(storyDTO.CoreStats)),
		LastPlayedAt:      storyDTO.LastPlayedAt, // Может быть nil
		IsAuthor:          storyDTO.IsAuthor,
	}
	for name, stat := range storyDTO.CoreStats {
		resp.CoreStats[name] = publishedCoreStatDetail{
			Description:        stat.Description,
			InitialValue:       stat.InitialValue,
			GameOverConditions: stat.GameOverConditions,
		}
	}

	log.Info("Successfully fetched published story details")
	c.JSON(http.StatusOK, resp)
}

// getPublishedStoryScene получает текущую игровую сцену для опубликованной истории.
func (h *GameplayHandler) getPublishedStoryScene(c *gin.Context) {
	userID, err := getUserIDFromContext(c) // <<< Меняем 'ok' на 'err'
	if err != nil {                        // <<< Проверяем 'err != nil'
		// Ошибка уже обработана в getUserIDFromContext
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in getPublishedStoryScene", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	log := h.logger.With(zap.String("storyID", id.String()))
	if userID != uuid.Nil { // Используем userID, так как он нужен для сервиса
		log = log.With(zap.String("userID", userID.String()))
	}
	log.Info("Fetching story scene")

	scene, err := h.service.GetStoryScene(c.Request.Context(), userID, id)
	if err != nil {
		// Логируем только если это НЕ стандартная ошибка (NotFound, NeedsGeneration, NotReadyYet)
		// Проверяем на ошибки сервиса и общие ошибки
		if !errors.Is(err, sharedModels.ErrNotFound) &&
			!errors.Is(err, sharedModels.ErrSceneNeedsGeneration) &&
			!errors.Is(err, sharedModels.ErrStoryNotReadyYet) &&
			!errors.Is(err, service.ErrStoryNotFound) && // Добавим проверку на ошибку сервиса
			!errors.Is(err, service.ErrSceneNotFound) { // Добавим проверку на ошибку сервиса
			log.Error("Error getting story scene (unhandled)", zap.Error(err))
		}
		handleServiceError(c, err, h.logger) // Используем общий обработчик
		return
	}

	// <<< НАЧАЛО ПРЕОБРАЗОВАНИЯ В DTO >>>
	// Десериализуем Content из json.RawMessage в структуру SceneContent
	var sceneContent sharedModels.SceneContent
	if err := json.Unmarshal(scene.Content, &sceneContent); err != nil {
		log.Error("Failed to unmarshal scene content", zap.String("sceneID", scene.ID.String()), zap.Error(err))
		handleServiceError(c, fmt.Errorf("internal error processing scene data"), h.logger) // Не отдаем детали ошибки клиенту
		return
	}

	// Создаем DTO для ответа
	responseDTO := GameSceneResponse{
		ID:               scene.ID.String(),
		PublishedStoryID: scene.PublishedStoryID.String(),
		Content: GameSceneContent{
			Type:                   sceneContent.Type,
			EndingText:             sceneContent.EndingText, // Копируем поля концовок
			NewPlayerDescription:   sceneContent.NewPlayerDescription,
			CoreStatsReset:         sceneContent.CoreStatsReset,
			EndingTextPreviousChar: sceneContent.EndingTextPreviousChar,
		},
	}

	// Если тип контента - выборы, то конвертируем их
	if sceneContent.Type == "choices" || sceneContent.Type == "continuation" {
		responseDTO.Content.Choices = make([]GameChoiceBlock, 0, len(sceneContent.Choices))
		for _, choiceBlock := range sceneContent.Choices {
			gameChoiceBlock := GameChoiceBlock{
				Shuffleable: choiceBlock.Shuffleable,
				Description: choiceBlock.Description,
				Options:     make([]GameSceneOption, 0, len(choiceBlock.Options)),
			}
			for _, option := range choiceBlock.Options {
				gameSceneOption := GameSceneOption{
					Text: option.Text,
					Consequences: GameConsequences{
						CoreStatsChange: option.Consequences.CoreStatsChange, // Копируем только изменения статов
						ResponseText:    option.Consequences.ResponseText,    // и текст-реакцию
					},
				}
				gameChoiceBlock.Options = append(gameChoiceBlock.Options, gameSceneOption)
			}
			responseDTO.Content.Choices = append(responseDTO.Content.Choices, gameChoiceBlock)
		}
	}
	// <<< КОНЕЦ ПРЕОБРАЗОВАНИЯ В DTO >>>

	log.Info("Successfully fetched and formatted story scene",
		zap.String("sceneID", scene.ID.String()),
	)
	// <<< ОТПРАВЛЯЕМ DTO >>>
	c.JSON(http.StatusOK, responseDTO)
}

// makeChoice обрабатывает выбор игрока в опубликованной истории.
func (h *GameplayHandler) makeChoice(c *gin.Context) {
	userID, err := getUserIDFromContext(c) // <<< Меняем 'ok' на 'err'
	if err != nil {                        // <<< Проверяем 'err != nil'
		// Ошибка уже обработана в getUserIDFromContext
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in makeChoice", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	var req MakeChoicesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid request body for makeChoice", zap.Stringer("userID", userID), zap.String("storyID", id.String()), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid request body: %v", sharedModels.ErrBadRequest, err), h.logger)
		return
	}

	log := h.logger.With(
		zap.Stringer("userID", userID),
		zap.String("storyID", id.String()),
		zap.Any("selectedOptionIndices", req.SelectedOptionIndices),
	)
	log.Info("Player making choices (batch)")

	err = h.service.MakeChoice(c.Request.Context(), userID, id, req.SelectedOptionIndices)
	if err != nil {
		// Логируем только если это НЕ стандартные ожидаемые ошибки
		if !errors.Is(err, sharedModels.ErrNotFound) &&
			!errors.Is(err, sharedModels.ErrBadRequest) && // BadRequest может быть при невалидном выборе
			!errors.Is(err, service.ErrStoryNotFound) &&
			!errors.Is(err, service.ErrSceneNotFound) &&
			!errors.Is(err, service.ErrInvalidChoiceIndex) && // Добавим ошибку неверного индекса
			!errors.Is(err, service.ErrNoChoicesAvailable) && // Добавим ошибку отсутствия выбора
			!errors.Is(err, sharedModels.ErrSceneNeedsGeneration) { // Если сцена требует генерации
			log.Error("Error making choice (unhandled)", zap.Error(err))
		}
		handleServiceError(c, err, h.logger) // Используем общий обработчик
		return
	}

	log.Info("Player choice processed successfully")

	// После успешного выбора, новая сцена будет доступна через getPublishedStoryScene
	// Возвращаем 204 No Content, т.к. результат нужно запрашивать отдельно
	c.Status(http.StatusNoContent)
}

// deletePlayerProgress удаляет прогресс игрока для опубликованной истории.
func (h *GameplayHandler) deletePlayerProgress(c *gin.Context) {
	userID, err := getUserIDFromContext(c) // <<< Меняем 'ok' на 'err'
	if err != nil {                        // <<< Проверяем 'err != nil'
		// Ошибка уже обработана в getUserIDFromContext
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in deletePlayerProgress", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	log := h.logger.With(zap.String("storyID", id.String()))
	if userID != uuid.Nil {
		log = log.With(zap.String("userID", userID.String()))
	}
	log.Info("Deleting player progress")

	err = h.service.DeletePlayerProgress(c.Request.Context(), userID, id)
	if err != nil {
		// Логируем только если это не ErrNotFound (ожидаемая ошибка, если прогресса нет)
		if !errors.Is(err, service.ErrPlayerProgressNotFound) && !errors.Is(err, sharedModels.ErrNotFound) {
			log.Error("Error deleting player progress", zap.Error(err))
		}
		// Можно вернуть 204 даже при ErrNotFound, т.к. итоговое состояние - прогресса нет.
		// Но если хотим четко сигнализировать, что прогресса и не было, используем handleServiceError
		if errors.Is(err, service.ErrPlayerProgressNotFound) || errors.Is(err, sharedModels.ErrNotFound) {
			// Если прогресса не найдено, можно просто вернуть 204, так как цель достигнута
			log.Info("Player progress not found, deletion skipped (considered success)")
			c.Status(http.StatusNoContent)
			return
		}
		// Для других ошибок используем общий обработчик
		handleServiceError(c, err, h.logger)
		return
	}

	log.Info("Player progress deleted successfully")
	c.Status(http.StatusNoContent)
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

	stories, nextCursor, err := h.service.ListLikedStories(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		log.Error("Error listing liked stories", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	storySummaries := make([]PublishedStorySummary, len(stories))
	for i, story := range stories {
		title := ""
		if story.Title != nil {
			title = *story.Title
		}
		description := ""
		if story.Description != nil {
			description = *story.Description
		}
		storySummaries[i] = PublishedStorySummary{
			ID:          story.ID.String(),
			Title:       title,
			Description: description,
			AuthorID:    story.UserID.String(),
			PublishedAt: story.CreatedAt,
			LikesCount:  story.LikesCount,
			IsLiked:     true, // Все истории в этом списке лайкнуты пользователем
		}
	}

	resp := PaginatedResponse{
		Data:       storySummaries,
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

	// Вызываем новый метод сервиса
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
