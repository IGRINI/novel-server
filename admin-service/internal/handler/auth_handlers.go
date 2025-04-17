package handler

import (
	"context"
	"errors"
	"net/http"
	sharedModels "novel-server/shared/models"
	"time"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

type loginPageData struct {
	Username string
	Error    string
}

func (h *AdminHandler) showLoginPage(c echo.Context) error {
	cookie, err := c.Cookie("admin_session")
	if err == nil && cookie != nil && cookie.Value != "" {
		h.logger.Debug("Attempting to validate admin token via auth-service for login page check")
		startTime := time.Now()
		_, verifyErr := h.authClient.ValidateAdminToken(c.Request().Context(), cookie.Value)
		duration := time.Since(startTime)
		h.logger.Debug("Finished validating admin token for login page check", zap.Duration("duration", duration), zap.Error(verifyErr))
		if verifyErr == nil {
			return c.Redirect(http.StatusSeeOther, "/admin/dashboard")
		}
		h.clearAuthCookies(c)
	}
	data := map[string]interface{}{
		"IsLoginPage": true,
		"Error":       c.QueryParam("error"),
	}
	return c.Render(http.StatusOK, "login.html", data)
}

func (h *AdminHandler) handleLogin(c echo.Context) error {
	username := c.FormValue("username")
	password := c.FormValue("password")
	h.logger.Info("Login attempt", zap.String("username", username))
	tokenDetails, authErr := h.authClient.Login(c.Request().Context(), username, password)
	if authErr != nil {
		h.logger.Warn("Login failed via auth-service", zap.String("username", username), zap.Error(authErr))
		userFacingError := "Неверный логин или пароль"
		if errors.Is(authErr, context.DeadlineExceeded) || errors.Is(authErr, context.Canceled) {
			userFacingError = "Ошибка соединения с сервисом аутентификации (таймаут)"
		} else if !errors.Is(authErr, sharedModels.ErrInvalidCredentials) {
			userFacingError = "Произошла внутренняя ошибка при попытке входа"
		}
		data := map[string]interface{}{
			"IsLoginPage": true,
			"Username":    username,
			"Error":       userFacingError,
		}
		return c.Render(http.StatusOK, "login.html", data)
	}
	h.logger.Info("User authenticated by auth-service, checking admin role...", zap.String("username", username))
	claims, verifyErr := h.authClient.ValidateAdminToken(c.Request().Context(), tokenDetails.AccessToken)
	if verifyErr != nil {
		h.logger.Error("Failed to verify access token immediately after login", zap.String("username", username), zap.Error(verifyErr))
		data := map[string]interface{}{
			"IsLoginPage": true,
			"Username":    username,
			"Error":       "Неверный логин или пароль",
		}
		return c.Render(http.StatusOK, "login.html", data)
	}
	if !sharedModels.HasRole(claims.Roles, sharedModels.RoleAdmin) {
		h.logger.Warn("Login rejected: user does not have admin role", zap.String("username", username), zap.String("userID", claims.UserID.String()), zap.Strings("roles", claims.Roles))
		data := map[string]interface{}{
			"IsLoginPage": true,
			"Username":    username,
			"Error":       "Неверный логин или пароль",
		}
		return c.Render(http.StatusOK, "login.html", data)
	}
	accessTokenCookie := new(http.Cookie)
	accessTokenCookie.Name = "admin_session"
	accessTokenCookie.Value = tokenDetails.AccessToken
	accessTokenCookie.Expires = time.Now().Add(15 * time.Minute)
	accessTokenCookie.Path = "/"
	accessTokenCookie.HttpOnly = true
	c.SetCookie(accessTokenCookie)
	refreshTokenCookie := new(http.Cookie)
	refreshTokenCookie.Name = "admin_refresh_session"
	refreshTokenCookie.Value = tokenDetails.RefreshToken
	refreshTokenCookie.Expires = time.Now().Add(7 * 24 * time.Hour)
	refreshTokenCookie.Path = "/"
	refreshTokenCookie.HttpOnly = true
	c.SetCookie(refreshTokenCookie)
	h.logger.Info("Admin login successful, setting cookies", zap.String("username", username), zap.String("userID", claims.UserID.String()))
	c.Response().Header().Set("HX-Redirect", "/admin/dashboard")
	return c.NoContent(http.StatusOK)
}

func (h *AdminHandler) handleLogout(c echo.Context) error {
	h.clearAuthCookies(c)
	h.logger.Info("User logged out")
	return c.Redirect(http.StatusSeeOther, "/login")
}

func (h *AdminHandler) clearAuthCookies(c echo.Context) {
	accessCookie := new(http.Cookie)
	accessCookie.Name = "admin_session"
	accessCookie.Value = ""
	accessCookie.Expires = time.Unix(0, 0)
	accessCookie.Path = "/"
	accessCookie.HttpOnly = true
	accessCookie.Secure = true
	accessCookie.SameSite = http.SameSiteLaxMode
	c.SetCookie(accessCookie)
	refreshCookie := new(http.Cookie)
	refreshCookie.Name = "admin_refresh_session"
	refreshCookie.Value = ""
	refreshCookie.Expires = time.Unix(0, 0)
	refreshCookie.Path = "/"
	refreshCookie.HttpOnly = true
	refreshCookie.Secure = true
	refreshCookie.SameSite = http.SameSiteStrictMode
	c.SetCookie(refreshCookie)
}
