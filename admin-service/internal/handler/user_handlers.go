package handler

import (
	"errors"
	"fmt"
	"net/http"
	"novel-server/admin-service/internal/client"
	sharedModels "novel-server/shared/models"
	"strconv"
	"strings"

	"novel-server/shared/entities"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type userUpdateFormData struct {
	Email    string   `form:"email"`
	Roles    []string `form:"roles"`
	IsBanned string   `form:"is_banned"`
}

func getUserIDFromContext(c *gin.Context) (uuid.UUID, bool) {
	val, ok := c.Get(string(sharedModels.UserContextKey))
	if !ok {
		return uuid.Nil, false
	}
	userID, ok := val.(uuid.UUID)
	if !ok {
		return uuid.Nil, false
	}
	return userID, true
}

func (h *AdminHandler) listUsers(c *gin.Context) {
	adminUserID, _ := getUserIDFromContext(c)
	log := h.logger.With(zap.String("adminUserID", adminUserID.String()))
	log.Info("Admin requested user list")
	limitStr := c.Query("limit")
	afterCursor := c.Query("after")
	limit := 20
	var err error
	if limitStr != "" {
		limit, err = strconv.Atoi(limitStr)
		if err != nil || limit <= 0 {
			log.Warn("Invalid limit parameter, using default", zap.String("limit", limitStr))
			limit = 20
		}
	}
	log = log.With(zap.Int("limit", limit), zap.String("after", afterCursor))
	log.Debug("Fetching user list with pagination")
	users, nextCursor, err := h.authClient.ListUsers(c.Request.Context(), limit, afterCursor)
	if err != nil {
		log.Error("Failed to get user list from auth-service", zap.Error(err))
		users = []sharedModels.User{}
		data := gin.H{
			"PageTitle":  "Управление пользователями",
			"Users":      users,
			"Error":      "Не удалось загрузить список пользователей: " + err.Error(),
			"IsLoggedIn": true,
			"NextCursor": "",
			"Limit":      limit,
		}
		c.HTML(http.StatusOK, "users.html", data)
		return
	}
	data := gin.H{
		"PageTitle":  "Управление пользователями",
		"Users":      users,
		"IsLoggedIn": true,
		"NextCursor": nextCursor,
		"Limit":      limit,
	}
	c.HTML(http.StatusOK, "users.html", data)
}

func (h *AdminHandler) handleBanUser(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID format"})
		return
	}
	adminUserID, _ := getUserIDFromContext(c)
	h.logger.Info("Admin attempting to ban user", zap.String("adminUserID", adminUserID.String()), zap.String("targetUserID", userID.String()))
	err = h.authClient.BanUser(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to ban user via auth client", zap.String("targetUserID", userID.String()), zap.Error(err))
		userFacingError := "Не удалось забанить пользователя."
		status := http.StatusInternalServerError
		if errors.Is(err, sharedModels.ErrUserNotFound) {
			userFacingError = "Пользователь не найден."
			status = http.StatusNotFound
		}
		c.AbortWithStatusJSON(status, gin.H{"error": userFacingError})
		return
	}
	userBansTotal.Inc()
	c.Header("HX-Refresh", "true")
	c.Status(http.StatusOK)
}

func (h *AdminHandler) handleUnbanUser(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID format"})
		return
	}
	adminUserID, _ := getUserIDFromContext(c)
	h.logger.Info("Admin attempting to unban user", zap.String("adminUserID", adminUserID.String()), zap.String("targetUserID", userID.String()))
	err = h.authClient.UnbanUser(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to unban user via auth client", zap.String("targetUserID", userID.String()), zap.Error(err))
		userFacingError := "Не удалось разбанить пользователя."
		status := http.StatusInternalServerError
		if errors.Is(err, sharedModels.ErrUserNotFound) {
			userFacingError = "Пользователь не найден."
			status = http.StatusNotFound
		}
		c.AbortWithStatusJSON(status, gin.H{"error": userFacingError})
		return
	}
	userUnbansTotal.Inc()
	c.Header("HX-Refresh", "true")
	c.Status(http.StatusOK)
}

func (h *AdminHandler) showUserEditPage(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID format"})
		return
	}
	h.logger.Info("Showing edit page for user", zap.String("userID", userID.String()))
	users, _, err := h.authClient.ListUsers(c.Request.Context(), 1, fmt.Sprintf("id:%s", userID.String()))
	if err != nil {
		h.logger.Error("Failed to get user details for editing", zap.String("userID", userID.String()), zap.Error(err))
		c.Redirect(http.StatusSeeOther, "/admin/users?error=fetch_failed")
		return
	}
	var user *sharedModels.User
	if len(users) > 0 {
		user = &users[0]
	} else {
		h.logger.Warn("User not found for edit page", zap.String("userID", userID.String()))
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "Пользователь не найден"})
		return
	}

	notificationStatus := c.Query("notification_status")

	data := gin.H{
		"PageTitle":          "Редактирование пользователя",
		"User":               user,
		"AllRoles":           sharedModels.AllRoles(),
		"CurrentUserRoles":   user.Roles,
		"IsLoggedIn":         true,
		"NotificationStatus": notificationStatus,
	}
	c.HTML(http.StatusOK, "user_edit.html", data)
}

