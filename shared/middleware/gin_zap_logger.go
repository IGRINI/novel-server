package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ZapLoggingMiddlewareForGin возвращает middleware для Gin, которое логирует запросы с помощью zap.
func ZapLoggingMiddlewareForGin(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// Обрабатываем запрос
		c.Next()

		// После обработки запроса
		latency := time.Since(start)
		path := c.Request.URL.Path
		rawQuery := c.Request.URL.RawQuery
		if rawQuery != "" {
			path = path + "?" + rawQuery
		}

		// Собираем поля для лога
		fields := []zap.Field{
			zap.Int("status", c.Writer.Status()),
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("ip", c.ClientIP()),
			zap.Duration("latency", latency),
			zap.String("user_agent", c.Request.UserAgent()),
		}

		// Добавляем Request ID, если он есть (обычно устанавливается другим middleware)
		requestID := c.Writer.Header().Get("X-Request-ID")
		if requestID == "" {
			requestID = c.GetHeader("X-Request-ID")
		}
		if requestID == "" {
			requestID = uuid.NewString()
			c.Header("X-Request-ID", requestID) // Устанавливаем его для ответа
		}
		fields = append(fields, zap.String("request_id", requestID))

		// Логируем ошибки, если они были добавлены в контекст Gin
		if len(c.Errors) > 0 {
			// Используем c.Errors.ByType(gin.ErrorTypeAny) для получения всех типов ошибок
			for _, ginErr := range c.Errors.ByType(gin.ErrorTypeAny) {
				// Логируем каждую ошибку отдельно с контекстом запроса
				log.Error("Request error", append(fields, zap.Error(ginErr.Err))...)
			}
		} else {
			// Логирование успешных запросов или клиентских ошибок (без c.Errors)
			status := c.Writer.Status()
			switch {
			case status >= http.StatusInternalServerError:
				log.Error("Server error", fields...)
			case status >= http.StatusBadRequest:
				log.Warn("Client error", fields...)
			default:
				log.Info("Request completed", fields...)
			}
		}
	}
}
