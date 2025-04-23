package config

import (
	"log"

	"github.com/ilyakaznacheev/cleanenv"
	"github.com/joho/godotenv"

	"novel-server/shared/logger" // Для конфигурации логгера
)

// Config структура для хранения всей конфигурации приложения.
type Config struct {
	AppEnv     string `env:"APP_ENV" env-default:"development"`
	Logger     logger.Config
	RabbitMQ   RabbitMQConfig   // Конфигурация RabbitMQ
	SanaServer SanaServerConfig // Конфигурация SANA сервера
	Storage    StorageConfig    // <<< ДОБАВЛЕНО: Конфигурация S3/Minio
}

// RabbitMQConfig конфигурация для подключения к RabbitMQ.
type RabbitMQConfig struct {
	URL              string      `env:"RABBITMQ_URL" env-required:"true"`
	ConsumerName     string      `env:"RABBITMQ_CONSUMER_NAME" env-default:"image_generator_worker"`
	TaskQueue        QueueConfig `env-prefix:"RABBITMQ_CHARACTER_IMAGE_TASK_QUEUE_"`
	ResultQueueName  string      `env:"IMAGE_GENERATION_RESULT_QUEUE" env-default:"image_generation_results"`
	ResultExchange   string      `env:"RABBITMQ_RESULT_EXCHANGE" env-default:""`    // Опционально: exchange для результатов
	ResultRoutingKey string      `env:"RABBITMQ_RESULT_ROUTING_KEY" env-default:""` // Опционально: routing key для результатов
}

// QueueConfig настройки для конкретной очереди RabbitMQ.
type QueueConfig struct {
	Name       string `env:"NAME" env-required:"true"`
	Durable    bool   `env:"DURABLE" env-default:"true"`
	AutoDelete bool   `env:"AUTO_DELETE" env-default:"false"`
	Exclusive  bool   `env:"EXCLUSIVE" env-default:"false"`
	NoWait     bool   `env:"NO_WAIT" env-default:"false"`
}

// SanaServerConfig конфигурация для подключения к локальному SANA серверу.
type SanaServerConfig struct {
	BaseURL string `env:"SANA_SERVER_BASE_URL" env-required:"true"`
	Timeout int    `env:"SANA_SERVER_TIMEOUT_SEC" env-default:"120"` // Таймаут в секундах
}

// StorageConfig конфигурация для подключения к S3-совместимому хранилищу.
type StorageConfig struct {
	Endpoint        string `env:"STORAGE_ENDPOINT" env-required:"true"`
	AccessKeyID     string `env:"STORAGE_ACCESS_KEY_ID" env-required:"true"`
	SecretAccessKey string `env:"STORAGE_SECRET_ACCESS_KEY" env-required:"true"`
	UseSSL          bool   `env:"STORAGE_USE_SSL" env-default:"true"`
	BucketName      string `env:"STORAGE_BUCKET_NAME" env-required:"true"`
	Region          string `env:"STORAGE_REGION" env-default:"us-east-1"` // Регион может быть важен для некоторых S3 API
}

// Load загружает конфигурацию из переменных окружения и .env файла.
func Load() *Config {
	// Загружаем .env файл (игнорируем ошибку, если файла нет)
	_ = godotenv.Load()

	var cfg Config

	// Используем cleanenv для загрузки конфигурации
	if err := cleanenv.ReadEnv(&cfg); err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}

	// TODO: Можно добавить дополнительную валидацию или логику после загрузки

	return &cfg
}
