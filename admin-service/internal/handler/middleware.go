package handler

import (
	"errors"
	"net/http"
	sharedModels "novel-server/shared/models"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// authMiddleware проверяет токен доступа, пытается обновить его при необходимости
// и добавляет информацию о пользователе в контекст Gin.
func (h *AdminHandler) authMiddleware(c *gin.Context) {
	log := h.logger.With(zap.String("middleware", "authMiddleware"))
	cookie, err := c.Cookie("admin_session")
	if err != nil {
		if errors.Is(err, http.ErrNoCookie) {
			log.Debug("Auth cookie not found, redirecting to login")
		} else {
			log.Error("Error reading auth cookie", zap.Error(err))
		}
		h.clearAuthCookies(c)
		c.Redirect(http.StatusSeeOther, "/login?error=session_required")
		c.Abort()
		return
	}

	tokenString := cookie
	log.Debug("Attempting to validate admin token via auth-service")
	startTime := time.Now()
	claims, err := h.authClient.ValidateAdminToken(c.Request.Context(), tokenString)
	duration := time.Since(startTime)
	log.Debug("Finished validating admin token via auth-service", zap.Duration("duration", duration), zap.Error(err))

	if err != nil {
		if errors.Is(err, sharedModels.ErrTokenExpired) {
			log.Info("Access token expired, attempting refresh")
			refreshCookie, refreshErr := c.Cookie("admin_refresh_session")
			if refreshErr != nil {
				log.Warn("Refresh token cookie not found after access token expired")
				h.clearAuthCookies(c)
				c.Redirect(http.StatusSeeOther, "/login?error=session_expired")
				c.Abort()
				return
			}
			newTokens, newClaims, refreshCallErr := h.authClient.RefreshAdminToken(c.Request.Context(), refreshCookie)
			if refreshCallErr != nil {
				log.Warn("Failed to refresh admin token via auth-service", zap.Error(refreshCallErr))
				h.clearAuthCookies(c)
				c.Redirect(http.StatusSeeOther, "/login?error=refresh_failed")
				c.Abort()
				return
			}
			log.Info("Admin token refreshed successfully")

			accessTokenTTL := int(15 * time.Minute.Seconds())
			c.SetCookie("admin_session", newTokens.AccessToken, accessTokenTTL, "/", "", true, true)
			refreshTokenTTL := int(7 * 24 * time.Hour.Seconds())
			c.SetCookie("admin_refresh_session", newTokens.RefreshToken, refreshTokenTTL, "/", "", true, true)

			claims = newClaims
		} else {
			log.Warn("Token validation failed via auth-service (not expired)", zap.Error(err))
			h.clearAuthCookies(c)
			c.Redirect(http.StatusSeeOther, "/login?error=invalid_token")
			c.Abort()
			return
		}
	}

	if !sharedModels.HasRole(claims.Roles, sharedModels.RoleAdmin) {
		log.Warn("User without admin role tried to access admin area", zap.String("userID", claims.UserID.String()), zap.Strings("roles", claims.Roles))
		h.clearAuthCookies(c)
		c.Redirect(http.StatusSeeOther, "/login?error=access_denied")
		c.Abort()
		return
	}

	c.Set(string(sharedModels.UserContextKey), claims.UserID)
	c.Set(string(sharedModels.RolesContextKey), claims.Roles)

	log.Debug("Auth middleware passed, proceeding to next handler", zap.String("userID", claims.UserID.String()))
	c.Next()
}

func (h *AdminHandler) clearAuthCookies(c *gin.Context) {
	c.SetCookie("admin_session", "", -1, "/", "", true, true)
	c.SetCookie("admin_refresh_session", "", -1, "/", "", true, true)
}
