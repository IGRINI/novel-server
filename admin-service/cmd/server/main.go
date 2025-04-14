package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"novel-server/admin-service/internal/config"
	"novel-server/admin-service/internal/handler"
	"os"
	"os/signal"
	"syscall"

	"github.com/labstack/echo/v4"
	echoMiddleware "github.com/labstack/echo/v4/middleware"
	"go.uber.org/zap"
	"novel-server/shared/logger"
	sharedMiddleware "novel-server/shared/middleware"
	"novel-server/admin-service/internal/web"
	"novel-server/admin-service/internal/client"
	sharedModels "novel-server/shared/models"
	"text/template"

	// Импорт для метрик Prometheus
	"github.com/prometheus/client_golang/prometheus/promhttp"
	// <<< Убираем импорт Echo Prometheus middleware >>>
	// echoPrometheus "github.com/labstack/echo-contrib/prometheus"
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
		appLogger.Fatal("Failed to load configuration", zap.Error(err)) // Используем appLogger
	}
	appLogger.Info("Configuration loaded")

	// --- Инициализация клиента auth-service ---
	// Создаем клиент со статичным секретом
	authClientInstance, err := client.NewAuthServiceClient(cfg.AuthServiceURL, cfg.ClientTimeout, appLogger, cfg.InterServiceSecret) // Передаем staticSecret
	if err != nil {
		appLogger.Fatal("Failed to create initial Auth Service client", zap.Error(err))
	}

	// --- Получаем межсервисный JWT токен ---
	tokenCtx, tokenCancel := context.WithTimeout(context.Background(), cfg.ClientTimeout)
	defer tokenCancel()
	
	receivedToken, err := authClientInstance.GenerateInterServiceToken(tokenCtx, cfg.ServiceID) // Используем созданный клиент
	if err != nil {
		appLogger.Fatal("Failed to obtain inter-service token from auth-service", zap.Error(err), zap.String("requestingService", cfg.ServiceID))
	}
	// --- Устанавливаем полученный JWT токен в клиент --- 
	authClientInstance.SetInterServiceToken(receivedToken) // Используем сеттер
	appLogger.Info("Successfully obtained and set inter-service JWT token")
	// --- Конец получения и установки токена ---

	// 3. Инициализация рендерера шаблонов
	// Берем debug режим из конфига (предполагаем, что Env == "development")
	debugMode := cfg.Env == "development"
	
	// <<< Создаем FuncMap >>>
	funcMap := template.FuncMap{
		"hasRole": sharedModels.HasRole, // Добавляем функцию hasRole
	}

	// <<< Передаем funcMap в конструктор >>>
	templateRenderer := web.NewTemplateRenderer("web/templates", debugMode, appLogger, funcMap)
	// <<< Убираем добавление функций после создания, т.к. они добавляются в loadTemplates >>>
	// if templateRenderer.Templates != nil { 
	// 	templateRenderer.Templates = templateRenderer.Templates.Funcs(template.FuncMap{
	// 		"hasRole": sharedModels.HasRole, 
	// 	})
	// }

	// 4. Инициализация Echo
	e := echo.New()
	e.Renderer = templateRenderer
	e.HTTPErrorHandler = handler.CustomHTTPErrorHandler

	// Базовые middleware для Echo
	e.Use(echoMiddleware.Recover())
	// Используем кастомный логгер запросов на основе zap
	e.Use(sharedMiddleware.EchoZapLogger(appLogger))

	// <<< Убираем Prometheus Middleware для Echo >>>
	// p := echoPrometheus.NewPrometheus("echo", nil) 
	// p.Use(e) 
	// <<< Конец удаления >>>

	// 5. Инициализация обработчика (Handler)
	adminHandler := handler.NewAdminHandler(cfg, appLogger, authClientInstance) // Передаем настроенный клиент

	// 6. Регистрация маршрутов (роутов)
	// Внутри RegisterRoutes уже настроено middleware для аутентификации/авторизации
	adminHandler.RegisterRoutes(e)
	appLogger.Info("Routes registered")

	// --- Регистрация эндпоинта для метрик Prometheus (остается без изменений) --- 
	appLogger.Info("Registering Prometheus metrics endpoint")
	e.GET("/metrics", echo.WrapHandler(promhttp.Handler()))

	// 7. Запуск сервера
	serverAddr := fmt.Sprintf(":%s", cfg.ServerPort)
	appLogger.Info("Starting admin server", zap.String("address", serverAddr))

	go func() {
		if err := e.Start(serverAddr); err != nil && err != http.ErrServerClosed { // Используем http.ErrServerClosed
			appLogger.Fatal("shutting down the server", zap.Error(err))
		}
	}()

	// 8. Ожидание сигнала для graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit // Блокируемся до получения сигнала

	appLogger.Info("Shutting down server...")

	// TODO: Добавить graceful shutdown для Echo и других ресурсов (БД, RabbitMQ и т.д.)
	// ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	// defer cancel()
	// if err := e.Shutdown(ctx); err != nil {
	// 	e.Logger.Fatal(err)
	// }

	appLogger.Info("Server gracefully stopped")
}
