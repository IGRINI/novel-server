package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	sharedModels "novel-server/shared/models"
	"strconv"

	"net/url"

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

// handleUpdateDraft обрабатывает POST-запрос для обновления черновика.
func (h *AdminHandler) handleUpdateDraft(c *gin.Context) {
	ctx := c.Request.Context()
	log := h.logger.With(zap.String("handler", "handleUpdateDraft"))

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

	// Получаем данные из формы
	configJson := c.PostForm("configJson")
	userInputJson := c.PostForm("userInputJson")

	// Валидация JSON на стороне клиента
	if configJson != "" && !json.Valid([]byte(configJson)) {
		log.Warn("Invalid config JSON submitted")
		// Перенаправляем обратно с сообщением об ошибке
		c.Redirect(http.StatusFound, fmt.Sprintf("/admin/users/%s/drafts/%s?error=Invalid+config+JSON+format", targetUserIDStr, draftIDStr))
		return
	}
	if userInputJson != "" && !json.Valid([]byte(userInputJson)) {
		log.Warn("Invalid user input JSON submitted")
		c.Redirect(http.StatusFound, fmt.Sprintf("/admin/users/%s/drafts/%s?error=Invalid+user+input+JSON+format", targetUserIDStr, draftIDStr))
		return
	}

	log.Info("Attempting to update draft via gameplay service")

	// Вызов метода gameplayClient
	err = h.gameplayClient.UpdateDraft(ctx, targetUserID, draftID, configJson, userInputJson)

	// Обработка ошибки клиента
	redirectURL := fmt.Sprintf("/admin/users/%s/drafts/%s", targetUserIDStr, draftIDStr)
	if err != nil {
		log.Error("Failed to update draft via gameplay service", zap.Error(err))
		redirectURL += fmt.Sprintf("?error=Failed+to+update+draft:+%s", url.QueryEscape(err.Error()))
	} else {
		log.Info("Draft updated successfully")
		redirectURL += "?success=Draft+updated+successfully"
	}

	c.Redirect(http.StatusFound, redirectURL)
}

// handleUpdateStory обрабатывает POST-запрос для обновления опубликованной истории.
func (h *AdminHandler) handleUpdateStory(c *gin.Context) {
	ctx := c.Request.Context()
	log := h.logger.With(zap.String("handler", "handleUpdateStory"))

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

	// Получаем данные из формы
	configJson := c.PostForm("configJson")
	setupJson := c.PostForm("setupJson")

	// Валидация JSON
	if configJson != "" && !json.Valid([]byte(configJson)) {
		log.Warn("Invalid config JSON submitted")
		c.Redirect(http.StatusFound, fmt.Sprintf("/admin/users/%s/stories/%s?error=Invalid+config+JSON+format", targetUserIDStr, storyIDStr))
		return
	}
	if setupJson != "" && !json.Valid([]byte(setupJson)) {
		log.Warn("Invalid setup JSON submitted")
		c.Redirect(http.StatusFound, fmt.Sprintf("/admin/users/%s/stories/%s?error=Invalid+setup+JSON+format", targetUserIDStr, storyIDStr))
		return
	}

	log.Info("Attempting to update story via gameplay service")

	// Вызов метода gameplayClient
	err = h.gameplayClient.UpdateStory(ctx, targetUserID, storyID, configJson, setupJson)

	// Обработка ошибки клиента
	redirectURL := fmt.Sprintf("/admin/users/%s/stories/%s", targetUserIDStr, storyIDStr)
	if err != nil {
		log.Error("Failed to update story via gameplay service", zap.Error(err))
		redirectURL += fmt.Sprintf("?error=Failed+to+update+story:+%s", url.QueryEscape(err.Error()))
	} else {
		log.Info("Story updated successfully")
		redirectURL += "?success=Story+updated+successfully"
	}

	c.Redirect(http.StatusFound, redirectURL)
}

// handleUpdateScene обрабатывает POST-запрос для обновления контента сцены.
func (h *AdminHandler) handleUpdateScene(c *gin.Context) {
	ctx := c.Request.Context()
	log := h.logger.With(zap.String("handler", "handleUpdateScene"))

	// Получаем ID пользователя, истории и сцены из URL
	targetUserIDStr := c.Param("user_id")
	targetUserID, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		log.Error("Invalid target user ID format", zap.String("user_id", targetUserIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный формат ID пользователя"})
		return
	}
	storyIDStr := c.Param("story_id")
	storyID, err := uuid.Parse(storyIDStr)
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
	log = log.With(zap.String("targetUserID", targetUserID.String()), zap.String("storyID", storyID.String()), zap.String("sceneID", sceneID.String()))

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
	err = h.gameplayClient.UpdateScene(ctx, targetUserID, storyID, sceneID, contentJson)

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
