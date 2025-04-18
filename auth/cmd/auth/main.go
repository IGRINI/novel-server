package main

import (
	"context"
	"fmt"
	"net/http"
	"novel-server/auth/internal/config"
	"novel-server/auth/internal/handler"
	"novel-server/auth/internal/service"
	"novel-server/shared/database"
	sharedLogger "novel-server/shared/logger"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	// Импорт для метрик Prometheus
	// "github.com/prometheus/client_golang/prometheus/promhttp"
	// <<< Добавляем импорт Gin Prometheus middleware >>>
	ginprometheus "github.com/zsais/go-gin-prometheus"
)

func main() {
	// --- Configuration ---
	cfg, err := config.LoadConfig("../../.env")
	if err != nil {
		fmt.Printf("Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// --- Logger Setup (Используем shared/logger) ---
	logger, err := sharedLogger.New(sharedLogger.Config{
		Level: cfg.LogLevel, // Берем уровень из конфига
		// Encoding: "json", // Можно задать формат вывода (json или console по умолчанию)
		// OutputPath: "/var/log/auth-service.log", // Можно задать файл
	})
	if err != nil {
		fmt.Printf("Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	zap.ReplaceGlobals(logger)
	zap.L().Info("Logger initialized successfully", zap.String("logLevel", cfg.LogLevel))
	zap.L().Info("Configuration loaded") // Убираем вывод всего конфига

	// --- Database Connections ---
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pgPool, err := setupPostgres(ctx, cfg)
	if err != nil {
		zap.L().Fatal("Failed to connect to PostgreSQL", zap.Error(err))
	}
	defer pgPool.Close()
	zap.L().Info("Connected to PostgreSQL")

	redisClient, err := setupRedis(ctx, cfg)
	if err != nil {
		zap.L().Fatal("Failed to connect to Redis", zap.Error(err))
	}
	defer redisClient.Close()
	zap.L().Info("Connected to Redis")

	// --- Dependency Injection ---
	// Логгеры для репозиториев и сервиса теперь создаются через .Named()
	userRepo := database.NewPgUserRepository(pgPool, logger.Named("PgUserRepo"))
	tokenRepo := database.NewRedisTokenRepository(redisClient, logger.Named("RedisTokenRepo"))
	authService := service.NewAuthService(userRepo, tokenRepo, cfg, logger.Named("AuthService"))
	authHandler := handler.NewAuthHandler(authService, userRepo, cfg)

	// --- HTTP Server Setup (Gin) ---
	gin.SetMode(gin.ReleaseMode)
	if cfg.Env == "development" {
		gin.SetMode(gin.DebugMode)
	}

	router := gin.New()
	router.Use(ginZapLogger(logger))
	router.Use(gin.Recovery())

	// <<< Возвращаем Prometheus Middleware >>>
	p := ginprometheus.NewPrometheus("gin") // Префикс для метрик (например, gin_request_...)
	p.Use(router)                           // Регистрируем middleware (он сам зарегистрирует /metrics)
	// <<< Конец возвращения >>>

	// Configure CORS Middleware
	corsConfig := cors.DefaultConfig()
	allowedOrigins := cfg.GetAllowedOrigins()
	if len(allowedOrigins) > 0 {
		corsConfig.AllowOrigins = allowedOrigins
	} else {
		corsConfig.AllowOrigins = []string{"http://localhost:3000"}
		zap.L().Info("CORSAllowedOrigins not set, allowing default", zap.String("origin", "http://localhost:3000"))
	}
	corsConfig.AllowMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Length", "Content-Type", "Authorization"}
	corsConfig.AllowCredentials = true
	corsConfig.MaxAge = 12 * time.Hour
	router.Use(cors.New(corsConfig))

	// Health Check Endpoint
	healthHandler := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
	router.GET("/health", healthHandler)
	router.HEAD("/health", healthHandler)

	// Register Application Routes
	authHandler.RegisterRoutes(router)

	// --- Start Server ---
	srv := &http.Server{
		Addr:         ":" + cfg.ServerPort,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	zap.L().Info("Starting server", zap.String("port", cfg.ServerPort))

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			zap.L().Fatal("Server listen error", zap.Error(err))
		}
	}()

	// --- Graceful Shutdown ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	zap.L().Info("Shutting down server...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		zap.L().Fatal("Server forced to shutdown", zap.Error(err))
	}

	zap.L().Info("Server exiting")
}

// setupPostgres initializes the PostgreSQL connection pool with retry logic.
func setupPostgres(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	zap.L().Debug("Setting up PostgreSQL connection...")
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBSSLMode)

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("unable to parse postgres config: %w", err)
	}
	// <<< Устанавливаем параметры пула из конфига (как в story-generator) >>>
	poolConfig.MaxConns = int32(cfg.DBMaxConns)    // Используем значение из auth/config
	poolConfig.MaxConnIdleTime = cfg.DBIdleTimeout // Используем значение из auth/config

	// --- Retry Logic ---
	var pool *pgxpool.Pool
	var lastErr error
	maxRetries := 50              // Оставляем 50 попыток
	retryDelay := 3 * time.Second // <<< Изменяем задержку на 3 секунды (как в story-generator) >>>

	zap.L().Info("Attempting to connect to PostgreSQL", zap.Int("max_retries", maxRetries), zap.Duration("retry_delay", retryDelay))

	for i := 0; i < maxRetries; i++ {
		attempt := i + 1
		// <<< Используем context.Background() для таймаута попытки (как в story-generator) >>>
		connectCtx, connectCancel := context.WithTimeout(context.Background(), 5*time.Second) // Таймаут на попытку подключения
		pool, err = pgxpool.NewWithConfig(connectCtx, poolConfig)
		connectCancel() // Отменяем контекст этой попытки

		if err != nil {
			lastErr = fmt.Errorf("unable to create postgres connection pool (attempt %d/%d): %w", attempt, maxRetries, err)
			zap.L().Warn("Postgres connection pool creation failed, retrying...",
				zap.Int("attempt", attempt),
				zap.Int("max_retries", maxRetries),
				zap.Error(err),
			)
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
			}
			continue // Следующая попытка
		}

		// Пытаемся пинговать, чтобы убедиться, что соединение живое
		// <<< Используем context.Background() для таймаута пинга (как в story-generator) >>>
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 2*time.Second) // Таймаут на пинг
		err = pool.Ping(pingCtx)
		pingCancel() // Отменяем контекст пинга

		if err == nil {
			zap.L().Info("Successfully connected and pinged PostgreSQL", zap.Int("attempt", attempt))
			return pool, nil // Успех!
		}

		// Если пинг не удался, закрываем созданный пул и повторяем
		pool.Close() // Важно закрыть неудачный пул
		lastErr = fmt.Errorf("unable to ping postgres database (attempt %d/%d): %w", attempt, maxRetries, err)
		zap.L().Warn("Postgres ping failed, retrying...",
			zap.Int("attempt", attempt),
			zap.Int("max_retries", maxRetries),
			zap.Error(err),
		)
		if i < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}

	// Если все попытки не удались
	zap.L().Error("Failed to connect to PostgreSQL after all retries", zap.Int("attempts", maxRetries), zap.Error(lastErr))
	return nil, fmt.Errorf("failed to connect to postgres after %d attempts: %w", maxRetries, lastErr)
}

