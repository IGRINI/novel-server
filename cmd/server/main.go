package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"novel-server/internal/api"
	"novel-server/internal/auth"
	"novel-server/internal/config"
	"novel-server/internal/database"
	"novel-server/internal/deepseek"
	"novel-server/internal/repository"
	"novel-server/internal/service"
)

func main() {
	// Загружаем конфигурацию
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// --- Инициализация базы данных ---
	log.Println("Initializing database and running migrations...")
	dbPool, err := database.InitDB(context.Background())
	if err != nil {
		log.Fatalf("Failed to initialize database and run migrations: %v", err)
	}
	defer database.CloseDB(dbPool)
	log.Println("Database initialization and migrations completed successfully.")
	// ----------------------------------

	// --- Инициализация JWT ---
	if err := auth.InitJWT(); err != nil {
		log.Fatalf("Failed to initialize JWT: %v", err)
	}
	// -------------------------

	// Инициализируем репозиторий новелл
	novelRepo := repository.NewPostgresNovelRepository(dbPool)
	log.Println("Novel repository initialized.")

	// Инициализируем репозиторий черновиков новелл
	draftRepo := repository.NewPostgresNovelDraftRepository(dbPool)
	log.Println("Novel draft repository initialized.")

	// Инициализируем клиент DeepSeek
	dsClient := deepseek.NewClient(cfg.DeepSeek.APIKey, cfg.DeepSeek.ModelName)

	// Инициализируем сервис для работы с новеллами
	novelContentService, err := service.NewNovelContentService(dsClient, novelRepo)
	if err != nil {
		log.Fatalf("Error creating novel content service: %v", err)
	}

	novelService, err := service.NewNovelService(dsClient, novelRepo, draftRepo, novelContentService)
	if err != nil {
		log.Fatalf("Error creating novel service: %v", err)
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
	log.Printf("Server starting on %s", addr)
	log.Printf("API endpoints: %s/generate-novel and %s/generate-novel-content", cfg.API.BasePath, cfg.API.BasePath)

	// Запуск HTTP сервера
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Could not start server: %v", err)
	}
}
