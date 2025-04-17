package handler

import (
	"errors"
	"fmt"
	"net/http"
	sharedModels "novel-server/shared/models"
	"strconv"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

const defaultLimit = 20

func (h *AdminHandler) listUserDrafts(c echo.Context) error {
	ctx := c.Request().Context()
	log := h.logger.With(zap.String("handler", "listUserDrafts"))
	targetUserIDStr := c.Param("user_id")
	targetUserID, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		log.Error("Invalid target user ID format", zap.String("user_id", targetUserIDStr), zap.Error(err))
		return echo.NewHTTPError(http.StatusBadRequest, "Неверный формат ID пользователя")
	}
	log = log.With(zap.String("targetUserID", targetUserID.String()))
	limitStr := c.QueryParam("limit")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > defaultLimit {
		limit = defaultLimit
	}
	cursor := c.QueryParam("cursor")
	var targetUser *sharedModels.User
	var userErr error
	users, _, listUsersErr := h.authClient.ListUsers(ctx, 1, fmt.Sprintf("id:%s", targetUserID.String()))
	if listUsersErr != nil {
		userErr = fmt.Errorf("failed to list users to find target: %w", listUsersErr)
		log.Error("Failed to get target user details via ListUsers", zap.Error(userErr))
	} else if len(users) == 0 {
		userErr = sharedModels.ErrUserNotFound
	} else {
		targetUser = &users[0]
	}
	var drafts []sharedModels.StoryConfig
	var nextCursor string
	var listErr error
	if userErr == nil {
		drafts, nextCursor, listErr = h.gameplayClient.ListUserDrafts(ctx, targetUserID, limit, cursor)
		if listErr != nil {
			log.Error("Failed to get user drafts from gameplay service", zap.Error(listErr))
		}
	}
	pageTitle := "Черновики пользователя"
	if targetUser != nil {
		pageTitle = fmt.Sprintf("Черновики пользователя %s (%s)", targetUser.Username, targetUserID.String())
	} else if userErr != nil {
		pageTitle = fmt.Sprintf("Черновики пользователя (ID: %s)", targetUserID.String())
	}
	data := map[string]interface{}{
		"PageTitle":  pageTitle,
		"TargetUser": targetUser,
		"Drafts":     drafts,
		"Limit":      limit,
		"NextCursor": nextCursor,
		"IsLoggedIn": true,
	}
	if userErr != nil {
		if errors.Is(userErr, sharedModels.ErrUserNotFound) {
			data["Error"] = "Пользователь не найден."
		} else {
			data["Error"] = "Не удалось загрузить данные пользователя: " + userErr.Error()
		}
		data["Drafts"] = []sharedModels.StoryConfig{}
	} else if listErr != nil {
		data["Error"] = "Не удалось загрузить черновики: " + listErr.Error()
	}
	return c.Render(http.StatusOK, "user_drafts.html", data)
}

func (h *AdminHandler) listUserStories(c echo.Context) error {
	ctx := c.Request().Context()
	log := h.logger.With(zap.String("handler", "listUserStories"))
	targetUserIDStr := c.Param("user_id")
	targetUserID, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		log.Error("Invalid target user ID format", zap.String("user_id", targetUserIDStr), zap.Error(err))
		return echo.NewHTTPError(http.StatusBadRequest, "Неверный формат ID пользователя")
	}
	log = log.With(zap.String("targetUserID", targetUserID.String()))
	limitStr := c.QueryParam("limit")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > defaultLimit {
		limit = defaultLimit
	}
	offsetStr := c.QueryParam("offset")
	offset, _ := strconv.Atoi(offsetStr)
	if offset < 0 {
		offset = 0
	}
	var targetUser *sharedModels.User
	var userErr error
	users, _, listUsersErr := h.authClient.ListUsers(ctx, 1, fmt.Sprintf("id:%s", targetUserID.String()))
	if listUsersErr != nil {
		userErr = fmt.Errorf("failed to list users: %w", listUsersErr)
		log.Error("Failed to get target user details via ListUsers", zap.Error(userErr))
	} else if len(users) == 0 {
		userErr = sharedModels.ErrUserNotFound
	} else {
		targetUser = &users[0]
	}
	var stories []*sharedModels.PublishedStory
	var hasMore bool
	var listErr error
	if userErr == nil {
		stories, hasMore, listErr = h.gameplayClient.ListUserPublishedStories(ctx, targetUserID, limit, offset)
		if listErr != nil {
			log.Error("Failed to get user published stories from gameplay service", zap.Error(listErr))
		}
	}
	data := map[string]interface{}{
		"TargetUser": targetUser,
		"Stories":    stories,
		"Limit":      limit,
		"Offset":     offset,
		"HasMore":    hasMore,
		"PageTitle":  "Опубликованные истории пользователя",
		"IsLoggedIn": true,
		"Error":      "",
	}
	if targetUser != nil {
		data["PageTitle"] = fmt.Sprintf("Истории пользователя %s (%s)", targetUser.Username, targetUserID.String())
	} else {
		data["PageTitle"] = fmt.Sprintf("Истории пользователя (ID: %s)", targetUserID.String())
	}
	if userErr != nil {
		data["Error"] = "Не удалось загрузить данные пользователя: " + userErr.Error()
	} else if listErr != nil {
		data["Error"] = "Не удалось загрузить истории: " + listErr.Error()
	}
	return c.Render(http.StatusOK, "user_stories.html", data)
}
