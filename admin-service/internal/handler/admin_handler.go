package handler

import (
	"novel-server/admin-service/internal/client"
	"novel-server/admin-service/internal/config"
	"novel-server/admin-service/internal/service"
	"novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models"

	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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
	configHandler  *ConfigHandler
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
	configHandler *ConfigHandler,
) *AdminHandler {
	if promptService == nil {
		logger.Warn("PromptService is nil during AdminHandler initialization")
	}
	if promptHandler == nil {
		logger.Warn("PromptHandler is nil during AdminHandler initialization")
	}
	if configHandler == nil {
		logger.Warn("ConfigHandler is nil during AdminHandler initialization")
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
		configHandler:  configHandler,
	}
}

func (h *AdminHandler) RegisterRoutes(router *gin.Engine) {
	router.GET("/login", h.showLoginPage)
	router.POST("/login", h.handleLogin)

	adminGroup := router.Group("/admin", h.AuthMiddleware)
	{
		adminGroup.GET("/dashboard", h.GetDashboardData)
		adminGroup.GET("/users", h.listUsers)
		adminGroup.GET("/logout", h.handleLogout)

		userGroup := adminGroup.Group("/users/:user_id")
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

		aiGroup := adminGroup.Group("/ai-playground")
		{
			aiGroup.GET("", h.handleAIPlaygroundPage)
			aiGroup.POST("/generate", h.handleAIPlaygroundGenerate)
		}

		if h.promptHandler != nil {
			h.promptHandler.RegisterPromptRoutes(adminGroup)
			h.logger.Info("Зарегистрированы роуты для PromptHandler в группе /admin/prompts")
		} else {
			h.logger.Warn("PromptHandler не инициализирован, роуты для промптов не зарегистрированы.")
		}

		if h.configHandler != nil {
			h.configHandler.RegisterConfigRoutes(adminGroup)
			h.logger.Info("Зарегистрированы роуты для ConfigHandler в группе /admin/configs")
		} else {
			h.logger.Warn("ConfigHandler не инициализирован, роуты для конфигов не зарегистрированы.")
		}
	}
}

func (h *AdminHandler) AuthMiddleware(c *gin.Context) {
	h.authMiddleware(c) // Вызываем реализацию из middleware.go
}

func (h *AdminHandler) GetDashboardData(c *gin.Context) {
	// Получаем данные из контекста Gin (установлены в middleware)
	rawUserID, userOK := c.Get(string(sharedModels.UserContextKey))
	rawRoles, rolesOK := c.Get(string(sharedModels.RolesContextKey))

	var userID uuid.UUID // <<< Изменен тип на uuid.UUID
	var roles []string
	if userOK {
		// Необходимо преобразовать тип, т.к. c.Get возвращает interface{}
		var typeOK bool
		userID, typeOK = rawUserID.(uuid.UUID) // <<< Присваиваем userID после преобразования
		if !typeOK {
			// Логируем ошибку, если тип в контексте не uuid.UUID
			h.logger.Error("Invalid type for user ID in context", zap.Any("rawUserID", rawUserID))
			// Можно прервать выполнение или установить userID в uuid.Nil
			userID = uuid.Nil
			userOK = false // Считаем, что пользователя не получили
		}
	}
	if rolesOK {
		roles = rawRoles.([]string)
	}

	// Используем userID.String() для логирования
	h.logger.Info("Admin dashboard requested", zap.String("adminUserID", userID.String()), zap.Strings("roles", roles))

	// <<< Получаем имя пользователя >>>
	var welcomeMessage string
	var userInfo *sharedModels.User // <<< ИСПОЛЬЗУЕМ models.User >>>
	var userFetchErr error
	if userOK {
		userInfo, userFetchErr = h.authClient.GetUserInfo(c.Request.Context(), userID) // Вызываем новый метод клиента
		if userFetchErr != nil {
			h.logger.Error("Failed to get user info for dashboard greeting", zap.Error(userFetchErr))
			welcomeMessage = fmt.Sprintf("Добро пожаловать, User %s! (Ошибка получения имени)", userID.String())
		} else if userInfo != nil {
			// <<< ПРЕДПОЛАГАЕМ поле Username, т.к. структуру User не видели >>>
			// Если есть DisplayName, использовать его: userInfo.DisplayName
			welcomeMessage = fmt.Sprintf("Добро пожаловать, %s!", userInfo.Username)
		} else {
			welcomeMessage = fmt.Sprintf("Добро пожаловать, User %s!", userID.String()) // На случай, если userInfo nil без ошибки
		}
	} else {
		welcomeMessage = "Добро пожаловать! (Не удалось определить пользователя)"
	}

	h.logger.Debug("Attempting to get user count via auth-service")
	startTime := time.Now()
	// Используем c.Request.Context()
	userCount, err := h.authClient.GetUserCount(c.Request.Context())
	duration := time.Since(startTime)
	h.logger.Debug("Finished getting user count via auth-service", zap.Duration("duration", duration), zap.Error(err))
	if err != nil {
		h.logger.Error("Failed to get user count from auth-service", zap.Error(err))
		userCount = -1 // Используем -1 как индикатор ошибки
	}

	// <<< ИЗМЕНЕНО: Получаем количество активных историй >>>
	h.logger.Debug("Attempting to get active story count via gameplay-service")
	activeStoriesStartTime := time.Now()
	activeStories, activeStoriesErr := h.gameplayClient.GetActiveStoryCount(c.Request.Context())
	activeStoriesDuration := time.Since(activeStoriesStartTime)
	h.logger.Debug("Finished getting active story count via gameplay-service", zap.Duration("duration", activeStoriesDuration), zap.Error(activeStoriesErr))

	if activeStoriesErr != nil {
		h.logger.Error("Failed to get active story count from gameplay-service", zap.Error(activeStoriesErr))
		activeStories = -1 // Используем -1 как индикатор ошибки
	}
	// <<< КОНЕЦ ИЗМЕНЕНИЯ >>>

	data := gin.H{ // <<< gin.H
		"PageTitle": "Дашборд",
		// Используем userID.String() в сообщении
		"WelcomeMessage": welcomeMessage, // Используем сформированное сообщение
		"UserRoles":      roles,
		"Stats": map[string]int{
			"totalUsers":    userCount,
			"activeStories": activeStories,
		},
		"UserCountError": err != nil,
		"IsLoggedIn":     true, // Предполагаем, что middleware гарантирует это
	}
	// Используем c.HTML
	c.HTML(http.StatusOK, "dashboard.html", data)
}
