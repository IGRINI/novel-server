package middleware

import (
	"context"
	"net/http"
	"strings"

	"novel-server/internal/authservice"
)

// AuthServiceMiddleware представляет middleware для проверки токенов через Auth Service
type AuthServiceMiddleware struct {
	authClient *authservice.Client
}

// NewAuthServiceMiddleware создает новый middleware для проверки токенов через Auth Service
func NewAuthServiceMiddleware(authClient *authservice.Client) func(http.Handler) http.Handler {
	m := &AuthServiceMiddleware{
		authClient: authClient,
	}
	return m.Middleware
}

// Middleware проверяет наличие и действительность JWT-токена в запросе через Auth Service
func (m *AuthServiceMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Извлекаем токен из заголовка Authorization
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "отсутствует или неверный формат токена доступа", http.StatusUnauthorized)
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")

		// Проверяем токен через Auth Service
		validateResp, err := m.authClient.ValidateToken(r.Context(), tokenString)
		if err != nil {
			http.Error(w, "ошибка при проверке токена: "+err.Error(), http.StatusUnauthorized)
			return
		}

		if !validateResp.Valid {
			http.Error(w, "недействительный токен доступа", http.StatusUnauthorized)
			return
		}

		// Добавляем информацию о пользователе в контекст запроса
		ctx := r.Context()
		ctx = context.WithValue(ctx, userIDKey, validateResp.UserID)
		ctx = context.WithValue(ctx, usernameKey, validateResp.Username)
		ctx = context.WithValue(ctx, userEmailKey, validateResp.Email)

		// Вызываем следующий обработчик с обновленным контекстом
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
