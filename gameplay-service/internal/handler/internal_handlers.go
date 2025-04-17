package handler

import (
	"errors"
	"net/http"
	sharedInterfaces "novel-server/shared/interfaces"
	"strconv"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

// listUserDraftsInternal возвращает список черновиков для указанного пользователя (для админки).
func (h *GameplayHandler) listUserDraftsInternal(c echo.Context) error {
	log := h.logger.With(zap.String("handler", "listUserDraftsInternal"))

	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Warn("Invalid user ID format", zap.String("user_id", userIDStr), zap.Error(err))
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid user ID format"})
	}

	limitStr := c.QueryParam("limit")
	cursor := c.QueryParam("cursor")
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

	drafts, nextCursor, err := h.service.ListUserDrafts(c.Request().Context(), userID, cursor, limit)
	if err != nil {
		if errors.Is(err, sharedInterfaces.ErrInvalidCursor) {
			log.Warn("Invalid cursor provided", zap.Error(err))
			return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid cursor format"})
		}
		log.Error("Failed to list user drafts internally", zap.Error(err))
		// Не используем handleServiceError, так как это внутренний API, возвращаем 500
		return c.JSON(http.StatusInternalServerError, APIError{Message: "Failed to retrieve drafts"})
	}

	// Используем ту же DTO PaginatedResponse, что и для публичного API
	response := PaginatedResponse{
		Data:       drafts, // Сервис уже возвращает []*StoryConfigSummary
		NextCursor: nextCursor,
	}
	return c.JSON(http.StatusOK, response)
}

// listUserStoriesInternal возвращает список опубликованных историй пользователя (для админки).
func (h *GameplayHandler) listUserStoriesInternal(c echo.Context) error {
	log := h.logger.With(zap.String("handler", "listUserStoriesInternal"))

	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Warn("Invalid user ID format", zap.String("user_id", userIDStr), zap.Error(err))
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid user ID format"})
	}

	limitStr := c.QueryParam("limit")
	offsetStr := c.QueryParam("offset")

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

	stories, err := h.service.ListUserPublishedStories(c.Request().Context(), userID, limit, offset)
	if err != nil {
		log.Error("Failed to list user published stories internally", zap.Error(err))
		return c.JSON(http.StatusInternalServerError, APIError{Message: "Failed to retrieve published stories"})
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

	return c.JSON(http.StatusOK, response)
}
