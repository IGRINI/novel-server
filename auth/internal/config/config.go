package config

import (
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

// Config holds the application configuration.
type Config struct {
	Env        string `envconfig:"ENV" default:"development"`
	LogLevel   string `envconfig:"LOG_LEVEL" default:"debug"`
	ServerPort string `envconfig:"SERVER_PORT" default:"8081"` // Default port for auth service

	// Database (assuming PostgreSQL for users)
	DBHost     string `envconfig:"DB_HOST" required:"true"`
	DBPort     string `envconfig:"DB_PORT" required:"true"`
	DBUser     string `envconfig:"DB_USER" required:"true"`
	DBPassword string `envconfig:"DB_PASSWORD" required:"true"`
	DBName     string `envconfig:"DB_NAME" required:"true"`
	DBSSLMode  string `envconfig:"DB_SSL_MODE" default:"disable"`

	// Redis (assuming Redis for tokens) - Add Redis connection details
	RedisAddr     string `envconfig:"REDIS_ADDR" default:"localhost:6379"` // Example Redis addr
	RedisPassword string `envconfig:"REDIS_PASSWORD" default:""`           // Example Redis password
	RedisDB       int    `envconfig:"REDIS_DB" default:"0"`                // Example Redis DB

	// JWT Settings
	JWTSecret       string        `envconfig:"JWT_SECRET" required:"true"`
	PasswordSalt    string        `envconfig:"PASSWORD_SALT" required:"true"`
	AccessTokenTTL  time.Duration `envconfig:"JWT_ACCESS_TOKEN_TTL" default:"15m"`
	RefreshTokenTTL time.Duration `envconfig:"JWT_REFRESH_TOKEN_TTL" default:"168h"` // 7 days

	// CORS Settings
	CORSAllowedOrigins string `envconfig:"CORS_ALLOWED_ORIGINS" default:"http://localhost:3000"` // Загружаем строку

	// Inter-service Communication
	ServiceID          string        `envconfig:"SERVICE_ID" default:"auth-service"`    // ID of this service
	InterServiceSecret string        `envconfig:"INTER_SERVICE_SECRET" required:"true"` // Secret for signing inter-service tokens
	InterServiceTTL    time.Duration `envconfig:"INTER_SERVICE_TTL" default:"5m"`       // TTL for inter-service tokens
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

// LoadConfig loads configuration from environment variables.
// It first tries to load a .env file from the project root if it exists.
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
	err := envconfig.Process("", &cfg)
	if err != nil {
		return nil, err
	}

	// Convert TTLs from minutes/hours in .env to time.Duration if needed
	// Note: envconfig handles time.Duration parsing directly (e.g., "15m", "168h")

	return &cfg, nil
}