func (h *AdminHandler) handleUserUpdate(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID format"})
		return
	}
	var formData userUpdateFormData
	if err := c.ShouldBind(&formData); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid form data: " + err.Error()})
		return
	}
	adminUserID, _ := getUserIDFromContext(c)
	h.logger.Info("Admin attempting to update user",
		zap.String("adminUserID", adminUserID.String()),
		zap.String("targetUserID", userID.String()),
		zap.String("email", formData.Email),
		zap.Strings("roles", formData.Roles),
		zap.String("is_banned_form", formData.IsBanned),
	)
	var rolesSlice []string
	if formData.Roles != nil {
		allDefinedRoles := sharedModels.AllRolesMap()
		validRoles := make([]string, 0, len(formData.Roles))
		for _, role := range formData.Roles {
			if _, ok := allDefinedRoles[role]; ok {
				validRoles = append(validRoles, role)
			} else {
				h.logger.Warn("Received invalid role from form during update", zap.String("invalidRole", role), zap.String("userID", userID.String()))
			}
		}
		rolesSlice = validRoles
	} else {
		rolesSlice = []string{}
	}
	isBanned := formData.IsBanned == "true"
	updatePayload := client.UserUpdatePayload{
		Email:    &formData.Email,
		Roles:    rolesSlice,
		IsBanned: &isBanned,
	}
	if formData.Email == "" {
		updatePayload.Email = nil
	}
	err = h.authClient.UpdateUser(c.Request.Context(), userID, updatePayload)
	users, _, listErr := h.authClient.ListUsers(c.Request.Context(), 1, fmt.Sprintf("id:%s", userID.String()))
	if listErr != nil {
		h.logger.Error("Failed to reload user after update attempt", zap.String("userID", userID.String()), zap.Error(listErr))
	}
	var user *sharedModels.User
	if listErr == nil && len(users) > 0 {
		user = &users[0]
	} else {
		h.logger.Warn("User not found in list after update attempt or list failed, using form data as fallback for render", zap.String("userID", userID.String()))
		user = &sharedModels.User{ID: userID, Username: "(unknown)", Email: formData.Email, Roles: rolesSlice, IsBanned: isBanned}
	}
	data := gin.H{
		"PageTitle":   "Редактирование пользователя",
		"User":        user,
		"RolesString": strings.Join(user.Roles, " "),
		"IsLoggedIn":  true,
	}
	if err != nil {
		h.logger.Error("Failed to update user via auth client", zap.String("targetUserID", userID.String()), zap.Error(err))
		data["Error"] = "Не удалось сохранить изменения. " + err.Error()
	} else {
		userUpdatesTotal.Inc()
		data["Success"] = "Изменения успешно сохранены!"
	}
	c.HTML(http.StatusOK, "user_edit.html", data)
}

func (h *AdminHandler) handleResetPassword(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID format"})
		return
	}
	adminUserID, _ := getUserIDFromContext(c)
	h.logger.Info("Admin attempting to reset password for user",
		zap.String("adminUserID", adminUserID.String()),
		zap.String("targetUserID", userID.String()),
	)
	newPassword, err := h.authClient.ResetPassword(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to reset password via auth client", zap.String("targetUserID", userID.String()), zap.Error(err))
		errorMessage := "Не удалось сбросить пароль."
		status := http.StatusInternalServerError
		if errors.Is(err, sharedModels.ErrUserNotFound) {
			errorMessage = "Пользователь не найден."
			status = http.StatusNotFound
		}
		c.Header("HX-Retarget", "#password-reset-status")
		c.Header("HX-Reswap", "innerHTML")
		c.String(status, fmt.Sprintf("<article aria-invalid='true'>%s</article>", errorMessage))
		return
	}
	passwordResetsTotal.Inc()
	h.logger.Info("Password reset successful for user", zap.String("targetUserID", userID.String()))
	c.Header("HX-Retarget", "#password-reset-status")
	c.Header("HX-Reswap", "innerHTML")
	responseHTML := fmt.Sprintf(
		`<article style="background-color: var(--pico-color-green-150); border-color: var(--pico-color-green-400);">
			Пароль успешно сброшен. Новый временный пароль:
			<pre><code>%s</code></pre>
			<small>Пожалуйста, скопируйте этот пароль и передайте пользователю. После первого входа ему следует сменить пароль.</small>
		</article>`,
		newPassword,
	)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(responseHTML))
}

func (h *AdminHandler) handleSendUserNotification(c *gin.Context) {
	userIDStr := c.Param("user_id")

	type notificationPayload struct {
		Message string `json:"message" form:"message"`
	}

	payload := new(notificationPayload)
	if err := c.ShouldBind(payload); err != nil {
		h.logger.Error("Failed to bind notification payload", zap.Error(err))
		c.String(http.StatusBadRequest, "Invalid request payload: "+err.Error())
		return
	}

	if payload.Message == "" {
		h.logger.Warn("Notification message is empty")
		c.String(http.StatusBadRequest, "Notification message cannot be empty")
		return
	}

	h.logger.Info("Attempting to send notification", zap.String("userID", userIDStr), zap.String("message", payload.Message))

	event := entities.UserPushEvent{
		UserID:  userIDStr,
		Title:   "Сообщение от администрации",
		Message: payload.Message,
	}

	redirectURL := fmt.Sprintf("/admin/users/%s/edit", userIDStr)

	if err := h.pushPublisher.PublishUserPushEvent(c.Request.Context(), event); err != nil {
		h.logger.Error("Failed to publish user push event", zap.Error(err), zap.String("userID", userIDStr))
		c.Redirect(http.StatusSeeOther, redirectURL+"?notification_status=error")
		return
	}

	h.logger.Info("Notification sent successfully", zap.String("userID", userIDStr))

	c.Redirect(http.StatusSeeOther, redirectURL+"?notification_status=success")
}
