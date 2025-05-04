package main

import (
	"context"
	"fmt"
	"net/http"
	"novel-server/auth/internal/config"
	"novel-server/auth/internal/handler"
	"novel-server/auth/internal/messaging"
	"novel-server/auth/internal/repository"
	"novel-server/auth/internal/service"
	"novel-server/shared/database"
	sharedLogger "novel-server/shared/logger"
	sharedMiddleware "novel-server/shared/middleware"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rabbitmq/amqp091-go"
	redis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	// <<< Rate Limit Imports >>>
	rateli "github.com/JGLTechnologies/gin-rate-limit"

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
		Level:    cfg.LogLevel,
		Encoding: "json",
	})
	if err != nil {
		fmt.Printf("Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	zap.ReplaceGlobals(logger)
	zap.L().Info("Logger initialized successfully", zap.String("logLevel", cfg.LogLevel))
	zap.L().Info("Configuration loaded")

	// --- External Connections ---
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

	// <<< ИЗМЕНЕНИЕ: Подключаемся к RabbitMQ с помощью функции connectRabbitMQ >>>
	mqConn, err := connectRabbitMQ(cfg.RabbitMQURL, logger)
	if err != nil {
		zap.L().Fatal("Failed to connect to RabbitMQ", zap.Error(err))
	}
	defer mqConn.Close()
	zap.L().Info("Connected to RabbitMQ")

	// --- Dependency Injection ---
	userRepo := database.NewPgUserRepository(pgPool, logger.Named("PgUserRepo"))
	tokenRepo := database.NewRedisTokenRepository(redisClient, logger.Named("RedisTokenRepo"))
	deviceTokenRepo := repository.NewDeviceTokenRepository(pgPool, logger.Named("PgDeviceTokenRepo"))
	deviceTokenService := service.NewDeviceTokenService(deviceTokenRepo, logger.Named("DeviceTokenService"))
	authSvc := service.NewAuthService(userRepo, tokenRepo, cfg, logger.Named("AuthService"))

	// <<< Rate Limiter Middleware Setup >>>
	// Initialize Redis store for rate limiter
	// Rate: 10 requests per minute per IP
	rateLimitStore := rateli.RedisStore(&rateli.RedisOptions{
		RedisClient: redisClient, // Pass the existing client
		Rate:        time.Minute,
		Limit:       10,
		// Prefix:    "rate_limit:auth:", // Prefix seems not supported by RedisStore factory
	})

	// Create rate limit middleware using the store
	rateLimitMiddleware := rateli.RateLimiter(rateLimitStore, &rateli.Options{
		ErrorHandler: func(c *gin.Context, info rateli.Info) {
			zap.L().Warn("Rate limit exceeded",
				zap.String("clientIP", c.ClientIP()),
				zap.Time("resetTime", info.ResetTime),
				zap.String("path", c.Request.URL.Path),
			)
			c.String(http.StatusTooManyRequests, "Too many requests. Try again in "+time.Until(info.ResetTime).String())
		},
		KeyFunc: func(c *gin.Context) string {
			return c.ClientIP() // Use client IP as the key
		},
	})
	zap.L().Info("Rate limiter middleware initialized")
	// <<< End Rate Limiter Setup >>>

	authHandler := handler.NewAuthHandler(authSvc, userRepo, deviceTokenService, cfg)

	// <<< Инициализация Consumer'а >>>
	tokenDeletionConsumer, err := messaging.NewTokenDeletionConsumer(mqConn, deviceTokenService, logger)
	if err != nil {
		zap.L().Fatal("Failed to create TokenDeletionConsumer", zap.Error(err))
	}

	// --- HTTP Server Setup (Gin) ---
	gin.SetMode(gin.ReleaseMode)
	if cfg.Env == "development" {
		gin.SetMode(gin.DebugMode)
	}

	router := gin.New()
	router.RedirectTrailingSlash = true
	router.Use(sharedMiddleware.GinZapLogger(logger))
	router.Use(gin.Recovery())

	// <<< Возвращаем Prometheus Middleware >>>
	p := ginprometheus.NewPrometheus("gin") // Префикс для метрик (например, gin_request_...)
	// <<< ПОКА НЕ ПРИМЕНЯЕМ ЗДЕСЬ >>>
	// p.Use(router)

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
	authHandler.RegisterRoutes(router, rateLimitMiddleware)

	// <<< ПРИМЕНЯЕМ Prometheus Middleware ПОСЛЕ регистрации роутов >>>
	p.Use(router)

	// --- Start Background Workers (Consumers) ---
	go func() {
		zap.L().Info("Starting TokenDeletionConsumer...")
		if err := tokenDeletionConsumer.StartConsuming(); err != nil {
			// Логируем ошибку, если consumer остановился не штатно.
			// В production, возможно, стоит паниковать или пытаться перезапустить.
			zap.L().Error("TokenDeletionConsumer stopped with error", zap.Error(err))
			// TODO: Рассмотреть стратегию обработки ошибок consumer'а (например, остановка сервиса)
		} else {
			zap.L().Info("TokenDeletionConsumer stopped gracefully.")
		}
	}()

	// --- Start HTTP Server ---
	srv := &http.Server{
		Addr:         ":" + cfg.ServerPort,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	zap.L().Info("Starting HTTP server", zap.String("port", cfg.ServerPort))

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			zap.L().Fatal("HTTP Server listen error", zap.Error(err))
		}
	}()

	// --- Graceful Shutdown ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	zap.L().Info("Shutting down server...")

	// <<< Останавливаем Consumer перед HTTP сервером >>>
	zap.L().Info("Stopping TokenDeletionConsumer...")
	if err := tokenDeletionConsumer.Stop(); err != nil {
		zap.L().Error("Error stopping TokenDeletionConsumer", zap.Error(err))
		// Не фатальная ошибка для shutdown, просто логируем
	}

	// Останавливаем HTTP сервер
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		zap.L().Error("HTTP Server forced to shutdown", zap.Error(err))
		// Ранее было Fatal, но лучше дать шанс закрыть другие ресурсы
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
	poolConfig.MaxConns = int32(cfg.DBMaxConns)
	poolConfig.MaxConnIdleTime = cfg.DBIdleTimeout

	var pool *pgxpool.Pool
	var lastErr error
	maxRetries := 50
	retryDelay := 3 * time.Second

	zap.L().Info("Attempting to connect to PostgreSQL", zap.Int("max_retries", maxRetries), zap.Duration("retry_delay", retryDelay))

	for i := 0; i < maxRetries; i++ {
		attempt := i + 1
		connectCtx, connectCancel := context.WithTimeout(context.Background(), 5*time.Second)
		pool, err = pgxpool.NewWithConfig(connectCtx, poolConfig)
		connectCancel()

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
			continue
		}

		pingCtx, pingCancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = pool.Ping(pingCtx)
		pingCancel()

		if err == nil {
			zap.L().Info("Successfully connected and pinged PostgreSQL", zap.Int("attempt", attempt))
			return pool, nil
		}

		pool.Close()
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

	zap.L().Error("Failed to connect to PostgreSQL after all retries", zap.Int("attempts", maxRetries), zap.Error(lastErr))
	return nil, fmt.Errorf("failed to connect to postgres after %d attempts: %w", maxRetries, lastErr)
}

