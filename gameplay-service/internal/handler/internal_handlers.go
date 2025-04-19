package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	sharedInterfaces "novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// APIError используется для ответа об ошибках в этом внутреннем хендлере
// (Возможно, стоит перенести в shared/models, если будет переиспользоваться)
// type APIError struct {
//  Message string `json:"message"`
// }

// listUserDraftsInternal возвращает список черновиков для указанного пользователя (для админки).
func (h *GameplayHandler) listUserDraftsInternal(c *gin.Context) {
	log := h.logger.With(zap.String("handler", "listUserDraftsInternal"))

	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Warn("Invalid user ID format", zap.String("user_id", userIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, sharedModels.ErrorResponse{Message: "Invalid user ID format"})
		return
	}

	limitStr := c.Query("limit")
	cursor := c.Query("cursor")
	limit := 20 // Значение по умолчанию
	if limitStr != "" {
		if l, parseErr := strconv.Atoi(limitStr); parseErr == nil && l > 0 {
			limit = l
		} else {
			log.Warn("Invalid limit parameter received, using default", zap.String("limit", limitStr), zap.Error(parseErr))
		}
	}

	log = log.With(zap.Stringer("userID", userID), zap.Int("limit", limit), zap.String("cursor", cursor))
	log.Info("Internal request for user drafts")

	drafts, nextCursor, err := h.service.ListUserDrafts(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		if errors.Is(err, sharedInterfaces.ErrInvalidCursor) {
			log.Warn("Invalid cursor provided", zap.Error(err))
			c.JSON(http.StatusBadRequest, sharedModels.ErrorResponse{Message: "Invalid cursor format"})
			return
		}
		log.Error("Failed to list user drafts internally", zap.Error(err))
		c.JSON(http.StatusInternalServerError, sharedModels.ErrorResponse{Message: "Failed to retrieve drafts"})
		return
	}

	// Используем ту же DTO PaginatedResponse, что и для публичного API
	response := PaginatedResponse{
		Data:       drafts, // Сервис уже возвращает []*StoryConfigSummary
		NextCursor: nextCursor,
	}
	c.JSON(http.StatusOK, response)
}

// listUserStoriesInternal возвращает список опубликованных историй пользователя (для админки).
func (h *GameplayHandler) listUserStoriesInternal(c *gin.Context) {
	log := h.logger.With(zap.String("handler", "listUserStoriesInternal"))

	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Warn("Invalid user ID format", zap.String("user_id", userIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, sharedModels.ErrorResponse{Message: "Invalid user ID format"})
		return
	}

	limitStr := c.Query("limit")
	offsetStr := c.Query("offset")

	limit := 20 // Значение по умолчанию
	if limitStr != "" {
		if l, parseErr := strconv.Atoi(limitStr); parseErr == nil && l > 0 {
			limit = l
		} else {
			log.Warn("Invalid limit parameter received, using default", zap.String("limit", limitStr), zap.Error(parseErr))
		}
	}
	offset := 0
	if offsetStr != "" {
		if o, parseErr := strconv.Atoi(offsetStr); parseErr == nil && o >= 0 {
			offset = o
		} else {
			log.Warn("Invalid offset parameter received, using default", zap.String("offset", offsetStr), zap.Error(parseErr))
		}
	}

	log = log.With(zap.Stringer("userID", userID), zap.Int("limit", limit), zap.Int("offset", offset))
	log.Info("Internal request for user published stories")

	stories, err := h.service.ListUserPublishedStories(c.Request.Context(), userID, limit, offset)
	if err != nil {
		log.Error("Failed to list user published stories internally", zap.Error(err))
		c.JSON(http.StatusInternalServerError, sharedModels.ErrorResponse{Message: "Failed to retrieve published stories"})
		return
	}

	// Для offset пагинации нам нужно определить 'hasMore'
	hasMore := len(stories) == limit

	// Отдаем только данные, без курсора. Клиент должен сам определить пагинацию.
	// Можно было бы создать отдельную DTO для этого ответа.
	type InternalListResponse struct {
		Data    interface{} `json:"data"`
		HasMore bool        `json:"has_more"`
	}

	response := InternalListResponse{
		Data:    stories, // Сервис возвращает []*PublishedStory
		HasMore: hasMore,
	}

	c.JSON(http.StatusOK, response)
}

