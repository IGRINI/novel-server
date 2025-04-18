package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"novel-server/admin-service/internal/config"
	"novel-server/admin-service/internal/handler"
	"os/signal"
	"syscall"
	"time"

	"novel-server/admin-service/internal/client"
	"novel-server/admin-service/internal/web"

	// <<< Убираем импорт интерфейсов, если он больше не нужен напрямую здесь >>>
	"novel-server/shared/logger"
	sharedMiddleware "novel-server/shared/middleware"
	sharedModels "novel-server/shared/models"
	"text/template"

	"github.com/labstack/echo/v4"
	echoMiddleware "github.com/labstack/echo/v4/middleware"
	"go.uber.org/zap"

	// Импорт для метрик Prometheus
	"github.com/prometheus/client_golang/prometheus/promhttp"
	// <<< Возвращаем импорт Echo Prometheus middleware >>>
	echoPrometheus "github.com/labstack/echo-contrib/prometheus"
)

var (
// interServiceToken string // Глобальная переменная больше не нужна
)

func main() {
	// 1. Инициализация логгера
	appLogger, err := logger.New(logger.Config{
		Level: "debug", // TODO: Взять из конфига/переменных окружения
	})
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer appLogger.Sync() // Очистка буфера логгера при выходе
	appLogger.Info("Logger initialized")

	// 2. Загрузка конфигурации
	cfg, err := config.LoadConfig(appLogger) // Передаем логгер в функцию загрузки конфига
	if err != nil {
		appLogger.Fatal("Failed to load configuration", zap.Error(err))
	}
	appLogger.Info("Configuration loaded")

	// --- Инициализация клиентов ---
	authClientInstance, err := client.NewAuthServiceClient(cfg.AuthServiceURL, cfg.ClientTimeout, appLogger, cfg.InterServiceSecret)
	if err != nil {
		appLogger.Fatal("Failed to create initial Auth Service client", zap.Error(err))
	}
	storyGenClientInstance, err := client.NewStoryGeneratorClient(cfg.StoryGeneratorURL, cfg.ClientTimeout, appLogger)
	if err != nil {
		appLogger.Fatal("Failed to create Story Generator client", zap.Error(err))
	}
	gameplayClientInstance, err := client.NewGameplayServiceClient(cfg.GameplayServiceURL, cfg.ClientTimeout, appLogger)
	if err != nil {
		appLogger.Fatal("Failed to create Gameplay Service client", zap.Error(err))
	}

	// --- Получаем первоначальный межсервисный JWT токен С ПОВТОРАМИ ---
	var receivedToken string
	maxRetries := 50
	retryDelay := 5 * time.Second
	var lastTokenErr error

	appLogger.Info("Attempting to obtain initial inter-service token from auth-service...")
	for i := 0; i < maxRetries; i++ {
		tokenCtx, tokenCancel := context.WithTimeout(context.Background(), cfg.ClientTimeout)
		receivedToken, err = authClientInstance.GenerateInterServiceToken(tokenCtx, cfg.ServiceID)
		tokenCancel()

		if err == nil {
			appLogger.Info("Successfully obtained initial inter-service token", zap.Int("attempt", i+1))
			lastTokenErr = nil
			break
		}

		lastTokenErr = err
		appLogger.Warn("Failed to obtain inter-service token, retrying...",
			zap.Int("attempt", i+1),
			zap.Int("max_retries", maxRetries),
			zap.Error(err))

		if i < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}

	if lastTokenErr != nil {
		appLogger.Fatal("Failed to obtain initial inter-service token after multiple retries",
			zap.Int("attempts", maxRetries),
			zap.Error(lastTokenErr))
	}

	// <<< Устанавливаем токен во ВСЕ клиенты >>>
	authClientInstance.SetInterServiceToken(receivedToken)
	gameplayClientInstance.SetInterServiceToken(receivedToken) // <<< Добавлено
	// TODO: Раскомментировать, если нужно
	// storyGenClientInstance.SetInterServiceToken(receivedToken)
	appLogger.Info("Inter-service token set for clients")

	// --- Контекст для Graceful Shutdown и запуска горутин ---
	appCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// --- Запускаем горутину для обновления токена ---
	// <<< Передаем gameplayClient в renewer >>>
	go startTokenRenewer(appCtx, authClientInstance, storyGenClientInstance, gameplayClientInstance, cfg.InterServiceTokenTTL, cfg.ServiceID, appLogger)
	appLogger.Info("Token renewer goroutine started")

	// 3. Инициализация рендерера шаблонов
	debugMode := cfg.Env == "development"
	funcMap := template.FuncMap{
		"hasRole": sharedModels.HasRole,
	}
	templateRenderer := web.NewTemplateRenderer("web/templates", debugMode, appLogger, funcMap)

	// 4. Инициализация Echo
	e := echo.New()
	e.Renderer = templateRenderer
	e.HTTPErrorHandler = handler.CustomHTTPErrorHandler

	// Базовые middleware для Echo
	e.Use(echoMiddleware.Recover())
	e.Use(sharedMiddleware.EchoZapLogger(appLogger))
	p := echoPrometheus.NewPrometheus("echo", nil)
	p.Use(e)

	// 5. Инициализация обработчика (Handler)
	// <<< Передаем КЛИЕНТЫ в хендлер >>>
	adminHandler := handler.NewAdminHandler(cfg, appLogger, authClientInstance, storyGenClientInstance, gameplayClientInstance)

	// 6. Регистрация маршрутов (роутов)
	adminHandler.RegisterRoutes(e)
	appLogger.Info("Routes registered")

	// --- Регистрация эндпоинта для метрик Prometheus ---
	appLogger.Info("Registering Prometheus metrics endpoint")
	e.GET("/metrics", echo.WrapHandler(promhttp.Handler()))

	// --- Регистрация healthcheck эндпоинта ---
	healthHandler := func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
	e.GET("/health", healthHandler)
	e.HEAD("/health", healthHandler) // <<< Добавляем обработку HEAD

	// 7. Запуск сервера
	serverAddr := fmt.Sprintf(":%s", cfg.ServerPort)
	appLogger.Info("Starting admin server", zap.String("address", serverAddr))

	go func() {
		if err := e.Start(serverAddr); err != nil && err != http.ErrServerClosed {
			appLogger.Fatal("shutting down the server", zap.Error(err))
		}
	}()

	// 8. Ожидание сигнала для graceful shutdown
	<-appCtx.Done() // Блокируемся до получения сигнала
	appLogger.Info("Shutting down server...")

	// --- Graceful Shutdown для Echo ---
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		appLogger.Error("Error during server shutdown", zap.Error(err))
	}

	appLogger.Info("Server gracefully stopped")
}

