package config

import (
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"

	"novel-server/shared/logger" // Для конфигурации логгера
)

// Config структура для хранения всей конфигурации приложения.
type Config struct {
	AppEnv             string `envconfig:"APP_ENV" default:"development"`
	Logger             logger.Config
	RabbitMQ           RabbitMQConfig
	SanaServer         SanaServerConfig
	PushGatewayURL     string `envconfig:"PUSHGATEWAY_URL" required:"true"`
	ImageSavePath      string `envconfig:"IMAGE_SAVE_PATH" required:"true"`
	ImagePublicBaseURL string `envconfig:"IMAGE_PUBLIC_BASE_URL" required:"true"`
}

// RabbitMQConfig конфигурация для подключения к RabbitMQ.
type RabbitMQConfig struct {
	URL              string `envconfig:"RABBITMQ_URL" required:"true"`
	ConsumerName     string `envconfig:"RABBITMQ_CONSUMER_NAME" default:"image_generator_worker"`
	TaskQueue        QueueConfig
	ResultQueueName  string `envconfig:"IMAGE_GENERATION_RESULT_QUEUE" default:"image_generation_results"`
	ResultExchange   string `envconfig:"RABBITMQ_RESULT_EXCHANGE" default:""`
	ResultRoutingKey string `envconfig:"RABBITMQ_RESULT_ROUTING_KEY" default:""`
}

// QueueConfig настройки для конкретной очереди RabbitMQ.
type QueueConfig struct {
	Name       string `envconfig:"RABBITMQ_CHARACTER_IMAGE_TASK_QUEUE_NAME" required:"true"`
	Durable    bool   `envconfig:"RABBITMQ_CHARACTER_IMAGE_TASK_QUEUE_DURABLE" default:"true"`
	AutoDelete bool   `envconfig:"RABBITMQ_CHARACTER_IMAGE_TASK_QUEUE_AUTO_DELETE" default:"false"`
	Exclusive  bool   `envconfig:"RABBITMQ_CHARACTER_IMAGE_TASK_QUEUE_EXCLUSIVE" default:"false"`
	NoWait     bool   `envconfig:"RABBITMQ_CHARACTER_IMAGE_TASK_QUEUE_NO_WAIT" default:"false"`
}

// SanaServerConfig конфигурация для подключения к локальному SANA серверу.
type SanaServerConfig struct {
	BaseURL string        `envconfig:"SANA_SERVER_BASE_URL" required:"true"`
	Timeout time.Duration `envconfig:"SANA_SERVER_TIMEOUT_SEC" default:"120s"`
}

// Load загружает конфигурацию из переменных окружения и .env файла.
func Load() *Config {
	// Загружаем .env файл (игнорируем ошибку, если файла нет)
	_ = godotenv.Load()

	var cfg Config

	// <<< ДИАГНОСТИКА: Логируем переменные ПЕРЕД обработкой >>>
	log.Printf("[DIAG] Перед envconfig.Process: RABBITMQ_URL='%s', RABBITMQ_CHARACTER_IMAGE_TASK_QUEUE_NAME='%s'",
		os.Getenv("RABBITMQ_URL"), os.Getenv("RABBITMQ_CHARACTER_IMAGE_TASK_QUEUE_NAME"))

	// Используем envconfig для загрузки НЕсекретных переменных
	err := envconfig.Process("", &cfg)

	// <<< ДИАГНОСТИКА: Логируем ошибку envconfig и значения ПОСЛЕ >>>
	log.Printf("[DIAG] Ошибка envconfig.Process: %v", err)
	log.Printf("[DIAG] После envconfig.Process: cfg.RabbitMQ.URL='%s', cfg.RabbitMQ.TaskQueue.Name='%s'",
		cfg.RabbitMQ.URL, cfg.RabbitMQ.TaskQueue.Name)

	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}

	// <<< ДОБАВЛЕНО: Логирование загруженной конфигурации >>>
	log.Printf("Configuration loaded for Image Generator:")
	log.Printf("  App Env: %s", cfg.AppEnv)
	log.Printf("  Logger Level: %s", cfg.Logger.Level)
	log.Printf("  RabbitMQ URL: %s", cfg.RabbitMQ.URL)
	log.Printf("  RabbitMQ Consumer Name: %s", cfg.RabbitMQ.ConsumerName)
	log.Printf("  RabbitMQ Task Queue Name: %s", cfg.RabbitMQ.TaskQueue.Name)
	log.Printf("  RabbitMQ Result Queue Name: %s", cfg.RabbitMQ.ResultQueueName)
	log.Printf("  SANA Base URL: %s", cfg.SanaServer.BaseURL)
	log.Printf("  SANA Timeout: %v", cfg.SanaServer.Timeout)
	log.Printf("  Pushgateway URL: %s", cfg.PushGatewayURL)
	log.Printf("  Image Save Path: %s", cfg.ImageSavePath)
	log.Printf("  Image Public Base URL: %s", cfg.ImagePublicBaseURL)
	// Секретов здесь нет, поэтому не логируем
	// PromptStyleSuffix слишком длинный для логирования
	// log.Printf("  Prompt Style Suffix: %s", cfg.PromptStyleSuffix)

	return &cfg
}
