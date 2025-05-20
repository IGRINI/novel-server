package novel_handlers

import (
	"context"
	"net/http"
	"novel-server/internal/auth"
	"novel-server/internal/logger"
)

// AuthMiddleware проверяет JWT токен и добавляет UserID в контекст
func AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Получаем токен из заголовка Authorization
		tokenString := r.Header.Get("Authorization")
		if tokenString == "" {
			logger.Logger.Warn("AUTH: no Authorization header provided")
			respondWithError(w, http.StatusUnauthorized, "Authorization token is required")
			return
		}

		// Если токен начинается с "Bearer ", удаляем этот префикс
		const bearerPrefix = "Bearer "
		if len(tokenString) > len(bearerPrefix) && tokenString[:len(bearerPrefix)] == bearerPrefix {
			tokenString = tokenString[len(bearerPrefix):]
		}

		// Проверяем токен и получаем UserID
		claims, err := auth.ValidateToken(tokenString)
		if err != nil {
			logger.Logger.Warn("AUTH: error validating token", "err", err)
			respondWithError(w, http.StatusUnauthorized, "Invalid or expired token")
			return
		}

		// Используем константу UserIDKey из пакета auth
		ctx := context.WithValue(r.Context(), auth.UserIDKey, claims.UserID)
		logger.Logger.Info("AUTH: user added to context", "userID", claims.UserID)
		next(w, r.WithContext(ctx))
	}
}
