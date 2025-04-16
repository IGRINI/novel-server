package config

import (
	"fmt"
	"os"
	"time"

	"novel-server/shared/utils"

	"github.com/joho/godotenv"
	"go.uber.org/zap"
)

// Config хранит конфигурацию сервиса админки
type Config struct {
	Env                  string
	ServerPort           string
	LogLevel             string
	AuthServiceURL       string
	StoryGeneratorURL    string
	ClientTimeout        time.Duration
	InterServiceTokenTTL time.Duration
	AuthServiceTimeout   time.Duration
	ServiceID            string
	// Секреты без тегов
	JWTSecret          string
	InterServiceSecret string
}

// LoadConfig загружает конфигурацию из переменных окружения и секретов
func LoadConfig(logger *zap.Logger) (*Config, error) {
	_ = godotenv.Load() // Загружаем .env, игнорируем ошибку

	port := getEnv("ADMIN_SERVER_PORT", "8084")

	// Используем utils.ReadSecret
	jwtSecret, err := utils.ReadSecret("jwt_secret")
	if err != nil {
		logger.Error("Не удалось прочитать JWT_SECRET из секрета Docker", zap.Error(err))
		return nil, err
	}

	interServiceSecret, err := utils.ReadSecret("inter_service_secret")
	if err != nil {
		logger.Error("Не удалось прочитать INTER_SERVICE_SECRET из секрета Docker", zap.Error(err))
		return nil, err
	}

	authServiceURL := getEnv("AUTH_SERVICE_URL", "http://auth-service:8081")
	storyGeneratorURL := getEnv("STORY_GENERATOR_URL", "http://story-generator:8083")

	clientTimeoutStr := getEnv("HTTP_CLIENT_TIMEOUT", "10s")
	clientTimeout, err := time.ParseDuration(clientTimeoutStr)
	if err != nil {
		logger.Warn("Invalid HTTP_CLIENT_TIMEOUT format, using default 10s", zap.String("value", clientTimeoutStr), zap.Error(err))
		clientTimeout = 10 * time.Second
	}

	logger.Info("Client timeout", zap.Duration("clientTimeout", clientTimeout))

	cfg := &Config{
		Env:                  getEnv("ENV", "development"),
		ServerPort:           port,
		LogLevel:             getEnv("LOG_LEVEL", "debug"),
		JWTSecret:            jwtSecret,
		AuthServiceURL:       authServiceURL,
		StoryGeneratorURL:    storyGeneratorURL,
		ClientTimeout:        clientTimeout,
		InterServiceSecret:   interServiceSecret,
		InterServiceTokenTTL: getDurationEnv("INTER_SERVICE_TOKEN_TTL", "1h"),
		AuthServiceTimeout:   getDurationEnv("AUTH_SERVICE_TIMEOUT", "5s"),
		ServiceID:            getEnv("SERVICE_ID", "admin-service"),
	}

	logger.Info("Конфигурация Admin Service загружена (секреты из файлов)",
		zap.String("env", cfg.Env),
		zap.String("port", cfg.ServerPort),
		zap.String("logLevel", cfg.LogLevel),
		zap.String("authServiceURL", cfg.AuthServiceURL),
		zap.String("storyGeneratorURL", cfg.StoryGeneratorURL),
		zap.Duration("clientTimeout", cfg.ClientTimeout),
		zap.Bool("jwtSecretLoaded", cfg.JWTSecret != ""),
		zap.Bool("interServiceSecretLoaded", cfg.InterServiceSecret != ""),
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

func getDurationEnv(key string, fallback string) time.Duration {
	if value, ok := os.LookupEnv(key); ok {
		duration, err := time.ParseDuration(value)
		if err != nil {
			fmt.Printf("Invalid %s format, using default %s\n", key, fallback)
			return time.Duration(0)
		}
		return duration
	}
	duration, err := time.ParseDuration(fallback)
	if err != nil {
		fmt.Printf("Invalid fallback format for %s, using default 0s\n", fallback)
		return time.Duration(0)
	}
	return duration
}
