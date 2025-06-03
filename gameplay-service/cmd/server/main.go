// Package main Novel Server Gameplay API
//
//	@title			Novel Server Gameplay API
//	@version		1.0
//	@description	API для игрового сервиса Novel Server - создание, управление и игра в интерактивных историях
//	@termsOfService	http://swagger.io/terms/
//
//	@contact.name	API Support
//	@contact.url	http://www.swagger.io/support
//	@contact.email	support@swagger.io
//
//	@license.name	Apache 2.0
//	@license.url	http://www.apache.org/licenses/LICENSE-2.0.html
//
//	@host		localhost:8080
//	@BasePath	/api/v1
//
//	@securityDefinitions.apikey	BearerAuth
//	@in							header
//	@name						Authorization
//	@description				Type "Bearer" followed by a space and JWT token.
//
//	@securityDefinitions.apikey	InternalAuth
//	@in							header
//	@name						X-Internal-Token
//	@description				Internal service authentication token
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"novel-server/gameplay-service/docs"
	"novel-server/gameplay-service/internal/config"
	"novel-server/gameplay-service/internal/handler"
	"novel-server/gameplay-service/internal/messaging"
	"novel-server/gameplay-service/internal/service"
	sharedDatabase "novel-server/shared/database" // Импорт для репозиториев
	"novel-server/shared/interfaces"
	sharedInterfaces "novel-server/shared/interfaces" // <<< Добавляем импорт shared/interfaces
	sharedLogger "novel-server/shared/logger"         // <<< Добавляем импорт shared/logger
	sharedMiddleware "novel-server/shared/middleware" // <<< Импортируем shared/middleware
	sharedModels "novel-server/shared/models"         // <<< ADDED: Import shared models for rate limiter >>>
	"os"
	"os/signal"
	"syscall"
	"time"

	"novel-server/gameplay-service/internal/clients"

	sharedConfigService "novel-server/shared/configservice" // <<< ДОБАВЛЕНО: Импорт shared configservice >>>
	sharedConsumer "novel-server/shared/messaging/consumer" // <<< ДОБАВЛЕНО: Импорт shared consumer >>>

	"github.com/gin-contrib/cors" // <<< Импортируем Gin CORS
	"github.com/gin-gonic/gin"    // <<< Импортируем Gin
	"github.com/google/uuid"      // <<< ADDED: Import UUID for rate limiter KeyFunc >>>
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	amqp "github.com/rabbitmq/amqp091-go"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap" // Импорт zap

	// <<< Rate Limit Imports >>>
	rateli "github.com/JGLTechnologies/gin-rate-limit"

	// Swagger imports
	_ "novel-server/gameplay-service/docs"

	swaggerFiles "github.com/swaggo/files"
)

