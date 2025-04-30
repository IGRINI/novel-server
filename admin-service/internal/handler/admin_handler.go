package handler

import (
	"novel-server/admin-service/internal/client"
	"novel-server/admin-service/internal/config"
	"novel-server/admin-service/internal/service"
	"novel-server/shared/database"
	"novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models"

	"errors"
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
	promptService  service.PromptService
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
	promptService service.PromptService,
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
	// Публичные роуты (без middleware)
	router.GET("/login", h.showLoginPage)
	router.POST("/login", h.handleLogin)

	// Группа для защищенных роутов админки (префикс /admin удаляется Traefik)
	adminGroup := router.Group("/", h.AuthMiddleware) // <<< ВОЗВРАЩАЕМ: Базовый путь теперь "/"
	{
		adminGroup.GET("/dashboard", h.GetDashboardData)
		adminGroup.GET("/users", h.listUsers)
		adminGroup.GET("/logout", h.handleLogout)

		// --- Frontend API Endpoints ---
		// Эти эндпоинты вызываются JavaScript'ом из админки
		frontendApiGroup := adminGroup.Group("/api")
		{
			// Промпты
			frontendApiGroup.GET("/prompts/:key/:language", h.handleGetPromptAPI)           // Получение одного промпта (из JS)
			frontendApiGroup.POST("/prompts", h.handleUpsertPromptAPI)                      // Создание/Обновление (из JS)
			frontendApiGroup.DELETE("/prompts/:key/:language", h.handleDeletePromptLangAPI) // Удаление языка (из JS)
			// Конфиги (если нужно будет редактировать через JS)
			// frontendApiGroup.POST("/configs", h.handleUpsertConfigAPI)
			// frontendApiGroup.DELETE("/configs/:key", h.handleDeleteConfigAPI)
		}
		h.logger.Info("Зарегистрированы Frontend API эндпоинты в группе /api")

		// Подгруппа для пользователей -> /users/:user_id
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

		// Подгруппа для AI Playground -> /ai-playground
		aiGroup := adminGroup.Group("/ai-playground")
		{
			aiGroup.GET("", h.handleAIPlaygroundPage)
			aiGroup.POST("/generate", h.handleAIPlaygroundGenerate)
		}

		// Регистрация роутов для PromptHandler (HTML страницы) -> /prompts
		if h.promptHandler != nil {
			h.promptHandler.RegisterPromptRoutes(adminGroup)
			h.logger.Info("Зарегистрированы роуты для PromptHandler (HTML) в группе /prompts") // <<< Изменено сообщение
		} else {
			h.logger.Warn("PromptHandler не инициализирован, HTML роуты для промптов не зарегистрированы.")
		}

		// Регистрация роутов для ConfigHandler (HTML страницы) -> /configs
		if h.configHandler != nil {
			h.configHandler.RegisterConfigRoutes(adminGroup)
			h.logger.Info("Зарегистрированы роуты для ConfigHandler (HTML) в группе /configs") // <<< Изменено сообщение
		} else {
			h.logger.Warn("ConfigHandler не инициализирован, HTML роуты для конфигов не зарегистрированы.")
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

// --- Новые обработчики для Frontend API ---

// handleUpsertPromptAPI обрабатывает POST запросы от JS для создания/обновления промпта.
// POST /api/prompts (защищено AuthMiddleware)
func (h *AdminHandler) handleUpsertPromptAPI(c *gin.Context) {
	var req UpsertPromptRequest // Используем ту же структуру, что и в ApiHandler
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Error("Invalid request body for upsert prompt API", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}

	if h.promptService == nil {
		h.logger.Error("PromptService is not initialized in AdminHandler", zap.String("key", req.Key), zap.String("language", req.Language))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error: prompt service unavailable"})
		return
	}

	prompt, err := h.promptService.UpsertPrompt(c.Request.Context(), req.Key, req.Language, req.Content)
	if err != nil {
		h.logger.Error("Failed to upsert prompt via API", zap.Error(err), zap.String("key", req.Key), zap.String("language", req.Language))
		// Ошибку конфликта не ожидаем при Upsert, но на всякий случай
		if errors.Is(err, database.ErrPromptAlreadyExists) { // Используем errors.Is и database.ErrPromptAlreadyExists
			c.JSON(http.StatusConflict, gin.H{"error": "Conflict during upsert (unexpected)"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save prompt"})
		}
		return
	}

	status := http.StatusOK
	// Проверяем, был ли промпт только что создан
	if prompt.CreatedAt.Equal(prompt.UpdatedAt) {
		status = http.StatusCreated
	}

	c.JSON(status, prompt) // Возвращаем созданный/обновленный промпт
}

// handleDeletePromptLangAPI обрабатывает DELETE запросы от JS для удаления языковой версии промпта.
// DELETE /api/prompts/:key/:language (защищено AuthMiddleware)
func (h *AdminHandler) handleDeletePromptLangAPI(c *gin.Context) {
	key := c.Param("key")
	language := c.Param("language")

	if key == "" || language == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Key and language parameters are required"})
		return
	}

	if h.promptService == nil {
		h.logger.Error("PromptService is not initialized in AdminHandler", zap.String("key", key), zap.String("language", language))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error: prompt service unavailable"})
		return
	}

	err := h.promptService.DeletePromptByKeyAndLang(c.Request.Context(), key, language)
	if err != nil {
		// Ошибка "не найдено" обрабатывается в сервисе как успех (nil), поэтому здесь только 500
		h.logger.Error("Failed to delete prompt by key and language via API", zap.Error(err), zap.String("key", key), zap.String("language", language))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete prompt language"})
		return
	}

	c.Status(http.StatusNoContent) // Успешное удаление
}

// handleGetPromptAPI обрабатывает GET запросы от JS для получения одной языковой версии промпта.
// GET /api/prompts/:key/:language (защищено AuthMiddleware)
func (h *AdminHandler) handleGetPromptAPI(c *gin.Context) {
	key := c.Param("key")
	language := c.Param("language")

	if key == "" || language == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Key and language parameters are required"})
		return
	}

	if h.promptService == nil {
		h.logger.Error("PromptService is not initialized in AdminHandler", zap.String("key", key), zap.String("language", language))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error: prompt service unavailable"})
		return
	}

	prompt, err := h.promptService.GetPrompt(c.Request.Context(), key, language)
	if err != nil {
		// Ошибка "не найдено" - это ожидаемый сценарий для JS, возвращаем 404
		if errors.Is(err, database.ErrPromptNotFound) {
			h.logger.Debug("Prompt not found for API get", zap.String("key", key), zap.String("language", language))
			c.JSON(http.StatusNotFound, gin.H{"error": "Prompt not found"})
		} else {
			// Другие ошибки - это 500
			h.logger.Error("Failed to get prompt via API", zap.Error(err), zap.String("key", key), zap.String("language", language))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get prompt"})
		}
		return
	}

	c.JSON(http.StatusOK, prompt) // Возвращаем найденный промпт
}
