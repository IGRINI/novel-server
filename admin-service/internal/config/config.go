package config

import (
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
	"go.uber.org/zap"
)

// Config хранит конфигурацию сервиса админки
type Config struct {
	Env             string
	ServerPort      string
	LogLevel        string
	JWTSecret       string
	AuthServiceURL  string
	ClientTimeout   time.Duration
	InterServiceSecret string
	ServiceID         string
	// Добавьте другие нужные параметры
}

// LoadConfig загружает конфигурацию из переменных окружения
func LoadConfig(logger *zap.Logger) (*Config, error) {
	_ = godotenv.Load() // Загружаем .env, игнорируем ошибку

	port := os.Getenv("ADMIN_SERVER_PORT")
	if port == "" {
		port = "8084" // Порт по умолчанию
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		logger.Error("Переменная окружения JWT_SECRET не установлена")
		return nil, fmt.Errorf("JWT_SECRET is not set")
	}

	authServiceURL := os.Getenv("AUTH_SERVICE_URL")
	if authServiceURL == "" {
		authServiceURL = "http://auth-service:8081"
		logger.Warn("AUTH_SERVICE_URL not set, using default", zap.String("url", authServiceURL))
	}

	clientTimeoutStr := getEnv("HTTP_CLIENT_TIMEOUT", "10s")
	clientTimeout, err := time.ParseDuration(clientTimeoutStr)
	if err != nil {
		logger.Warn("Invalid HTTP_CLIENT_TIMEOUT format, using default 10s", zap.String("value", clientTimeoutStr), zap.Error(err))
		clientTimeout = 10 * time.Second
	}

	cfg := &Config{
		Env:             getEnv("ENV", "development"),
		ServerPort:      port,
		LogLevel:        getEnv("LOG_LEVEL", "debug"),
		JWTSecret:       jwtSecret,
		AuthServiceURL:  authServiceURL,
		ClientTimeout:   clientTimeout,
		InterServiceSecret: getEnv("INTER_SERVICE_SECRET", ""),
		ServiceID:       getEnv("SERVICE_ID", "admin-service"),
	}

	secretLoaded := cfg.InterServiceSecret != ""
	logger.Info("Конфигурация Admin Service загружена",
		zap.String("env", cfg.Env),
		zap.String("port", cfg.ServerPort),
		zap.String("logLevel", cfg.LogLevel),
		zap.String("authServiceURL", cfg.AuthServiceURL),
		zap.Duration("clientTimeout", cfg.ClientTimeout),
		zap.Bool("interServiceSecretLoaded", secretLoaded),
		zap.String("serviceID", cfg.ServiceID),
	)

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
