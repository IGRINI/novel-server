package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"novel-server/shared/utils"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

// Config содержит конфигурацию для Gameplay Service
type Config struct {
	// Настройки сервера
	Port     string `envconfig:"GAMEPLAY_SERVER_PORT" default:"8082"`
	Env      string `envconfig:"ENV" default:"development"`
	LogLevel string `envconfig:"LOG_LEVEL" default:"info"`

	// <<< ДОБАВЛЕНО: Настройки CORS >>>
	AllowedOrigins []string `envconfig:"ALLOWED_ORIGINS"` // Список разрешенных origin (через запятую)

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
	RabbitMQURL               string `envconfig:"RABBITMQ_URL" required:"true"`
	GenerationTaskQueue       string `envconfig:"GENERATION_TASK_QUEUE" default:"story_generation_tasks"`
	InternalUpdatesQueueName  string `envconfig:"INTERNAL_UPDATES_QUEUE_NAME" default:"internal_updates"`
	ClientUpdatesQueueName    string `envconfig:"CLIENT_UPDATES_QUEUE_NAME" default:"client_updates"`
	PushNotificationQueueName string `envconfig:"PUSH_NOTIFICATION_QUEUE_NAME" default:"push_notifications"`
	ImageGeneratorTaskQueue   string `envconfig:"IMAGE_GENERATOR_TASK_QUEUE" default:"image_generation_tasks"`
	ImageGeneratorResultQueue string `envconfig:"IMAGE_GENERATION_RESULT_QUEUE" default:"image_generation_results"`
	ConfigUpdatesQueueName    string `envconfig:"CONFIG_UPDATES_QUEUE_NAME" default:"config_updates"`

	// Настройки JWT (для проверки токена пользователя в middleware)
	// Секретное поле БЕЗ envconfig тега
	JWTSecret string

	// <<< Добавляем секрет для межсервисных токенов >>>
	InterServiceSecret string

	// <<< Добавляем URL для auth-service >>>
	AuthServiceURL string `envconfig:"AUTH_SERVICE_URL" required:"true"`

	// <<< ДОБАВЛЕНО: Настройки генерации >>>
	GenerationLimitPerUser int `envconfig:"GENERATION_LIMIT_PER_USER" default:"1"` // Лимит одновременных генераций на пользователя

	// <<< ДОБАВЛЕНО: Настройки Consumer >>>
	ConsumerConcurrency int `envconfig:"CONSUMER_CONCURRENCY" default:"10"` // Кол-во обработчиков сообщений

	// <<< ДОБАВЛЕНО: Стили для промптов >>>
	StoryPreviewPromptStyleSuffix string `envconfig:"STORY_PREVIEW_PROMPT_STYLE_SUFFIX" default:", a cinematic key art illustration for an interactive story, moody and atmospheric lighting, strong silhouette or central figure, minimal background detail, glowing accents, dark color palette with story-themed elements, dramatic composition, highly detailed digital painting"`
	CharacterPromptStyleSuffix    string `envconfig:"CHARACTER_PROMPT_STYLE_SUFFIX" default:", a stylized portrait of a story character in moody, atmospheric lighting, with neon glow accents, soft shadows, minimal background, cohesive color grading, dark color palette, and subtle mystical or technological elements depending on the setting"`

	// <<< ДОБАВЛЕНО: Список поддерживаемых языков >>>
	SupportedLanguages []string // Будет загружено вручную
}

// GetDSN возвращает строку подключения (DSN) для PostgreSQL
func (c *Config) GetDSN() string {
	// Пароль теперь в c.DBPassword
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName, c.DBSSLMode)
}

// LoadConfig загружает конфигурацию из переменных окружения и секретов
func LoadConfig() (*Config, error) {
	_ = godotenv.Load() // Загружаем .env файл, если он есть

	maxConnsStr := getEnv("DB_MAX_CONNS", "10")
	maxConns, err := strconv.Atoi(maxConnsStr)
	if err != nil {
		return nil, fmt.Errorf("невалидное значение DB_MAX_CONNS: %w", err)
	}

	idleTimeoutStr := getEnv("DB_IDLE_TIMEOUT_SECONDS", "300") // 5 минут по умолчанию
	idleTimeoutSec, err := strconv.Atoi(idleTimeoutStr)
	if err != nil {
		return nil, fmt.Errorf("невалидное значение DB_IDLE_TIMEOUT_SECONDS: %w", err)
	}

	var cfg Config
	// Загружаем НЕсекретные переменные
	err = envconfig.Process("", &cfg)
	if err != nil {
		return nil, fmt.Errorf("ошибка загрузки конфигурации gameplay-service: %w", err)
	}

	// <<< ВРУЧНУЮ ЗАГРУЖАЕМ SUPPORTED_LANGUAGES >>>
	supportedLangsStr := getEnv("SUPPORTED_LANGUAGES", "en,ru") // Используем значение по умолчанию, если переменная не установлена
	cfg.SupportedLanguages = []string{}
	if supportedLangsStr != "" {
		splitLangs := strings.Split(supportedLangsStr, ",")
		for _, lang := range splitLangs {
			trimmedLang := strings.TrimSpace(lang)
			if trimmedLang != "" {
				cfg.SupportedLanguages = append(cfg.SupportedLanguages, trimmedLang)
			}
		}
	}
	if len(cfg.SupportedLanguages) == 0 {
		log.Println("WARN: Supported languages list is empty after loading from SUPPORTED_LANGUAGES env.")
		// Можно установить дефолтное значение, если пустой список недопустим
		// cfg.SupportedLanguages = []string{"en", "ru"}
	}
	// <<< КОНЕЦ РУЧНОЙ ЗАГРУЗКИ >>>

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

	// <<< Загружаем межсервисный секрет >>>
	cfg.InterServiceSecret, loadErr = utils.ReadSecret("inter_service_secret")
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
	log.Printf("  Config Updates Queue Name: %s", cfg.ConfigUpdatesQueueName)
	log.Printf("  Client Updates Queue Name: %s", cfg.ClientUpdatesQueueName)
	log.Printf("  Push Notification Queue Name: %s", cfg.PushNotificationQueueName)
	log.Printf("  Image Generator Task Queue: %s", cfg.ImageGeneratorTaskQueue)
	log.Printf("  Image Generator Result Queue: %s", cfg.ImageGeneratorResultQueue)
	log.Println("  JWT Secret: [ЗАГРУЖЕН]")
	log.Println("  Inter-Service Secret: [ЗАГРУЖЕН]")
	log.Printf("  Supported Languages: %v", cfg.SupportedLanguages) // <<< ЛОГИРУЕМ ЯЗЫКИ >>>
	log.Printf("  Generation Limit Per User: %d", cfg.GenerationLimitPerUser)
	log.Printf("  Auth Service URL: %s", cfg.AuthServiceURL)
	log.Printf("  Allowed Origins: %v", cfg.AllowedOrigins)
	log.Printf("  Consumer Concurrency: %d", cfg.ConsumerConcurrency)
	// Логируем суффиксы, если они не пустые (чтобы не засорять лог)
	if cfg.StoryPreviewPromptStyleSuffix != "" {
		log.Printf("  Story Preview Prompt Suffix: [CONFIGURED]")
	}
	if cfg.CharacterPromptStyleSuffix != "" {
		log.Printf("  Character Prompt Suffix: [CONFIGURED]")
	}

	cfg.DBMaxConns = maxConns
	cfg.DBIdleTimeout = time.Duration(idleTimeoutSec) * time.Second

	return &cfg, nil
}

// getEnv получает значение переменной окружения или возвращает значение по умолчанию.
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
