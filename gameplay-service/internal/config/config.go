package config

import (
	"fmt"
	"log"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Config содержит конфигурацию для Gameplay Service
type Config struct {
	// Настройки сервера
	Port string `envconfig:"PORT" default:"8082"` // Пример порта

	// Настройки PostgreSQL
	DBHost        string        `envconfig:"DB_HOST" required:"true"`
	DBPort        string        `envconfig:"DB_PORT" default:"5432"`
	DBUser        string        `envconfig:"DB_USER" required:"true"`
	DBPassword    string        `envconfig:"DB_PASSWORD" required:"true"`
	DBName        string        `envconfig:"DB_NAME" required:"true"`
	DBSSLMode     string        `envconfig:"DB_SSL_MODE" default:"disable"`
	DBMaxConns    int           `envconfig:"DB_MAX_CONNECTIONS" default:"10"`
	DBIdleTimeout time.Duration `envconfig:"DB_MAX_IDLE_MINUTES" default:"5m"`

	// Настройки RabbitMQ
	RabbitMQURL              string `envconfig:"RABBITMQ_URL" required:"true"`
	GenerationTaskQueue      string `envconfig:"GENERATION_TASK_QUEUE" default:"story_generation_tasks"`
	InternalUpdatesQueueName string `envconfig:"INTERNAL_UPDATES_QUEUE_NAME" default:"internal_updates"`
	ClientUpdatesQueueName   string `envconfig:"CLIENT_UPDATES_QUEUE_NAME" default:"client_updates"`

	// Настройки JWT (для проверки токена пользователя в middleware)
	JWTSecret string `envconfig:"JWT_SECRET" required:"true"`
}

// GetDSN возвращает строку подключения (DSN) для PostgreSQL
func (c *Config) GetDSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName, c.DBSSLMode)
}

// LoadConfig загружает конфигурацию из переменных окружения
func LoadConfig() (*Config, error) {
	var cfg Config
	err := envconfig.Process("", &cfg)
	if err != nil {
		return nil, fmt.Errorf("ошибка загрузки конфигурации gameplay-service: %w", err)
	}

	log.Printf("Конфигурация Gameplay Service загружена:")
	log.Printf("  Port: %s", cfg.Port)
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