func main() {
	_ = godotenv.Load()
	log.Println("Запуск Gameplay Service...")

	// Загрузка конфигурации
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Ошибка загрузки конфигурации: %v", err)
	}
	log.Println("Конфигурация загружена")

	// --- Инициализация логгера (Используем shared/logger) ---
	logger, err := sharedLogger.New(sharedLogger.Config{
		Level:    cfg.LogLevel,
		Encoding: "json", // Или cfg.LogEncoding
	})
	if err != nil {
		log.Fatalf("Не удалось инициализировать логгер: %v", err)
	}
	defer logger.Sync()
	logger.Info("Логгер инициализирован", zap.String("logLevel", cfg.LogLevel))
	// --------------------------

	// Подключение к PostgreSQL
	dbPool, err := setupDatabase(cfg, logger) // Передаем логгер
	if err != nil {
		logger.Fatal("Не удалось подключиться к БД", zap.Error(err))
	}
	defer dbPool.Close()
	logger.Info("Успешное подключение к PostgreSQL")

	// Подключение к RabbitMQ
	rabbitConn, err := connectRabbitMQ(cfg.RabbitMQURL, logger) // Передаем логгер
	if err != nil {
		logger.Fatal("Не удалось подключиться к RabbitMQ", zap.Error(err))
	}
	defer rabbitConn.Close()
	logger.Info("Успешное подключение к RabbitMQ")

	// --- Инициализация репозиториев --- //
	storyConfigRepo := sharedDatabase.NewPgStoryConfigRepository(dbPool, logger)
	publishedRepo := sharedDatabase.NewPgPublishedStoryRepository(dbPool, logger)
	sceneRepo := sharedDatabase.NewPgStorySceneRepository(dbPool, logger)
	playerProgressRepo := sharedDatabase.NewPgPlayerProgressRepository(dbPool, logger)
	playerGameStateRepo := sharedDatabase.NewPgPlayerGameStateRepository(dbPool, logger)
	likeRepo := sharedDatabase.NewPgLikeRepository(dbPool, logger)
	imageReferenceRepo := sharedDatabase.NewPgImageReferenceRepository(dbPool, logger)
	genResultRepo := sharedDatabase.NewPgGenerationResultRepository(dbPool, logger)

	// <<< НОВОЕ: Инициализация репозитория динамических конфигов >>>
	dynamicConfigRepo := sharedDatabase.NewPgDynamicConfigRepository(dbPool, logger)
	// --------------------------------------------------------

	// --- Инициализация паблишеров --- //
	taskPublisher, err := messaging.NewRabbitMQTaskPublisher(rabbitConn, cfg.GenerationTaskQueue)
	if err != nil {
		logger.Fatal("Не удалось создать TaskPublisher", zap.Error(err))
	}
	clientUpdatePublisher, err := messaging.NewRabbitMQClientUpdatePublisher(rabbitConn, cfg.ClientUpdatesQueueName)
	if err != nil {
		logger.Fatal("Не удалось создать ClientUpdatePublisher", zap.Error(err))
	}
	pushPublisher, err := messaging.NewRabbitMQPushNotificationPublisher(rabbitConn, cfg.PushNotificationQueueName)
	if err != nil {
		logger.Fatal("Не удалось создать PushNotificationPublisher", zap.Error(err))
	}
	characterImageTaskPublisher, err := messaging.NewRabbitMQCharacterImageTaskPublisher(rabbitConn, cfg.ImageGeneratorTaskQueue)
	if err != nil {
		logger.Fatal("Не удалось создать CharacterImageTaskPublisher", zap.Error(err))
	}
	characterImageTaskBatchPublisher, err := messaging.NewRabbitMQCharacterImageTaskBatchPublisher(rabbitConn, cfg.ImageGeneratorTaskQueue)
	if err != nil {
		logger.Fatal("Не удалось создать CharacterImageTaskBatchPublisher", zap.Error(err))
	}

	// --- Инициализация HTTP клиента для Auth Service --- //
	authServiceClient := clients.NewHTTPAuthServiceClient(
		cfg.AuthServiceURL,
		cfg.InterServiceSecret,
		logger,
	)
	logger.Info("Auth Service client initialized")

	// <<< НОВОЕ: Инициализация ConfigService >>> // <<< Используем sharedConfigService >>>
	configService, err := sharedConfigService.NewConfigService(dynamicConfigRepo, logger, dbPool)
	if err != nil {
		logger.Fatal("Не удалось инициализировать ConfigService", zap.Error(err))
	}
	logger.Info("ConfigService инициализирован")
	// ------------------------------------------

	// <<< НОВОЕ: Инициализация GameLoopService отдельно >>>
	gameLoopService := service.NewGameLoopService(
		publishedRepo, sceneRepo, playerProgressRepo, playerGameStateRepo,
		taskPublisher,
		storyConfigRepo,
		imageReferenceRepo,
		characterImageTaskBatchPublisher,
		dynamicConfigRepo,
		clientUpdatePublisher,
		logger,
		cfg,
		dbPool,
		characterImageTaskPublisher,
	)
	logger.Info("GameLoopService инициализирован")

	// --- Инициализация сервисов --- //
	gameplayService := service.NewGameplayService(
		storyConfigRepo,
		publishedRepo,
		sceneRepo,
		playerProgressRepo,
		playerGameStateRepo,
		likeRepo,
		imageReferenceRepo,
		dynamicConfigRepo,
		taskPublisher,
		characterImageTaskBatchPublisher,
		clientUpdatePublisher,
		dbPool,
		logger,
		authServiceClient,
		cfg,
		configService,
		gameLoopService, // <<< ПЕРЕДАЕМ СОЗДАННЫЙ GameLoopService
	)

	// <<< Добавляем инициализацию StoryBrowsingService >>>
	storyBrowsingService := service.NewStoryBrowsingService(
		publishedRepo,
		sceneRepo,
		playerProgressRepo,
		playerGameStateRepo,
		likeRepo,
		imageReferenceRepo,
		authServiceClient, // Используем тот же authClient
		logger,
		dbPool, // <<< Передаем пул соединений >>>
	)

	// <<< Добавляем инициализацию LikeService >>>
	likeService := service.NewLikeService(
		publishedRepo,
		playerGameStateRepo, // Убедитесь, что gameStateRepo инициализирован
		authServiceClient,
		logger,
		dbPool, // <<< Передаем пул соединений >>>
	)

	// <<< Rate Limiter Middleware Setup >>>
	// General IP-based rate limiter (100 req/min)
	generalStore := rateli.InMemoryStore(&rateli.InMemoryOptions{
		Rate:  time.Minute,
		Limit: 100,
	})
	generalRateLimitMiddleware := rateli.RateLimiter(generalStore, &rateli.Options{
		ErrorHandler: func(c *gin.Context, info rateli.Info) {
			logger.Warn("General rate limit exceeded", zap.String("clientIP", c.ClientIP()), zap.Time("resetTime", info.ResetTime), zap.String("path", c.Request.URL.Path))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, sharedModels.ErrorResponse{
				Code:    "RATE_LIMIT_EXCEEDED", // Define a proper code if needed
				Message: "Too many requests (general limit). Try again in " + time.Until(info.ResetTime).Round(time.Second).String(),
			})
		},
		KeyFunc: func(c *gin.Context) string {
			return c.ClientIP()
		},
	})
	logger.Info("General IP rate limiter middleware initialized (100 req/min)")

	// User-based rate limiter for generation endpoints (20 req/min)
	// IMPORTANT: This KeyFunc requires the userID to be set in the context by a preceding AuthMiddleware
	generationStore := rateli.InMemoryStore(&rateli.InMemoryOptions{
		Rate:  time.Minute,
		Limit: 20,
	})
	generationRateLimitMiddleware := rateli.RateLimiter(generationStore, &rateli.Options{
		ErrorHandler: func(c *gin.Context, info rateli.Info) {
			userID, _ := c.Get(string(sharedModels.UserContextKey))
			logger.Warn("Generation rate limit exceeded", zap.Any("userID", userID), zap.String("clientIP", c.ClientIP()), zap.Time("resetTime", info.ResetTime), zap.String("path", c.Request.URL.Path))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, sharedModels.ErrorResponse{
				Code:    "RATE_LIMIT_EXCEEDED",
				Message: "Too many generation requests (user limit). Try again in " + time.Until(info.ResetTime).Round(time.Second).String(),
			})
		},
		KeyFunc: func(c *gin.Context) string {
			userIDAny, exists := c.Get(string(sharedModels.UserContextKey))
			if !exists {
				// Fallback to IP if userID is not set (should not happen after auth middleware)
				logger.Warn("UserID not found in context for generation rate limiter, falling back to IP")
				return c.ClientIP()
			}
			userID, ok := userIDAny.(uuid.UUID)
			if !ok {
				logger.Error("Invalid UserID type in context for generation rate limiter", zap.Any("value", userIDAny))
				return c.ClientIP() // Fallback to IP
			}
			return userID.String() // Use UserID as the key
		},
	})
	logger.Info("Generation user rate limiter middleware initialized (20 req/min)")
	// <<< End Rate Limiter Setup >>>

	// --- Инициализация хендлеров --- //
	gameplayHandler := handler.NewGameplayHandler(gameplayService, logger, cfg.JWTSecret, cfg.InterServiceSecret, storyConfigRepo, publishedRepo, cfg, storyBrowsingService, likeService) // <<< Передаем likeService >>>

	// --- Первоначальная проверка зависших задач --- //
	logger.Info("Performing initial check for stuck tasks...")
	markStuckDraftsAsError(storyConfigRepo, 0, logger)
	markStuckPublishedStoriesAsError(publishedRepo, dbPool, 0, logger)
	markStuckPlayerGameStatesAsError(playerGameStateRepo, dbPool, 0, logger)
	logger.Info("Initial check for stuck tasks completed.")

	// --- Запуск периодической проверки зависших задач --- //
	logger.Info("Starting periodic checks for stuck tasks...")
	go func() {
		for {
			markStuckDraftsAsError(storyConfigRepo, 1*time.Hour, logger)
			time.Sleep(1 * time.Hour)
		}
	}()
	go func() {
		for {
			markStuckPublishedStoriesAsError(publishedRepo, dbPool, 1*time.Hour, logger)
			time.Sleep(1 * time.Hour)
		}
	}()
	go func() {
		for {
			markStuckPlayerGameStatesAsError(playerGameStateRepo, dbPool, 30*time.Minute, logger)
			time.Sleep(30 * time.Minute)
		}
	}()

	// --- Инициализация консьюмера уведомлений --- //
	notificationConsumer, err := messaging.NewNotificationConsumer(
		rabbitConn,
		// Зависимости для NotificationProcessor:
		storyConfigRepo,
		publishedRepo,
		sceneRepo,
		playerGameStateRepo,
		playerProgressRepo,
		imageReferenceRepo,
		genResultRepo,
		clientUpdatePublisher,
		taskPublisher,
		pushPublisher,
		characterImageTaskPublisher,
		characterImageTaskBatchPublisher,
		authServiceClient,
		logger,
		// Параметры самого консьюмера:
		cfg.InternalUpdatesQueueName,
		cfg,
		dbPool,          // <<< Добавляем dbPool >>>
		gameLoopService, // <<< ИЗМЕНЕНО: Передаем gameLoopService вместо gameplayService
	)
	if err != nil {
		logger.Fatal("Не удалось создать консьюмер уведомлений", zap.Error(err))
	}
	// Запускаем консьюмер в отдельной горутине
	go func() {
		logger.Info("Запуск горутины консьюмера уведомлений...", zap.String("queue", cfg.InternalUpdatesQueueName))
		if err := notificationConsumer.StartConsuming(); err != nil {
			logger.Error("Консьюмер уведомлений завершился с ошибкой", zap.Error(err))
		}
		logger.Info("Горутина консьюмера уведомлений завершена.")
	}()

	// --- Инициализация второго консьюмера (для результатов Image Generator) --- //
	imageResultConsumer, err := messaging.NewNotificationConsumer(
		rabbitConn,
		// Используем те же зависимости, что и для основного консьюмера
		storyConfigRepo, publishedRepo, sceneRepo, playerGameStateRepo, playerProgressRepo,
		imageReferenceRepo,
		genResultRepo,
		clientUpdatePublisher, taskPublisher, pushPublisher, characterImageTaskPublisher,
		characterImageTaskBatchPublisher,
		authServiceClient,
		logger,
		// Но слушаем другую очередь:
		cfg.ImageGeneratorResultQueue,
		cfg,
		dbPool,          // <<< Добавляем dbPool >>>
		gameLoopService, // <<< ИЗМЕНЕНО: Передаем gameLoopService вместо gameplayService
	)
	if err != nil {
		logger.Fatal("Не удалось создать консьюмер результатов изображений", zap.Error(err))
	}
	go func() {
		logger.Info("Запуск горутины консьюмера результатов изображений...", zap.String("queue", cfg.ImageGeneratorResultQueue))
		if err := imageResultConsumer.StartConsuming(); err != nil {
			logger.Error("Консьюмер результатов изображений завершился с ошибкой", zap.Error(err))
		}
		logger.Info("Горутина консьюмера результатов изображений завершена.")
	}()

	// <<< НОВОЕ: Инициализация консьюмера обновлений конфигурации >>>
	logger.Info("Настройка RabbitMQ консьюмеров...")
	consumerCtx, consumerCancel := context.WithCancel(context.Background())

	// --- ConfigUpdateConsumer для config_updates --- // <<< ИЗМЕНЕНИЕ >>>
	configUpdateConsumer := sharedConsumer.NewConfigUpdateConsumer(
		rabbitConn,    // Используем созданное соединение
		configService, // Передаем конкретный ConfigService
		logger,
	)
	// Запускаем консьюмера (если Start возвращает ошибку)
	// Передаем consumerCtx для управления жизненным циклом
	go func() {
		logger.Info("Запуск горутины ConfigUpdateConsumer...")
		if err := configUpdateConsumer.Start(consumerCtx); err != nil {
			logger.Error("ConfigUpdateConsumer завершился с ошибкой", zap.Error(err))
			consumerCancel() // Отменяем контекст при ошибке, чтобы остановить другие консьюмеры, если нужно
		}
		logger.Info("Горутина ConfigUpdateConsumer завершена.")
	}()

	// --- Настройка Gin --- //
	gin.SetMode(gin.ReleaseMode)
	if cfg.Env == "development" {
		gin.SetMode(gin.DebugMode)
		logger.Info("Running in Development mode")
	} else {
		logger.Info("Running in Release mode")
	}

	router := gin.New()
	router.RedirectTrailingSlash = true // Добавляем автоматическое перенаправление для слешей

	// <<< Используем Gin логгер запросов (предполагаем, что он есть в sharedMiddleware) >>>
	router.Use(sharedMiddleware.GinZapLogger(logger))
	router.Use(gin.Recovery()) // <<< Используем Gin Recovery

	// <<< Настройка Gin CORS Middleware >>>
	corsConfig := cors.DefaultConfig() // Начинаем с дефолтных настроек
	// Удаляем TODO, т.к. теперь берем из конфига
	if cfg.Env == "development" && len(cfg.AllowedOrigins) == 0 {
		// В режиме разработки, если origins не заданы явно, разрешаем все для удобства
		logger.Warn("CORS AllowedOrigins не задан в конфиге, разрешаю '*' для development режима")
		corsConfig.AllowOrigins = []string{"*"}
	} else if len(cfg.AllowedOrigins) > 0 {
		// Если origins заданы в конфиге, используем их
		logger.Info("Используются CORS AllowedOrigins из конфига", zap.Strings("origins", cfg.AllowedOrigins))
		corsConfig.AllowOrigins = cfg.AllowedOrigins
	} else {
		// В production (или если origins пустой массив), не разрешаем никакие origins (кроме необходимых для preflight)
		logger.Warn("CORS AllowedOrigins не задан или пуст в production режиме. CORS будет заблокирован для большинства запросов.")
		corsConfig.AllowOrigins = []string{} // Пустой список, gin-cors правильно это обработает
	}
	// Остальные настройки оставляем
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization"}
	corsConfig.AllowCredentials = true // Если нужны куки или Authorization header
	corsConfig.MaxAge = 12 * time.Hour
	router.Use(cors.New(corsConfig))

	// Apply general rate limiter globally (or to /api/v1 group)
	// router.Use(generalRateLimitMiddleware) // Option 1: Global

	// --- Регистрация маршрутов --- //
	// Мы будем применять middleware непосредственно к группам, а не передавать в RegisterRoutes
	gameplayHandler.RegisterRoutes(router, generalRateLimitMiddleware, generationRateLimitMiddleware) // <<< Pass middleware again >>>

	// Apply rate limiters within RegisterRoutes is better for clarity
	// If RegisterRoutes cannot be modified, apply here:
	/*
		apiV1 := router.Group("/api/v1")
		apiV1.Use(generalRateLimitMiddleware) // General IP limit
		apiV1.Use(gameplayHandler.AuthMiddleware()) // Auth must run before user-based rate limit
		{
			// Apply generation limiter to specific routes
			apiV1.POST("/stories/generate", generationRateLimitMiddleware, gameplayHandler.generateInitialStory)
			apiV1.POST("/stories/:id/revise", generationRateLimitMiddleware, gameplayHandler.reviseStoryConfig)
			apiV1.POST("/stories/drafts/:draft_id/retry", generationRateLimitMiddleware, gameplayHandler.retryDraftGeneration)
			apiV1.POST("/published-stories/:story_id/gamestates/:game_state_id/choice", generationRateLimitMiddleware, gameplayHandler.makeChoice)
			apiV1.POST("/published-stories/:story_id/retry", generationRateLimitMiddleware, gameplayHandler.retryPublishedStoryGeneration)
			apiV1.POST("/published-stories/:story_id/gamestates/:game_state_id/retry", generationRateLimitMiddleware, gameplayHandler.retrySpecificGameStateGeneration)

			// ... other routes without generation limit ...
			apiV1.GET("/stories", gameplayHandler.listStoryConfigs)
			// etc.
		}
		// Internal routes usually don't need these limits
		internal := router.Group("/internal")
		internal.Use(gameplayHandler.InternalAuthMiddleware())
		{
			// ... internal routes ...
		}
	*/

	// --- Регистрация healthcheck эндпоинта для Gin --- //
	healthHandler := func(c *gin.Context) { // <<< Используем gin.Context
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
	router.GET("/health", healthHandler) // <<< Регистрируем на Gin роутере
	router.HEAD("/health", healthHandler)

	// --- Swagger UI --- //
	if cfg.Env == "development" {
		// Swagger UI доступен только в development режиме
		router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
		logger.Info("Swagger UI enabled at /swagger/index.html")
	}

	// --- OpenAPI JSON endpoint (всегда доступен для machine-readable документации) --- //
	router.GET("/api/openapi.json", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.Data(http.StatusOK, "application/json", []byte(docs.SwaggerInfo.ReadDoc()))
	})
	router.GET("/api/openapi.yaml", func(c *gin.Context) {
		c.Header("Content-Type", "application/yaml")
		// Конвертируем JSON в YAML или возвращаем заглушку
		c.String(http.StatusOK, "# OpenAPI YAML not available, use JSON endpoint")
	})

	logger.Info("Gameplay сервер готов к запуску", zap.String("port", cfg.Port))

	// --- Запуск HTTP сервера --- //
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router, // <<< Передаем Gin роутер
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		logger.Info("Запуск HTTP сервера...", zap.String("address", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Ошибка запуска HTTP сервера", zap.Error(err))
		}
	}()

	// --- Graceful shutdown --- //
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("Получен сигнал завершения, начинаем graceful shutdown...")

	// Останавливаем консьюмер
	logger.Info("Остановка консьюмера уведомлений...")
	notificationConsumer.Stop()
	logger.Info("Консьюмер уведомлений остановлен.")

	// <<< Останавливаем второй консьюмер >>>
	logger.Info("Остановка консьюмера результатов изображений...")
	imageResultConsumer.Stop()
	logger.Info("Консьюмер результатов изображений остановлен.")

	// <<< НОВОЕ: Остановка консьюмера обновлений конфигурации >>>
	logger.Info("Остановка консьюмера обновлений конфигурации...")
	consumerCancel() // Сигнал для всех консьюмеров на завершение

	// Останавливаем конкретно ConfigUpdateConsumer
	if err := configUpdateConsumer.Stop(); err != nil {
		logger.Error("Ошибка при остановке ConfigUpdateConsumer", zap.Error(err))
	}
	logger.Info("ConfigUpdateConsumer остановлен.")
	// <<< КОНЕЦ >>>

	// Shutdown HTTP сервера
	logger.Info("Остановка HTTP сервера...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatal("Ошибка при graceful shutdown HTTP сервера", zap.Error(err))
	}

	logger.Info("Gameplay Service успешно остановлен")
}