// setupRedis initializes the Redis client with retry logic.
func setupRedis(ctx context.Context, cfg *config.Config) (*redis.Client, error) {
	zap.L().Debug("Setting up Redis connection...")
	redisOpts := &redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	}
	zap.L().Info("Redis connection options configured", zap.String("address", redisOpts.Addr), zap.Int("db", redisOpts.DB))

	var client *redis.Client
	var lastErr error
	maxRetries := 50
	retryDelay := 3 * time.Second

	zap.L().Info("Attempting to connect and ping Redis", zap.Int("max_retries", maxRetries), zap.Duration("retry_delay", retryDelay))

	for i := 0; i < maxRetries; i++ {
		attempt := i + 1
		client = redis.NewClient(redisOpts)

		pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := client.Ping(pingCtx).Result()
		pingCancel()

		if err == nil {
			zap.L().Info("Successfully connected and pinged Redis", zap.Int("attempt", attempt))
			return client, nil
		}

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

	zap.L().Error("Failed to connect to Redis after all retries", zap.Int("attempts", maxRetries), zap.Error(lastErr))
	return nil, fmt.Errorf("failed to connect to redis after %d attempts: %w", maxRetries, lastErr)
}

// connectRabbitMQ пытается подключиться к RabbitMQ с несколькими попытками
// (Скопировано из gameplay-service/cmd/server/main.go)
func connectRabbitMQ(url string, logger *zap.Logger) (*amqp091.Connection, error) {
	var conn *amqp091.Connection
	var err error
	maxRetries := 50
	retryDelay := 5 * time.Second // Используем задержку 5 секунд как в gameplay-service
	logger.Info("Attempting to connect to RabbitMQ",
		zap.String("url", maskRabbitMQURL(url)), // Логируем URL без пароля (используем старую функцию маскировки)
		zap.Int("max_retries", maxRetries),
		zap.Duration("retry_delay", retryDelay),
	)
	for i := 0; i < maxRetries; i++ {
		attempt := i + 1
		conn, err = amqp091.Dial(url)
		if err == nil {
			logger.Info("Successfully connected to RabbitMQ",
				zap.Int("attempt", attempt),
				zap.Int("max_attempts", maxRetries),
			)
			// Добавляем обработчик закрытия соединения для логирования (оставляем из старой версии auth)
			go func() {
				notifyClose := make(chan *amqp091.Error)
				conn.NotifyClose(notifyClose)
				err := <-notifyClose
				if err != nil {
					logger.Error("RabbitMQ connection closed unexpectedly", zap.Error(err))
					// TODO: Рассмотреть механизм переподключения или остановки сервиса
				} else {
					logger.Info("RabbitMQ connection closed gracefully.")
				}
			}()
			return conn, nil
		}
		logger.Warn("RabbitMQ connection failed, retrying...",
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", maxRetries),
			zap.Duration("retry_delay", retryDelay),
			zap.Error(err),
		)
		time.Sleep(retryDelay)
	}
	logger.Error("Failed to connect to RabbitMQ after all retries", zap.Int("attempts", maxRetries), zap.Error(err))
	return nil, fmt.Errorf("failed to connect to RabbitMQ after %d attempts: %w", maxRetries, err)
}

// maskRabbitMQURL маскирует пароль в URL для логирования (оставляем эту функцию, так как она используется в connectRabbitMQ выше)
func maskRabbitMQURL(urlStr string) string {
	// Простой парсинг, чтобы найти @ и //
	atIndex := -1
	schemaIndex := -1
	for i := 0; i < len(urlStr); i++ {
		if urlStr[i] == '@' {
			atIndex = i
			break
		}
	}
	for i := 0; i+1 < len(urlStr); i++ {
		if urlStr[i] == ':' && urlStr[i+1] == '/' && urlStr[i+2] == '/' {
			schemaIndex = i + 2
			break
		}
	}

	if atIndex != -1 && schemaIndex != -1 && atIndex > schemaIndex+1 {
		return urlStr[:schemaIndex+1] + "//****:****@" + urlStr[atIndex+1:]
	}
	return urlStr // Возвращаем как есть, если формат не стандартный
}
