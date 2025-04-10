package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config содержит конфигурацию приложения
type Config struct {
	Environment string
	Server      ServerConfig
	Database    DatabaseConfig
	AI          AIConfig
	CORS        CORSConfig
	JWT         JWTConfig
}

// ServerConfig содержит конфигурацию сервера
type ServerConfig struct {
	Port                int
	BasePath            string
	ReadTimeoutSeconds  int
	WriteTimeoutSeconds int
	IdleTimeoutSeconds  int
}

// DatabaseConfig содержит конфигурацию базы данных
type DatabaseConfig struct {
	Host               string
	Port               int
	User               string
	Password           string
	Name               string
	SSLMode            string
	MaxConnections     int
	MaxConnIdleMinutes int
}

// AIConfig содержит конфигурацию для AI API
type AIConfig struct {
	APIKey      string
	Model       string
	BaseURL     string
	Timeout     int
	MaxAttempts int
}

// CORSConfig содержит конфигурацию CORS
type CORSConfig struct {
	AllowedOrigins []string
}

// JWTConfig содержит конфигурацию JWT
type JWTConfig struct {
	Secret          string
	PasswordSalt    string
	AccessTokenTTL  int // Время жизни access токена в минутах
	RefreshTokenTTL int // Время жизни refresh токена в часах
}

// Load загружает конфигурацию из переменных окружения
func Load(env string) (Config, error) {
	cfg := Config{
		Environment: env,
		Server: ServerConfig{
			Port:                getEnvInt("SERVER_PORT", 8080),
			BasePath:            getEnvStr("SERVER_BASE_PATH", "/api"),
			ReadTimeoutSeconds:  getEnvInt("SERVER_READ_TIMEOUT", 15),
			WriteTimeoutSeconds: getEnvInt("SERVER_WRITE_TIMEOUT", 15),
			IdleTimeoutSeconds:  getEnvInt("SERVER_IDLE_TIMEOUT", 60),
		},
		Database: DatabaseConfig{
			Host:               getEnvStr("DB_HOST", "localhost"),
			Port:               getEnvInt("DB_PORT", 5432),
			User:               getEnvStr("DB_USER", "postgres"),
			Password:           getEnvStr("DB_PASSWORD", "postgres"),
			Name:               getEnvStr("DB_NAME", "novel"),
			SSLMode:            getEnvStr("DB_SSL_MODE", "disable"),
			MaxConnections:     getEnvInt("DB_MAX_CONNECTIONS", 10),
			MaxConnIdleMinutes: getEnvInt("DB_MAX_IDLE_MINUTES", 5),
		},
		AI: AIConfig{
			APIKey:      getEnvStr("AI_API_KEY", ""),
			Model:       getEnvStr("AI_MODEL", "gpt-3.5-turbo"),
			BaseURL:     getEnvStr("AI_BASE_URL", "https://api.openai.com/v1"),
			Timeout:     getEnvInt("AI_TIMEOUT", 60),
			MaxAttempts: getEnvInt("AI_MAX_ATTEMPTS", 3),
		},
		CORS: CORSConfig{
			AllowedOrigins: strings.Split(getEnvStr("CORS_ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:8080"), ","),
		},
		JWT: JWTConfig{
			Secret:          getEnvStr("JWT_SECRET", "your-256-bit-secret"),
			PasswordSalt:    getEnvStr("PASSWORD_SALT", "default-password-salt"),
			AccessTokenTTL:  getEnvInt("JWT_ACCESS_TOKEN_TTL", 60),
			RefreshTokenTTL: getEnvInt("JWT_REFRESH_TOKEN_TTL", 168), // 7 дней
		},
	}

	// Проверка обязательных настроек
	if cfg.AI.APIKey == "" {
		return cfg, fmt.Errorf("AI_API_KEY not set")
	}

	return cfg, nil
}

// getEnvStr возвращает строковое значение из переменной окружения или значение по умолчанию
func getEnvStr(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

// getEnvInt возвращает целочисленное значение из переменной окружения или значение по умолчанию
func getEnvInt(key string, defaultValue int) int {
	if value, exists := os.LookupEnv(key); exists {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}
