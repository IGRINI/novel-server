package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	// Импортируем для ErrorResponse
)

// --- Вспомогательные функции и типы (скопировано из auth/handlers.go) ---

type ErrorResponse struct {
	Error string `json:"error"`
}

func respondWithError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(ErrorResponse{Error: message})
}

// --- Конец вспомогательных функций ---

// Ключ для хранения ID пользователя в контексте
type contextKey string

const UserIDKey contextKey = "userID"

// JWTMiddleware проверяет JWT токен и добавляет userID в контекст
func JWTMiddleware(secretKey []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				respondWithError(w, http.StatusUnauthorized, "отсутствует заголовок Authorization")
				return
			}

			parts := strings.Split(authHeader, " ")
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				respondWithError(w, http.StatusUnauthorized, "неверный формат заголовка Authorization")
				return
			}

			tokenString := parts[1]

			token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
				// Проверка метода подписи
				if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("неожиданный метод подписи: %v", token.Header["alg"])
				}
				return secretKey, nil
			})

			if err != nil {
				respondWithError(w, http.StatusUnauthorized, fmt.Sprintf("ошибка валидации токена: %v", err))
				return
			}

			if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
				// Извлекаем userID из клеймов (убедитесь, что имя клейма совпадает с тем, что используется при генерации)
				userID, ok := claims["user_id"].(string)
				if !ok || userID == "" {
					respondWithError(w, http.StatusUnauthorized, "не удалось извлечь user_id из токена")
					return
				}

				// Добавляем userID в контекст
				ctx := context.WithValue(r.Context(), UserIDKey, userID)
				next.ServeHTTP(w, r.WithContext(ctx))
			} else {
				respondWithError(w, http.StatusUnauthorized, "невалидный токен")
			}
		})
	}
}

// GetUserIDFromContext извлекает userID из контекста
func GetUserIDFromContext(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(UserIDKey).(string)
	return userID, ok
}
