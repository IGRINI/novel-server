package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	sharedModels "novel-server/shared/models"

	// Добавляем импорт сервиса

	"github.com/gin-gonic/gin" // <<< Используем Gin
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

	// Используем новую вспомогательную функцию
	id, ok := parseUUIDParam(c, "id", h.logger)
	if !ok {
		return // Ошибка уже обработана и ответ отправлен
	}

	config, err := h.service.GetStoryConfig(c.Request.Context(), id, userID)
	if err != nil {
		h.logger.Error("Error getting story config", zap.String("userID", userID.String()), zap.String("storyID", id.String()), zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	// Проверяем, есть ли сам конфиг
	if len(config.Config) == 0 {
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

	// Сначала анмаршалим в sharedModels.Config, чтобы получить вывод AI как есть
	var actualUserConfig sharedModels.Config
	if err := json.Unmarshal(config.Config, &actualUserConfig); err != nil {
		h.logger.Error("Failed to unmarshal StoryConfig.Config into sharedModels.Config",
			zap.String("storyID", id.String()),
			zap.Error(err),
		)
		handleServiceError(c, fmt.Errorf("failed to parse base story config for story %s: %w", id.String(), err), h.logger)
		return
	}

	// Структуры для ответа клиенту (эти определения могут быть вынесены на уровень пакета, если используются в других местах)
	type parsedCoreStat struct {
		Description        string                   `json:"description"`
		InitialValue       int                      `json:"initial_value,omitempty"`        // omitempty, если может не быть
		GameOverConditions parsedGameOverConditions `json:"game_over_conditions,omitempty"` // omitempty, если может не быть
	}
	type StoryConfigParsedDetail struct {
		Title             string                    `json:"title"`
		ShortDescription  string                    `json:"short_description"`
		Franchise         string                    `json:"franchise,omitempty"`
		Genre             string                    `json:"genre"`
		Language          string                    `json:"language"`           // Добавим язык из StoryConfig
		PlayerName        string                    `json:"player_name"`        // ProtagonistName
		PlayerDescription string                    `json:"player_description"` // ProtagonistDescription
		WorldContext      string                    `json:"world_context"`
		StorySummary      string                    `json:"story_summary"`
		CoreStats         map[string]parsedCoreStat `json:"core_stats"`
		PlayerPrefs       sharedModels.PlayerPrefs  `json:"player_preferences"` // Добавим PlayerPrefs
		// Статус черновика
		Status sharedModels.StoryStatus `json:"status"`
	}

	// Заполняем publicDetail из actualUserConfig
	publicDetail := StoryConfigParsedDetail{
		Title:             actualUserConfig.Title,
		ShortDescription:  actualUserConfig.ShortDescription,
		Franchise:         actualUserConfig.Franchise,
		Genre:             actualUserConfig.Genre,
		Language:          config.Language, // Язык берем из основного объекта StoryConfig
		PlayerName:        actualUserConfig.ProtagonistName,
		PlayerDescription: actualUserConfig.ProtagonistDescription,
		WorldContext:      actualUserConfig.WorldContext,
		StorySummary:      actualUserConfig.StorySummary,
		CoreStats:         make(map[string]parsedCoreStat),
		PlayerPrefs:       actualUserConfig.PlayerPrefs,
		Status:            config.Status, // Статус берем из основного объекта StoryConfig
	}

	// Заполняем CoreStats. На этом этапе у нас есть только описания из вывода Narrator.
	// InitialValue и GameOverConditions появятся после генерации NovelSetup.
	// Для черновика мы можем либо не показывать их, либо показывать с нулевыми/дефолтными значениями.
	// Здесь я просто передаю описание.
	if actualUserConfig.CoreStats != nil {
		for name, narratorStat := range actualUserConfig.CoreStats { // narratorStat is NarratorCsStat
			var goc parsedGameOverConditions
			switch narratorStat.Go {
			case "min":
				goc.Min = true
			case "max":
				goc.Max = true
			case "both":
				goc.Min = true
				goc.Max = true
			}

			publicDetail.CoreStats[name] = parsedCoreStat{
				Description: narratorStat.Description,
				// InitialValue не устанавливается из narratorStat, так как его там нет.
				// Это значение должно приходить из NovelSetupContent, если оно уже было сгенерировано.
				GameOverConditions: goc,
			}
		}
	}

	// TODO: Если это черновик, который уже прошел этап Setup (т.е. есть PublishedStory и в нем есть Setup),
	// то нужно загрузить PublishedStory.Setup, анмаршалить его в NovelSetupContent
	// и обогатить publicDetail.CoreStats полями InitialValue и GameOverConditions.
	// Это выходит за рамки исправления текущей ошибки анмаршалинга.

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

	// Используем новую вспомогательную функцию
	id, ok := parseUUIDParam(c, "id", h.logger)
	if !ok {
		return // Ошибка уже обработана
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

	// Используем новую вспомогательную функцию
	draftID, ok := parseUUIDParam(c, "id", h.logger)
	if !ok {
		return // Ошибка уже обработана
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

	// Используем новую вспомогательную функцию
	limit, cursor, ok := parsePaginationParams(c, 10, 100, h.logger)
	if !ok {
		return // Ошибка уже обработана
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

	// Используем новую вспомогательную функцию
	draftID, ok := parseUUIDParam(c, "draft_id", h.logger)
	if !ok {
		return // Ошибка уже обработана
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

	// Используем новую вспомогательную функцию
	draftID, ok := parseUUIDParam(c, "id", h.logger)
	if !ok {
		return // Ошибка уже обработана
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