// setupRedis initializes the Redis client with retry logic.
func setupRedis(ctx context.Context, cfg *config.Config) (*redis.Client, error) {
	zap.L().Debug("Setting up Redis connection...")
	// Опции выносим за цикл
	redisOpts := &redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	}
	zap.L().Info("Redis connection options configured", zap.String("address", redisOpts.Addr), zap.Int("db", redisOpts.DB))

	var client *redis.Client
	var lastErr error
	maxRetries := 50
	retryDelay := 3 * time.Second // <<< Уменьшил задержку до 3 сек, как у Postgres

	zap.L().Info("Attempting to connect and ping Redis", zap.Int("max_retries", maxRetries), zap.Duration("retry_delay", retryDelay))

	for i := 0; i < maxRetries; i++ {
		attempt := i + 1
		// <<< Создаем клиент ВНУТРИ цикла >>>
		client = redis.NewClient(redisOpts)

		// Используем context.Background() с таймаутом для пинга
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second) // <<< Таймаут на пинг
		_, err := client.Ping(pingCtx).Result()
		pingCancel() // Отменяем контекст этой попытки

		if err == nil {
			zap.L().Info("Successfully connected and pinged Redis", zap.Int("attempt", attempt))
			return client, nil // Успех!
		}

		// <<< Закрываем неудачный клиент перед следующей попыткой >>>
		client.Close()
		lastErr = fmt.Errorf("unable to ping redis (attempt %d/%d): %w", attempt, maxRetries, err)
		zap.L().Warn("Redis ping failed, retrying...",
			zap.Int("attempt", attempt),
			zap.Int("max_retries", maxRetries),
			zap.Error(err),
		)
		if i < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}

	// Если все попытки не удались
	// client уже будет закрыт после последней неудачной попытки
	zap.L().Error("Failed to connect to Redis after all retries", zap.Int("attempts", maxRetries), zap.Error(lastErr))
	return nil, fmt.Errorf("failed to connect to redis after %d attempts: %w", maxRetries, lastErr)
}

// ginZapLogger returns a gin.HandlerFunc (middleware) that logs requests using zap.
func ginZapLogger(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)
		clientIP := c.ClientIP()
		method := c.Request.Method
		statusCode := c.Writer.Status()
		errorMessage := c.Errors.ByType(gin.ErrorTypePrivate).String()

		fields := []zapcore.Field{
			zap.Int("status", statusCode),
			zap.String("method", method),
			zap.String("path", path),
			zap.String("query", query),
			zap.String("ip", clientIP),
			zap.Duration("latency", latency),
			zap.String("user-agent", c.Request.UserAgent()),
		}
		if errorMessage != "" {
			fields = append(fields, zap.String("error", errorMessage))
		}

		if statusCode >= http.StatusInternalServerError {
			logger.Error("Request handled", fields...)
		} else if statusCode >= http.StatusBadRequest {
			logger.Warn("Request handled", fields...)
		} else {
			logger.Info("Request handled", fields...)
		}
	}
}
