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
	var claims *sharedModels.Claims
	var adminToken string   // сохранённый в контекст токен доступа администратора
	var tokenRefreshed bool // Флаг, указывающий, был ли токен обновлен

	cookie, err := c.Cookie("admin_session")
	if err != nil {
		if errors.Is(err, http.ErrNoCookie) {
			log.Debug("Auth cookie 'admin_session' not found, attempting refresh with 'admin_refresh_session'")
			// Не выходим сразу, а пытаемся обновить по refresh токену ниже
		} else {
			// Другая ошибка чтения основной куки - выходим
			log.Error("Error reading auth cookie 'admin_session', returning 404", zap.Error(err))
			c.String(http.StatusNotFound, "404 page not found")
			c.Abort()
			return
		}
	} else {
		// Кука admin_session найдена, валидируем ее
		tokenString := cookie
		log.Debug("Attempting to validate admin token via auth-service")
		startTime := time.Now()
		claims, err = h.authClient.ValidateAdminToken(c.Request.Context(), tokenString)
		duration := time.Since(startTime)
		log.Debug("Finished validating admin token via auth-service", zap.Duration("duration", duration), zap.Error(err))

		if err == nil {
			// Токен валиден, проверяем роль
			if !sharedModels.HasRole(claims.Roles, sharedModels.RoleAdmin) {
				log.Warn("User without admin role after token validation, returning 404", zap.String("userID", claims.UserID.String()), zap.Strings("roles", claims.Roles))
				c.String(http.StatusNotFound, "404 page not found")
				c.Abort()
				return
			}
			// Устанавливаем данные и токен в контекст
			c.Set(string(sharedModels.UserContextKey), claims.UserID)
			c.Set(string(sharedModels.RolesContextKey), claims.Roles)
			adminToken = tokenString
			c.Set("admin_token", adminToken)
			log.Debug("Auth middleware passed (token valid), proceeding to next handler", zap.String("userID", claims.UserID.String()))
			c.Next()
			return
		}
		// Валидация не удалась (включая истекший или неверный токен): пытаемся обновить
		log.Warn("Admin token validation failed, attempting refresh", zap.Error(err))
	}

	// Сюда попадаем, если admin_session отсутствует ИЛИ если она истекла
	log.Debug("Attempting token refresh using 'admin_refresh_session'")
	refreshCookie, refreshErr := c.Cookie("admin_refresh_session")
	if refreshErr != nil {
		log.Warn("Refresh token cookie 'admin_refresh_session' not found, returning 404", zap.Error(refreshErr))
		c.String(http.StatusNotFound, "404 page not found")
		c.Abort()
		return
	}

	newTokens, newClaims, refreshCallErr := h.authClient.RefreshAdminToken(c.Request.Context(), refreshCookie)
	if refreshCallErr != nil {
		log.Warn("Failed to refresh admin token via auth-service, returning 404", zap.Error(refreshCallErr))
		// Очищаем куки на всякий случай, если рефреш не удался
		h.clearAuthCookies(c)
		c.String(http.StatusNotFound, "404 page not found")
		c.Abort()
		return
	}
	log.Info("Admin token refreshed successfully")

	// Устанавливаем новые куки
	accessTokenTTL := int(15 * time.Minute.Seconds())
	c.SetCookie("admin_session", newTokens.AccessToken, accessTokenTTL, "/", "", true, true) // Secure=true, HttpOnly=true
	refreshTokenTTL := int(7 * 24 * time.Hour.Seconds())
	c.SetCookie("admin_refresh_session", newTokens.RefreshToken, refreshTokenTTL, "/", "", true, true) // Secure=true, HttpOnly=true

	claims = newClaims // Используем новые клеймы
	tokenRefreshed = true

	// После успешного обновления ТОЧНО проверяем роль
	if !sharedModels.HasRole(claims.Roles, sharedModels.RoleAdmin) {
		log.Warn("User without admin role tried to access admin area after token refresh, returning 404", zap.String("userID", claims.UserID.String()), zap.Strings("roles", claims.Roles))
		// Очищаем куки, т.к. роль неверная
		h.clearAuthCookies(c)
		c.String(http.StatusNotFound, "404 page not found")
		c.Abort()
		return
	}

	// Все проверки пройдены (токен был обновлен и роль верна)
	c.Set(string(sharedModels.UserContextKey), claims.UserID)
	c.Set(string(sharedModels.RolesContextKey), claims.Roles)
	adminToken = newTokens.AccessToken // сохраняем новый токен после обновления
	c.Set("admin_token", adminToken)

	logMsg := "Auth middleware passed"
	if tokenRefreshed {
		logMsg += " (after refresh)"
	}
	log.Debug(logMsg+", proceeding to next handler", zap.String("userID", claims.UserID.String()))
	c.Next()
}

func (h *AdminHandler) clearAuthCookies(c *gin.Context) {
	c.SetCookie("admin_session", "", -1, "/", "", true, true)
	c.SetCookie("admin_refresh_session", "", -1, "/", "", true, true)
}
