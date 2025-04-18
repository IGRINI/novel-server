package middleware

import (
	"errors"
	"net/http"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// InterServiceAuthMiddlewareGin создает Gin middleware для проверки межсервисного JWT.
func InterServiceAuthMiddlewareGin(verifier interfaces.TokenVerifier, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		log := logger.With(zap.String("path", c.Request.URL.Path))

		tokenString := c.Request.Header.Get("X-Internal-Service-Token")
		if tokenString == "" {
			log.Warn("X-Internal-Service-Token header missing")
			c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{Message: "Unauthorized: Missing inter-service token"})
			return
		}

		// Верифицируем токен (используем VerifyInterServiceToken)
		claims, err := verifier.VerifyInterServiceToken(c.Request.Context(), tokenString)
		if err != nil {
			status := http.StatusUnauthorized
			msg := "Unauthorized: Invalid inter-service token"
			if errors.Is(err, models.ErrTokenExpired) {
				msg = "Unauthorized: Inter-service token expired"
			} else if errors.Is(err, models.ErrTokenMalformed) || errors.Is(err, models.ErrTokenInvalid) {
				// Используем общее сообщение
			} else {
				log.Error("Unexpected inter-service token verification error", zap.Error(err))
				status = http.StatusInternalServerError
				msg = "Internal server error during inter-service token verification"
			}

			tokenSnippet := ""
			if len(tokenString) > 15 {
				tokenSnippet = tokenString[:15] + "..."
			} else {
				tokenSnippet = tokenString
			}
			log.Warn("Inter-service token verification failed", zap.Error(err), zap.String("tokenSnippet", tokenSnippet))
			c.AbortWithStatusJSON(status, models.ErrorResponse{Message: msg})
			return
		}

		// Не добавляем UserID/Roles в контекст, так как это межсервисный вызов.
		// Добавляем ID сервиса-источника, если он есть в claims.
		if claims != nil && claims.Subject != "" { // Предполагаем, что ServiceID в Subject
			c.Set(string(models.SourceServiceContextKey), claims.Subject)
			log.Debug("Inter-service request authorized", zap.String("sourceService", claims.Subject))
		} else {
			log.Warn("Inter-service token authorized but Subject (source service) is missing")
			// Продолжаем выполнение, но логируем предупреждение
		}

		c.Next()
	}
}
