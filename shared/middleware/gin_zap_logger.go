package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ZapLoggingMiddlewareForGin возвращает middleware для Gin, которое логирует запросы с помощью zap.
// Добавлена логика пропуска логирования для healthcheck и metrics эндпоинтов.
func ZapLoggingMiddlewareForGin(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		// <<< НАЧАЛО ИЗМЕНЕНИЙ: Пропускаем логирование для /health и /metrics >>>
		// Проверяем путь ДО обработки запроса
		if path == "/health" || path == "/metrics" {
			c.Next() // Просто передаем управление дальше
			return   // Выходим из middleware, логирование не будет выполнено
		}
		// <<< КОНЕЦ ИЗМЕНЕНИЙ >>>

		// Обрабатываем запрос (только если путь не /health и не /metrics)
		c.Next()

		// После обработки запроса
		latency := time.Since(start)
		// path уже получен выше
		rawQuery := c.Request.URL.RawQuery
		if rawQuery != "" {
			// Важно: Не переопределять path здесь, если он /health или /metrics,
			// но мы до этого кода не дойдем благодаря return выше.
			// Если бы дошли, то нужно было бы получать path снова или использовать исходное значение.
			pathWithQuery := path + "?" + rawQuery
			path = pathWithQuery // Обновляем path для лога, если есть query
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
