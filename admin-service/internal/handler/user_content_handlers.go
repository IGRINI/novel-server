package handler

import (
	"errors"
	"fmt"
	"net/http"
	sharedModels "novel-server/shared/models"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const defaultLimit = 20

func (h *AdminHandler) listUserDrafts(c *gin.Context) {
	ctx := c.Request.Context()
	log := h.logger.With(zap.String("handler", "listUserDrafts"))
	targetUserIDStr := c.Param("user_id")
	targetUserID, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		log.Error("Invalid target user ID format", zap.String("user_id", targetUserIDStr), zap.Error(err))
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Неверный формат ID пользователя"})
		return
	}
	log = log.With(zap.String("targetUserID", targetUserID.String()))
	limitStr := c.Query("limit")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > defaultLimit {
		limit = defaultLimit
	}
	cursor := c.Query("cursor")
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
	data := gin.H{
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
	c.HTML(http.StatusOK, "user_drafts.html", data)
}

func (h *AdminHandler) showDraftDetailsPage(c *gin.Context) {
	ctx := c.Request.Context()
	log := h.logger.With(zap.String("handler", "showDraftDetailsPage"))

	// Получаем ID пользователя и черновика из URL
	targetUserIDStr := c.Param("user_id")
	targetUserID, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		log.Error("Invalid target user ID format", zap.String("user_id", targetUserIDStr), zap.Error(err))
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Неверный формат ID пользователя"})
		return
	}
	draftIDStr := c.Param("draft_id")
	draftID, err := uuid.Parse(draftIDStr)
	if err != nil {
		log.Error("Invalid draft ID format", zap.String("draft_id", draftIDStr), zap.Error(err))
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Неверный формат ID черновика"})
		return
	}
	log = log.With(zap.String("targetUserID", targetUserID.String()), zap.String("draftID", draftID.String()))

	// Получаем информацию о пользователе (опционально, для заголовка)
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

	// Получаем детали черновика
	draft, draftErr := h.gameplayClient.GetDraftDetails(ctx, targetUserID, draftID)
	if draftErr != nil {
		log.Error("Failed to get draft details from gameplay service", zap.Error(draftErr))
		// Не прерываем, просто покажем ошибку на странице
	}

	// Готовим данные для шаблона
	pageTitle := "Детали черновика"
	if draft != nil {
		if draft.Title != "" {
			pageTitle = fmt.Sprintf("Черновик: %s", draft.Title)
		} else {
			pageTitle = fmt.Sprintf("Черновик: %s", draft.ID.String())
		}
	} else {
		pageTitle = fmt.Sprintf("Черновик ID: %s", draftID.String())
	}
	if targetUser != nil {
		pageTitle += fmt.Sprintf(" (Пользователь: %s)", targetUser.Username)
	}

	data := gin.H{
		"PageTitle":  pageTitle,
		"TargetUser": targetUser,
		"Draft":      draft,
		"IsLoggedIn": true, // Предполагаем, что middleware гарантирует это
	}
	if userErr != nil {
		if errors.Is(userErr, sharedModels.ErrUserNotFound) {
			data["UserError"] = "Пользователь не найден."
		} else {
			data["UserError"] = "Не удалось загрузить данные пользователя: " + userErr.Error()
		}
	}
	if draftErr != nil {
		if errors.Is(draftErr, sharedModels.ErrNotFound) {
			data["DraftError"] = "Черновик не найден."
		} else {
			data["DraftError"] = "Не удалось загрузить детали черновика: " + draftErr.Error()
		}
	}

	c.HTML(http.StatusOK, "draft_details.html", data)
}

func (h *AdminHandler) listUserStories(c *gin.Context) {
	ctx := c.Request.Context()
	log := h.logger.With(zap.String("handler", "listUserStories"))
	targetUserIDStr := c.Param("user_id")
	targetUserID, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		log.Error("Invalid target user ID format", zap.String("user_id", targetUserIDStr), zap.Error(err))
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Неверный формат ID пользователя"})
		return
	}
	log = log.With(zap.String("targetUserID", targetUserID.String()))
	limitStr := c.Query("limit")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > defaultLimit {
		limit = defaultLimit
	}
	offsetStr := c.Query("offset")
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
	data := gin.H{
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
	c.HTML(http.StatusOK, "user_stories.html", data)
}

func (h *AdminHandler) showPublishedStoryDetailsPage(c *gin.Context) {
	ctx := c.Request.Context()
	log := h.logger.With(zap.String("handler", "showPublishedStoryDetailsPage"))

	// Получаем ID пользователя и истории из URL
	targetUserIDStr := c.Param("user_id")
	targetUserID, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		log.Error("Invalid target user ID format", zap.String("user_id", targetUserIDStr), zap.Error(err))
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Неверный формат ID пользователя"})
		return
	}
	storyIDStr := c.Param("story_id")
	storyID, err := uuid.Parse(storyIDStr)
	if err != nil {
		log.Error("Invalid story ID format", zap.String("story_id", storyIDStr), zap.Error(err))
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Неверный формат ID истории"})
		return
	}
	log = log.With(zap.String("targetUserID", targetUserID.String()), zap.String("storyID", storyID.String()))

	// Получаем информацию о пользователе (опционально, для заголовка)
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

	// Получаем детали истории
	story, storyErr := h.gameplayClient.GetPublishedStoryDetails(ctx, targetUserID, storyID)
	if storyErr != nil {
		log.Error("Failed to get published story details from gameplay service", zap.Error(storyErr))
		// Не прерываем, просто покажем ошибку на странице
	}

	// Получаем список сцен истории
	var scenes []sharedModels.StoryScene
	var scenesErr error
	if storyErr == nil && story != nil { // Запрашиваем сцены, только если история найдена
		scenes, scenesErr = h.gameplayClient.ListStoryScenes(ctx, targetUserID, storyID)
		if scenesErr != nil {
			log.Error("Failed to list story scenes from gameplay service", zap.Error(scenesErr))
			// Не прерываем, просто покажем ошибку на странице
		}
	} else {
		// Если история не найдена, сцены тоже не загружаем
		scenes = make([]sharedModels.StoryScene, 0)
	}

	// Готовим данные для шаблона
	pageTitle := "Детали истории"
	if story != nil {
		if story.Title != nil && *story.Title != "" {
			pageTitle = fmt.Sprintf("История: %s", *story.Title)
		} else {
			pageTitle = fmt.Sprintf("История: %s", story.ID.String())
		}
	} else {
		pageTitle = fmt.Sprintf("История ID: %s", storyID.String())
	}
	if targetUser != nil {
		pageTitle += fmt.Sprintf(" (Пользователь: %s)", targetUser.Username)
	}

	data := gin.H{
		"PageTitle":  pageTitle,
		"TargetUser": targetUser,
		"Story":      story,
		"Scenes":     scenes,
		"IsLoggedIn": true,
	}
	if userErr != nil {
		if errors.Is(userErr, sharedModels.ErrUserNotFound) {
			data["UserError"] = "Пользователь не найден."
		} else {
			data["UserError"] = "Не удалось загрузить данные пользователя: " + userErr.Error()
		}
	}
	if storyErr != nil {
		if errors.Is(storyErr, sharedModels.ErrNotFound) {
			data["StoryError"] = "История не найдена."
		} else {
			data["StoryError"] = "Не удалось загрузить детали истории: " + storyErr.Error()
		}
	}
	if scenesErr != nil {
		data["ScenesError"] = "Не удалось загрузить список сцен: " + scenesErr.Error()
	}

	c.HTML(http.StatusOK, "story_details.html", data)
}
