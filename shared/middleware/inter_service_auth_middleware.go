package middleware

import (
	"errors"
	"net/http"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

// InterServiceAuthMiddleware создает Echo middleware для проверки межсервисного JWT.
func InterServiceAuthMiddleware(verifier interfaces.TokenVerifier, logger *zap.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			log := logger.With(zap.String("path", c.Request().URL.Path))

			tokenString := c.Request().Header.Get("X-Internal-Service-Token")
			if tokenString == "" {
				log.Warn("X-Internal-Service-Token header missing")
				return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized: Missing inter-service token")
			}

			// Верифицируем токен (используем VerifyInterServiceToken)
			claims, err := verifier.VerifyInterServiceToken(c.Request().Context(), tokenString)
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
				return echo.NewHTTPError(status, msg)
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

			return next(c)
		}
	}
}
