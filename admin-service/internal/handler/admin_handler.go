package handler

import (
	"novel-server/admin-service/internal/client"
	"novel-server/admin-service/internal/config"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

type AdminHandler struct {
	logger         *zap.Logger
	authClient     client.AuthServiceHttpClient
	storyGenClient client.StoryGeneratorClient
	gameplayClient client.GameplayServiceClient
}

func NewAdminHandler(
	cfg *config.Config,
	logger *zap.Logger,
	authClient client.AuthServiceHttpClient,
	storyGenClient client.StoryGeneratorClient,
	gameplayClient client.GameplayServiceClient,
) *AdminHandler {
	return &AdminHandler{
		logger:         logger.Named("AdminHandler"),
		authClient:     authClient,
		storyGenClient: storyGenClient,
		gameplayClient: gameplayClient,
	}
}

func (h *AdminHandler) RegisterRoutes(e *echo.Echo) {
	e.GET("/login", h.showLoginPage)
	e.POST("/login", h.handleLogin)

	adminApiGroup := e.Group("", h.authMiddleware)
	adminApiGroup.GET("/dashboard", h.getDashboardData)
	adminApiGroup.GET("/users", h.listUsers)
	adminApiGroup.GET("/logout", h.handleLogout)
	adminApiGroup.POST("/users/:user_id/ban", h.handleBanUser)
	adminApiGroup.DELETE("/users/:user_id/ban", h.handleUnbanUser)
	adminApiGroup.GET("/users/:user_id/edit", h.showUserEditPage)
	adminApiGroup.POST("/users/:user_id", h.handleUserUpdate)
	adminApiGroup.POST("/users/:user_id/reset-password", h.handleResetPassword)
	adminApiGroup.GET("/users/:user_id/drafts", h.listUserDrafts)
	adminApiGroup.GET("/users/:user_id/stories", h.listUserStories)
	adminApiGroup.GET("/ai-playground", h.handleAIPlaygroundPage)
	adminApiGroup.POST("/ai-playground/generate", h.handleAIPlaygroundGenerate)
}
