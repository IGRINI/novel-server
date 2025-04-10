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

	"novel-server/internal/authservice"
	"novel-server/internal/config"
	"novel-server/internal/database"
)

func main() {
	// Загрузка переменных окружения
	if err := godotenv.Load(); err != nil {
		fmt.Printf("Warning: could not load .env file: %v\n", err)
	}

	// Инициализация логгера
	initLogger()

	// Парсинг флагов командной строки
	env := flag.String("env", "development", "Environment: development, production")
	servicePort := flag.Int("port", 8081, "Auth service port")
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

	// Инициализация репозитория и сервиса аутентификации
	authRepo := authservice.NewRepository(dbPool)
	authService := authservice.NewService(authRepo, cfg.JWT.Secret, cfg.JWT.PasswordSalt)

	// Устанавливаем время жизни токенов
	authService.SetTokenTTL(
		time.Duration(cfg.JWT.AccessTokenTTL)*time.Minute,
		time.Duration(cfg.JWT.RefreshTokenTTL)*time.Hour,
	)

	// Инициализация HTTP обработчиков
	authHandlers := authservice.NewHandler(authService)

	// Настройка маршрутов
	router := mux.NewRouter()

	// Маршруты аутентификации
	router.HandleFunc("/auth/register", authHandlers.Register).Methods("POST")
	router.HandleFunc("/auth/login", authHandlers.Login).Methods("POST")
	router.HandleFunc("/auth/refresh", authHandlers.RefreshToken).Methods("POST")
	router.HandleFunc("/auth/validate", authHandlers.ValidateToken).Methods("GET")
	router.HandleFunc("/auth/logout", authHandlers.Logout).Methods("POST")

	// Защищенные маршруты (требуют JWT проверки)
	protectedRouter := router.PathPrefix("/auth").Subrouter()
	protectedRouter.Use(authservice.JWTMiddleware([]byte(cfg.JWT.Secret)))
	protectedRouter.HandleFunc("/logout-all", authHandlers.LogoutAll).Methods("POST")
	protectedRouter.HandleFunc("/display-name", authHandlers.UpdateDisplayName).Methods("PUT")

	// Маршрут для межсервисной валидации токенов
	router.HandleFunc("/internal/validate-token", authHandlers.InternalValidateToken).Methods("POST")

	// Маршрут для получения сервисного токена
	router.HandleFunc("/service/token", authHandlers.CreateServiceToken).Methods("POST")

	// Настройка CORS
	c := cors.New(cors.Options{
		AllowedOrigins:   cfg.CORS.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
	})

	// Создание HTTP сервера
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", *servicePort),
		Handler:      c.Handler(router),
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeoutSeconds) * time.Second,
		IdleTimeout:  time.Duration(cfg.Server.IdleTimeoutSeconds) * time.Second,
	}

	// Запуск сервера в горутине
	go func() {
		log.Info().Int("port", *servicePort).Msg("starting Auth service")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server failed")
		}
	}()

	// Настройка плавного завершения
	gracefulShutdown(server)
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

// gracefulShutdown обеспечивает плавное завершение работы сервера
func gracefulShutdown(server *http.Server) {
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

	log.Info().Msg("server stopped gracefully")
}
