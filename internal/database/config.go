package database

import (
	"fmt"
	"os"
)

// Config представляет конфигурацию подключения к базе данных
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string
}

// NewConfig создает новую конфигурацию из переменных окружения
func NewConfig() *Config {
	return &Config{
		Host:     getEnvOrDefault("DATABASE_HOST", "localhost"),
		Port:     getEnvAsIntOrDefault("DATABASE_PORT", 5432),
		User:     getEnvOrDefault("DATABASE_USER", "postgres"),
		Password: getEnvOrDefault("DATABASE_PASSWORD", "postgres"),
		DBName:   getEnvOrDefault("DATABASE_NAME", "novel_db"),
		SSLMode:  "disable", // В .env не указан, оставляем по умолчанию
	}
}

// ConnectionString возвращает строку подключения к базе данных
func (c *Config) ConnectionString() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.DBName, c.SSLMode,
	)
}

// getEnvOrDefault возвращает значение переменной окружения или значение по умолчанию
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvAsIntOrDefault возвращает значение переменной окружения как целое число или значение по умолчанию
func getEnvAsIntOrDefault(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		var result int
		if _, err := fmt.Sscanf(value, "%d", &result); err == nil {
			return result
		}
	}
	return defaultValue
}
