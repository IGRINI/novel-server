package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"novel-server/shared/utils"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

// Config holds the application configuration.
type Config struct {
	Env         string `envconfig:"APP_ENV" default:"development"`
	ServerPort  string `envconfig:"SERVER_PORT" default:"8081"`
	LogLevel    string `envconfig:"LOG_LEVEL" default:"info"`
	RabbitMQURL string `envconfig:"RABBITMQ_URL" default:"amqp://guest:guest@localhost:5672/"`
	// <<< Добавляем ServiceID >>>
	ServiceID string `envconfig:"SERVICE_ID" default:"auth-service"`

	// Database settings
	DBHost    string `envconfig:"DB_HOST" default:"localhost"`
	DBPort    string `envconfig:"DB_PORT" default:"5432"`
	DBUser    string `envconfig:"DB_USER" default:"postgres"`
	DBName    string `envconfig:"DB_NAME" default:"novel_db"`
	DBSSLMode string `envconfig:"DB_SSL_MODE" default:"disable"`
	// <<< Добавляем параметры пула (как в story-generator) >>>
	DBMaxConns    int           `envconfig:"DB_MAX_CONNECTIONS" default:"10"`
	DBIdleTimeout time.Duration `envconfig:"DB_MAX_IDLE_MINUTES" default:"5m"`
	// Секретное поле БЕЗ envconfig тега
	DBPassword string

	// Redis settings
	RedisAddr     string `envconfig:"REDIS_ADDR" default:"localhost:6379"`
	RedisPassword string `envconfig:"REDIS_PASSWORD" default:""`
	RedisDB       int    `envconfig:"REDIS_DB" default:"0"`

	// JWT settings
	// <<< Переименовываем поля TTL >>>
	AccessTokenTTL  time.Duration `envconfig:"ACCESS_TOKEN_TTL" default:"15m"`
	RefreshTokenTTL time.Duration `envconfig:"REFRESH_TOKEN_TTL" default:"720h"` // 720h = 30 days
	// Секретные поля БЕЗ envconfig тега
	JWTSecret string
	// <<< Переименовываем Salt обратно в Pepper >>>
	PasswordPepper string

	// Inter-service communication
	InterServiceSecret string `envconfig:"INTER_SERVICE_SECRET" default:""`
	// <<< Добавляем InterServiceTokenTTL >>>
	InterServiceTokenTTL time.Duration `envconfig:"INTER_SERVICE_TOKEN_TTL" default:"1h"`

	// CORS
	CORSAllowedOrigins string `envconfig:"CORS_ALLOWED_ORIGINS" default:""` // Comma-separated list
}

// GetAllowedOrigins parses the comma-separated CORS_ALLOWED_ORIGINS string.
func (c *Config) GetAllowedOrigins() []string {
	if c.CORSAllowedOrigins == "" {
		return nil
	}
	return strings.Split(c.CORSAllowedOrigins, ",")
}

