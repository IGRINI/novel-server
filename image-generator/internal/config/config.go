package config

import (
	"log"

	"github.com/ilyakaznacheev/cleanenv"
	"github.com/joho/godotenv"

	"novel-server/shared/logger" // Для конфигурации логгера
)

// Config структура для хранения всей конфигурации приложения.
type Config struct {
	AppEnv             string `env:"APP_ENV" env-default:"development"`
	Logger             logger.Config
	RabbitMQ           RabbitMQConfig   // Конфигурация RabbitMQ
	SanaServer         SanaServerConfig // Конфигурация SANA сервера
	PushGatewayURL     string           `env:"PUSHGATEWAY_URL" env-required:"true"`                                                                                                                                                                                                                                                             // <<< ДОБАВЛЕНО: URL для Pushgateway
	PromptStyleSuffix  string           `env:"IMAGE_PROMPT_STYLE_SUFFIX" env-default:", a stylized portrait of a story character in moody, atmospheric lighting, with neon glow accents, soft shadows, minimal background, cohesive color grading, dark color palette, and subtle mystical or technological elements depending on the setting"` // <<< ДОБАВЛЕНО: Строка для добавления к промпту
	ImageSavePath      string           `env:"IMAGE_SAVE_PATH" env-required:"true"`                                                                                                                                                                                                                                                             // <<< Добавлено: Путь для сохранения изображений
	ImagePublicBaseURL string           `env:"IMAGE_PUBLIC_BASE_URL" env-required:"true"`                                                                                                                                                                                                                                                       // <<< Добавлено: Базовый URL для изображений
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
