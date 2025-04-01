package api

import (
	"net/http"
	"novel-server/internal/api/novel_handlers"
	"novel-server/internal/service"
)

// NewNovelHandler создает новый экземпляр обработчика
func NewNovelHandler(novelService *service.NovelService, novelContentService *service.NovelContentService) *novel_handlers.NovelHandler {
	return novel_handlers.NewNovelHandler(novelService, novelContentService)
}

// RegisterHandlers регистрирует все обработчики API на указанном мультиплексоре
func RegisterHandlers(mux *http.ServeMux, novelService *service.NovelService, novelContentService *service.NovelContentService, basePath string) {
	// Создаем обработчик для новелл
	novelHandler := novel_handlers.NewNovelHandler(novelService, novelContentService)

	// Регистрируем маршруты для обработчика новелл
	novelHandler.RegisterRoutes(mux, basePath)
}

// AuthMiddleware проверяет JWT токен и добавляет UserID в контекст
func AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return novel_handlers.AuthMiddleware(next)
}
