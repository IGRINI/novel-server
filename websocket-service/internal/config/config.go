package config

import (
	"fmt"
	"log"

	"github.com/kelseyhightower/envconfig"
)

// Config содержит конфигурацию для WebSocket сервиса
type Config struct {
	Port                   string `envconfig:"WEBSOCKET_SERVER_PORT" default:"8083"`
	RabbitMQURL            string `envconfig:"RABBITMQ_URL" required:"true"`
	ClientUpdatesQueueName string `envconfig:"CLIENT_UPDATES_QUEUE_NAME" default:"client_updates"`
	JWTSecret              string `envconfig:"JWT_SECRET" required:"true"`
}

// LoadConfig загружает конфигурацию из переменных окружения
func LoadConfig() (*Config, error) {
	var cfg Config
	err := envconfig.Process("", &cfg)
	if err != nil {
		return nil, fmt.Errorf("ошибка загрузки конфигурации: %w", err)
	}

	// Логируем загруженную конфигурацию (кроме секретов)
	log.Printf("Конфигурация WebSocket сервиса загружена:")
	log.Printf("  Port: %s", cfg.Port)
	log.Printf("  RabbitMQ URL: %s", cfg.RabbitMQURL)
	log.Printf("  Client Updates Queue Name: %s", cfg.ClientUpdatesQueueName)
	log.Println("  JWT Secret: [ЗАГРУЖЕН]")

	return &cfg, nil
}
