package middleware

import (
	"net/http"
	"time"

	// "novel-server/shared/logger" // <<< Импорт logger здесь не нужен, т.к. используется только zap.Logger

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

// EchoZapLogger возвращает middleware для Echo, которое логирует запросы с помощью zap.
// Принимает zap.Logger (предпочтительно созданный через shared/logger.New).
func EchoZapLogger(log *zap.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()

			req := c.Request()
			res := c.Response()

			// <<< Собираем базовые поля запроса СРАЗУ >>>
			requestFields := []zap.Field{
				zap.String("method", req.Method),
				zap.String("uri", req.RequestURI),
				zap.String("host", req.Host),
				zap.String("remote_ip", c.RealIP()),
				zap.String("user_agent", req.UserAgent()),
			}
			id := req.Header.Get(echo.HeaderXRequestID)
			if id == "" {
				id = res.Header().Get(echo.HeaderXRequestID)
			}
			if id != "" {
				requestFields = append(requestFields, zap.String("request_id", id))
			}
			// <<< Конец сбора базовых полей >>>

			err := next(c) // Выполняем хендлер

			latency := time.Since(start)
			// Добавляем статус и задержку к общим полям
			fields := append(requestFields,
				zap.Int("status", res.Status),
				zap.Duration("latency", latency),
			)

			if err != nil {
				// <<< Добавляем поля запроса в лог ОШИБКИ >>>
				// Добавляем саму ошибку в поля
				errorFields := append(fields, zap.Error(err))
				// Логируем ошибку уровня Error
				log.Error("Handler error", errorFields...)
				// Отдаем ошибку дальше (Echo сам установит статус ответа по ней)
				return err
			}

			// <<< Логирование успешных или клиентских ошибок (без err != nil) >>>
			n := res.Status
			switch {
			case n >= http.StatusInternalServerError:
				log.Error("Server error", fields...) // Логируем с полями
			case n >= http.StatusBadRequest:
				log.Warn("Client error", fields...) // Логируем с полями
			case n >= http.StatusMultipleChoices:
				log.Warn("Redirection", fields...) // Логируем с полями
			default:
				log.Info("Success", fields...) // Логируем с полями
			}

			return nil // Ошибки не было
		}
	}
}
