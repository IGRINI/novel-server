package middleware

import (
	"context"
	"errors"
	"net/http"
	"novel-server/shared/models"
	"strings"

	"go.uber.org/zap"
)

// TokenVerifier определяет функцию, которая проверяет строку токена и возвращает claims.
// Ошибки могут быть models.ErrTokenInvalid, models.ErrTokenExpired, models.ErrTokenMalformed и т.д.
type TokenVerifier func(ctx context.Context, tokenString string) (*models.Claims, error)

// AuthMiddleware создает HTTP middleware для проверки JWT и ролей.
// Оно извлекает токен, верифицирует его с помощью предоставленного verifier,
// проверяет наличие необходимых ролей и добавляет UserID/Roles в контекст запроса.
func AuthMiddleware(verifier TokenVerifier, logger *zap.Logger, requiredRoles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			log := logger.With(zap.String("path", r.URL.Path)) // Добавляем путь к логам

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				log.Warn("Authorization header missing")
				models.SendJSONError(w, "Unauthorized: Missing token", http.StatusUnauthorized)
				return
			}

			parts := strings.Split(authHeader, " ")
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				log.Warn("Malformed Authorization header", zap.String("header", authHeader))
				models.SendJSONError(w, "Unauthorized: Malformed token header", http.StatusUnauthorized)
				return
			}
			tokenString := parts[1]

			claims, err := verifier(ctx, tokenString)
			if err != nil {
				status := http.StatusUnauthorized
				msg := "Unauthorized: Invalid token"
				if errors.Is(err, models.ErrTokenExpired) {
					msg = "Unauthorized: Token expired"
				} else if errors.Is(err, models.ErrTokenMalformed) || errors.Is(err, models.ErrTokenInvalid) {
					// Используем одинаковое сообщение для невалидного и некорректного формата
				} else {
					// Для неожиданных ошибок верификации
					log.Error("Unexpected token verification error", zap.Error(err))
					status = http.StatusInternalServerError
					msg = "Internal server error during token verification"
				}
				// Логгируем начало токена для отладки, не весь токен
				tokenSnippet := ""
				if len(tokenString) > 10 {
					tokenSnippet = tokenString[:10] + "..."
				} else {
					tokenSnippet = tokenString
				}
				log.Warn("Token verification failed", zap.Error(err), zap.String("tokenSnippet", tokenSnippet))
				models.SendJSONError(w, msg, status)
				return
			}

			if len(requiredRoles) > 0 {
				// Используем хелпер HasRole из models
				hasRequiredRole := false
				for _, requiredRole := range requiredRoles {
					if models.HasRole(claims.Roles, requiredRole) {
						hasRequiredRole = true
						break
					}
				}

				if !hasRequiredRole {
					log.Warn("User does not have required role",
						zap.String("userID", claims.UserID.String()),
						zap.Strings("userRoles", claims.Roles),
						zap.Strings("requiredRoles", requiredRoles),
					)
					models.SendJSONError(w, "Forbidden: Insufficient permissions", http.StatusForbidden)
					return
				}
			}

			// Добавляем информацию в контекст
			ctx = context.WithValue(ctx, models.UserContextKey, claims.UserID)
			ctx = context.WithValue(ctx, models.RolesContextKey, claims.Roles)

			log.Debug("User authorized", zap.String("userID", claims.UserID.String()), zap.Strings("roles", claims.Roles))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
