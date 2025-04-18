package config

import (
	"fmt"
	"log"

	"github.com/ilyakaznacheev/cleanenv"
)

type Config struct {
	RabbitMQ          RabbitMQConfig
	FCM               FCMConfig
	APNS              APNSConfig
	TokenService      TokenServiceConfig
	Log               LogConfig
	PushQueueName     string `yaml:"push_queue_name" env:"PUSH_QUEUE_NAME" env-default:"push_notifications"`
	WorkerConcurrency int    `yaml:"worker_concurrency" env:"WORKER_CONCURRENCY" env-default:"10"`
	HealthCheckPort   string `yaml:"health_check_port" env:"HEALTH_CHECK_PORT" env-default:"8088"`
}

type RabbitMQConfig struct {
	URI string `yaml:"uri" env:"RABBITMQ_URI" env-required:"true"`
}

type FCMConfig struct {
	CredentialsPath string `yaml:"credentials_path" env:"FCM_CREDENTIALS_PATH"` // Путь к файлу ключа сервис-аккаунта (рекомендуемый)
}

type APNSConfig struct {
	KeyID   string `yaml:"key_id" env:"APNS_KEY_ID"`     // Required if APNS is used
	TeamID  string `yaml:"team_id" env:"APNS_TEAM_ID"`   // Required if APNS is used
	KeyPath string `yaml:"key_path" env:"APNS_KEY_PATH"` // Required if APNS is used
	Topic   string `yaml:"topic" env:"APNS_TOPIC"`       // Required if APNS is used
	// Production bool   `yaml:"production" env:"APNS_PRODUCTION" env-default:"false"` // Optional: APNS environment
}

type TokenServiceConfig struct {
	URL string `yaml:"url" env:"TOKEN_SERVICE_URL"` // Optional: URL of the token service
}

type LogConfig struct {
	Level string `yaml:"level" env:"LOG_LEVEL" env-default:"info"`
}

func LoadConfig() (*Config, error) {
	// TODO: Рассмотреть возможность чтения пути к конфигу из флага командной строки
	configPath := "config.yml" // Путь по умолчанию

	var cfg Config

	if err := cleanenv.ReadConfig(configPath, &cfg); err != nil {
		log.Printf("Предупреждение: не удалось прочитать файл конфигурации '%s': %v. Попытка чтения из переменных окружения.", configPath, err)
		// Если файл не найден или ошибка чтения, пытаемся загрузить только из env
		if err := cleanenv.ReadEnv(&cfg); err != nil {
			return nil, fmt.Errorf("ошибка загрузки конфигурации: %w", err)
		}
	}

	log.Printf("Конфигурация успешно загружена. RabbitMQ URI: %s, Push Queue: %s", cfg.RabbitMQ.URI, cfg.PushQueueName)

	return &cfg, nil
}