// markStuckDraftsAsError устанавливает статус Error для зависших ЧЕРНОВИКОВ (StoryConfig).
func markStuckDraftsAsError(repo sharedInterfaces.StoryConfigRepository, staleThreshold time.Duration, logger *zap.Logger) {
	// Небольшая задержка перед проверкой
	time.Sleep(5 * time.Second)
	logger.Info("Checking for stuck draft tasks (StoryConfig) to set Error status...", zap.Duration("staleThreshold", staleThreshold))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second) // Таймаут на всю операцию
	defer cancel()

	updatedCount, err := repo.FindAndMarkStaleGeneratingDraftsAsError(ctx, staleThreshold)
	if err != nil {
		logger.Error("Failed to find and mark stale draft tasks", zap.Error(err))
		return
	}

	if updatedCount == 0 {
		logger.Info("No stuck draft tasks found to set Error status.")
	} else {
		logger.Warn("Set Error status for stuck draft tasks", zap.Int64("count", updatedCount))
	}
}

// markStuckPublishedStoriesAsError устанавливает статус Error для зависших ОПУБЛИКОВАННЫХ историй.
func markStuckPublishedStoriesAsError(repo sharedInterfaces.PublishedStoryRepository, querier interfaces.DBTX, staleThreshold time.Duration, logger *zap.Logger) {
	// Небольшая задержка перед проверкой (чуть больше, чем для черновиков)
	time.Sleep(10 * time.Second)
	logger.Info("Checking for stuck published stories to set Error status...", zap.Duration("staleThreshold", staleThreshold))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second) // Таймаут на всю операцию
	defer cancel()

	updatedCount, err := repo.FindAndMarkStaleGeneratingAsError(ctx, querier, staleThreshold)
	if err != nil {
		logger.Error("Failed to find and mark stale published stories", zap.Error(err))
		return
	}

	if updatedCount == 0 {
		logger.Info("No stuck published stories found to set Error status.")
	} else {
		logger.Warn("Set Error status for stuck published stories", zap.Int64("count", updatedCount))
	}
}

