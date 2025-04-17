package handler

import (
	"errors"
	"fmt"
	"net/http"
	"novel-server/admin-service/internal/client"
	sharedModels "novel-server/shared/models"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

type userUpdateFormData struct {
	Email    string   `form:"email"`
	Roles    []string `form:"roles"`
	IsBanned string   `form:"is_banned"`
}

func (h *AdminHandler) listUsers(c echo.Context) error {
	userID, _ := sharedModels.GetUserIDFromContext(c.Request().Context())
	log := h.logger.With(zap.Uint64("adminUserID", userID))
	log.Info("Admin requested user list")
	limitStr := c.QueryParam("limit")
	afterCursor := c.QueryParam("after")
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
	users, nextCursor, err := h.authClient.ListUsers(c.Request().Context(), limit, afterCursor)
	if err != nil {
		log.Error("Failed to get user list from auth-service", zap.Error(err))
		users = []sharedModels.User{}
		data := map[string]interface{}{
			"PageTitle":  "Управление пользователями",
			"Users":      users,
			"Error":      "Не удалось загрузить список пользователей: " + err.Error(),
			"IsLoggedIn": true,
			"NextCursor": "",
			"Limit":      limit,
		}
		return c.Render(http.StatusOK, "users.html", data)
	}
	data := map[string]interface{}{
		"PageTitle":  "Управление пользователями",
		"Users":      users,
		"IsLoggedIn": true,
		"NextCursor": nextCursor,
		"Limit":      limit,
	}
	return c.Render(http.StatusOK, "users.html", data)
}

func (h *AdminHandler) handleBanUser(c echo.Context) error {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid user ID format")
	}
	adminUserID, _ := sharedModels.GetUserIDFromContext(c.Request().Context())
	h.logger.Info("Admin attempting to ban user", zap.String("adminUserID", strconv.FormatUint(adminUserID, 10)), zap.String("targetUserID", userID.String()))
	err = h.authClient.BanUser(c.Request().Context(), userID)
	if err != nil {
		h.logger.Error("Failed to ban user via auth client", zap.String("targetUserID", userID.String()), zap.Error(err))
		userFacingError := "Не удалось забанить пользователя."
		if errors.Is(err, sharedModels.ErrUserNotFound) {
			userFacingError = "Пользователь не найден."
			return echo.NewHTTPError(http.StatusNotFound, userFacingError)
		}
		return echo.NewHTTPError(http.StatusInternalServerError, userFacingError)
	}
	userBansTotal.Inc()
	users, _, listErr := h.authClient.ListUsers(c.Request().Context(), 20, "")
	if listErr != nil {
		h.logger.Error("Failed to reload user list after ban", zap.String("bannedUserID", userID.String()), zap.Error(listErr))
		c.Response().Header().Set("HX-Refresh", "true")
		return c.NoContent(http.StatusOK)
	}
	var updatedUser *sharedModels.User
	for i := range users {
		if users[i].ID == userID {
			updatedUser = &users[i]
			break
		}
	}
	if updatedUser == nil {
		c.Response().Header().Set("HX-Refresh", "true")
		return c.NoContent(http.StatusOK)
	}
	c.Response().Header().Set("HX-Refresh", "true")
	return c.NoContent(http.StatusOK)
}

func (h *AdminHandler) handleUnbanUser(c echo.Context) error {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid user ID format")
	}
	adminUserID, _ := sharedModels.GetUserIDFromContext(c.Request().Context())
	h.logger.Info("Admin attempting to unban user", zap.String("adminUserID", strconv.FormatUint(adminUserID, 10)), zap.String("targetUserID", userID.String()))
	err = h.authClient.UnbanUser(c.Request().Context(), userID)
	if err != nil {
		h.logger.Error("Failed to unban user via auth client", zap.String("targetUserID", userID.String()), zap.Error(err))
		userFacingError := "Не удалось разбанить пользователя."
		if errors.Is(err, sharedModels.ErrUserNotFound) {
			userFacingError = "Пользователь не найден."
			return echo.NewHTTPError(http.StatusNotFound, userFacingError)
		}
		return echo.NewHTTPError(http.StatusInternalServerError, userFacingError)
	}
	userUnbansTotal.Inc()
	c.Response().Header().Set("HX-Refresh", "true")
	return c.NoContent(http.StatusOK)
}

