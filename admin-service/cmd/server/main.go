package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"novel-server/admin-service/internal/client"
	"novel-server/admin-service/internal/config"
	"novel-server/admin-service/internal/handler"
	"novel-server/admin-service/internal/messaging"
	sharedLogger "novel-server/shared/logger"
	sharedMiddleware "novel-server/shared/middleware"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

func main() {
	// Используем стандартный log для самых ранних ошибок, до инициализации zap
	log.Println("Запуск Admin Service...")

	// --- Загрузка конфигурации ---
	// Передаем nil логгер, так как он еще не создан.
	// LoadConfig должен уметь работать с nil логгером или использовать стандартный log.
	// TODO: Проверить реализацию config.LoadConfig
	cfg, err := config.LoadConfig(nil)
	if err != nil {
		log.Fatalf("Ошибка загрузки конфигурации: %v", err)
	}
	log.Println("Конфигурация загружена")

	// --- Инициализация логгера (Используем shared/logger) ---
	logger, err := sharedLogger.New(sharedLogger.Config{
		Level:      cfg.LogLevel,
		Encoding:   "json", // Или cfg.LogEncoding, если есть в конфиге
		OutputPath: "",     // stdout по умолчанию, или cfg.LogOutputPath
	})
	if err != nil {
		log.Fatalf("Не удалось инициализировать логгер: %v", err)
	}
	defer logger.Sync()
	sugar := logger.Sugar()
	sugar.Info("Логгер инициализирован", zap.String("logLevel", cfg.LogLevel))

	// Если LoadConfig требовал логгер, можно передать его сюда повторно,
	// но лучше чтобы LoadConfig не требовал логгер.
	// cfg, err = config.LoadConfig(logger) // Пример, если LoadConfig надо обновить
	// if err != nil {
	// 	sugar.Fatalf("Ошибка повторной загрузки конфигурации с логгером: %v", err)
	// }

	// --- Подключение к RabbitMQ ---
	rabbitConn, err := connectRabbitMQ(cfg.RabbitMQ.URL, logger) // Передаем созданный логгер
	if err != nil {
		sugar.Fatalf("Не удалось подключиться к RabbitMQ: %v", err)
	}
	defer rabbitConn.Close()
	sugar.Info("Успешно подключено к RabbitMQ")

	// --- Создание Push Notification Publisher ---
	pushPublisher, err := messaging.NewRabbitMQPushPublisher(rabbitConn, cfg.RabbitMQ.PushQueueName, logger)
	if err != nil {
		sugar.Fatalf("Не удалось создать PushNotificationPublisher: %v", err)
	}
	defer func() {
		if err := pushPublisher.Close(); err != nil {
			sugar.Errorf("Ошибка при закрытии канала PushNotificationPublisher: %v", err)
		}
	}()

	// --- Инициализация клиентов сервисов ---
	authClient, err := client.NewAuthServiceClient(cfg.AuthServiceURL, cfg.ClientTimeout, logger, cfg.InterServiceSecret)
	if err != nil {
		sugar.Fatalf("Не удалось создать AuthServiceClient: %v", err)
	}
	storyGenClient, err := client.NewStoryGeneratorClient(cfg.StoryGeneratorURL, cfg.ClientTimeout, logger)
	if err != nil {
		sugar.Fatalf("Не удалось создать StoryGeneratorClient: %v", err)
	}
	gameplayClient, err := client.NewGameplayServiceClient(cfg.GameplayServiceURL, cfg.ClientTimeout, logger)
	if err != nil {
		sugar.Fatalf("Не удалось создать GameplayServiceClient: %v", err)
	}

	// --- Создание обработчика HTTP ---
	h := handler.NewAdminHandler(cfg, logger, authClient, storyGenClient, gameplayClient, pushPublisher)

	// --- Настройка Gin ---
	router := gin.New()

	router.Use(gin.Recovery())
	router.Use(sharedMiddleware.GinZapLogger(logger))

	custom404Path := "./admin-service/web/static/404.html"
	router.Use(handler.CustomErrorMiddleware(logger, custom404Path))

	// CORS Middleware
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization", "HX-Request", "HX-Target", "HX-Current-URL"}
	corsConfig.AllowCredentials = true
	router.Use(cors.New(corsConfig))

	// Загрузка HTML шаблонов
	router.LoadHTMLGlob("./admin-service/web/templates/**/*")

	// Настройка маршрутов
	h.RegisterRoutes(router)

	// --- Запуск HTTP сервера ---
	serverAddr := ":" + cfg.ServerPort
	srv := &http.Server{
		Addr:    serverAddr,
		Handler: router,
	}

	go func() {
		sugar.Infof("Admin сервер запускается на порту %s", cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			sugar.Fatalf("Ошибка запуска HTTP сервера: %v", err)
		}
	}()

	// --- Graceful shutdown ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	sugar.Info("Получен сигнал завершения, начинаем остановку сервера...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		sugar.Fatalf("Ошибка при остановке сервера: %v", err)
	}

	sugar.Info("Сервер успешно остановлен")
}

// connectRabbitMQ остается без изменений, но теперь получает корректный логгер
func connectRabbitMQ(uri string, logger *zap.Logger) (*amqp.Connection, error) {
	var connection *amqp.Connection
	var err error
	maxRetries := 5
	retryDelay := 5 * time.Second

	for i := 0; i < maxRetries; i++ {
		connection, err = amqp.Dial(uri)
		if err == nil {
			logger.Info("Подключение к RabbitMQ успешно установлено")
			go func() {
				notifyClose := make(chan *amqp.Error)
				connection.NotifyClose(notifyClose)
				closeErr := <-notifyClose
				if closeErr != nil {
					logger.Error("Соединение с RabbitMQ разорвано", zap.Error(closeErr))
				}
			}()
			return connection, nil
		}
		logger.Warn("Не удалось подключиться к RabbitMQ, попытка переподключения...",
			zap.Error(err),
			zap.Int("retry", i+1),
			zap.Duration("delay", retryDelay),
		)
		time.Sleep(retryDelay)
	}
	return nil, fmt.Errorf("не удалось подключиться к RabbitMQ после %d попыток: %w", maxRetries, err)
}

// getEnv остается без изменений
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
