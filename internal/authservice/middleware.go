package authservice

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// Ключи контекста
type contextKey string

const (
	userIDKey    contextKey = "user_id"
	usernameKey  contextKey = "username"
	userEmailKey contextKey = "user_email"
)

// JWTMiddleware проверяет наличие и действительность JWT-токена в запросе
func JWTMiddleware(jwtSecret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, "отсутствует или неверный формат токена доступа", http.StatusUnauthorized)
				return
			}

			tokenString := strings.TrimPrefix(authHeader, "Bearer ")

			token, err := jwt.ParseWithClaims(tokenString, &CustomClaims{}, func(token *jwt.Token) (interface{}, error) {
				if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("неожиданный метод подписи")
				}
				return jwtSecret, nil
			})

			if err != nil {
				if errors.Is(err, jwt.ErrTokenExpired) {
					http.Error(w, "токен доступа истек", http.StatusUnauthorized)
					return
				}
				http.Error(w, "недействительный токен доступа", http.StatusUnauthorized)
				return
			}

			if claims, ok := token.Claims.(*CustomClaims); ok && token.Valid {
				// Добавляем информацию о пользователе в контекст запроса
				ctx := r.Context()
				ctx = context.WithValue(ctx, userIDKey, claims.UserID)
				ctx = context.WithValue(ctx, usernameKey, claims.Username)
				ctx = context.WithValue(ctx, userEmailKey, claims.Email)
				// Вызываем следующий обработчик с обновленным контекстом
				next.ServeHTTP(w, r.WithContext(ctx))
			} else {
				http.Error(w, "недействительный токен доступа", http.StatusUnauthorized)
			}
		})
	}
}

// ServiceAuthMiddleware проверяет наличие и действительность токена сервиса
func ServiceAuthMiddleware(jwtSecret []byte, trustedServices map[string]string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Service-Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, "отсутствует или неверный формат токена сервиса", http.StatusUnauthorized)
				return
			}

			tokenString := strings.TrimPrefix(authHeader, "Bearer ")

			token, err := jwt.ParseWithClaims(tokenString, &ServiceClaims{}, func(token *jwt.Token) (interface{}, error) {
				if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("неожиданный метод подписи")
				}
				return jwtSecret, nil
			})

			if err != nil {
				if errors.Is(err, jwt.ErrTokenExpired) {
					http.Error(w, "токен сервиса истек", http.StatusUnauthorized)
					return
				}
				http.Error(w, "недействительный токен сервиса", http.StatusUnauthorized)
				return
			}

			if claims, ok := token.Claims.(*ServiceClaims); ok && token.Valid {
				// Проверяем, является ли сервис доверенным
				if _, ok := trustedServices[claims.ServiceID]; !ok {
					http.Error(w, "сервис не авторизован", http.StatusUnauthorized)
					return
				}

				// Добавляем информацию о сервисе в контекст запроса
				ctx := r.Context()
				ctx = context.WithValue(ctx, contextKey("service_id"), claims.ServiceID)
				ctx = context.WithValue(ctx, contextKey("service_name"), claims.ServiceName)

				// Вызываем следующий обработчик с обновленным контекстом
				next.ServeHTTP(w, r.WithContext(ctx))
			} else {
				http.Error(w, "недействительный токен сервиса", http.StatusUnauthorized)
			}
		})
	}
}

// GetUserIDFromContext извлекает ID пользователя из контекста
func GetUserIDFromContext(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(userIDKey).(string)
	return userID, ok
}

// GetUsernameFromContext извлекает имя пользователя из контекста
func GetUsernameFromContext(ctx context.Context) (string, bool) {
	username, ok := ctx.Value(usernameKey).(string)
	return username, ok
}

// GetUserEmailFromContext извлекает email пользователя из контекста
func GetUserEmailFromContext(ctx context.Context) (string, bool) {
	email, ok := ctx.Value(userEmailKey).(string)
	return email, ok
}

// GetServiceIDFromContext извлекает ID сервиса из контекста
func GetServiceIDFromContext(ctx context.Context) (string, bool) {
	serviceID, ok := ctx.Value(contextKey("service_id")).(string)
	return serviceID, ok
}

// GetServiceNameFromContext извлекает название сервиса из контекста
func GetServiceNameFromContext(ctx context.Context) (string, bool) {
	serviceName, ok := ctx.Value(contextKey("service_name")).(string)
	return serviceName, ok
}