func (h *AdminHandler) showUserEditPage(c echo.Context) error {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid user ID format")
	}
	h.logger.Info("Showing edit page for user", zap.String("userID", userID.String()))
	users, _, err := h.authClient.ListUsers(c.Request().Context(), 20, "")
	if err != nil {
		h.logger.Error("Failed to get user list for editing", zap.String("userID", userID.String()), zap.Error(err))
		return c.Redirect(http.StatusSeeOther, "/admin/users?error=fetch_failed")
	}
	var user *sharedModels.User
	for i := range users {
		if users[i].ID == userID {
			user = &users[i]
			break
		}
	}
	if err != nil {
		if errors.Is(err, sharedModels.ErrUserNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "Пользователь не найден")
		}
		h.logger.Error("Failed to get user details for edit page", zap.String("userID", userID.String()), zap.Error(err))
		return echo.NewHTTPError(http.StatusInternalServerError, "Не удалось загрузить данные пользователя")
	}
	if user == nil {
		return echo.NewHTTPError(http.StatusNotFound, "Пользователь не найден")
	}
	data := map[string]interface{}{
		"PageTitle":        "Редактирование пользователя",
		"User":             user,
		"AllRoles":         sharedModels.AllRoles(),
		"CurrentUserRoles": user.Roles,
		"IsLoggedIn":       true,
	}
	return c.Render(http.StatusOK, "user_edit.html", data)
}

func (h *AdminHandler) handleUserUpdate(c echo.Context) error {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid user ID format")
	}
	var formData userUpdateFormData
	if err := c.Bind(&formData); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid form data: "+err.Error())
	}
	adminUserID, _ := sharedModels.GetUserIDFromContext(c.Request().Context())
	h.logger.Info("Admin attempting to update user",
		zap.String("adminUserID", strconv.FormatUint(adminUserID, 10)),
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
	err = h.authClient.UpdateUser(c.Request().Context(), userID, updatePayload)
	users, _, listErr := h.authClient.ListUsers(c.Request().Context(), 20, "")
	if listErr != nil {
		h.logger.Error("Failed to reload user list after update attempt", zap.String("userID", userID.String()), zap.Error(listErr))
	}
	var user *sharedModels.User
	if listErr == nil {
		for i := range users {
			if users[i].ID == userID {
				user = &users[i]
				break
			}
		}
	}
	if user == nil {
		h.logger.Warn("User not found in list after update attempt or list failed, using form data as fallback for render", zap.String("userID", userID.String()))
		user = &sharedModels.User{ID: userID, Username: "(unknown)", Email: formData.Email, Roles: rolesSlice, IsBanned: isBanned}
	}
	data := map[string]interface{}{
		"PageTitle":   "Редактирование пользователя",
		"User":        user,
		"RolesString": strings.Join(user.Roles, " "),
		"IsLoggedIn":  true,
	}
	if err != nil {
		h.logger.Error("Failed to update user via auth client", zap.String("targetUserID", userID.String()), zap.Error(err))
		data["Error"] = "Не удалось сохранить изменения. " + err.Error()
		return c.Render(http.StatusOK, "user_edit.html", data)
	}
	userUpdatesTotal.Inc()
	data["Success"] = "Изменения успешно сохранены!"
	return c.Render(http.StatusOK, "user_edit.html", data)
}

func (h *AdminHandler) handleResetPassword(c echo.Context) error {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid user ID format")
	}
	adminUserID, _ := sharedModels.GetUserIDFromContext(c.Request().Context())
	h.logger.Info("Admin attempting to reset password for user",
		zap.String("adminUserID", strconv.FormatUint(adminUserID, 10)),
		zap.String("targetUserID", userID.String()),
	)
	newPassword, err := h.authClient.ResetPassword(c.Request().Context(), userID)
	if err != nil {
		h.logger.Error("Failed to reset password via auth client", zap.String("targetUserID", userID.String()), zap.Error(err))
		errorMessage := "Не удалось сбросить пароль."
		if errors.Is(err, sharedModels.ErrUserNotFound) {
			errorMessage = "Пользователь не найден."
		}
		c.Response().Header().Set("HX-Retarget", "#password-reset-status")
		c.Response().Header().Set("HX-Reswap", "innerHTML")
		return c.String(http.StatusInternalServerError, fmt.Sprintf("<article aria-invalid='true'>%s</article>", errorMessage))
	}
	passwordResetsTotal.Inc()
	h.logger.Info("Password reset successful for user", zap.String("targetUserID", userID.String()))
	c.Response().Header().Set("HX-Retarget", "#password-reset-status")
	c.Response().Header().Set("HX-Reswap", "innerHTML")
	responseHTML := fmt.Sprintf(
		`<article style="background-color: var(--pico-color-green-150); border-color: var(--pico-color-green-400);">
			Пароль успешно сброшен. Новый временный пароль:
			<pre><code>%s</code></pre>
			<small>Пожалуйста, скопируйте этот пароль и передайте пользователю. После первого входа ему следует сменить пароль.</small>
		</article>`,
		newPassword,
	)
	return c.HTML(http.StatusOK, responseHTML)
}
