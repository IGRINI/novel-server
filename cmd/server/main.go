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
	"github.com/jmoiron/sqlx"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/rs/cors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"novel-server/internal/authservice"
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

	// Инициализация sqlx.DB
	log.Info().Msg("initializing sqlx database connection...")
	sqlxDB, err := initSqlxDatabase(cfg.Database)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize sqlx database")
	}
	defer sqlxDB.Close()
	log.Info().Msg("sqlx database connection established")

	// Применяем миграции
	log.Info().Msg("applying database migrations...")
	if err := database.ApplyMigrations(dbPool, "internal/database/migrations"); err != nil {
		log.Fatal().Err(err).Msg("failed to apply migrations")
	}
	log.Info().Msg("database migrations applied successfully")

	// Инициализация AI клиента
	aiClient := initAIClient(cfg.AI)

	// Инициализация репозиториев
	novelRepo := repository.NewNovelRepository(dbPool, sqlxDB)

	// Создаем менеджер задач
	taskManager := taskmanager.NewManager()

	// Создаем реальный менеджер WebSocket
	wsManager := ws.NewWebSocketManager()
	wsManager.Start()

	// Инициализация клиента Auth Service
	authClient := initAuthClient()

	// Инициализация сервисов
	novelService := service.NewNovelService(*novelRepo, taskManager, *aiClient, wsManager)

	// Инициализация HTTP обработчиков
	novelHandlers := delivery.New(novelService)

	// Настройка маршрутов
	router := mux.NewRouter()

	// Маршрут для WebSocket (не требует проверки JWT)
	router.Handle("/ws", wsManager.Handler()).Methods("GET")

	// Создаем подмаршрутизатор для API, требующего аутентификации
	apiRouter := router.PathPrefix("/api").Subrouter()

	// Применяем Middleware: сначала логгирование, потом JWT с использованием Auth Service
	apiRouter.Use(LoggingMiddleware)
	jwtMiddleware := middleware.NewAuthServiceMiddleware(authClient)
	apiRouter.Use(jwtMiddleware)

	// Регистрация API маршрутов
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

// initSqlxDatabase инициализирует соединение с базой данных с использованием sqlx
func initSqlxDatabase(cfg config.DatabaseConfig) (*sqlx.DB, error) {
	connString := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Name, cfg.SSLMode)

	db, err := sqlx.Connect("postgres", connString)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database using sqlx: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxConnections)
	db.SetMaxIdleConns(cfg.MaxConnections / 2) // Пример: половина от максимального
	db.SetConnMaxIdleTime(time.Duration(cfg.MaxConnIdleMinutes) * time.Minute)

	// Проверка соединения
	if err := db.Ping(); err != nil {
		db.Close() // Закрываем соединение, если пинг не удался
		return nil, fmt.Errorf("failed to ping database using sqlx: %w", err)
	}

	return db, nil
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

// initAuthClient инициализирует клиент для Auth Service
func initAuthClient() *authservice.Client {
	baseURL := os.Getenv("AUTH_SERVICE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8081" // URL Auth Service по умолчанию
	}

	serviceID := os.Getenv("SERVICE_ID")
	if serviceID == "" {
		serviceID = "novel-service" // ID сервиса по умолчанию
	}

	apiKey := os.Getenv("AUTH_SERVICE_API_KEY")
	timeout, _ := time.ParseDuration(os.Getenv("AUTH_SERVICE_TIMEOUT"))
	if timeout == 0 {
		timeout = 5 * time.Second // Таймаут по умолчанию
	}

	cfg := authservice.ClientConfig{
		BaseURL:   baseURL,
		ServiceID: serviceID,
		APIKey:    apiKey,
		Timeout:   timeout,
	}

	return authservice.NewClient(cfg)
}

// LoggingMiddleware внедряет настроенный логгер в контекст запроса
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Внедряем глобальный логгер в контекст запроса
		ctxWithLogger := log.Logger.WithContext(r.Context())
		// Создаем новый запрос с обновленным контекстом
		r = r.WithContext(ctxWithLogger)
		// Передаем запрос дальше
		next.ServeHTTP(w, r)
	})
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
