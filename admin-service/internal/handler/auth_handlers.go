package handler

import (
	"context"
	"errors"
	"net/http"
	sharedModels "novel-server/shared/models"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type loginPageData struct {
	Username string
	Error    string
}

func (h *AdminHandler) showLoginPage(c *gin.Context) {
	cookie, err := c.Cookie("admin_session")
	if err == nil && cookie != "" {
		h.logger.Debug("Attempting to validate admin token via auth-service for login page check")
		startTime := time.Now()
		_, verifyErr := h.authClient.ValidateAdminToken(c.Request.Context(), cookie)
		duration := time.Since(startTime)
		h.logger.Debug("Finished validating admin token for login page check", zap.Duration("duration", duration), zap.Error(verifyErr))
		if verifyErr == nil {
			c.Redirect(http.StatusSeeOther, "/admin/dashboard")
			return
		}
		h.clearAuthCookies(c)
	}
	data := gin.H{
		"IsLoginPage": true,
		"Error":       c.Query("error"),
	}
	c.HTML(http.StatusOK, "login.html", data)
}

func (h *AdminHandler) handleLogin(c *gin.Context) {
	username := c.PostForm("username")
	password := c.PostForm("password")
	h.logger.Info("Login attempt", zap.String("username", username))
	tokenDetails, authErr := h.authClient.Login(c.Request.Context(), username, password)
	if authErr != nil {
		h.logger.Warn("Login failed via auth-service", zap.String("username", username), zap.Error(authErr))
		userFacingError := "Неверный логин или пароль"
		if errors.Is(authErr, context.DeadlineExceeded) || errors.Is(authErr, context.Canceled) {
			userFacingError = "Ошибка соединения с сервисом аутентификации (таймаут)"
		} else if !errors.Is(authErr, sharedModels.ErrInvalidCredentials) {
			userFacingError = "Произошла внутренняя ошибка при попытке входа"
		}
		data := gin.H{
			"IsLoginPage": true,
			"Username":    username,
			"Error":       userFacingError,
		}
		c.HTML(http.StatusOK, "login.html", data)
		return
	}
	h.logger.Info("User authenticated by auth-service, checking admin role...", zap.String("username", username))
	claims, verifyErr := h.authClient.ValidateAdminToken(c.Request.Context(), tokenDetails.AccessToken)
	if verifyErr != nil {
		h.logger.Error("Failed to verify access token immediately after login", zap.String("username", username), zap.Error(verifyErr))
		data := gin.H{
			"IsLoginPage": true,
			"Username":    username,
			"Error":       "Неверный логин или пароль",
		}
		c.HTML(http.StatusOK, "login.html", data)
		return
	}
	if !sharedModels.HasRole(claims.Roles, sharedModels.RoleAdmin) {
		h.logger.Warn("Login rejected: user does not have admin role", zap.String("username", username), zap.String("userID", claims.UserID.String()), zap.Strings("roles", claims.Roles))
		data := gin.H{
			"IsLoginPage": true,
			"Username":    username,
			"Error":       "Неверный логин или пароль",
		}
		c.HTML(http.StatusOK, "login.html", data)
		return
	}

	accessTokenTTL := int(15 * time.Minute.Seconds())
	c.SetCookie("admin_session", tokenDetails.AccessToken, accessTokenTTL, "/", "", true, true)
	refreshTokenTTL := int(7 * 24 * time.Hour.Seconds())
	c.SetCookie("admin_refresh_session", tokenDetails.RefreshToken, refreshTokenTTL, "/", "", true, true)

	h.logger.Info("Admin login successful, setting cookies", zap.String("username", username), zap.String("userID", claims.UserID.String()))
	c.Redirect(http.StatusSeeOther, "/admin/dashboard")
}

func (h *AdminHandler) handleLogout(c *gin.Context) {
	h.clearAuthCookies(c)
	h.logger.Info("User logged out")
	c.Redirect(http.StatusSeeOther, "/login")
}
