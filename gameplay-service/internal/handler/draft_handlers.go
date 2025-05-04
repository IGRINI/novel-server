package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	sharedModels "novel-server/shared/models"
	"strconv"

	// Добавляем импорт сервиса

	"github.com/gin-gonic/gin" // <<< Используем Gin
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Новый запрос для начальной генерации
type generateInitialStoryRequest struct {
	Prompt   string `json:"prompt" binding:"required"`   // <<< Используем binding для Gin
	Language string `json:"language" binding:"required"` // <<< Добавляем язык
}

// Новый обработчик для начальной генерации
func (h *GameplayHandler) generateInitialStory(c *gin.Context) { // <<< *gin.Context
	userID, err := getUserIDFromContext(c)
	if err != nil {
		// getUserIDFromContext уже вызвал Abort
		return
	}

	var req generateInitialStoryRequest
	// Используем ShouldBindJSON для Gin
	if err := c.ShouldBindJSON(&req); err != nil {
		// Используем handleServiceError (который вызовет Abort)
		h.logger.Warn("Invalid request body for generateInitialStory", zap.Stringer("userID", userID), zap.Error(err))
		handleServiceError(c, sharedModels.ErrBadRequest, h.logger) // Передаем базовую ошибку
		return
	}

	// <<< ВОССТАНАВЛИВАЕМ ВАЛИДАЦИЮ ЯЗЫКА >>>
	// Используем карту из GameplayHandler для быстрой проверки
	if _, ok := h.supportedLanguagesMap[req.Language]; !ok {
		// Язык не найден в списке разрешенных
		h.logger.Warn("Unsupported language provided for generateInitialStory", zap.Stringer("userID", userID), zap.String("language", req.Language))
		// Возвращаем ошибку Bad Request
		handleServiceError(c, fmt.Errorf("%w: unsupported language '%s'", sharedModels.ErrBadRequest, req.Language), h.logger)
		return
	}
	// <<< КОНЕЦ ВАЛИДАЦИИ ЯЗЫКА >>>

	// Вызываем новый метод сервиса
	config, err := h.service.GenerateInitialStory(c.Request.Context(), userID, req.Prompt, req.Language)
	if err != nil {
		// GenerateInitialStory может вернуть ошибку И конфиг (если ошибка была при отправке задачи)
		if config != nil {
			h.logger.Error("Error publishing initial generation task", zap.String("userID", userID.String()), zap.Error(err))
			// Возвращаем 500 и сам конфиг (с ID и статусом Error)
			c.JSON(http.StatusInternalServerError, config)
			return // Завершаем обработку
		}
		// Если ошибка была до отправки (например, проверка конкуренции), обрабатываем стандартно
		h.logger.Error("Error generating initial story", zap.String("userID", userID.String()), zap.Error(err))
		handleServiceError(c, err, h.logger) // Используем обновленный handleServiceError
		return
	}

	// Возвращаем 202 Accepted и созданный конфиг (со статусом generating)
	c.JSON(http.StatusAccepted, config)
}

