package handler

import (
	"novel-server/admin-service/internal/client"
	"novel-server/admin-service/internal/config"
	"novel-server/shared/interfaces"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type AdminHandler struct {
	logger         *zap.Logger
	authClient     client.AuthServiceHttpClient
	storyGenClient client.StoryGeneratorClient
	gameplayClient client.GameplayServiceClient
	pushPublisher  interfaces.PushEventPublisher
}

func NewAdminHandler(
	cfg *config.Config,
	logger *zap.Logger,
	authClient client.AuthServiceHttpClient,
	storyGenClient client.StoryGeneratorClient,
	gameplayClient client.GameplayServiceClient,
	pushPublisher interfaces.PushEventPublisher,
) *AdminHandler {
	return &AdminHandler{
		logger:         logger.Named("AdminHandler"),
		authClient:     authClient,
		storyGenClient: storyGenClient,
		gameplayClient: gameplayClient,
		pushPublisher:  pushPublisher,
	}
}

func (h *AdminHandler) RegisterRoutes(router *gin.Engine) {
	router.GET("/login", h.showLoginPage)
	router.POST("/login", h.handleLogin)

	adminApiGroup := router.Group("/", h.authMiddleware)
	{
		adminApiGroup.GET("/dashboard", h.getDashboardData)
		adminApiGroup.GET("/users", h.listUsers)
		adminApiGroup.GET("/logout", h.handleLogout)

		userGroup := adminApiGroup.Group("/users/:user_id")
		{
			userGroup.POST("/ban", h.handleBanUser)
			userGroup.DELETE("/ban", h.handleUnbanUser)
			userGroup.GET("/edit", h.showUserEditPage)
			userGroup.POST("/", h.handleUserUpdate)
			userGroup.POST("/reset-password", h.handleResetPassword)
			userGroup.GET("/drafts", h.listUserDrafts)
			userGroup.GET("/drafts/:draft_id", h.showDraftDetailsPage)
			userGroup.POST("/drafts/:draft_id", h.handleUpdateDraft)
			userGroup.GET("/stories", h.listUserStories)
			userGroup.GET("/stories/:story_id", h.showPublishedStoryDetailsPage)
			userGroup.POST("/stories/:story_id", h.handleUpdateStory)
			userGroup.POST("/stories/:story_id/scenes/:scene_id", h.handleUpdateScene)
			userGroup.POST("/send-notification", h.handleSendUserNotification)
		}

		aiGroup := adminApiGroup.Group("/ai-playground")
		{
			aiGroup.GET("", h.handleAIPlaygroundPage)
			aiGroup.POST("/generate", h.handleAIPlaygroundGenerate)
		}
	}
}
