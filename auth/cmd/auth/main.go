package main

import (
	"context"
	"fmt"
	"net/http"
	"novel-server/auth/internal/config"
	"novel-server/auth/internal/handler"
	"novel-server/auth/internal/service"
	"novel-server/shared/database"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	// --- Configuration ---
	cfg, err := config.LoadConfig("../../.env")
	if err != nil {
		fmt.Printf("Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// --- Logger Setup (zap) ---
	logger, err := setupZapLogger(cfg.LogLevel)
	if err != nil {
		fmt.Printf("Failed to initialize zap logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	zap.ReplaceGlobals(logger)
	zap.L().Info("Logger initialized successfully", zap.String("logLevel", cfg.LogLevel))
	zap.L().Info("Configuration loaded", zap.Any("config", cfg))

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
	userRepoLogger := logger.Named("PgUserRepo")
	tokenRepoLogger := logger.Named("RedisTokenRepo")
	authServiceLogger := logger.Named("AuthService")

	userRepo := database.NewPgUserRepository(pgPool, userRepoLogger)
	tokenRepo := database.NewRedisTokenRepository(redisClient, tokenRepoLogger)
	authService := service.NewAuthService(userRepo, tokenRepo, cfg, authServiceLogger)
	authHandler := handler.NewAuthHandler(authService, userRepo)

	// --- HTTP Server Setup (Gin) ---
	gin.SetMode(gin.ReleaseMode)
	if cfg.Env == "development" {
		gin.SetMode(gin.DebugMode)
	}

	router := gin.New()
	router.Use(ginZapLogger(logger))
	router.Use(gin.Recovery())

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
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

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

// setupZapLogger initializes a zap logger based on the configured level.
func setupZapLogger(logLevel string) (*zap.Logger, error) {
	level := zap.NewAtomicLevel()
	err := level.UnmarshalText([]byte(strings.ToLower(logLevel)))
	if err != nil {
		level.SetLevel(zap.InfoLevel)
		fmt.Printf("Invalid log level '%s', defaulting to 'info'\n", logLevel)
	}

	config := zap.NewDevelopmentConfig()
	config.Level = level

	return config.Build()
}

// setupPostgres initializes the PostgreSQL connection pool.
func setupPostgres(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	zap.L().Debug("Connecting to PostgreSQL...")
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBSSLMode)

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("unable to parse postgres config: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("unable to create postgres connection pool: %w", err)
	}

	zap.L().Debug("Pinging PostgreSQL...")
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("unable to ping postgres database: %w", err)
	}

	return pool, nil
}

// setupRedis initializes the Redis client.
func setupRedis(ctx context.Context, cfg *config.Config) (*redis.Client, error) {
	zap.L().Debug("Connecting to Redis...")
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	zap.L().Debug("Pinging Redis...")
	if _, err := client.Ping(ctx).Result(); err != nil {
		client.Close()
		return nil, fmt.Errorf("unable to ping redis: %w", err)
	}

	return client, nil
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
