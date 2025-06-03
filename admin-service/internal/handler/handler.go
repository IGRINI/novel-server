package handler

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	// Предполагаемые импорты, которые могут понадобиться для registerUserRoutes, если он останется
	// "novel-server/admin-service/internal/auth"
	// "novel-server/admin-service/internal/service"
	// "novel-server/admin-service/web"
)

// Handler -.
type Handler struct {
	logger *zap.Logger
	// Возможно, здесь нужны другие зависимости, но пока оставляем так
}

// NewHandler -.
func NewHandler(logger *zap.Logger /* другие зависимости */) *Handler {
	return &Handler{
		logger: logger.Named("Handler"),
		// инициализация других зависимостей
	}
}

func (h *Handler) RegisterRoutes(api *gin.RouterGroup) {
	// h.registerUserRoutes(api) // Пока закомментировано, т.к. метод не определен
	// Здесь должна быть регистрация маршрутов, связанных с этим обработчиком
	// Например, health check:
	api.GET("/health", h.healthCheck)
}

// @Summary Проверка состояния сервиса
// @Description Возвращает статус работы Admin Service
// @Tags health
// @Produce json
// @Success 200 {object} map[string]interface{} "Сервис работает"
// @Router /health [get]
func (h *Handler) healthCheck(c *gin.Context) {
	c.JSON(200, gin.H{"status": "ok"})
}

// Место для других методов обработчика, например, registerUserRoutes, если он тут нужен
