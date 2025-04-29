package handler

import (
	"novel-server/admin-service/internal/client"
	"novel-server/admin-service/internal/config"
	"novel-server/admin-service/internal/service"
	"novel-server/shared/interfaces"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type AdminHandler struct {
	logger         *zap.Logger
	cfg            *config.Config
	authClient     client.AuthServiceHttpClient
	storyGenClient client.StoryGeneratorClient
	gameplayClient client.GameplayServiceClient
	pushPublisher  interfaces.PushEventPublisher
	promptService  *service.PromptService
	promptHandler  *PromptHandler
}

func NewAdminHandler(
	cfg *config.Config,
	logger *zap.Logger,
	authClient client.AuthServiceHttpClient,
	storyGenClient client.StoryGeneratorClient,
	gameplayClient client.GameplayServiceClient,
	pushPublisher interfaces.PushEventPublisher,
	promptService *service.PromptService,
	promptHandler *PromptHandler,
) *AdminHandler {
	if promptService == nil {
		logger.Warn("PromptService is nil during AdminHandler initialization")
	}
	if promptHandler == nil {
		logger.Warn("PromptHandler is nil during AdminHandler initialization")
	}
	if cfg == nil {
		logger.Fatal("Config is nil during AdminHandler initialization")
	}
	return &AdminHandler{
		logger:         logger.Named("AdminHandler"),
		cfg:            cfg,
		authClient:     authClient,
		storyGenClient: storyGenClient,
		gameplayClient: gameplayClient,
		pushPublisher:  pushPublisher,
		promptService:  promptService,
		promptHandler:  promptHandler,
	}
}

func (h *AdminHandler) RegisterRoutes(router *gin.Engine) {
	router.GET("/login", h.showLoginPage)
	router.POST("/login", h.handleLogin)

	adminApiGroup := router.Group("/", h.AuthMiddleware)
	{
		adminApiGroup.GET("/dashboard", h.GetDashboardData)
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
			storyGroup := userGroup.Group("/stories/:story_id")
			{
				storyGroup.GET("", h.showPublishedStoryDetailsPage)
				storyGroup.POST("", h.handleUpdateStory)
				storyGroup.POST("/scenes/:scene_id", h.handleUpdateScene)
				storyGroup.POST("/scenes/:scene_id/delete", h.handleDeleteScene)
				storyGroup.GET("/progress/:progress_id/edit", h.showEditPlayerProgressPage)
				storyGroup.POST("/progress/:progress_id", h.handleUpdatePlayerProgress)
			}
			userGroup.POST("/send-notification", h.handleSendUserNotification)
		}

		aiGroup := adminApiGroup.Group("/ai-playground")
		{
			aiGroup.GET("", h.handleAIPlaygroundPage)
			aiGroup.POST("/generate", h.handleAIPlaygroundGenerate)
		}

		// <<< НОВОЕ: Регистрация роутов для нового PromptHandler >>>
		if h.promptHandler != nil {
			promptHandlerGroup := adminApiGroup.Group("/prompts")
			{
				promptHandlerGroup.GET("", h.promptHandler.ShowPrompts)                 // GET /admin/prompts
				promptHandlerGroup.GET("/new", h.promptHandler.ShowCreatePromptForm)    // GET /admin/prompts/new
				promptHandlerGroup.POST("", h.promptHandler.CreatePrompt)               // POST /admin/prompts
				promptHandlerGroup.GET("/:id/edit", h.promptHandler.ShowEditPromptForm) // GET /admin/prompts/:id/edit
				// Используем POST для обновления и удаления, т.к. форма HTML отправляет POST
				promptHandlerGroup.POST("/:id", h.promptHandler.UpdatePrompt)        // POST /admin/prompts/:id (Update)
				promptHandlerGroup.POST("/:id/delete", h.promptHandler.DeletePrompt) // POST /admin/prompts/:id/delete (Delete)
			}
			h.logger.Info("Зарегистрированы роуты для PromptHandler в группе /admin/prompts")
		} else {
			h.logger.Warn("PromptHandler не инициализирован, роуты для промптов не зарегистрированы.")
		}
	}
}

func (h *AdminHandler) AuthMiddleware(c *gin.Context) {
	// ... (код middleware)
}

func (h *AdminHandler) GetDashboardData(c *gin.Context) {
	// ... (код обработчика)
}