// <<< ДОБАВЛЕНО: Обработчик для получения деталей черновика >>>
func (h *GameplayHandler) getDraftDetailsInternal(c *gin.Context) {
	log := h.logger.With(zap.String("handler", "getDraftDetailsInternal"))
	draftIDStr := c.Param("draft_id")
	draftID, err := uuid.Parse(draftIDStr)
	if err != nil {
		log.Warn("Invalid draft ID format", zap.String("draft_id", draftIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, sharedModels.ErrorResponse{Message: "Invalid draft ID format"})
		return
	}
	log = log.With(zap.String("draftID", draftID.String()))
	log.Info("Internal request for draft details")

	// Вызываем новый метод сервиса (DraftService)
	draft, err := h.service.GetDraftDetailsInternal(c.Request.Context(), draftID)
	if err != nil {
		// Обрабатываем ошибку (например, NotFound)
		if errors.Is(err, sharedModels.ErrNotFound) {
			log.Info("Draft not found")
			c.JSON(http.StatusNotFound, sharedModels.ErrorResponse{Message: "Draft not found"})
		} else {
			log.Error("Failed to get draft details internally", zap.Error(err))
			c.JSON(http.StatusInternalServerError, sharedModels.ErrorResponse{Message: "Failed to retrieve draft details"})
		}
		return
	}
	c.JSON(http.StatusOK, draft)
}

// <<< ДОБАВЛЕНО: Обработчик для получения деталей опубликованной истории >>>
func (h *GameplayHandler) getPublishedStoryDetailsInternal(c *gin.Context) {
	log := h.logger.With(zap.String("handler", "getPublishedStoryDetailsInternal"))
	storyIDStr := c.Param("story_id")
	storyID, err := uuid.Parse(storyIDStr)
	if err != nil {
		log.Warn("Invalid story ID format", zap.String("story_id", storyIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, sharedModels.ErrorResponse{Message: "Invalid story ID format"})
		return
	}
	log = log.With(zap.String("storyID", storyID.String()))
	log.Info("Internal request for published story details")

	// Вызываем новый метод сервиса (StoryBrowsingService)
	story, err := h.service.GetPublishedStoryDetailsInternal(c.Request.Context(), storyID)
	if err != nil {
		// Обрабатываем ошибку (например, NotFound)
		if errors.Is(err, sharedModels.ErrNotFound) {
			log.Info("Published story not found")
			c.JSON(http.StatusNotFound, sharedModels.ErrorResponse{Message: "Published story not found"})
		} else {
			log.Error("Failed to get published story details internally", zap.Error(err))
			c.JSON(http.StatusInternalServerError, sharedModels.ErrorResponse{Message: "Failed to retrieve published story details"})
		}
		return
	}
	c.JSON(http.StatusOK, story)
}

// <<< ДОБАВЛЕНО: Обработчик для получения списка сцен истории >>>
func (h *GameplayHandler) listStoryScenesInternal(c *gin.Context) {
	log := h.logger.With(zap.String("handler", "listStoryScenesInternal"))
	storyIDStr := c.Param("story_id")
	storyID, err := uuid.Parse(storyIDStr)
	if err != nil {
		log.Warn("Invalid story ID format", zap.String("story_id", storyIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, sharedModels.ErrorResponse{Message: "Invalid story ID format"})
		return
	}
	log = log.With(zap.String("storyID", storyID.String()))
	log.Info("Internal request for story scenes list")

	// Вызываем новый метод сервиса (StoryBrowsingService)
	scenes, err := h.service.ListStoryScenesInternal(c.Request.Context(), storyID)
	if err != nil {
		// Можно добавить обработку NotFound, если решено, что пустой список - это ошибка
		log.Error("Failed to list story scenes internally", zap.Error(err))
		c.JSON(http.StatusInternalServerError, sharedModels.ErrorResponse{Message: "Failed to retrieve story scenes"})
		return
	}

	// Возвращаем список сцен (может быть пустым)
	c.JSON(http.StatusOK, scenes)
}

// <<< ДОБАВЛЕНО: Обработчик для обновления черновика >>>
func (h *GameplayHandler) updateDraftInternal(c *gin.Context) {
	log := h.logger.With(zap.String("handler", "updateDraftInternal"))

	// Парсим ID
	userIDStr := c.Param("user_id") // userID не используется сервисом, но есть в URL
	draftIDStr := c.Param("draft_id")
	draftID, err := uuid.Parse(draftIDStr)
	if err != nil {
		log.Warn("Invalid draft ID format", zap.String("draft_id", draftIDStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid draft ID", sharedModels.ErrBadRequest), h.logger)
		return
	}
	log = log.With(zap.String("draftID", draftID.String()), zap.String("userIDParam", userIDStr))

	// Парсим тело запроса
	type updateRequest struct {
		ConfigJson    string                   `json:"configJson"`
		UserInputJson string                   `json:"userInputJson"`
		Status        sharedModels.StoryStatus `json:"status"`
	}
	var req updateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Warn("Invalid request body for draft update", zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid request body: %v", sharedModels.ErrBadRequest, err), h.logger)
		return
	}

	log.Info("Handling internal draft update request", zap.String("newStatus", string(req.Status)))

	// Вызываем сервис
	err = h.service.UpdateDraftInternal(c.Request.Context(), draftID, req.ConfigJson, req.UserInputJson, req.Status)
	if err != nil {
		log.Error("Error updating draft internally", zap.Error(err))
		handleServiceError(c, err, h.logger) // Передаем ошибку для стандартизированной обработки
		return
	}

	log.Info("Internal draft update successful")
	c.Status(http.StatusNoContent) // Успех
}

// <<< ДОБАВЛЕНО: Обработчик для обновления истории >>>
func (h *GameplayHandler) updateStoryInternal(c *gin.Context) {
	log := h.logger.With(zap.String("handler", "updateStoryInternal"))

	// Парсим ID
	userIDStr := c.Param("user_id")
	storyIDStr := c.Param("story_id")
	storyID, err := uuid.Parse(storyIDStr)
	if err != nil {
		log.Warn("Invalid story ID format", zap.String("story_id", storyIDStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID", sharedModels.ErrBadRequest), h.logger)
		return
	}
	log = log.With(zap.String("storyID", storyID.String()), zap.String("userIDParam", userIDStr))

	// Парсим тело запроса
	type updateRequest struct {
		ConfigJson json.RawMessage          `json:"configJson"`
		SetupJson  json.RawMessage          `json:"setupJson"`
		Status     sharedModels.StoryStatus `json:"status"`
	}
	var req updateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Warn("Invalid request body for story update", zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid request body: %v", sharedModels.ErrBadRequest, err), h.logger)
		return
	}

	log.Info("Handling internal story update request", zap.String("newStatus", string(req.Status)))

	// Вызываем сервис, передавая json.RawMessage напрямую
	err = h.service.UpdateStoryInternal(c.Request.Context(), storyID, req.ConfigJson, req.SetupJson, req.Status)
	if err != nil {
		log.Error("Error updating story internally", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	log.Info("Internal story update successful")
	c.Status(http.StatusNoContent)
}

// <<< ДОБАВЛЕНО: Обработчик для обновления сцены >>>
func (h *GameplayHandler) updateSceneInternal(c *gin.Context) {
	log := h.logger.With(zap.String("handler", "updateSceneInternal"))

	// Парсим ID
	userIDStr := c.Param("user_id")
	storyIDStr := c.Param("story_id")
	sceneIDStr := c.Param("scene_id")
	sceneID, err := uuid.Parse(sceneIDStr)
	if err != nil {
		log.Warn("Invalid scene ID format", zap.String("scene_id", sceneIDStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid scene ID", sharedModels.ErrBadRequest), h.logger)
		return
	}
	log = log.With(zap.String("sceneID", sceneID.String()), zap.String("storyIDParam", storyIDStr), zap.String("userIDParam", userIDStr))

	// Парсим тело запроса
	type updateRequest struct {
		ContentJson string `json:"contentJson" binding:"required"`
	}
	var req updateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Warn("Invalid request body for scene update", zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid request body: %v", sharedModels.ErrBadRequest, err), h.logger)
		return
	}

	log.Info("Handling internal scene update request")

	// Вызываем сервис
	err = h.service.UpdateSceneInternal(c.Request.Context(), sceneID, req.ContentJson)
	if err != nil {
		log.Error("Error updating scene internally", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	log.Info("Internal scene update successful")
	c.JSON(http.StatusOK, gin.H{"message": "Scene updated successfully"}) // Отвечаем JSON для AJAX запроса
}
