package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"

	"novel-server/websocket-service/internal/config"
	"novel-server/websocket-service/internal/handler"
	"novel-server/websocket-service/internal/messaging"
	"novel-server/websocket-service/internal/service"
)

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

	// Настройка основного HTTP-сервера
	mainMux := http.NewServeMux()
	mainMux.HandleFunc("/ws", wsHandler.ServeWS) // Основной эндпоинт для WebSocket

	mainServer := &http.Server{
		Addr:    fmt.Sprintf(":%s", cfg.Server.Port),
		Handler: mainMux,
	}

	// Настройка и запуск HTTP-сервера для метрик Prometheus
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok"}`))
	})
	metricsServer := &http.Server{
		Addr:    fmt.Sprintf(":%s", cfg.Server.MetricsPort), // Используем отдельный порт для метрик
		Handler: metricsMux,
	}

	go func() {
		logger.Info().Msgf("Starting main server on port %s", cfg.Server.Port)
		if err := mainServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("Failed to start main server")
		}
	}()

	go func() {
		logger.Info().Msgf("Starting metrics server on port %s", cfg.Server.MetricsPort)
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("Failed to start metrics server")
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
		logger.Error().Err(err).Msg("Main server shutdown failed")
	}
	if err := metricsServer.Shutdown(ctx); err != nil {
		logger.Error().Err(err).Msg("Metrics server shutdown failed")
	}
	if err := rabbitConsumer.StopConsuming(); err != nil {
		logger.Error().Err(err).Msg("RabbitMQ consumer stop failed")
	}

	logger.Info().Msg("Servers gracefully stopped")
}