// markStuckPlayerGameStatesAsError устанавливает статус Error для зависших состояний игры игрока.
func markStuckPlayerGameStatesAsError(repo sharedInterfaces.PlayerGameStateRepository, querier interfaces.DBTX, staleThreshold time.Duration, logger *zap.Logger) {
	// Небольшая задержка перед проверкой (еще чуть больше)
	time.Sleep(15 * time.Second)
	logger.Info("Checking for stuck player game states to set Error status...", zap.Duration("staleThreshold", staleThreshold))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second) // Таймаут на всю операцию
	defer cancel()

	updatedCount, err := repo.FindAndMarkStaleGeneratingAsError(ctx, querier, staleThreshold)
	if err != nil {
		logger.Error("Failed to find and mark stale player game states", zap.Error(err))
		return
	}

	if updatedCount == 0 {
		logger.Info("No stuck player game states found to set Error status.")
	} else {
		logger.Warn("Set Error status for stuck player game states", zap.Int64("count", updatedCount))
	}
}

// setupDatabase инициализирует и возвращает пул соединений с БД
func setupDatabase(cfg *config.Config, logger *zap.Logger) (*pgxpool.Pool, error) {
	var dbPool *pgxpool.Pool
	var err error
	maxRetries := 50 // Увеличим количество попыток
	retryDelay := 3 * time.Second

	dsn := cfg.GetDSN()
	poolConfig, parseErr := pgxpool.ParseConfig(dsn)
	if parseErr != nil {
		// Если DSN некорректен, нет смысла пытаться подключаться
		return nil, fmt.Errorf("ошибка парсинга DSN: %w", parseErr)
	}
	poolConfig.MaxConns = int32(cfg.DBMaxConns)
	poolConfig.MaxConnIdleTime = cfg.DBIdleTimeout

	for i := 0; i < maxRetries; i++ {
		attempt := i + 1
		logger.Debug("Попытка подключения к PostgreSQL...",
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", maxRetries),
		)

		// Таймаут на одну попытку подключения и пинга
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		dbPool, err = pgxpool.NewWithConfig(ctx, poolConfig)
		if err != nil {
			logger.Warn("Не удалось создать пул соединений",
				zap.Int("attempt", attempt),
				zap.Error(err),
			)
			cancel()
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
			}
			continue // Переходим к следующей попытке
		}

		// Пытаемся пинговать
		if err = dbPool.Ping(ctx); err != nil {
			logger.Warn("Не удалось выполнить ping к PostgreSQL",
				zap.Int("attempt", attempt),
				zap.Error(err),
			)
			dbPool.Close() // Закрываем неудачный пул
			cancel()
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
			}
			continue // Переходим к следующей попытке
		}

		// Если дошли сюда, подключение и пинг успешны
		cancel() // Отменяем таймаут текущей попытки
		logger.Info("Успешное подключение и ping к PostgreSQL", zap.Int("attempt", attempt))
		return dbPool, nil
	}

	// Если цикл завершился без успешного подключения
	logger.Error("Не удалось подключиться к PostgreSQL после всех попыток", zap.Int("attempts", maxRetries))
	return nil, fmt.Errorf("не удалось подключиться к БД после %d попыток: %w", maxRetries, err) // Возвращаем последнюю ошибку
}

// connectRabbitMQ пытается подключиться к RabbitMQ с несколькими попытками
func connectRabbitMQ(url string, logger *zap.Logger) (*amqp.Connection, error) {
	var conn *amqp.Connection
	var err error
	maxRetries := 50
	retryDelay := 5 * time.Second
	for i := 0; i < maxRetries; i++ {
		conn, err = amqp.Dial(url)
		if err == nil {
			logger.Info("Успешное подключение к RabbitMQ",
				zap.Int("attempt", i+1),
				zap.Int("max_attempts", maxRetries),
			)
			return conn, nil
		}
		logger.Warn("Не удалось подключиться к RabbitMQ",
			zap.Int("attempt", i+1),
			zap.Int("max_attempts", maxRetries),
			zap.Duration("retry_delay", retryDelay),
			zap.Error(err),
		)
		time.Sleep(retryDelay)
	}
	return nil, fmt.Errorf("не удалось подключиться к RabbitMQ после %d попыток: %w", maxRetries, err)
}
