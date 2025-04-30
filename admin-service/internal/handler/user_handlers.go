package handler

import (
	"errors"
	"fmt"
	"net/http"
	"novel-server/admin-service/internal/client"
	sharedModels "novel-server/shared/models"
	"strconv"

	// Возвращаем импорт entities

	"novel-server/shared/interfaces"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type userUpdateFormData struct {
	Email       string   `form:"email"`
	DisplayName string   `form:"display_name"`
	Roles       []string `form:"roles"`
	IsBanned    string   `form:"is_banned"`
}

const defaultUserListLimit = 20
const maxUserListLimit = 100

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
	limitStr := c.DefaultQuery("limit", strconv.Itoa(defaultUserListLimit))
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 || limit > maxUserListLimit {
		limit = defaultUserListLimit
	}
	log = log.With(zap.Int("limit", limit))
	log.Debug("Fetching user list with pagination")
	after := c.Query("after")
	users, nextCursor, err := h.authClient.ListUsers(c.Request.Context(), limit, after)
	userFacingError := ""
	if err != nil {
		log.Error("Failed to get user list from auth-service", zap.Error(err))
		userFacingError = "Не удалось загрузить список пользователей: " + err.Error()
		data := gin.H{
			"PageTitle":  "Управление пользователями",
			"Users":      users,
			"Error":      userFacingError,
			"IsLoggedIn": true,
			"NextCursor": nextCursor,
			"Limit":      limit,
		}
		log.Debug("HANDLER: listUsers - Rendering template users.html (with error)")
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
	log.Debug("HANDLER: listUsers - Rendering template users.html")
	c.HTML(http.StatusOK, "users.html", data)
}

func (h *AdminHandler) handleBanUser(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.logger.Warn("Invalid user ID format for ban", zap.String("rawID", userIDStr), zap.Error(err))
		c.String(http.StatusBadRequest, "Неверный ID пользователя.")
		c.Abort()
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
		h.logger.Warn("Invalid user ID format for unban", zap.String("rawID", userIDStr), zap.Error(err))
		c.String(http.StatusBadRequest, "Неверный ID пользователя.")
		c.Abort()
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
		h.logger.Warn("Invalid user ID format for edit page", zap.String("rawID", userIDStr), zap.Error(err))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	h.logger.Info("Showing edit page for user", zap.String("userID", userID.String()))
	users, _, err := h.authClient.ListUsers(c.Request.Context(), 1, fmt.Sprintf("id:%s", userID.String()))
	if err != nil {
		h.logger.Error("Failed to get user details for editing (via ListUsers)", zap.String("userID", userID.String()), zap.Error(err))
		c.Redirect(http.StatusSeeOther, "/admin/users?error=fetch_failed")
		return
	}
	if len(users) == 0 {
		h.logger.Warn("User not found for edit page", zap.String("userID", userID.String()))
		c.Redirect(http.StatusSeeOther, "/admin/users?error=not_found")
		return
	}
	user := users[0]

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
		h.logger.Warn("Invalid user ID format for update", zap.String("rawID", userIDStr), zap.Error(err))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	var formData userUpdateFormData
	if err := c.ShouldBind(&formData); err != nil {
		h.logger.Warn("Failed to bind user update form data", zap.String("userID", userIDStr), zap.Error(err))
		c.Redirect(http.StatusSeeOther, fmt.Sprintf("/admin/users/%s/edit?error=invalid_data", userIDStr))
		return
	}
	adminUserID, _ := getUserIDFromContext(c)
	h.logger.Info("Admin attempting to update user",
		zap.String("adminUserID", adminUserID.String()),
		zap.String("targetUserID", userID.String()),
		zap.String("email", formData.Email),
		zap.String("displayName", formData.DisplayName),
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
	var emailPtr *string
	if formData.Email != "" {
		emailPtr = &formData.Email
	}
	var displayNamePtr *string
	if formData.DisplayName != "" {
		displayNamePtr = &formData.DisplayName
	}
	updatePayload := client.UserUpdatePayload{
		Email:       emailPtr,
		DisplayName: displayNamePtr,
		Roles:       rolesSlice,
		IsBanned:    &isBanned,
	}
	err = h.authClient.UpdateUser(c.Request.Context(), userID, updatePayload)
	if err != nil {
		h.logger.Error("Failed to update user via auth client", zap.String("targetUserID", userID.String()), zap.Error(err))
		c.Redirect(http.StatusSeeOther, fmt.Sprintf("/admin/users/%s/edit?error=update_failed", userIDStr))
		return
	}
	userUpdatesTotal.Inc()
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/admin/users/%s/edit?success=updated", userIDStr))
}

func (h *AdminHandler) handleResetPassword(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.logger.Warn("Invalid user ID format for password reset", zap.String("rawID", userIDStr), zap.Error(err))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	adminUserID, _ := getUserIDFromContext(c)
	h.logger.Info("Admin attempting to reset password for user",
		zap.String("adminUserID", adminUserID.String()),
		zap.String("targetUserID", userID.String()),
	)
	_, err = h.authClient.ResetPassword(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to reset password via auth-service", zap.String("targetUserID", userID.String()), zap.Error(err))
		c.Redirect(http.StatusSeeOther, fmt.Sprintf("/admin/users/%s/edit?error=password_reset_failed", userIDStr))
		return
	}
	passwordResetsTotal.Inc()
	h.logger.Info("User password reset successfully", zap.String("targetUserID", userID.String()))
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/admin/users/%s/edit?success=password_reset", userIDStr))
}

func (h *AdminHandler) handleSendUserNotification(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.logger.Warn("Invalid user ID format for sending notification", zap.String("rawID", userIDStr), zap.Error(err))
		c.Redirect(http.StatusSeeOther, fmt.Sprintf("/admin/users/%s/edit?error=invalid_user_id", userIDStr))
		return
	}

	message := c.PostForm("notification_message")
	if message == "" {
		h.logger.Warn("Notification message is empty", zap.String("userID", userIDStr))
		c.Redirect(http.StatusSeeOther, fmt.Sprintf("/admin/users/%s/edit?error=empty_message", userIDStr))
		return
	}

	h.logger.Info("Attempting to send notification", zap.String("userID", userIDStr), zap.String("message", message))

	// ИЗМЕНЕНО: Используем тип interfaces.PushNotificationEvent и поле Body
	event := interfaces.PushNotificationEvent{
		UserID: userID.String(),                      // Используем uuid.UUID
		Title:  "Сообщение от администрации",         // Используем правильное название поля Title
		Body:   message,                              // <<< ИЗМЕНЕНО с Message на Body
		Data:   map[string]string{"source": "admin"}, // Можно добавить доп. данные
	}

	redirectURL := fmt.Sprintf("/admin/users/%s/edit", userIDStr)

	// Используем правильное имя метода PublishPushEvent (из интерфейса)
	if err := h.pushPublisher.PublishPushEvent(c.Request.Context(), event); err != nil {
		h.logger.Error("Failed to publish user push event", zap.Error(err), zap.String("userID", userIDStr))
		c.Redirect(http.StatusSeeOther, redirectURL+"?notification_status=error")
		return
	}

	h.logger.Info("Notification sent successfully", zap.String("userID", userIDStr))

	c.Redirect(http.StatusSeeOther, redirectURL+"?notification_status=success")
}
