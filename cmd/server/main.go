package main

import (
	"context"
	"fmt"
	"net/http"
	"novel-server/internal/api"
	"novel-server/internal/auth"
	"novel-server/internal/config"
	"novel-server/internal/database"
	"novel-server/internal/deepseek"
	"novel-server/internal/logger"
	"novel-server/internal/repository"
	"novel-server/internal/service"
	"os"
)

func main() {
	// Загружаем конфигурацию
	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Logger.Error("Failed to load config", "err", err)
		os.Exit(1)
	}

	// --- Инициализация базы данных ---
	logger.Logger.Info("Initializing database and running migrations...")
	dbPool, err := database.InitDB(context.Background())
	if err != nil {
		logger.Logger.Error("Failed to initialize database and run migrations", "err", err)
		os.Exit(1)
	}
	defer database.CloseDB(dbPool)
	logger.Logger.Info("Database initialization and migrations completed successfully")
	// ----------------------------------

	// --- Инициализация JWT ---
	if err := auth.InitJWT(); err != nil {
		logger.Logger.Error("Failed to initialize JWT", "err", err)
		os.Exit(1)
	}
	// -------------------------

	// Инициализируем репозиторий новелл
	novelRepo := repository.NewPostgresNovelRepository(dbPool)
	logger.Logger.Info("Novel repository initialized")

	// Инициализируем репозиторий черновиков новелл
	draftRepo := repository.NewPostgresNovelDraftRepository(dbPool)
	logger.Logger.Info("Novel draft repository initialized")

	// Инициализируем клиент DeepSeek
	dsClient := deepseek.NewClient(cfg.DeepSeek.APIKey, cfg.DeepSeek.ModelName)

	// Инициализируем сервис для работы с новеллами
	novelContentService, err := service.NewNovelContentService(dsClient, novelRepo)
	if err != nil {
		logger.Logger.Error("Error creating novel content service", "err", err)
		os.Exit(1)
	}

	novelService, err := service.NewNovelService(dsClient, novelRepo, draftRepo, novelContentService)
	if err != nil {
		logger.Logger.Error("Error creating novel service", "err", err)
		os.Exit(1)
	}

	// Создаем мультиплексор для маршрутов
	mux := http.NewServeMux()

	// Инициализируем обработчик API
	api.RegisterHandlers(mux, novelService, novelContentService, cfg.API.BasePath)

	// Базовый корневой маршрут
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello, Novel Server!")
	})

	// Определяем адрес сервера
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	logger.Logger.Info("Server starting", "addr", addr)
	logger.Logger.Info("API endpoints", "generate", fmt.Sprintf("%s/generate-novel", cfg.API.BasePath), "content", fmt.Sprintf("%s/generate-novel-content", cfg.API.BasePath))

	// Запуск HTTP сервера
	if err := http.ListenAndServe(addr, mux); err != nil {
		logger.Logger.Error("Could not start server", "err", err)
		os.Exit(1)
	}
}
