package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
	draft, draftErr := h.gameplayClient.GetDraftDetailsInternal(ctx, draftID)
	if draftErr != nil {
		log.Error("Failed to get draft details from gameplay service", zap.Error(draftErr))
		// Не прерываем, просто покажем ошибку на странице
	}

	// Определяем доступные статусы для черновика
	availableDraftStatuses := []sharedModels.StoryStatus{
		sharedModels.StatusDraft,
		sharedModels.StatusGenerating,
		sharedModels.StatusRevising,
		sharedModels.StatusError,
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
		"PageTitle":         pageTitle,
		"TargetUser":        targetUser,
		"Draft":             draft,
		"AvailableStatuses": availableDraftStatuses,
		"IsLoggedIn":        true,
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
		userErr = fmt.Errorf("failed to list users: %w", listUsersErr)
		log.Error("Failed to get target user details via ListUsers", zap.Error(userErr))
	} else if len(users) == 0 {
		userErr = sharedModels.ErrUserNotFound
	} else {
		targetUser = &users[0]
	}

	// Получаем детали истории
	story, storyErr := h.gameplayClient.GetPublishedStoryDetailsInternal(ctx, storyID)
	if storyErr != nil {
		log.Error("Failed to get published story details from gameplay service", zap.Error(storyErr))
		// Не прерываем
	}

	// Получаем сцены истории
	var scenes []sharedModels.StoryScene
	var scenesErr error
	if storyErr == nil {
		scenes, scenesErr = h.gameplayClient.ListStoryScenesInternal(ctx, storyID)
		if scenesErr != nil {
			log.Warn("Failed to get story scenes", zap.Error(scenesErr))
			// Не прерываем, просто покажем предупреждение
		}
	}

	// Определяем доступные статусы для опубликованной истории
	availableStoryStatuses := []sharedModels.StoryStatus{
		sharedModels.StatusSetupPending,
		sharedModels.StatusFirstScenePending,
		sharedModels.StatusGeneratingScene,
		sharedModels.StatusReady,
		sharedModels.StatusGameOverPending,
		sharedModels.StatusCompleted,
		sharedModels.StatusError,
		// sharedModels.StatusSetupGenerating, // Не используется?
	}

	// Готовим данные для шаблона
	pageTitle := "Детали опубликованной истории"
	if story != nil {
		if story.Title != nil && *story.Title != "" {
			pageTitle = fmt.Sprintf("История: %s", *story.Title)
		} else {
			pageTitle = fmt.Sprintf("История ID: %s", story.ID.String())
		}
	} else {
		pageTitle = fmt.Sprintf("История ID: %s", storyID.String())
	}
	if targetUser != nil {
		pageTitle += fmt.Sprintf(" (Пользователь: %s)", targetUser.Username)
	}

	data := gin.H{
		"PageTitle":         pageTitle,
		"TargetUser":        targetUser,
		"Story":             story,
		"Scenes":            scenes,
		"AvailableStatuses": availableStoryStatuses,
		"IsLoggedIn":        true,
		"URLQuery":          c.Request.URL.Query(),
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
		data["ScenesError"] = "Не удалось загрузить сцены: " + scenesErr.Error()
	}

	c.HTML(http.StatusOK, "story_details.html", data)
}

// handleUpdateDraft обрабатывает POST-запрос для обновления черновика.
func (h *AdminHandler) handleUpdateDraft(c *gin.Context) {
	ctx := c.Request.Context()
	log := h.logger.With(zap.String("handler", "handleUpdateDraft"))

	// Получаем ID пользователя и черновика из URL
	targetUserIDStr := c.Param("user_id")
	_, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		log.Error("Invalid target user ID format", zap.String("user_id", targetUserIDStr), zap.Error(err))
		c.Redirect(http.StatusSeeOther, c.Request.Referer())
		return
	}
	draftIDStr := c.Param("draft_id")
	draftID, err := uuid.Parse(draftIDStr)
	if err != nil {
		log.Error("Invalid draft ID format", zap.String("draft_id", draftIDStr), zap.Error(err))
		c.Redirect(http.StatusSeeOther, c.Request.Referer())
		return
	}
	log = log.With(zap.String("draftID", draftID.String()))

	// Читаем данные из формы
	configJsonStr := c.PostForm("configJson")
	userInputJsonStr := c.PostForm("userInputJson")
	statusStr := c.PostForm("status")

	// Валидация статуса
	status := sharedModels.StoryStatus(statusStr)
	isValidStatus := false
	for _, validStatus := range []sharedModels.StoryStatus{
		sharedModels.StatusDraft,
		sharedModels.StatusGenerating,
		sharedModels.StatusRevising,
		sharedModels.StatusError,
	} {
		if status == validStatus {
			isValidStatus = true
			break
		}
	}
	if !isValidStatus {
		log.Error("Invalid status value submitted", zap.String("status", statusStr))
		c.Redirect(http.StatusSeeOther, c.Request.Referer())
		return
	}
	log = log.With(zap.String("newStatus", string(status)))

	// Вызываем Gameplay Service для обновления
	err = h.gameplayClient.UpdateDraftInternal(ctx, draftID, configJsonStr, userInputJsonStr, status)
	if err != nil {
		log.Error("Failed to update draft via gameplay service", zap.Error(err))
	} else {
		log.Info("Draft updated successfully")
	}

	c.Redirect(http.StatusSeeOther, c.Request.Referer())
}

