package middleware

import (
	"context"
	"errors"
	"net/http"
	"novel-server/shared/models"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// TokenVerifier определяет функцию, которая проверяет строку токена и возвращает claims.
// Ошибки могут быть models.ErrTokenInvalid, models.ErrTokenExpired, models.ErrTokenMalformed и т.д.
type TokenVerifier func(ctx context.Context, tokenString string) (*models.Claims, error)

// Ключи контекста Gin (строки)
const (
	GinUserContextKey  = "user_id"
	GinRolesContextKey = "user_roles"
)

// AuthMiddleware создает Gin middleware для проверки JWT и ролей.
// Оно извлекает токен, верифицирует его с помощью предоставленного verifier,
// проверяет наличие необходимых ролей и добавляет UserID/Roles в контекст Gin.
func AuthMiddleware(verifier TokenVerifier, logger *zap.Logger, requiredRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		log := logger.With(zap.String("path", c.Request.URL.Path))

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			log.Warn("Authorization header missing")
			c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{
				Code:    models.ErrCodeUnauthorized,
				Message: "Unauthorized: Missing token",
			})
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			log.Warn("Malformed Authorization header", zap.String("header", authHeader))
			c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{
				Code:    models.ErrCodeUnauthorized,
				Message: "Unauthorized: Malformed token header",
			})
			return
		}
		tokenString := parts[1]

		claims, err := verifier(ctx, tokenString)
		if err != nil {
			status := http.StatusUnauthorized
			code := models.ErrCodeTokenInvalid
			msg := "Unauthorized: Invalid token"

			if errors.Is(err, models.ErrTokenExpired) {
				code = models.ErrCodeTokenExpired
				msg = "Unauthorized: Token expired"
			} else if errors.Is(err, models.ErrTokenMalformed) || errors.Is(err, models.ErrTokenInvalid) || errors.Is(err, models.ErrTokenNotFound) {
				code = models.ErrCodeTokenInvalid
				msg = "Unauthorized: Token is invalid or expired"
			} else {
				log.Error("Unexpected token verification error", zap.Error(err))
				status = http.StatusInternalServerError
				code = models.ErrCodeInternal
				msg = "Internal server error during token verification"
			}
			tokenSnippet := ""
			if len(tokenString) > 10 {
				tokenSnippet = tokenString[:10] + "..."
			} else {
				tokenSnippet = tokenString
			}
			log.Warn("Token verification failed", zap.Error(err), zap.String("tokenSnippet", tokenSnippet))
			c.AbortWithStatusJSON(status, models.ErrorResponse{Code: code, Message: msg})
			return
		}

		if len(requiredRoles) > 0 {
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
				c.AbortWithStatusJSON(http.StatusForbidden, models.ErrorResponse{
					Code:    models.ErrCodeForbidden,
					Message: "Forbidden: Insufficient permissions",
				})
				return
			}
		}

		c.Set(GinUserContextKey, claims.UserID)
		c.Set(GinRolesContextKey, claims.Roles)

		log.Debug("User authorized", zap.String("userID", claims.UserID.String()), zap.Strings("roles", claims.Roles))
		c.Next()
	}
}
