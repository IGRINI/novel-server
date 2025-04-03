package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/rs/cors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"novel-server/internal/auth"
	"novel-server/internal/config"
	"novel-server/internal/database"
	delivery "novel-server/internal/delivery/http"
	"novel-server/internal/delivery/http/middleware"
	ws "novel-server/internal/delivery/websocket"
	"novel-server/internal/repository"
	"novel-server/internal/service"
	"novel-server/pkg/ai"
	"novel-server/pkg/taskmanager"
)

func main() {
	// Загрузка переменных окружения
	if err := godotenv.Load(); err != nil {
		// Логируем как предупреждение, т.к. в production .env может не использоваться
		fmt.Printf("Warning: could not load .env file: %v\n", err)
	}

	// Инициализация логгера
	initLogger()

	// Парсинг флагов командной строки
	env := flag.String("env", "development", "Environment: development, production")
	flag.Parse()

	// Загрузка конфигурации
	cfg, err := config.Load(*env)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load configuration")
	}

	// Инициализация соединения с БД
	log.Info().Msg("connecting to database...")
	dbPool, err := initDatabase(cfg.Database)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to database")
	}
	defer dbPool.Close()
	log.Info().Msg("database connection established")

	// Применяем миграции
	log.Info().Msg("applying database migrations...")
	if err := database.ApplyMigrations(dbPool, "internal/database/migrations"); err != nil {
		log.Fatal().Err(err).Msg("failed to apply migrations")
	}
	log.Info().Msg("database migrations applied successfully")

	// Инициализация AI клиента
	aiClient := initAIClient(cfg.AI)

	// Инициализация репозиториев
	novelRepo := repository.NewNovelRepository(dbPool)
	userRepo := auth.NewRepository(dbPool)

	// Создаем менеджер задач
	taskManager := taskmanager.NewManager()

	// Создаем реальный менеджер WebSocket
	wsManager := ws.NewWebSocketManager()
	wsManager.Start()

	// Инициализация сервисов
	novelService := service.NewNovelService(novelRepo, aiClient, taskManager, wsManager)
	authService := auth.NewService(userRepo, cfg.JWT.Secret)

	// Инициализация HTTP обработчиков
	novelHandlers := delivery.New(novelService)
	authHandlers := auth.NewHandler(authService)

	// Настройка маршрутов
	router := mux.NewRouter()

	// Маршруты аутентификации (не требуют middleware)
	authRouter := router.PathPrefix("/auth").Subrouter()
	authRouter.HandleFunc("/register", authHandlers.Register).Methods("POST")
	authRouter.HandleFunc("/login", authHandlers.Login).Methods("POST")

	// Маршрут для WebSocket (не требует JWT middleware)
	router.Handle("/ws", wsManager.Handler()).Methods("GET")

	// Создаем подмаршрутизатор для API, требующего аутентификации
	apiRouter := router.PathPrefix("/api").Subrouter()

	// Применяем JWT Middleware к этому подмаршрутизатору
	jwtMiddleware := middleware.JWTMiddleware([]byte(cfg.JWT.Secret))
	apiRouter.Use(jwtMiddleware)

	// Регистрация остальных маршрутов на apiRouter
	novelHandlers.RegisterRoutes(apiRouter)

	// Настройка CORS
	c := cors.New(cors.Options{
		AllowedOrigins:   cfg.CORS.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
	})

	// Создание HTTP сервера
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      c.Handler(router),
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeoutSeconds) * time.Second,
		IdleTimeout:  time.Duration(cfg.Server.IdleTimeoutSeconds) * time.Second,
	}

	// Запуск сервера в горутине
	go func() {
		log.Info().Int("port", cfg.Server.Port).Msg("starting HTTP server")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server failed")
		}
	}()

	// Настройка плавного завершения
	gracefulShutdown(server, taskManager)
}

// initLogger настраивает глобальный логгер
func initLogger() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.With().Caller().Logger()

	// В режиме разработки используем более читаемый вывод
	if os.Getenv("ENV") != "production" {
		output := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
		log.Logger = zerolog.New(output).With().Timestamp().Caller().Logger()
	}

	// Настройка уровня логирования
	logLevel := zerolog.InfoLevel
	if lvl, err := zerolog.ParseLevel(os.Getenv("LOG_LEVEL")); err == nil {
		logLevel = lvl
	}
	zerolog.SetGlobalLevel(logLevel)
}

// initDatabase инициализирует соединение с базой данных
func initDatabase(cfg config.DatabaseConfig) (*pgxpool.Pool, error) {
	ctx := context.Background()
	connString := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Name, cfg.SSLMode)

	poolConfig, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database connection string: %w", err)
	}

	poolConfig.MaxConns = int32(cfg.MaxConnections)
	poolConfig.MaxConnIdleTime = time.Duration(cfg.MaxConnIdleMinutes) * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create database connection pool: %w", err)
	}

	// Проверка соединения
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return pool, nil
}

// initAIClient инициализирует клиент для работы с ИИ-сервисами
func initAIClient(cfg config.AIConfig) *ai.Client {
	aiCfg := ai.Config{
		APIKey:     cfg.APIKey,
		ModelName:  cfg.Model,
		Timeout:    cfg.Timeout,
		MaxRetries: cfg.MaxAttempts,
	}
	client, err := ai.New(aiCfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize AI client")
	}
	return client
}

// gracefulShutdown обеспечивает плавное завершение работы сервера
func gracefulShutdown(server *http.Server, taskManager taskmanager.ITaskManager) {
	// Ожидание сигнала остановки
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Info().Msg("shutting down server...")

	// Создаем контекст с таймаутом для завершения
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Остановка HTTP сервера
	if err := server.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("server shutdown failed")
	}

	// Остановка менеджера задач
	if taskManager != nil {
		if err := taskManager.Shutdown(ctx); err != nil {
			log.Error().Err(err).Msg("task manager shutdown failed")
		}
	}

	log.Info().Msg("server stopped gracefully")
}
