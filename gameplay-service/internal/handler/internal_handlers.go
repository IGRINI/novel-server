package handler

import (
	"errors"
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
