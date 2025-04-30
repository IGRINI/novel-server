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
// Если аутентификация или авторизация не проходит, возвращает 404 Not Found.
func (h *AdminHandler) authMiddleware(c *gin.Context) {
	log := h.logger.With(zap.String("middleware", "authMiddleware"))
	cookie, err := c.Cookie("admin_session")
	if err != nil {
		if errors.Is(err, http.ErrNoCookie) {
			log.Debug("Auth cookie not found, returning 404")
		} else {
			log.Error("Error reading auth cookie, returning 404", zap.Error(err))
		}
		c.String(http.StatusNotFound, "404 page not found")
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
				log.Warn("Refresh token cookie not found after access token expired, returning 404")
				c.String(http.StatusNotFound, "404 page not found")
				c.Abort()
				return
			}
			newTokens, newClaims, refreshCallErr := h.authClient.RefreshAdminToken(c.Request.Context(), refreshCookie)
			if refreshCallErr != nil {
				log.Warn("Failed to refresh admin token via auth-service, returning 404", zap.Error(refreshCallErr))
				c.String(http.StatusNotFound, "404 page not found")
				c.Abort()
				return
			}
			log.Info("Admin token refreshed successfully")

			// Устанавливаем новые куки
			accessTokenTTL := int(15 * time.Minute.Seconds())
			c.SetCookie("admin_session", newTokens.AccessToken, accessTokenTTL, "/", "", true, true)
			refreshTokenTTL := int(7 * 24 * time.Hour.Seconds())
			c.SetCookie("admin_refresh_session", newTokens.RefreshToken, refreshTokenTTL, "/", "", true, true)

			claims = newClaims // Используем новые клеймы
		} else {
			log.Warn("Token validation failed via auth-service (not expired), returning 404", zap.Error(err))
			c.String(http.StatusNotFound, "404 page not found")
			c.Abort()
			return
		}
	}

	// Проверяем роль ПОСЛЕ валидации или обновления токена
	if !sharedModels.HasRole(claims.Roles, sharedModels.RoleAdmin) {
		log.Warn("User without admin role tried to access admin area, returning 404", zap.String("userID", claims.UserID.String()), zap.Strings("roles", claims.Roles))
		c.String(http.StatusNotFound, "404 page not found")
		c.Abort()
		return
	}

	// Все проверки пройдены, устанавливаем данные в контекст
	c.Set(string(sharedModels.UserContextKey), claims.UserID)
	c.Set(string(sharedModels.RolesContextKey), claims.Roles)

	log.Debug("Auth middleware passed, proceeding to next handler", zap.String("userID", claims.UserID.String()))
	c.Next()
}

func (h *AdminHandler) clearAuthCookies(c *gin.Context) {
	c.SetCookie("admin_session", "", -1, "/", "", true, true)
	c.SetCookie("admin_refresh_session", "", -1, "/", "", true, true)
}
