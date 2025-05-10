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

	// Получаем токен администратора из cookie
	adminToken, cookieErr := c.Cookie("admin_session")
	if cookieErr != nil {
		log.Error("Failed to get admin_session cookie", zap.Error(cookieErr))
		userErr = fmt.Errorf("failed to get admin session: %w", cookieErr)
	} else {
		userDetail, getErr := h.authClient.GetUserInfo(ctx, targetUserID, adminToken)
		if getErr != nil {
			if errors.Is(getErr, sharedModels.ErrUserNotFound) {
				userErr = sharedModels.ErrUserNotFound
			} else {
				userErr = fmt.Errorf("failed to get user details: %w", getErr)
				log.Error("Failed to get target user details via GetUserInfo", zap.Error(userErr))
			}
		} else {
			targetUser = userDetail
		}
	}
	var drafts []sharedModels.StoryConfig
	var nextCursor string
	var listErr error
	if userErr == nil {
		drafts, nextCursor, listErr = h.gameplayClient.ListUserDrafts(ctx, targetUserID, limit, cursor, adminToken)
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

	// Получаем токен администратора из cookie
	adminToken, cookieErr := c.Cookie("admin_session")
	if cookieErr != nil {
		log.Error("Failed to get admin_session cookie", zap.Error(cookieErr))
		userErr = fmt.Errorf("failed to get admin session: %w", cookieErr)
	} else {
		userDetail, getErr := h.authClient.GetUserInfo(ctx, targetUserID, adminToken)
		if getErr != nil {
			if errors.Is(getErr, sharedModels.ErrUserNotFound) {
				userErr = sharedModels.ErrUserNotFound
			} else {
				userErr = fmt.Errorf("failed to get user details: %w", getErr)
				log.Error("Failed to get target user details via GetUserInfo", zap.Error(userErr))
			}
		} else {
			targetUser = userDetail
		}
	}

	// Получаем детали черновика
	var draft *sharedModels.StoryConfig
	var draftErr error
	if userErr == nil {
		draft, draftErr = h.gameplayClient.GetDraftDetailsInternal(ctx, draftID, adminToken)
		if draftErr != nil {
			log.Error("Failed to get draft details from gameplay service", zap.Error(draftErr))
			// Не прерываем, просто покажем ошибку на странице
		}
	}

	// Определяем доступные статусы для черновика
	availableDraftStatuses := []sharedModels.StoryStatus{
		sharedModels.StatusDraft,
		sharedModels.StatusGenerating,
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

	// Получаем токен администратора из cookie
	adminToken, cookieErr := c.Cookie("admin_session")
	if cookieErr != nil {
		log.Error("Failed to get admin_session cookie", zap.Error(cookieErr))
		userErr = fmt.Errorf("failed to get admin session: %w", cookieErr)
	} else {
		userDetail, getErr := h.authClient.GetUserInfo(ctx, targetUserID, adminToken)
		if getErr != nil {
			if errors.Is(getErr, sharedModels.ErrUserNotFound) {
				userErr = sharedModels.ErrUserNotFound
			} else {
				userErr = fmt.Errorf("failed to get user details: %w", getErr)
				log.Error("Failed to get target user details via GetUserInfo", zap.Error(userErr))
			}
		} else {
			targetUser = userDetail
		}
	}
	var stories []*sharedModels.PublishedStory
	var hasMore bool
	var listErr error
	if userErr == nil {
		stories, hasMore, listErr = h.gameplayClient.ListUserPublishedStories(ctx, targetUserID, limit, offset, adminToken)
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

	// Получаем токен администратора из cookie
	adminToken, cookieErr := c.Cookie("admin_session")
	if cookieErr != nil {
		log.Error("Failed to get admin_session cookie", zap.Error(cookieErr))
		userErr = fmt.Errorf("failed to get admin session: %w", cookieErr)
	} else {
		userDetail, getErr := h.authClient.GetUserInfo(ctx, targetUserID, adminToken)
		if getErr != nil {
			if errors.Is(getErr, sharedModels.ErrUserNotFound) {
				userErr = sharedModels.ErrUserNotFound
			} else {
				userErr = fmt.Errorf("failed to get user details: %w", getErr)
				log.Error("Failed to get target user details via GetUserInfo", zap.Error(userErr))
			}
		} else {
			targetUser = userDetail
		}
	}

	// Получаем детали истории
	story, storyErr := h.gameplayClient.GetPublishedStoryDetailsInternal(ctx, storyID, adminToken)
	if storyErr != nil {
		log.Error("Failed to get published story details from gameplay service", zap.Error(storyErr))
		// Не прерываем
	}

	// Получаем сцены истории
	var scenes []sharedModels.StoryScene
	var scenesErr error
	if storyErr == nil {
		scenes, scenesErr = h.gameplayClient.ListStoryScenesInternal(ctx, storyID, adminToken)
		if scenesErr != nil {
			log.Warn("Failed to get story scenes", zap.Error(scenesErr))
			// Не прерываем, просто покажем предупреждение
		}
	}

	// Получаем состояния игроков для этой истории
	var playerStates []sharedModels.PlayerGameState
	var playerStatesErr error
	if storyErr == nil { // Запрашиваем, только если история найдена
		playerStates, playerStatesErr = h.gameplayClient.ListStoryPlayersInternal(ctx, storyID, adminToken)
		if playerStatesErr != nil {
			log.Warn("Failed to get player states for story", zap.Error(playerStatesErr))
			// Не прерываем, покажем ошибку в шаблоне
		}
	}

	// Определяем доступные статусы для опубликованной истории
	availableStoryStatuses := []sharedModels.StoryStatus{
		sharedModels.StatusSetupPending,
		sharedModels.StatusFirstScenePending,
		sharedModels.StatusReady,
		sharedModels.StatusError,
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
		"PlayerStates":      playerStates,
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
	if playerStatesErr != nil {
		data["PlayerStatesError"] = "Не удалось загрузить состояния игроков: " + playerStatesErr.Error()
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

	// Получаем токен администратора
	adminToken, err := c.Cookie("admin_session")
	if err != nil {
		log.Error("Failed to get admin token from cookie", zap.Error(err))
		c.Redirect(http.StatusSeeOther, c.Request.Referer())
		return
	}

	// Вызываем Gameplay Service для обновления
	err = h.gameplayClient.UpdateDraftInternal(ctx, draftID, configJsonStr, userInputJsonStr, status, adminToken)
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
		sharedModels.StatusReady,
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

	// Получаем токен администратора
	adminToken, err := c.Cookie("admin_session")
	if err != nil {
		log.Error("Failed to get admin token from cookie", zap.Error(err))
		c.Redirect(http.StatusSeeOther, c.Request.Referer())
		return
	}

	// Вызываем Gameplay Service для обновления
	err = h.gameplayClient.UpdateStoryInternal(ctx, storyID, configJsonStr, setupJsonStr, status, adminToken)

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

	// Получаем токен администратора
	adminToken, err := c.Cookie("admin_session")
	if err != nil {
		log.Error("Failed to get admin token from cookie", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Не удалось получить токен администратора",
		})
		return
	}

	// Вызов метода gameplayClient
	err = h.gameplayClient.UpdateSceneInternal(ctx, sceneID, contentJson, adminToken)

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

// handleDeleteScene обрабатывает POST-запрос для удаления сцены.
func (h *AdminHandler) handleDeleteScene(c *gin.Context) {
	ctx := c.Request.Context()
	log := h.logger.With(zap.String("handler", "handleDeleteScene"))

	// Получаем ID пользователя, истории и сцены из URL
	targetUserIDStr := c.Param("user_id")
	storyIDStr := c.Param("story_id")
	sceneIDStr := c.Param("scene_id")
	sceneID, err := uuid.Parse(sceneIDStr)
	if err != nil {
		log.Error("Invalid scene ID format", zap.String("scene_id", sceneIDStr), zap.Error(err))
		// Редирект с ошибкой (можно улучшить, добавив flash сообщение)
		redirectURL := fmt.Sprintf("/admin/users/%s/stories/%s?error=invalid_scene_id", targetUserIDStr, storyIDStr)
		c.Redirect(http.StatusSeeOther, redirectURL)
		return
	}
	log = log.With(zap.String("sceneID", sceneID.String()))

	// Получаем токен администратора
	adminToken, err := c.Cookie("admin_session")
	if err != nil {
		log.Error("Failed to get admin token from cookie", zap.Error(err))
		c.Redirect(http.StatusSeeOther, fmt.Sprintf("/users/%s/stories/%s?error=admin_token", targetUserIDStr, storyIDStr))
		return
	}

	// Вызов метода gameplayClient для удаления сцены
	err = h.gameplayClient.DeleteSceneInternal(ctx, sceneID, adminToken)

	// Формируем URL для редиректа (обратно на страницу деталей истории)
	redirectURL := fmt.Sprintf("/admin/users/%s/stories/%s", targetUserIDStr, storyIDStr)

	if err != nil {
		log.Error("Failed to delete scene via gameplay service", zap.Error(err))
		errorMsg := "Не удалось удалить сцену"
		if errors.Is(err, sharedModels.ErrNotFound) {
			errorMsg = "Сцена не найдена"
		}
		redirectURL += "?error=delete_failed&msg=" + url.QueryEscape(errorMsg)
	} else {
		log.Info("Scene deleted successfully")
		redirectURL += "?success=deleted"
	}

	c.Redirect(http.StatusSeeOther, redirectURL)
}

// --- Player Progress Handlers ---

// showEditPlayerProgressPage отображает страницу редактирования прогресса игрока.
func (h *AdminHandler) showEditPlayerProgressPage(c *gin.Context) {
	ctx := c.Request.Context()
	log := h.logger.With(zap.String("handler", "showEditPlayerProgressPage"))

	// Получаем ID из URL
	targetUserIDStr := c.Param("user_id")
	storyIDStr := c.Param("story_id")
	progressIDStr := c.Param("progress_id")

	progressID, err := uuid.Parse(progressIDStr)
	if err != nil {
		log.Error("Invalid progress ID format", zap.String("progress_id", progressIDStr), zap.Error(err))
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Неверный формат ID прогресса"})
		return
	}
	log = log.With(zap.String("progressID", progressID.String()))

	// Получаем токен администратора
	adminToken, err := c.Cookie("admin_session")
	if err != nil {
		log.Error("Failed to get admin token from cookie", zap.Error(err))
		c.HTML(http.StatusInternalServerError, "error.html", gin.H{
			"Error":      "Ошибка проверки сессии",
			"Message":    err.Error(),
			"IsLoggedIn": true,
		})
		return
	}

	// Получаем детали прогресса
	progress, progressErr := h.gameplayClient.GetPlayerProgressInternal(ctx, progressID, adminToken)
	if progressErr != nil {
		log.Error("Failed to get player progress details", zap.Error(progressErr))
		// Можно показать ошибку на странице или редиректнуть
		// Пока просто передадим ошибку в шаблон
	}

	// Формируем JSON для отображения в редакторе
	var progressJSON string
	if progress != nil {
		// Предполагаем, что в PlayerProgress есть поле ProgressData для игрового прогресса
		jsonBytes, err := json.MarshalIndent(progress, "", "  ")
		if err != nil {
			log.Error("Failed to marshal progress to JSON", zap.Error(err))
			progressJSON = "Error marshaling JSON: " + err.Error()
		} else {
			progressJSON = string(jsonBytes)
		}
	} else {
		// Не удалось получить прогресс
		progressJSON = ""
	}

	// Формируем заголовок страницы
	pageTitle := "Редактирование прогресса игрока"

	data := gin.H{
		"PageTitle":     pageTitle,
		"ProgressID":    progressID.String(),
		"UserID":        targetUserIDStr,
		"StoryID":       storyIDStr,
		"Progress":      progress,
		"ProgressError": progressErr,
		"ProgressJSON":  progressJSON,
		"IsLoggedIn":    true,
		"AdminToken":    adminToken, // Добавляем токен в контекст для использования в JavaScript
	}

	c.HTML(http.StatusOK, "edit_player_progress.html", data)
}

// handleUpdatePlayerProgress обрабатывает POST-запрос для обновления прогресса игрока.
func (h *AdminHandler) handleUpdatePlayerProgress(c *gin.Context) {
	ctx := c.Request.Context()
	log := h.logger.With(zap.String("handler", "handleUpdatePlayerProgress"))

	// Получаем ID из URL
	targetUserIDStr := c.Param("user_id")
	storyIDStr := c.Param("story_id")
	progressIDStr := c.Param("progress_id")
	progressID, err := uuid.Parse(progressIDStr)
	if err != nil {
		log.Error("Invalid progress ID format on update", zap.String("progress_id", progressIDStr), zap.Error(err))
		// Редирект на страницу редактирования с общей ошибкой
		redirectURL := fmt.Sprintf("/admin/users/%s/stories/%s/progress/%s/edit?error=internal", targetUserIDStr, storyIDStr, progressIDStr)
		c.Redirect(http.StatusSeeOther, redirectURL)
		return
	}
	log = log.With(zap.String("progressID", progressID.String()))

	// Получаем токен администратора
	adminToken, err := c.Cookie("admin_session")
	if err != nil {
		log.Error("Failed to get admin token from cookie", zap.Error(err))
		redirectURLBase := fmt.Sprintf("/users/%s/stories/%s", targetUserIDStr, storyIDStr)
		c.Redirect(http.StatusSeeOther, redirectURLBase+"?error=admin_token")
		return
	}

	// Формируем URL для редиректа в случае успеха или ошибки
	redirectURLBase := fmt.Sprintf("/admin/users/%s/stories/%s/progress/%s/edit", targetUserIDStr, storyIDStr, progressIDStr)

	// Читаем данные из формы
	coreStatsJson := c.PostForm("coreStatsJson")
	storyVarsJson := c.PostForm("storyVarsJson")
	globalFlagsJson := c.PostForm("globalFlagsJson")
	currentStateHash := c.PostForm("currentStateHash")
	sceneIndexStr := c.PostForm("sceneIndex")
	lastStorySummary := c.PostForm("lastStorySummary")
	lastFutureDirection := c.PostForm("lastFutureDirection")
	lastVarImpactSummary := c.PostForm("lastVarImpactSummary")

	// Подготовка данных для обновления
	updateData := make(map[string]interface{})

	// Валидация и добавление JSON полей
	var coreStats map[string]int
	if coreStatsJson != "" {
		if err := json.Unmarshal([]byte(coreStatsJson), &coreStats); err != nil {
			log.Warn("Invalid coreStatsJson format", zap.Error(err))
			c.Redirect(http.StatusSeeOther, redirectURLBase+"?error=invalid_core_stats_json")
			return
		}
		updateData["core_stats"] = coreStats
	} else {
		updateData["core_stats"] = nil // Явно указываем null, если поле пустое
	}

	var storyVars map[string]interface{}
	if storyVarsJson != "" {
		if err := json.Unmarshal([]byte(storyVarsJson), &storyVars); err != nil {
			log.Warn("Invalid storyVarsJson format", zap.Error(err))
			c.Redirect(http.StatusSeeOther, redirectURLBase+"?error=invalid_story_vars_json")
			return
		}
		updateData["story_variables"] = storyVars
	} else {
		updateData["story_variables"] = nil // Явно указываем null
	}

	var globalFlags []string
	if globalFlagsJson != "" {
		if err := json.Unmarshal([]byte(globalFlagsJson), &globalFlags); err != nil {
			log.Warn("Invalid globalFlagsJson format", zap.Error(err))
			c.Redirect(http.StatusSeeOther, redirectURLBase+"?error=invalid_global_flags_json")
			return
		}
		updateData["global_flags"] = globalFlags
	} else {
		updateData["global_flags"] = nil // Явно указываем null
	}

	// Добавление остальных полей
	updateData["current_state_hash"] = currentStateHash

	sceneIndex, err := strconv.Atoi(sceneIndexStr)
	if err != nil {
		log.Warn("Invalid sceneIndex format", zap.String("sceneIndex", sceneIndexStr), zap.Error(err))
		c.Redirect(http.StatusSeeOther, redirectURLBase+"?error=invalid_scene_index")
		return
	}
	updateData["scene_index"] = sceneIndex

	// Для строковых полей просто передаем как есть
	// gameplay-service должен сам обработать пустые строки, если нужно
	updateData["last_story_summary"] = lastStorySummary
	updateData["last_future_direction"] = lastFutureDirection
	updateData["last_var_impact_summary"] = lastVarImpactSummary

	log.Debug("Attempting to update player progress", zap.Any("data", updateData))

	// Вызов метода клиента
	err = h.gameplayClient.UpdatePlayerProgressInternal(ctx, progressID, updateData, adminToken)
	if err != nil {
		log.Error("Failed to update player progress via gameplay service", zap.Error(err))
		errorMsg := "Не удалось обновить прогресс"
		if errors.Is(err, sharedModels.ErrPlayerProgressNotFound) {
			errorMsg = "Прогресс игрока не найден"
		} else if errors.Is(err, sharedModels.ErrBadRequest) {
			errorMsg = "Неверные данные для обновления" // Уточнить по ответу gameplay-service
		}
		c.Redirect(http.StatusSeeOther, redirectURLBase+"?error=update_failed&msg="+url.QueryEscape(errorMsg))
		return
	}

	log.Info("Player progress updated successfully")
	c.Redirect(http.StatusSeeOther, redirectURLBase+"?success=updated")
}

// <<< ДОБАВЛЕНО: Обработчик удаления черновика >>>
func (h *AdminHandler) handleDeleteDraft(c *gin.Context) {
	ctx := c.Request.Context()
	log := h.logger.With(zap.String("handler", "handleDeleteDraft"))

	targetUserIDStr := c.Param("user_id")
	userID, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		log.Warn("Invalid user ID format for delete draft", zap.Error(err))
		c.Status(http.StatusBadRequest)
		return
	}
	draftIDStr := c.Param("draft_id")
	draftID, err := uuid.Parse(draftIDStr)
	if err != nil {
		log.Warn("Invalid draft ID format for delete draft", zap.Error(err))
		c.Status(http.StatusBadRequest)
		return
	}
	log = log.With(zap.String("userID", userID.String()), zap.String("draftID", draftID.String()))

	log.Info("Attempting to delete draft via internal API")
	adminToken, _ := c.Cookie("admin_session")
	err = h.gameplayClient.DeleteDraftInternal(ctx, userID, draftID, adminToken)

	if err != nil {
		log.Error("Failed to delete draft via gameplay service", zap.Error(err))
		if errors.Is(err, sharedModels.ErrNotFound) {
			// Ошибка 404, элемент уже удален, для HTMX это тоже успех
			c.Status(http.StatusOK)
		} else if errors.Is(err, sharedModels.ErrForbidden) {
			c.Status(http.StatusForbidden)
		} else {
			c.Status(http.StatusInternalServerError)
		}
		return
	}

	log.Info("Draft deleted successfully")
	// Возвращаем 200 OK с пустым телом, чтобы HTMX удалил строку
	c.Status(http.StatusOK)
}

// <<< ДОБАВЛЕНО: Обработчик удаления опубликованной истории >>>
func (h *AdminHandler) handleDeleteStory(c *gin.Context) {
	ctx := c.Request.Context()
	log := h.logger.With(zap.String("handler", "handleDeleteStory"))

	targetUserIDStr := c.Param("user_id")
	userID, err := uuid.Parse(targetUserIDStr)
	if err != nil {
		log.Warn("Invalid user ID format for delete story", zap.Error(err))
		c.Status(http.StatusBadRequest)
		return
	}
	storyIDStr := c.Param("story_id")
	storyID, err := uuid.Parse(storyIDStr)
	if err != nil {
		log.Warn("Invalid story ID format for delete story", zap.Error(err))
		c.Status(http.StatusBadRequest)
		return
	}
	log = log.With(zap.String("userID", userID.String()), zap.String("storyID", storyID.String()))

	log.Info("Attempting to delete published story via internal API")
	adminToken, _ := c.Cookie("admin_session")
	err = h.gameplayClient.DeletePublishedStoryInternal(ctx, userID, storyID, adminToken)

	if err != nil {
		log.Error("Failed to delete published story via gameplay service", zap.Error(err))
		if errors.Is(err, sharedModels.ErrNotFound) {
			// Ошибка 404, элемент уже удален, для HTMX это тоже успех
			c.Status(http.StatusOK)
		} else if errors.Is(err, sharedModels.ErrForbidden) {
			c.Status(http.StatusForbidden)
		} else {
			c.Status(http.StatusInternalServerError)
		}
		return
	}

	log.Info("Published story deleted successfully")
	// Возвращаем 200 OK с пустым телом, чтобы HTMX удалил строку
	c.Status(http.StatusOK)
}
