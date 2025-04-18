package handler

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// CustomErrorMiddleware создает Gin middleware для кастомной обработки ошибок.
// Логирует ошибки сервера и отдает кастомную страницу 404.
func CustomErrorMiddleware(logger *zap.Logger, custom404Path string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Сначала выполняем обработчики
		c.Next()

		// Проверяем наличие ошибок, добавленных обработчиками
		if len(c.Errors) > 0 {
			for _, ginErr := range c.Errors {
				// Логируем все ошибки, попавшие в c.Errors
				logger.Error("Handler error",
					zap.Error(ginErr.Err),
					zap.String("meta", ginErr.Meta.(string)), // Предполагаем, что Meta - строка
					zap.Int("type", int(ginErr.Type)),        // Используем zap.Int и кастуем тип
					zap.String("path", c.Request.URL.Path),
					zap.String("method", c.Request.Method),
				)
			}
			// Если ошибки были, но ответ еще не отправлен, отправляем стандартный 500
			if !c.Writer.Written() {
				c.String(http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError))
			}
			return // Не продолжаем обработку, если были ошибки
		}

		// Если ошибок не было, проверяем статус ответа
		status := c.Writer.Status()

		// Обработка 404 - Отдаем кастомную страницу
		if status == http.StatusNotFound && !c.Writer.Written() {
			content, readErr := os.ReadFile(custom404Path)
			if readErr != nil {
				logger.Error("Could not read custom 404 page", zap.Error(readErr), zap.String("path", custom404Path))
				// Отдаем стандартный 404, если кастомный не прочитался
				c.String(http.StatusNotFound, http.StatusText(http.StatusNotFound))
			} else {
				c.Data(http.StatusNotFound, "text/html; charset=utf-8", content)
			}
			return // Прекращаем обработку
		}

		// Логирование 5xx ошибок (если они не попали в c.Errors)
		if status >= http.StatusInternalServerError {
			// Мы не знаем точную ошибку здесь, только статус. Основные ошибки должны логироваться выше.
			logger.Warn("Request resulted in server error status",
				zap.Int("status", status),
				zap.String("path", c.Request.URL.Path),
				zap.String("method", c.Request.Method),
			)
		}

		// Для других статусов ничего не делаем, Gin обработает их стандартно
	}
}