// handleUpdateStory обрабатывает POST-запрос для обновления опубликованной истории.
func (h *AdminHandler) handleUpdateStory(c *gin.Context) {
	ctx := c.Request.Context()
	log := h.logger.With(zap.String("handler", "handleUpdateStory"))

	// Получаем ID пользователя и истории из URL
	targetUserIDStr := c.Param("user_id")
	_, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		log.Error("Invalid target user ID format", zap.String("user_id", targetUserIDStr), zap.Error(err))
		c.Redirect(http.StatusSeeOther, c.Request.Referer())
		return
	}
	storyIDStr := c.Param("story_id")
	storyID, err := uuid.Parse(storyIDStr)
	if err != nil {
		log.Error("Invalid story ID format", zap.String("story_id", storyIDStr), zap.Error(err))
		c.Redirect(http.StatusSeeOther, c.Request.Referer())
		return
	}
	log = log.With(zap.String("storyID", storyID.String()))

	// Читаем данные из формы
	configJsonStr := c.PostForm("configJson")
	setupJsonStr := c.PostForm("setupJson")
	statusStr := c.PostForm("status")

	// Валидация статуса
	status := sharedModels.StoryStatus(statusStr)
	isValidStatus := false
	for _, validStatus := range []sharedModels.StoryStatus{
		sharedModels.StatusSetupPending,
		sharedModels.StatusFirstScenePending,
		sharedModels.StatusGeneratingScene,
		sharedModels.StatusReady,
		sharedModels.StatusGameOverPending,
		sharedModels.StatusCompleted,
		sharedModels.StatusError,
	} {
		if status == validStatus {
			isValidStatus = true
			break
		}
	}
	if !isValidStatus {
		log.Error("Invalid status value submitted", zap.String("status", statusStr))
		c.Redirect(http.StatusSeeOther, c.Request.Referer())
		return
	}
	log = log.With(zap.String("newStatus", string(status)))

	// Вызываем Gameplay Service для обновления
	err = h.gameplayClient.UpdateStoryInternal(ctx, storyID, configJsonStr, setupJsonStr, status)

	// Добавляем параметры к редиректу
	redirectURL := c.Request.Referer() // Базовый URL для редиректа
	if redirectURL == "" {
		// Fallback, если Referer не доступен
		redirectURL = fmt.Sprintf("/admin/users/%s/stories/%s", c.Param("user_id"), storyID.String())
	}

	if err != nil {
		log.Error("Failed to update story via gameplay service", zap.Error(err))
		// Добавляем параметр ошибки к URL
		redirectURL += "?error=update_failed&msg=" + url.QueryEscape(err.Error())
	} else {
		log.Info("Story updated successfully")
		// Добавляем параметр успеха к URL
		redirectURL += "?success=updated"
	}

	c.Redirect(http.StatusSeeOther, redirectURL)
}

// handleUpdateScene обрабатывает POST-запрос для обновления контента сцены.
func (h *AdminHandler) handleUpdateScene(c *gin.Context) {
	ctx := c.Request.Context()
	log := h.logger.With(zap.String("handler", "handleUpdateScene"))

	// Получаем ID пользователя, истории и сцены из URL
	targetUserIDStr := c.Param("user_id")
	_, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		log.Error("Invalid target user ID format", zap.String("user_id", targetUserIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный формат ID пользователя"})
		return
	}
	storyIDStr := c.Param("story_id")
	_, err = uuid.Parse(storyIDStr)
	if err != nil {
		log.Error("Invalid story ID format", zap.String("story_id", storyIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный формат ID истории"})
		return
	}
	sceneIDStr := c.Param("scene_id")
	sceneID, err := uuid.Parse(sceneIDStr)
	if err != nil {
		log.Error("Invalid scene ID format", zap.String("scene_id", sceneIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный формат ID сцены"})
		return
	}
	log = log.With(zap.String("sceneID", sceneID.String()))

	// Структура для парсинга JSON из тела запроса
	type updateSceneRequest struct {
		ContentJson string `json:"contentJson" binding:"required"`
	}
	var req updateSceneRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		log.Warn("Invalid request body for scene update", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Неверный формат запроса: %v", err)})
		return
	}

	contentJson := req.ContentJson
	// Дополнительная валидация JSON (хотя ShouldBindJSON уже проверяет структуру)
	if contentJson == "" || !json.Valid([]byte(contentJson)) {
		log.Warn("Invalid scene content JSON submitted")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Невалидный или пустой JSON контента сцены"})
		return
	}

	log.Info("Attempting to update scene via gameplay service")

	// Вызов метода gameplayClient
	err = h.gameplayClient.UpdateSceneInternal(ctx, sceneID, contentJson)

	// Обработка ошибки клиента и возврат JSON
	if err != nil {
		log.Error("Failed to update scene via gameplay service", zap.Error(err))
		// Определяем статус код по типу ошибки (если возможно)
		statusCode := http.StatusInternalServerError
		if errors.Is(err, sharedModels.ErrNotFound) {
			statusCode = http.StatusNotFound
		} else if errors.Is(err, sharedModels.ErrBadRequest) {
			statusCode = http.StatusBadRequest
		}
		c.JSON(statusCode, gin.H{"error": fmt.Sprintf("Не удалось обновить сцену: %v", err)})
		return
	}

	log.Info("Scene updated successfully")
	c.JSON(http.StatusOK, gin.H{"message": "Сцена успешно обновлена!"})
}
