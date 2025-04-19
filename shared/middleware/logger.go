package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// GinZapLogger возвращает middleware для Gin, которое логирует запросы с помощью zap.
// Включает пропуск логирования для healthcheck/metrics и добавление request ID.
func GinZapLogger(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		// Пропускаем логирование для /health и /metrics
		if path == "/health" || path == "/metrics" {
			c.Next() // Просто передаем управление дальше
			return   // Выходим из middleware, логирование не будет выполнено
		}

		// Обрабатываем запрос
		c.Next()

		// После обработки запроса
		latency := time.Since(start)
		rawQuery := c.Request.URL.RawQuery
		if rawQuery != "" {
			path = path + "?" + rawQuery // Обновляем path для лога, если есть query
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

		// Добавляем Request ID
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
			for _, ginErr := range c.Errors.ByType(gin.ErrorTypeAny) {
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
