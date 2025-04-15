package config

import (
	"fmt"
	"log"
	"time"

	"novel-server/shared/utils"

	"github.com/kelseyhightower/envconfig"
)

// Config содержит конфигурацию для Gameplay Service
type Config struct {
	// Настройки сервера
	Port     string `envconfig:"GAMEPLAY_SERVER_PORT" default:"8082"`
	LogLevel string `envconfig:"LOG_LEVEL" default:"info"`

	// Настройки PostgreSQL
	DBHost        string        `envconfig:"DB_HOST" required:"true"`
	DBPort        string        `envconfig:"DB_PORT" default:"5432"`
	DBUser        string        `envconfig:"DB_USER" required:"true"`
	DBName        string        `envconfig:"DB_NAME" required:"true"`
	DBSSLMode     string        `envconfig:"DB_SSL_MODE" default:"disable"`
	DBMaxConns    int           `envconfig:"DB_MAX_CONNECTIONS" default:"10"`
	DBIdleTimeout time.Duration `envconfig:"DB_MAX_IDLE_MINUTES" default:"5m"`
	// Секретное поле БЕЗ envconfig тега
	DBPassword string

	// Настройки RabbitMQ
	RabbitMQURL              string `envconfig:"RABBITMQ_URL" required:"true"`
	GenerationTaskQueue      string `envconfig:"GENERATION_TASK_QUEUE" default:"story_generation_tasks"`
	InternalUpdatesQueueName string `envconfig:"INTERNAL_UPDATES_QUEUE_NAME" default:"internal_updates"`
	ClientUpdatesQueueName   string `envconfig:"CLIENT_UPDATES_QUEUE_NAME" default:"client_updates"`

	// Настройки JWT (для проверки токена пользователя в middleware)
	// Секретное поле БЕЗ envconfig тега
	JWTSecret string
}

// GetDSN возвращает строку подключения (DSN) для PostgreSQL
func (c *Config) GetDSN() string {
	// Пароль теперь в c.DBPassword
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName, c.DBSSLMode)
}

// LoadConfig загружает конфигурацию из переменных окружения и секретов
func LoadConfig() (*Config, error) {
	var cfg Config
	// Загружаем НЕсекретные переменные
	err := envconfig.Process("", &cfg)
	if err != nil {
		return nil, fmt.Errorf("ошибка загрузки конфигурации gameplay-service: %w", err)
	}

	// Загружаем ОБЯЗАТЕЛЬНЫЕ секреты
	var loadErr error
	cfg.DBPassword, loadErr = utils.ReadSecret("db_password")
	if loadErr != nil {
		return nil, loadErr
	}

	cfg.JWTSecret, loadErr = utils.ReadSecret("jwt_secret")
	if loadErr != nil {
		return nil, loadErr
	}

	log.Printf("Конфигурация Gameplay Service загружена (секреты из файлов):")
	log.Printf("  Port: %s", cfg.Port)
	log.Printf("  LogLevel: %s", cfg.LogLevel)
	log.Printf("  DB DSN: postgres://%s:***@%s:%s/%s?sslmode=%s", cfg.DBUser, cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBSSLMode)
	log.Printf("  DB Max Conns: %d", cfg.DBMaxConns)
	log.Printf("  DB Idle Timeout: %v", cfg.DBIdleTimeout)
	log.Printf("  RabbitMQ URL: %s", cfg.RabbitMQURL)
	log.Printf("  Generation Task Queue: %s", cfg.GenerationTaskQueue)
	log.Printf("  Internal Updates Queue Name: %s", cfg.InternalUpdatesQueueName)
	log.Printf("  Client Updates Queue Name: %s", cfg.ClientUpdatesQueueName)
	log.Println("  JWT Secret: [ЗАГРУЖЕН]")

	return &cfg, nil
}