// LoadConfig loads configuration from environment variables and secrets.
// envFilePath is optional, primarily for local development.
func LoadConfig(envFilePath ...string) (*Config, error) {
	var cfg Config

	// <<< Возвращаем старую логику загрузки .env >>>
	if len(envFilePath) > 0 && envFilePath[0] != "" {
		if _, err := os.Stat(envFilePath[0]); err == nil {
			err = godotenv.Load(envFilePath[0])
			if err != nil {
				log.Printf("Warning: Could not load %s file: %v", envFilePath[0], err)
			} else {
				log.Printf("Loaded configuration from %s", envFilePath[0])
			}
		} else if !os.IsNotExist(err) {
			log.Printf("Warning: Error checking %s file: %v", envFilePath[0], err)
		}
	}

	// Load non-secret values from environment variables
	err := envconfig.Process("", &cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to process env config: %w", err)
	}

	// Load REQUIRED secrets (from files or env vars using ReadSecret)
	var loadErr error
	cfg.DBPassword, loadErr = utils.ReadSecret("db_password")
	if loadErr != nil {
		return nil, loadErr
	}
	cfg.JWTSecret, loadErr = utils.ReadSecret("jwt_secret")
	if loadErr != nil {
		return nil, loadErr
	}
	// <<< Читаем секрет password_pepper >>>
	cfg.PasswordPepper, loadErr = utils.ReadSecret("password_pepper")
	if loadErr != nil {
		return nil, loadErr
	}

	// <<< Возвращаем старую логику для опционального INTER_SERVICE_SECRET >>>
	cfg.InterServiceSecret, loadErr = utils.ReadSecret("inter_service_secret")
	if loadErr != nil {
		// Если INTER_SERVICE_SECRET не найден, это не фатальная ошибка,
		// но может потребоваться для работы InternalAuthMiddleware.
		// Оставляем значение по умолчанию из envconfig.
		log.Printf("Warning: Could not read optional secret 'inter_service_secret': %v. Using default/env value: '%s'", loadErr, cfg.InterServiceSecret)
	}

	// Convert Redis DB from string if needed (env var might be string)
	redisDBStr := os.Getenv("REDIS_DB")
	if redisDBStr != "" {
		redisDBInt, err := strconv.Atoi(redisDBStr)
		if err == nil {
			cfg.RedisDB = redisDBInt
		} else {
			// Используем fmt.Printf вместо log.Printf, т.к. логгер еще может быть не инициализирован
			fmt.Printf("Warning: invalid REDIS_DB format '%s', using default %d\n", redisDBStr, cfg.RedisDB)
		}
	}

	// <<< Обновляем логирование, добавляя новые параметры БД >>>
	fmt.Println("Configuration Loaded:")
	fmt.Printf("  APP_ENV: %s\n", cfg.Env)
	fmt.Printf("  SERVER_PORT: %s\n", cfg.ServerPort)
	fmt.Printf("  LOG_LEVEL: %s\n", cfg.LogLevel)
	fmt.Printf("  SERVICE_ID: %s\n", cfg.ServiceID)
	fmt.Printf("  DB_HOST: %s\n", cfg.DBHost)
	fmt.Printf("  DB_PORT: %s\n", cfg.DBPort)
	fmt.Printf("  DB_USER: %s\n", cfg.DBUser)
	fmt.Printf("  DB_NAME: %s\n", cfg.DBName)
	fmt.Printf("  DB_SSL_MODE: %s\n", cfg.DBSSLMode)
	fmt.Printf("  DB_MAX_CONNECTIONS: %d\n", cfg.DBMaxConns)
	fmt.Printf("  DB_MAX_IDLE_MINUTES: %v\n", cfg.DBIdleTimeout)
	fmt.Printf("  DB_PASSWORD: [LOADED]\n")
	fmt.Printf("  REDIS_ADDR: %s\n", cfg.RedisAddr)
	fmt.Printf("  REDIS_DB: %d\n", cfg.RedisDB)
	fmt.Printf("  REDIS_PASSWORD: %s\n", maskSecret(cfg.RedisPassword))
	fmt.Printf("  ACCESS_TOKEN_TTL: %v\n", cfg.AccessTokenTTL)
	fmt.Printf("  REFRESH_TOKEN_TTL: %v\n", cfg.RefreshTokenTTL)
	fmt.Printf("  JWT_SECRET: [LOADED]\n")
	fmt.Printf("  PASSWORD_PEPPER: [LOADED]\n") // Используем Pepper
	fmt.Printf("  INTER_SERVICE_SECRET: %s\n", maskSecret(cfg.InterServiceSecret))
	fmt.Printf("  INTER_SERVICE_TOKEN_TTL: %v\n", cfg.InterServiceTokenTTL)
	fmt.Printf("  CORS_ALLOWED_ORIGINS: %s\n", cfg.CORSAllowedOrigins)

	return &cfg, nil
}

// maskSecret returns "[LOADED]" if the secret is not empty, otherwise "[NOT SET]".
func maskSecret(secret string) string {
	if secret != "" {
		return "[LOADED]"
	}
	return "[NOT SET]"
}
