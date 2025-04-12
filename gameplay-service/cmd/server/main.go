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
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/labstack/echo/v4"
	echoMiddleware "github.com/labstack/echo/v4/middleware"
	amqp "github.com/rabbitmq/amqp091-go"
)

func main() {
	_ = godotenv.Load()
	log.Println("Запуск Gameplay Service...")

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Ошибка загрузки конфигурации: %v", err)
	}

	// Подключение к PostgreSQL
	dbPool, err := setupDatabase(cfg)
	if err != nil {
		log.Fatalf("Не удалось подключиться к БД: %v", err)
	}
	defer dbPool.Close()
	log.Println("Успешное подключение к PostgreSQL")

	// Подключение к RabbitMQ
	rabbitConn, err := connectRabbitMQ(cfg.RabbitMQURL)
	if err != nil {
		log.Fatalf("Не удалось подключиться к RabbitMQ: %v", err)
	}
	defer rabbitConn.Close()
	log.Println("Успешное подключение к RabbitMQ")

	// Создаем отдельный канал для TaskPublisher
	pubTaskChannel, err := rabbitConn.Channel()
	if err != nil {
		log.Fatalf("Не удалось открыть канал RabbitMQ для TaskPublisher: %v", err)
	}
	defer pubTaskChannel.Close()

	// Создаем отдельный канал для ClientUpdatePublisher
	pubClientUpdateChannel, err := rabbitConn.Channel()
	if err != nil {
		log.Fatalf("Не удалось открыть канал RabbitMQ для ClientUpdatePublisher: %v", err)
	}
	defer pubClientUpdateChannel.Close()

	// Инициализация зависимостей
	storyConfigRepo := repository.NewPostgresStoryConfigRepository(dbPool)
	taskPublisher := messaging.NewRabbitMQPublisher(pubTaskChannel, cfg.GenerationTaskQueue)
	clientUpdatePublisher, err := messaging.NewRabbitMQClientUpdatePublisher(rabbitConn, cfg.ClientUpdatesQueueName)
	if err != nil {
		log.Fatalf("Не удалось создать ClientUpdatePublisher: %v", err)
	}
	gameplayService := service.NewGameplayService(storyConfigRepo, taskPublisher)
	gameplayHandler := handler.NewGameplayHandler(gameplayService)

	// Инициализация консьюмера уведомлений
	notificationConsumer, err := messaging.NewNotificationConsumer(
		rabbitConn,
		storyConfigRepo,
		clientUpdatePublisher,
		cfg.InternalUpdatesQueueName, // Слушаем очередь internal_updates
	)
	if err != nil {
		log.Fatalf("Не удалось создать консьюмер уведомлений: %v", err)
	}
	// Запускаем консьюмер в отдельной горутине
	go func() {
		log.Println("Запуск горутины консьюмера уведомлений...")
		if err := notificationConsumer.StartConsuming(); err != nil {
			// В реальном приложении здесь нужна более надежная обработка ошибок
			log.Printf("Консьюмер уведомлений завершился с ошибкой: %v", err)
		}
		log.Println("Горутина консьюмера уведомлений завершена.")
	}()

	// Настройка Echo
	e := echo.New()
	e.Use(echoMiddleware.Logger())
	e.Use(echoMiddleware.Recover())
	e.Use(echoMiddleware.CORSWithConfig(echoMiddleware.CORSConfig{ // TODO: Настроить CORS
		AllowOrigins: []string{"*"},
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions},
		AllowHeaders: []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAccept, echo.HeaderAuthorization},
	}))

	// Регистрация маршрутов
	gameplayHandler.RegisterRoutes(e, cfg.JWTSecret)

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
	log.Println("Получен сигнал завершения, начинаем graceful shutdown...")

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
func setupDatabase(cfg *config.Config) (*pgxpool.Pool, error) {
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
func connectRabbitMQ(url string) (*amqp.Connection, error) {
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
		log.Printf("Не удалось подключиться к RabbitMQ (попытка %d/%d): %v. Повтор через %v...", i+1, maxRetries, err, retryDelay)
		time.Sleep(retryDelay)
	}
	return nil, err
}
