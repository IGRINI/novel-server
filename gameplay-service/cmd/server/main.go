package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"novel-server/gameplay-service/internal/config"
	"novel-server/gameplay-service/internal/handler"
	"novel-server/gameplay-service/internal/messaging"
	"novel-server/gameplay-service/internal/service"
	sharedDatabase "novel-server/shared/database"     // Импорт для репозиториев
	sharedInterfaces "novel-server/shared/interfaces" // <<< Добавляем импорт shared/interfaces
	sharedLogger "novel-server/shared/logger"         // <<< Добавляем импорт shared/logger

	// <<< Добавляем импорт shared/messaging
	sharedMiddleware "novel-server/shared/middleware" // <<< Импортируем shared/middleware
	sharedModels "novel-server/shared/models"
	"os"
	"os/signal"
	"syscall"
	"time"

	"novel-server/gameplay-service/internal/clients"

	"github.com/gin-contrib/cors" // <<< Импортируем Gin CORS
	"github.com/gin-gonic/gin"    // <<< Импортируем Gin
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap" // Импорт zap
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
	// <<< ДОБАВЛЕНО: Создание репозитория для image references >>>
	// Теперь конструктор существует
	imageReferenceRepo := sharedDatabase.NewPgImageReferenceRepository(dbPool, logger) // Используем sharedDatabase

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
	// <<< ДОБАВЛЕНО: Создание паблишера для задач генерации изображений >>>
	// Поле ImageGeneratorTaskQueue добавлено в config
	characterImageTaskPublisher, err := messaging.NewRabbitMQCharacterImageTaskPublisher(rabbitConn, cfg.ImageGeneratorTaskQueue) // Используем очередь из конфига
	if err != nil {
		logger.Fatal("Не удалось создать CharacterImageTaskPublisher", zap.Error(err))
	}
	// <<< ДОБАВЛЕНО: Создание паблишера для БАТЧЕЙ задач генерации изображений >>>
	characterImageTaskBatchPublisher, err := messaging.NewRabbitMQCharacterImageTaskBatchPublisher(rabbitConn, cfg.ImageGeneratorTaskQueue) // Используем ту же очередь
	if err != nil {
		logger.Fatal("Не удалось создать CharacterImageTaskBatchPublisher", zap.Error(err))
	}

	// --- Инициализация HTTP клиента для Auth Service --- //
	authServiceClient := clients.NewHTTPAuthServiceClient(
		cfg.AuthServiceURL,     // URL из конфига
		cfg.InterServiceSecret, // Секрет из конфига
		logger,                 // Логгер
	)
	logger.Info("Auth Service client initialized")

	// --- Инициализация сервисов (ОТКАТ ЛИШНИХ ИЗМЕНЕНИЙ) --- //
	gameplayService := service.NewGameplayService(storyConfigRepo, publishedRepo, sceneRepo, playerProgressRepo, playerGameStateRepo, likeRepo, taskPublisher, dbPool, logger, authServiceClient, cfg)

	// --- Инициализация хендлеров (ОТКАТ ЛИШНИХ ИЗМЕНЕНИЙ) --- //
	gameplayHandler := handler.NewGameplayHandler(gameplayService, logger, cfg.JWTSecret, cfg.InterServiceSecret, storyConfigRepo, publishedRepo, cfg)

	// --- Проверка зависших задач --- //
	go markStuckDraftsAsError(storyConfigRepo, logger)
	go markStuckPublishedStoriesAsError(publishedRepo, 1*time.Hour, logger)
	// <<< ДОБАВЛЕНО: Проверка зависших состояний игры игрока >>>
	go markStuckPlayerGameStatesAsError(playerGameStateRepo, 30*time.Minute, logger)

	// --- Инициализация консьюмера уведомлений --- //
	notificationConsumer, err := messaging.NewNotificationConsumer(
		rabbitConn,
		// Зависимости для NotificationProcessor:
		storyConfigRepo,
		publishedRepo,
		sceneRepo,
		playerGameStateRepo,
		imageReferenceRepo,
		clientUpdatePublisher,
		taskPublisher,
		pushPublisher,
		characterImageTaskPublisher,
		characterImageTaskBatchPublisher,
		logger,
		// Параметры самого консьюмера:
		cfg.InternalUpdatesQueueName, // queueName
		cfg,                          // cfg (для консьюмера и процессора)
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
		storyConfigRepo, publishedRepo, sceneRepo, playerGameStateRepo, imageReferenceRepo,
		clientUpdatePublisher, taskPublisher, pushPublisher, characterImageTaskPublisher,
		characterImageTaskBatchPublisher,
		logger,
		// Но слушаем другую очередь:
		cfg.ImageGeneratorResultQueue, // queueName
		cfg,                           // cfg (для консьюмера и процессора)
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

	// --- Регистрация маршрутов (ОТКАТ ЛИШНИХ ИЗМЕНЕНИЙ) --- //
	gameplayHandler.RegisterRoutes(router) // Передаем Gin роутер

	// --- Регистрация healthcheck эндпоинта для Gin --- //
	healthHandler := func(c *gin.Context) { // <<< Используем gin.Context
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
	router.GET("/health", healthHandler) // <<< Регистрируем на Gin роутере
	router.HEAD("/health", healthHandler)

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

	// Shutdown HTTP сервера
	logger.Info("Остановка HTTP сервера...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatal("Ошибка при graceful shutdown HTTP сервера", zap.Error(err))
	}

	logger.Info("Gameplay Service успешно остановлен")
}

// <<< ПЕРЕИМЕНОВАНО и немного изменено >>>
// markStuckDraftsAsError устанавливает статус Error для зависших ЧЕРНОВИКОВ (StoryConfig).
func markStuckDraftsAsError(repo sharedInterfaces.StoryConfigRepository, logger *zap.Logger) {
	// Небольшая задержка перед проверкой
	time.Sleep(5 * time.Second)
	logger.Info("Checking for stuck draft tasks (StoryConfig) to set Error status...")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Используем существующий метод для поиска 'generating' и 'revising'
	stuckConfigs, err := repo.FindGeneratingConfigs(ctx)
	if err != nil {
		logger.Error("Failed to get list of stuck draft tasks to set Error status", zap.Error(err))
		return
	}

	if len(stuckConfigs) == 0 {
		logger.Info("No stuck draft tasks found to set Error status.")
		return
	}

	logger.Info("Found stuck draft tasks to set Error status", zap.Int("count", len(stuckConfigs)))
	updatedCount := 0
	errorCount := 0
	errorDetails := "Task timed out or got stuck during generation/revision."

	for _, cfg := range stuckConfigs {
		logFields := []zap.Field{
			zap.String("storyConfigID", cfg.ID.String()),
			zap.String("userID", cfg.UserID.String()),
			zap.String("currentStatus", string(cfg.Status)),
		}

		// Проверяем еще раз статус 'generating' (убираем Revising)
		if cfg.Status != sharedModels.StatusGenerating {
			logger.Info("Draft task status changed, skipping update", logFields...)
			continue
		}

		// Используем UpdateStatusAndError, чтобы записать причину
		updateCtx, updateCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := repo.UpdateStatusAndError(updateCtx, cfg.ID, sharedModels.StatusError, errorDetails); err != nil {
			logger.Error("Error setting Error status for stuck draft task", append(logFields, zap.Error(err))...)
			errorCount++
		} else {
			logger.Warn("Set Error status for stuck draft task", logFields...)
			updatedCount++
		}
		updateCancel()
	}

	logger.Info("Finished processing stuck draft tasks",
		zap.Int("totalFound", len(stuckConfigs)),
		zap.Int("updatedToError", updatedCount),
		zap.Int("updateErrors", errorCount),
	)
}

// <<< НОВАЯ ФУНКЦИЯ >>>
// markStuckPublishedStoriesAsError устанавливает статус Error для зависших ОПУБЛИКОВАННЫХ историй.
func markStuckPublishedStoriesAsError(repo sharedInterfaces.PublishedStoryRepository, staleThreshold time.Duration, logger *zap.Logger) {
	// Небольшая задержка перед проверкой (чуть больше, чем для черновиков)
	time.Sleep(10 * time.Second)
	logger.Info("Checking for stuck published stories to set Error status...", zap.Duration("staleThreshold", staleThreshold))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second) // Таймаут на всю операцию
	defer cancel()

	updatedCount, err := repo.FindAndMarkStaleGeneratingAsError(ctx, staleThreshold)
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

// <<< НОВАЯ ФУНКЦИЯ >>>
// markStuckPlayerGameStatesAsError устанавливает статус Error для зависших состояний игры игрока.
func markStuckPlayerGameStatesAsError(repo sharedInterfaces.PlayerGameStateRepository, staleThreshold time.Duration, logger *zap.Logger) {
	// Небольшая задержка перед проверкой (еще чуть больше)
	time.Sleep(15 * time.Second)
	logger.Info("Checking for stuck player game states to set Error status...", zap.Duration("staleThreshold", staleThreshold))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second) // Таймаут на всю операцию
	defer cancel()

	updatedCount, err := repo.FindAndMarkStaleGeneratingAsError(ctx, staleThreshold)
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

// <<< ДОБАВЛЯЮ ФУНКЦИЮ ИЗ STORY-GENERATOR >>>
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

// <<< КОНЕЦ ДОБАВЛЕНИЯ >>>
