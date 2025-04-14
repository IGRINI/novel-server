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

			err := next(c)
			if err != nil {
				log.Error("Handler error", zap.Error(err))
			}

			latency := time.Since(start)

			fields := []zap.Field{
				zap.Int("status", res.Status),
				zap.String("method", req.Method),
				zap.String("uri", req.RequestURI),
				zap.String("host", req.Host),
				zap.String("remote_ip", c.RealIP()),
				zap.Duration("latency", latency),
				zap.String("user_agent", req.UserAgent()),
			}

			id := req.Header.Get(echo.HeaderXRequestID)
			if id == "" {
				id = res.Header().Get(echo.HeaderXRequestID)
				if id != "" {
					fields = append(fields, zap.String("request_id", id))
				}
			}

			n := res.Status
			switch {
			case n >= http.StatusInternalServerError:
				log.Error("Server error", fields...)
			case n >= http.StatusBadRequest:
				log.Warn("Client error", fields...)
			case n >= http.StatusMultipleChoices:
				log.Warn("Redirection", fields...)
			default:
				log.Info("Success", fields...)
			}

			return err
		}
	}
} 