func (h *GameplayHandler) getStoryConfig(c *gin.Context) { // <<< *gin.Context
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return // getUserIDFromContext уже вызвал Abort
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in getStoryConfig", zap.String("id", idStr), zap.Error(err))
		// Используем handleServiceError
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	config, err := h.service.GetStoryConfig(c.Request.Context(), id, userID)
	if err != nil {
		h.logger.Error("Error getting story config", zap.String("userID", userID.String()), zap.String("storyID", id.String()), zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	// Проверяем, есть ли сам конфиг
	if config.Config == nil || len(config.Config) == 0 {
		detail := StoryConfigDetail{
			ID:        config.ID.String(),
			CreatedAt: config.CreatedAt,
			Status:    string(config.Status),
			Config:    nil,
		}
		h.logger.Warn("Story config JSON is nil, returning basic details", zap.String("storyID", id.String()), zap.String("status", string(config.Status)))
		c.JSON(http.StatusOK, detail)
		return
	}

	// Определяем внутреннюю структуру для анмаршалинга JSON из config.Config
	// Используем теги json, соответствующие сжатым ключам из промпта narrator.md
	type internalCoreStat struct {
		D  string `json:"d"`
		Iv int    `json:"iv"`
		Go struct {
			Min bool `json:"min"`
			Max bool `json:"max"`
		} `json:"go"`
	}
	type internalConfig struct {
		T     string                      `json:"t"`
		Sd    string                      `json:"sd"`
		Fr    string                      `json:"fr"`
		Gn    string                      `json:"gn"`
		Ac    bool                        `json:"ac"`
		Pn    string                      `json:"pn"`
		PDesc string                      `json:"p_desc"`
		Wc    string                      `json:"wc"`
		Ss    string                      `json:"ss"`
		Cs    map[string]internalCoreStat `json:"cs"`
	}

	var parsedInternal internalConfig
	if err := json.Unmarshal(config.Config, &parsedInternal); err != nil {
		h.logger.Error("Failed to unmarshal story config JSON", zap.String("storyID", id.String()), zap.Error(err))
		// Создаем ошибку для handleServiceError
		parsingErr := fmt.Errorf("failed to parse internal story config for story %s", id.String())
		handleServiceError(c, parsingErr, h.logger) // Это приведет к 500 Internal Error
		return
	}

	// Преобразуем внутреннюю структуру в публичный DTO
	publicDetail := StoryConfigParsedDetail{
		Title:             parsedInternal.T,
		ShortDescription:  parsedInternal.Sd,
		Franchise:         parsedInternal.Fr,
		Genre:             parsedInternal.Gn,
		IsAdultContent:    parsedInternal.Ac,
		PlayerName:        parsedInternal.Pn,
		PlayerDescription: parsedInternal.PDesc,
		WorldContext:      parsedInternal.Wc,
		StorySummary:      parsedInternal.Ss,
		CoreStats:         make(map[string]parsedCoreStat, len(parsedInternal.Cs)),
	}

	// Преобразуем карту статов
	for name, internalStat := range parsedInternal.Cs {
		publicDetail.CoreStats[name] = parsedCoreStat{
			Description:  internalStat.D,
			InitialValue: internalStat.Iv,
			GameOverConditions: parsedGameOverConditions{
				Min: internalStat.Go.Min,
				Max: internalStat.Go.Max,
			},
		}
	}

	c.JSON(http.StatusOK, publicDetail)
}

// Обновляем reviseStoryRequest и reviseStoryConfig
type reviseStoryRequest struct {
	RevisionPrompt string `json:"revision_prompt" binding:"required"` // <<< Используем binding
}

func (h *GameplayHandler) reviseStoryConfig(c *gin.Context) { // <<< *gin.Context
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in reviseStoryConfig", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	var req reviseStoryRequest
	// Используем ShouldBindJSON
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid request body for reviseStoryConfig", zap.Stringer("userID", userID), zap.String("storyID", id.String()), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: %s", sharedModels.ErrBadRequest, err.Error()), h.logger)
		return
	}

	err = h.service.ReviseDraft(c.Request.Context(), id, userID, req.RevisionPrompt)
	if err != nil {
		h.logger.Error("Error revising draft", zap.String("userID", userID.String()), zap.String("storyID", id.String()), zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	// Возвращаем 202 Accepted
	c.Status(http.StatusAccepted) // <<< Используем c.Status для No Content
}

// PublishStoryResponse определяет тело ответа при публикации истории.
type PublishStoryResponse struct {
	PublishedStoryID string `json:"published_story_id"`
}

// publishStoryDraft обрабатывает запрос на публикацию черновика.
func (h *GameplayHandler) publishStoryDraft(c *gin.Context) { // <<< *gin.Context
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return
	}

	idStr := c.Param("id")
	draftID, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid draft ID format received", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	h.logger.Info("Received request to publish draft", zap.String("userID", userID.String()), zap.String("draftID", draftID.String()))

	publishedID, err := h.service.PublishDraft(c.Request.Context(), draftID, userID)
	if err != nil {
		h.logger.Error("Error publishing draft", zap.String("userID", userID.String()), zap.String("draftID", draftID.String()), zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	h.logger.Info("Draft published successfully", zap.String("userID", userID.String()), zap.String("draftID", draftID.String()), zap.String("publishedID", publishedID.String()))

	resp := PublishStoryResponse{PublishedStoryID: publishedID.String()}
	c.JSON(http.StatusAccepted, resp)
}

// listStoryConfigs получает список черновиков пользователя с пагинацией.
func (h *GameplayHandler) listStoryConfigs(c *gin.Context) { // <<< *gin.Context
	h.logger.Debug(">>> Entered listStoryConfigs <<<") // <<< НОВЫЙ ЛОГ В САМОМ НАЧАЛЕ
	userID, err := getUserIDFromContext(c)
	if err != nil {
		// <<< ИСПРАВЛЕНО: Обработка ошибки контекста >>>
		h.logger.Error("Failed to get valid userID from context in listStoryConfigs", zap.Error(err))
		c.AbortWithStatusJSON(http.StatusInternalServerError, sharedModels.ErrorResponse{
			Code:    sharedModels.ErrCodeInternal,
			Message: "Internal error processing user context: " + err.Error(), // Добавим текст ошибки
		})
		return // Оставляем return после Abort
	}

	// Используем c.Query для Gin
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

	h.logger.Debug("Fetching story drafts",
		zap.String("userID", userID.String()),
		zap.Int("limit", limit),
		zap.String("cursor", cursor),
	)

	drafts, nextCursor, err := h.service.ListUserDrafts(c.Request.Context(), userID, cursor, limit)
	// <<< ДОБАВЛЕНО: Логирование результата сервисного метода >>>
	if err != nil {
		h.logger.Error("Error listing story drafts from service", zap.String("userID", userID.String()), zap.Error(err))
	} else {
		h.logger.Debug("Service ListUserDrafts returned",
			zap.String("userID", userID.String()),
			zap.Int("draft_count", len(drafts)),
			zap.Stringp("next_cursor", &nextCursor)) // Логируем указатель на nextCursor
	}

	if err != nil {
		h.logger.Error("Error listing story drafts", zap.String("userID", userID.String()), zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	draftSummaries := make([]StoryConfigSummary, len(drafts))
	for i, draft := range drafts {
		draftSummaries[i] = StoryConfigSummary{
			ID:          draft.ID.String(),
			Title:       draft.Title,
			Description: draft.Description,
			CreatedAt:   draft.CreatedAt,
			Status:      string(draft.Status),
		}
	}

	resp := PaginatedResponse{
		Data:       draftSummaries,
		NextCursor: nextCursor,
	}

	h.logger.Debug("Successfully fetched story drafts",
		zap.String("userID", userID.String()),
		zap.Int("count", len(draftSummaries)),
		zap.Bool("hasNext", nextCursor != ""),
	)

	// <<< ДОБАВЛЕНО: Логирование перед отправкой ответа >>>
	h.logger.Debug("Data prepared for JSON response in listStoryConfigs",
		zap.Any("response_data", resp))

	c.JSON(http.StatusOK, resp)
}

// <<< ДОБАВЛЕНО: Обработчик для повторной генерации >>>
func (h *GameplayHandler) retryDraftGeneration(c *gin.Context) { // <<< *gin.Context
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return
	}

	draftIDStr := c.Param("draft_id")
	draftID, err := uuid.Parse(draftIDStr)
	if err != nil {
		h.logger.Warn("Invalid draft ID format in retryDraftGeneration", zap.String("id", draftIDStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid draft ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	log := h.logger.With(zap.String("userID", userID.String()), zap.String("draftID", draftID.String()))
	log.Info("Handling retry draft generation request")

	err = h.service.RetryDraftGeneration(c.Request.Context(), draftID, userID)
	if err != nil {
		log.Error("Error retrying draft generation", zap.Error(err))
		// Используем handleServiceError для стандартизации
		handleServiceError(c, err, h.logger)
		return
	}

	log.Info("Draft retry request accepted")
	c.Status(http.StatusAccepted)
}

// <<<<< НАЧАЛО ОБРАБОТЧИКА УДАЛЕНИЯ ЧЕРНОВИКА >>>>>
func (h *GameplayHandler) deleteDraft(c *gin.Context) { // <<< *gin.Context
	userID, err := getUserIDFromContext(c)
	if err != nil {
		// getUserIDFromContext уже вызвал Abort
		return
	}

	idStr := c.Param("id")
	draftID, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid draft ID format in deleteDraft", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid draft ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	// Вызываем метод сервиса
	err = h.service.DeleteDraft(c.Request.Context(), draftID, userID)
	if err != nil {
		h.logger.Error("Error deleting draft", zap.String("userID", userID.String()), zap.String("draftID", draftID.String()), zap.Error(err))
		handleServiceError(c, err, h.logger) // Обрабатываем стандартные ошибки, включая ErrNotFound
		return
	}

	// При успехе возвращаем 204 No Content
	c.Status(http.StatusNoContent)
}

// <<<<< КОНЕЦ ОБРАБОТЧИКА УДАЛЕНИЯ ЧЕРНОВИКА >>>>>