// <<< Добавляем функцию startTokenRenewer >>>
// <<< Обновляем сигнатуру, добавляем gameplayClient >>>
func startTokenRenewer(ctx context.Context,
	authClient client.AuthServiceHttpClient,
	storyGenClient client.StoryGeneratorClient, // Оставляем на всякий случай
	gameplayClient client.GameplayServiceClient, // <<< Добавлено
	tokenTTL time.Duration,
	serviceID string,
	logger *zap.Logger) {
	log := logger.Named("TokenRenewer")
	// Рассчитываем интервал обновления (например, 90% от TTL)
	renewInterval := time.Duration(float64(tokenTTL) * 0.9)
	// Добавляем минимальный интервал, если TTL слишком короткий
	if renewInterval <= 10*time.Second { // Минимальный интервал - 10 секунд
		log.Warn("Inter-service token TTL is very short, setting renew interval to 10s", zap.Duration("tokenTTL", tokenTTL))
		renewInterval = 10 * time.Second
	} else {
		log.Info("Token renew interval set", zap.Duration("interval", renewInterval), zap.Duration("tokenTTL", tokenTTL))
	}

	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()

	log.Info("Ticker created successfully")
	log.Info("Starting token renewal loop")
	for {
		select {
		case <-ticker.C:
			log.Info("Attempting to renew inter-service token...")
			renewCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second) // Таймаут для запроса
			newToken, err := authClient.GenerateInterServiceToken(renewCtx, serviceID)
			cancel()

			if err != nil {
				log.Error("Failed to renew inter-service token", zap.Error(err))
				// Ждем некоторое время перед следующей попыткой при ошибке
				select {
				case <-time.After(1 * time.Minute):
					continue
				case <-ctx.Done():
					log.Info("Shutdown signal received while waiting after error.")
					return
				}
			}

			authClient.SetInterServiceToken(newToken)
			gameplayClient.SetInterServiceToken(newToken) // <<< Устанавливаем токен
			// TODO: Раскомментировать, если нужно
			// storyGenClient.SetInterServiceToken(newToken)
			log.Info("Inter-service token renewed successfully for all clients")

		case <-ctx.Done():
			log.Info("Shutdown signal received, stopping token renewal.")
			return // Завершаем горутину
		}
	}
}

// <<< Удаляем функцию setupDatabase >>>
/*
func setupDatabase(cfg *config.Config, logger *zap.Logger) (*pgxpool.Pool, error) {
	// ... (код подключения к БД) ...
}
*/
