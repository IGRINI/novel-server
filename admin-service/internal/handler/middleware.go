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

func (h *AdminHandler) authMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		cookie, err := c.Cookie("admin_session")
		if err != nil {
			if errors.Is(err, http.ErrNoCookie) {
				return echo.ErrNotFound
			}
			h.logger.Error("Error reading auth cookie", zap.Error(err))
			return echo.NewHTTPError(http.StatusInternalServerError, "Internal server error")
		}

		tokenString := cookie.Value
		h.logger.Debug("Attempting to validate admin token via auth-service")
		startTime := time.Now()
		claims, err := h.authClient.ValidateAdminToken(c.Request().Context(), tokenString)
		duration := time.Since(startTime)
		h.logger.Debug("Finished validating admin token via auth-service", zap.Duration("duration", duration), zap.Error(err))

		if err != nil {
			if errors.Is(err, sharedModels.ErrTokenExpired) {
				h.logger.Info("Access token expired, attempting refresh")
				refreshCookie, refreshErr := c.Cookie("admin_refresh_session")
				if refreshErr != nil {
					h.logger.Warn("Refresh token cookie not found after access token expired")
					h.clearAuthCookies(c)
					return echo.ErrNotFound
				}
				newTokens, newClaims, refreshCallErr := h.authClient.RefreshAdminToken(c.Request().Context(), refreshCookie.Value)
				if refreshCallErr != nil {
					h.logger.Warn("Failed to refresh admin token via auth-service", zap.Error(refreshCallErr))
					h.clearAuthCookies(c)
					return echo.ErrNotFound
				}
				h.logger.Info("Admin token refreshed successfully")
				accessTokenCookie := new(http.Cookie)
				accessTokenCookie.Name = "admin_session"
				accessTokenCookie.Value = newTokens.AccessToken
				accessTokenCookie.Expires = time.Now().Add(15 * time.Minute)
				accessTokenCookie.Path = "/"
				accessTokenCookie.HttpOnly = true
				accessTokenCookie.Secure = true
				accessTokenCookie.SameSite = http.SameSiteLaxMode
				c.SetCookie(accessTokenCookie)
				refreshTokenCookie := new(http.Cookie)
				refreshTokenCookie.Name = "admin_refresh_session"
				refreshTokenCookie.Value = newTokens.RefreshToken
				refreshTokenCookie.Expires = time.Now().Add(7 * 24 * time.Hour)
				refreshTokenCookie.Path = "/"
				refreshTokenCookie.HttpOnly = true
				refreshTokenCookie.Secure = true
				refreshTokenCookie.SameSite = http.SameSiteStrictMode
				c.SetCookie(refreshTokenCookie)
				claims = newClaims
			} else {
				h.logger.Warn("Token validation failed via auth-service (not expired)", zap.Error(err))
				h.clearAuthCookies(c)
				return echo.ErrNotFound
			}
		}

		if !sharedModels.HasRole(claims.Roles, sharedModels.RoleAdmin) {
			h.logger.Warn("User without admin role tried to access admin area", zap.String("userID", claims.UserID.String()), zap.Strings("roles", claims.Roles))
			h.clearAuthCookies(c)
			return echo.ErrNotFound
		}

		ctx := context.WithValue(c.Request().Context(), sharedModels.UserContextKey, claims.UserID)
		ctx = context.WithValue(ctx, sharedModels.RolesContextKey, claims.Roles)
		c.SetRequest(c.Request().WithContext(ctx))
		return next(c)
	}
}
