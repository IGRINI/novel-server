package config

import (
	"fmt"
	"log"

	"novel-server/shared/utils"

	"github.com/kelseyhightower/envconfig"
)

// Config содержит всю конфигурацию для WebSocket сервиса.
type Config struct {
	Server      ServerConfig
	RabbitMQ    RabbitMQConfig
	AuthService AuthServiceConfig
	// JWTSecret больше не нужен здесь, он будет в AuthServiceConfig
}

// ServerConfig содержит настройки HTTP сервера.
type ServerConfig struct {
	Port        string `envconfig:"PORT" default:"8083"`         // Основной порт для WebSocket
	MetricsPort string `envconfig:"METRICS_PORT" default:"9092"` // Порт для Prometheus метрик
}

// RabbitMQConfig содержит настройки для подключения к RabbitMQ.
type RabbitMQConfig struct {
	URL       string `envconfig:"RABBITMQ_URL" required:"true"`
	QueueName string `envconfig:"CLIENT_UPDATES_QUEUE_NAME" default:"client_updates"` // Имя очереди для получения обновлений
}

// AuthServiceConfig содержит настройки для взаимодействия с сервисом авторизации.
type AuthServiceConfig struct {
	URL       string `envconfig:"AUTH_SERVICE_URL" required:"true"`
	JWTSecret string // Загружается из секрета
}

// LoadConfig загружает конфигурацию из переменных окружения и секретов
func LoadConfig() (*Config, error) {
	var cfg Config
	err := envconfig.Process("", &cfg)
	if err != nil {
		return nil, fmt.Errorf("ошибка загрузки конфигурации: %w", err)
	}

	var loadErr error
	cfg.AuthService.JWTSecret, loadErr = utils.ReadSecret("jwt_secret")
	if loadErr != nil {
		return nil, loadErr
	}

	log.Printf("Конфигурация WebSocket сервиса загружена (секреты из файлов):")
	log.Printf("  Port: %s", cfg.Server.Port)
	log.Printf("  RabbitMQ URL: %s", cfg.RabbitMQ.URL)
	log.Printf("  Client Updates Queue Name: %s", cfg.RabbitMQ.QueueName)
	log.Println("  JWT Secret: [ЗАГРУЖЕН]")

	return &cfg, nil
}
