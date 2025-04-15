package config

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"novel-server/shared/utils"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

// Config holds the application configuration.
type Config struct {
	Env        string `envconfig:"ENV" default:"development"`
	LogLevel   string `envconfig:"LOG_LEVEL" default:"debug"`
	ServerPort string `envconfig:"SERVER_PORT" default:"8081"` // Default port for auth service

	// Database (assuming PostgreSQL for users)
	DBHost    string `envconfig:"DB_HOST" required:"true"`
	DBPort    string `envconfig:"DB_PORT" required:"true"`
	DBUser    string `envconfig:"DB_USER" required:"true"`
	DBName    string `envconfig:"DB_NAME" required:"true"`
	DBSSLMode string `envconfig:"DB_SSL_MODE" default:"disable"`
	// Секретное поле БЕЗ envconfig тега
	DBPassword string

	// Redis (assuming Redis for tokens) - Add Redis connection details
	RedisAddr string `envconfig:"REDIS_ADDR" default:"localhost:6379"` // Example Redis addr
	RedisDB   int    `envconfig:"REDIS_DB" default:"0"`                // Example Redis DB
	// Секретное поле БЕЗ envconfig тега (если пароль используется)
	RedisPassword string

	// JWT Settings - Секретные поля БЕЗ envconfig тегов
	JWTSecret       string
	PasswordPepper  string
	AccessTokenTTL  time.Duration `envconfig:"JWT_ACCESS_TOKEN_TTL" default:"15m"`
	RefreshTokenTTL time.Duration `envconfig:"JWT_REFRESH_TOKEN_TTL" default:"168h"` // 7 days

	// CORS Settings
	CORSAllowedOrigins string `envconfig:"CORS_ALLOWED_ORIGINS" default:"http://localhost:3000"` // Загружаем строку

	// Inter-service Communication - Секретное поле БЕЗ envconfig тега
	ServiceID            string `envconfig:"SERVICE_ID" default:"auth-service"` // ID of this service
	InterServiceSecret   string
	InterServiceTokenTTL time.Duration `envconfig:"INTER_SERVICE_TOKEN_TTL" envDefault:"1h"`
}

// GetAllowedOrigins splits the CORSAllowedOrigins string into a slice.
func (c *Config) GetAllowedOrigins() []string {
	if c.CORSAllowedOrigins == "" {
		return nil // Или вернуть дефолтный origin, если нужно
	}
	// Убираем пробелы и разбиваем по запятой
	origins := strings.Split(strings.ReplaceAll(c.CORSAllowedOrigins, " ", ""), ",")
	// Можно добавить дополнительную валидацию URL, если нужно
	return origins
}

// LoadConfig loads configuration from environment variables and secrets.
func LoadConfig(envFilePath string) (*Config, error) {
	// Attempt to load .env file relative to the auth service root or project root
	// Adjust path as necessary depending on where you run the service from
	if _, err := os.Stat(envFilePath); err == nil {
		err = godotenv.Load(envFilePath)
		if err != nil {
			log.Printf("Warning: Could not load %s file: %v", envFilePath, err)
			// Continue without .env file if it's just a warning
		} else {
			log.Printf("Loaded configuration from %s", envFilePath)
		}
	} else if !os.IsNotExist(err) {
		// Log other errors related to accessing the .env file
		log.Printf("Warning: Error checking %s file: %v", envFilePath, err)
	}

	var cfg Config
	// Загружаем НЕсекретные переменные из окружения
	err := envconfig.Process("", &cfg)
	if err != nil {
		return nil, fmt.Errorf("error processing env vars: %w", err)
	}

	// Загружаем ОБЯЗАТЕЛЬНЫЕ секреты из файлов
	var loadErr error
	cfg.DBPassword, loadErr = utils.ReadSecret("db_password")
	if loadErr != nil {
		return nil, loadErr
	}

	cfg.JWTSecret, loadErr = utils.ReadSecret("jwt_secret")
	if loadErr != nil {
		return nil, loadErr
	}

	cfg.PasswordPepper, loadErr = utils.ReadSecret("password_pepper")
	if loadErr != nil {
		return nil, loadErr
	}

	cfg.InterServiceSecret, loadErr = utils.ReadSecret("inter_service_secret")
	if loadErr != nil {
		return nil, loadErr
	}

	// Загружаем НЕОБЯЗАТЕЛЬНЫЕ секреты (например, пароль Redis)
	redisPass, err := utils.ReadSecret("redis_password")
	if err == nil {
		cfg.RedisPassword = redisPass
		log.Println("Redis password loaded from secret.")
	} else {
		// Если секрет не найден, просто оставляем поле пустым (поведение по умолчанию)
		log.Printf("Optional secret 'redis_password' not found or failed to read: %v. Assuming no password.", err)
	}

	log.Println("Configuration loaded successfully (secrets read from files).")
	return &cfg, nil
}
