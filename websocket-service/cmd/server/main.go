// Package main WebSocket Service
//
//	@title			Novel Server WebSocket API
//	@version		1.0
//	@description	Real-time уведомления для Novel Server
//	@termsOfService	http://swagger.io/terms/
//
//	@contact.name	API Support
//	@contact.url	http://www.swagger.io/support
//	@contact.email	support@swagger.io
//
//	@license.name	Apache 2.0
//	@license.url	http://www.apache.org/licenses/LICENSE-2.0.html
//
//	@host		localhost:8083
//	@BasePath	/ws
//
//	@securityDefinitions.apikey	BearerAuth
//	@in							header
//	@name						Authorization
//	@description				Type "Bearer" followed by a space and JWT token.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"novel-server/websocket-service/docs"
	"novel-server/websocket-service/internal/config"
	"novel-server/websocket-service/internal/handler"
	"novel-server/websocket-service/internal/messaging"
	"novel-server/websocket-service/internal/service"

	_ "novel-server/websocket-service/docs"
)

// @Summary Проверка состояния сервиса
// @Description Возвращает статус работы WebSocket Service
// @Tags health
// @Produce json
// @Success 200 {object} map[string]interface{} "Сервис работает"
// @Router /health [get]
func healthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// @Summary WebSocket соединение
// @Description Устанавливает WebSocket соединение для real-time уведомлений
// @Tags websocket
// @Param token query string true "JWT токен для аутентификации"
// @Success 101 {string} string "Switching Protocols"
// @Failure 400 {object} map[string]interface{} "Неверные параметры"
// @Failure 401 {object} map[string]interface{} "Неавторизован"
// @Security BearerAuth
// @Router /ws [get]
func wsEndpoint(wsHandler *handler.WebSocketHandler) gin.HandlerFunc {
	return gin.WrapF(wsHandler.ServeWS)
}

func main() {
	// Инициализация логгера
	output := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
	logger := zerolog.New(output).With().Timestamp().Logger()

	// Загрузка .env файла (если есть)
	if err := godotenv.Load(); err != nil {
		logger.Info().Msg("No .env file found")
	}

	// Загрузка конфигурации
	var cfg config.Config
	err := envconfig.Process("", &cfg)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to process config")
	}

	logger.Info().Msgf("Конфигурация загружена: %+v", cfg)

	// Создание менеджера соединений
	connManager := handler.NewConnectionManager()

	// Создание и запуск консьюмера RabbitMQ
	rabbitConsumer, err := messaging.NewRabbitMQConsumer(&cfg.RabbitMQ, connManager, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create RabbitMQ consumer")
	}
	go func() {
		if err := rabbitConsumer.StartConsuming(); err != nil {
			logger.Error().Err(err).Msg("RabbitMQ consumer stopped with error")
		}
	}()

	// Создание сервиса аутентификации
	authService := service.NewAuthService(&cfg.AuthService, logger)

	// Создание обработчика WebSocket с проверкой Origin
	wsHandler := handler.NewWebSocketHandler(connManager, authService, cfg.Server.AllowedOrigins, logger)

	// Настройка Gin роутера
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Logger())
	router.Use(gin.Recovery())

	// CORS configuration
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization"}
	corsConfig.AllowCredentials = true
	router.Use(cors.New(corsConfig))

	// Health check
	router.GET("/health", healthCheck)

	// OpenAPI JSON endpoint
	router.GET("/api/openapi.json", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.Data(http.StatusOK, "application/json", []byte(docs.SwaggerInfo.ReadDoc()))
	})
	router.GET("/api/openapi.yaml", func(c *gin.Context) {
		c.Header("Content-Type", "application/yaml")
		// Конвертируем JSON в YAML или возвращаем заглушку
		c.String(http.StatusOK, "# OpenAPI YAML not available, use JSON endpoint")
	})

	// Swagger UI
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// WebSocket endpoint
	router.GET("/ws", wsEndpoint(wsHandler))

	// Metrics endpoint
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	mainServer := &http.Server{
		Addr:    fmt.Sprintf(":%s", cfg.Server.Port),
		Handler: router,
	}

	go func() {
		logger.Info().Msgf("Starting server on port %s", cfg.Server.Port)
		if err := mainServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("Failed to start server")
		}
	}()

	// Ожидание сигнала завершения
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info().Msg("Shutting down servers...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := mainServer.Shutdown(ctx); err != nil {
		logger.Error().Err(err).Msg("Server shutdown failed")
	}
	if err := rabbitConsumer.StopConsuming(); err != nil {
		logger.Error().Err(err).Msg("RabbitMQ consumer stop failed")
	}

	logger.Info().Msg("Servers gracefully stopped")
}
