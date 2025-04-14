package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"novel-server/gameplay-service/internal/config"
	"novel-server/gameplay-service/internal/handler"
	"novel-server/gameplay-service/internal/messaging"
	"novel-server/gameplay-service/internal/repository"
	"novel-server/gameplay-service/internal/service"
	sharedDatabase "novel-server/shared/database" // Импорт для PublishedStoryRepository
	sharedLogger "novel-server/shared/logger"   // <<< Импортируем общий логгер
	sharedMiddleware "novel-server/shared/middleware" // <<< Импортируем shared/middleware
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/labstack/echo/v4"
	echoMiddleware "github.com/labstack/echo/v4/middleware"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap" // Импорт zap
)

func main() {
	_ = godotenv.Load()
	log.Println("Запуск Gameplay Service...")

	// <<< Загружаем конфиг ДО инициализации логгера
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Ошибка загрузки конфигурации: %v", err) // Используем стандартный логгер, т.к. zap еще нет
	}

	// --- Инициализация логгера (Используем shared/logger) ---
	logger, err := sharedLogger.New(sharedLogger.Config{
		Level: cfg.LogLevel, // <<< Берем уровень из конфига
	})
	if err != nil {
		log.Fatalf("Не удалось инициализировать логгер: %v", err)
	}
	defer logger.Sync() // Flush буфера логгера при выходе
	logger.Info("Logger initialized", zap.String("logLevel", cfg.LogLevel))
	// --------------------------

	// Убираем повторную загрузку конфига
	// cfg, err := config.LoadConfig()
	// if err != nil {
	// 	logger.Fatal("Ошибка загрузки конфигурации", zap.Error(err))
	// }

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

	// Создаем отдельный канал для TaskPublisher
	pubTaskChannel, err := rabbitConn.Channel()
	if err != nil {
		logger.Fatal("Не удалось открыть канал RabbitMQ для TaskPublisher", zap.Error(err))
	}
	defer pubTaskChannel.Close()

	// Создаем отдельный канал для ClientUpdatePublisher
	pubClientUpdateChannel, err := rabbitConn.Channel()
	if err != nil {
		logger.Fatal("Не удалось открыть канал RabbitMQ для ClientUpdatePublisher", zap.Error(err))
	}
	defer pubClientUpdateChannel.Close()

	// Инициализация зависимостей
	// Передаем logger, он будет использован внутри через .Named()
	storyConfigRepo := repository.NewPgStoryConfigRepository(dbPool, logger)
	publishedRepo := sharedDatabase.NewPgPublishedStoryRepository(dbPool, logger)
	sceneRepo := sharedDatabase.NewPgStorySceneRepository(dbPool, logger)
	playerProgressRepo := sharedDatabase.NewPgPlayerProgressRepository(dbPool, logger)

	taskPublisher, err := messaging.NewRabbitMQTaskPublisher(rabbitConn, cfg.GenerationTaskQueue)
	if err != nil {
		logger.Fatal("Не удалось создать TaskPublisher", zap.Error(err))
	}
	clientUpdatePublisher, err := messaging.NewRabbitMQClientUpdatePublisher(rabbitConn, cfg.ClientUpdatesQueueName)
	if err != nil {
		logger.Fatal("Не удалось создать ClientUpdatePublisher", zap.Error(err))
	}
	gameplayService := service.NewGameplayService(storyConfigRepo, publishedRepo, sceneRepo, playerProgressRepo, taskPublisher, dbPool, logger)
	gameplayHandler := handler.NewGameplayHandler(gameplayService, logger, cfg.JWTSecret)

	// Инициализация консьюмера уведомлений
	notificationConsumer, err := messaging.NewNotificationConsumer(
		rabbitConn,
		storyConfigRepo,
		publishedRepo,
		sceneRepo,
		clientUpdatePublisher,
		taskPublisher,
		cfg.InternalUpdatesQueueName,
	)
	if err != nil {
		logger.Fatal("Не удалось создать консьюмер уведомлений", zap.Error(err))
	}
	// Запускаем консьюмер в отдельной горутине
	go func() {
		logger.Info("Запуск горутины консьюмера уведомлений...")
		if err := notificationConsumer.StartConsuming(); err != nil {
			logger.Error("Консьюмер уведомлений завершился с ошибкой", zap.Error(err))
		}
		logger.Info("Горутина консьюмера уведомлений завершена.")
	}()

	// Настройка Echo
	e := echo.New()
	// <<< Используем общий логгер запросов из shared/middleware
	e.Use(sharedMiddleware.EchoZapLogger(logger))
	e.Use(echoMiddleware.Recover())
	e.Use(echoMiddleware.CORSWithConfig(echoMiddleware.CORSConfig{ // TODO: Настроить CORS
		AllowOrigins: []string{"*"},
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions},
		AllowHeaders: []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAccept, echo.HeaderAuthorization},
	}))

	// Регистрация маршрутов
	gameplayHandler.RegisterRoutes(e)

	log.Printf("Gameplay сервер слушает на порту %s", cfg.Port)

	// Запуск HTTP сервера
	go func() {
		if err := e.Start(":" + cfg.Port); err != nil && err != http.ErrServerClosed {
			e.Logger.Fatal("Ошибка запуска HTTP сервера: ", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("Получен сигнал завершения, начинаем graceful shutdown...")

	// Останавливаем консьюмер
	notificationConsumer.Stop()

	// Shutdown Echo
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(ctx); err != nil {
		e.Logger.Fatal("Ошибка при graceful shutdown Echo: ", err)
	}

	log.Println("Gameplay Service успешно остановлен")
}

// setupDatabase инициализирует и возвращает пул соединений с БД
func setupDatabase(cfg *config.Config, logger *zap.Logger) (*pgxpool.Pool, error) { // Добавляем логгер
	// ... (реализация без изменений, как в других сервисах) ...
	dsn := cfg.GetDSN()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("ошибка парсинга DSN: %w", err)
	}
	config.MaxConns = int32(cfg.DBMaxConns)
	config.MaxConnIdleTime = cfg.DBIdleTimeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dbPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать пул соединений: %w", err)
	}
	if err = dbPool.Ping(ctx); err != nil {
		dbPool.Close()
		return nil, fmt.Errorf("не удалось подключиться к БД (ping failed): %w", err)
	}
	return dbPool, nil
}

// connectRabbitMQ пытается подключиться к RabbitMQ с несколькими попытками
func connectRabbitMQ(url string, logger *zap.Logger) (*amqp.Connection, error) { // Добавляем логгер
	// ... (реализация без изменений, как в других сервисах) ...
	var conn *amqp.Connection
	var err error
	maxRetries := 5
	retryDelay := 5 * time.Second
	for i := 0; i < maxRetries; i++ {
		conn, err = amqp.Dial(url)
		if err == nil {
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
	return nil, err
}
