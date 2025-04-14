package main

import (
	"context"
	"log"
	"net/http"
	"novel-server/shared/authutils"
	sharedMiddleware "novel-server/shared/middleware"
	"novel-server/websocket-service/internal/config"
	"novel-server/websocket-service/internal/handler"
	"novel-server/websocket-service/internal/messaging"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/labstack/echo/v4"
	echoMiddleware "github.com/labstack/echo/v4/middleware"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
	"novel-server/shared/logger"
)

func main() {
	// Загружаем .env файл (если есть) для локальной разработки
	_ = godotenv.Load()

	logCfg := logger.Config{ Level: "info" }
	appLogger, err := logger.New(logCfg)
	if err != nil {
		log.Fatalf("Failed to init logger: %v", err)
	}
	defer appLogger.Sync()
	appLogger.Info("Starting WebSocket Service...")

	cfg, err := config.LoadConfig()
	if err != nil {
		appLogger.Fatal("Failed to load config", zap.Error(err))
	}
	appLogger.Info("Config loaded")

	rabbitConn, err := connectRabbitMQ(cfg.RabbitMQURL, appLogger)
	if err != nil {
		appLogger.Fatal("Failed to connect to RabbitMQ", zap.Error(err))
	}
	defer rabbitConn.Close()
	appLogger.Info("Connected to RabbitMQ")

	connManager := handler.NewConnectionManager( /* appLogger.Named("ConnManager") */ )

	mqConsumer, err := messaging.NewConsumer(rabbitConn, connManager, cfg.ClientUpdatesQueueName /* , appLogger.Named("Consumer") */)
	if err != nil {
		appLogger.Fatal("Failed to create RabbitMQ consumer", zap.Error(err))
	}
	go func() {
		if err := mqConsumer.StartConsuming(); err != nil {
			appLogger.Error("RabbitMQ consumer error", zap.Error(err))
		}
	}()
	appLogger.Info("RabbitMQ consumer started")

	e := echo.New()
	e.Use(sharedMiddleware.EchoZapLogger(appLogger))
	e.Use(echoMiddleware.Recover())

	e.Use(echoMiddleware.CORSWithConfig(echoMiddleware.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodOptions},
		AllowHeaders: []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAccept, echo.HeaderAuthorization},
	}))

	tokenVerifier, err := authutils.NewJWTVerifier(cfg.JWTSecret, appLogger)
	if err != nil {
		appLogger.Fatal("Failed to create JWT verifier", zap.Error(err))
	}

	wsHandler := handler.NewWebSocketHandler(connManager)

	wsGroup := e.Group("/ws")
	wsGroup.Use(echo.WrapMiddleware(sharedMiddleware.AuthMiddleware(tokenVerifier.VerifyToken, appLogger)))
	wsGroup.GET("", wsHandler.Handle)

	appLogger.Info("WebSocket server listening", zap.String("port", cfg.Port))

	go func() {
		if err := e.Start(":" + cfg.Port); err != nil && err != http.ErrServerClosed {
			appLogger.Fatal("Server start error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	appLogger.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(ctx); err != nil {
		appLogger.Error("Echo shutdown error", zap.Error(err))
	}

	appLogger.Info("WebSocket service stopped")
}

func connectRabbitMQ(url string, logger *zap.Logger) (*amqp.Connection, error) {
	var conn *amqp.Connection
	var err error
	maxRetries := 5
	retryDelay := 5 * time.Second

	for i := 0; i < maxRetries; i++ {
		conn, err = amqp.Dial(url)
		if err == nil {
			return conn, nil
		}
		logger.Warn("Failed to connect to RabbitMQ",
			zap.Int("attempt", i+1),
			zap.Int("max_attempts", maxRetries),
			zap.Duration("retry_delay", retryDelay),
			zap.Error(err),
		)
		time.Sleep(retryDelay)
	}
	return nil, err
}
