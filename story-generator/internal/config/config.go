package config

import (
	"fmt"
	"log"
	"strings"
	"time"

	"novel-server/shared/utils"

	"github.com/kelseyhightower/envconfig"
)

// Config содержит конфигурацию для воркера генерации историй
type Config struct {
	// Настройки RabbitMQ
	RabbitMQURL string `envconfig:"RABBITMQ_URL" default:"amqp://guest:guest@localhost:5672/"`

	// Настройки воркера
	PromptsDir string `envconfig:"PROMPTS_DIR" default:"../promts"` // Путь относительно корня воркера

	// Настройки AI (OpenRouter)
	AIBaseURL        string        `envconfig:"AI_BASE_URL" default:"https://openrouter.ai/api/v1"`
	AIModel          string        `envconfig:"AI_MODEL" default:"deepseek/deepseek-chat"` // Уточнил модель по умолчанию
	AITimeout        time.Duration `envconfig:"AI_TIMEOUT" default:"120s"`                 // Увеличил таймаут
	AIMaxAttempts    int           `envconfig:"AI_MAX_ATTEMPTS" default:"3"`
	AIBaseRetryDelay time.Duration `envconfig:"AI_BASE_RETRY_DELAY" default:"1s"` // Добавляем базовую задержку
	// Секретное поле БЕЗ envconfig тега
	AIAPIKey string

	// Настройки PostgreSQL
	DBHost        string        `envconfig:"DB_HOST" default:"localhost"`
	DBPort        string        `envconfig:"DB_PORT" default:"5432"`
	DBUser        string        `envconfig:"DB_USER" default:"postgres"`
	DBName        string        `envconfig:"DB_NAME" default:"novel_db"`
	DBSSLMode     string        `envconfig:"DB_SSL_MODE" default:"disable"`
	DBMaxConns    int           `envconfig:"DB_MAX_CONNECTIONS" default:"10"`
	DBIdleTimeout time.Duration `envconfig:"DB_MAX_IDLE_MINUTES" default:"5m"`
	// Секретное поле БЕЗ envconfig тега
	DBPassword string

	// Дополнительные настройки можно добавить сюда
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
		return nil, fmt.Errorf("ошибка загрузки конфигурации: %w", err)
	}

	// Загружаем ОБЯЗАТЕЛЬНЫЕ секреты
	var loadErr error
	cfg.AIAPIKey, loadErr = utils.ReadSecret("ai_api_key")
	if loadErr != nil {
		return nil, loadErr
	}

	cfg.DBPassword, loadErr = utils.ReadSecret("db_password")
	if loadErr != nil {
		return nil, loadErr
	}

	// Логируем загруженную конфигурацию (кроме паролей/ключей)
	log.Printf("Конфигурация загружена (секреты из файлов):")
	log.Printf("  RabbitMQ URL: %s", cfg.RabbitMQURL)
	log.Printf("  Prompts Dir: %s", cfg.PromptsDir)
	log.Printf("  AI Base URL: %s", cfg.AIBaseURL)
	log.Printf("  AI Model: %s", cfg.AIModel)
	log.Printf("  AI Timeout: %v", cfg.AITimeout)
	log.Printf("  AI Max Attempts: %d", cfg.AIMaxAttempts)
	log.Printf("  AI Base Retry Delay: %v", cfg.AIBaseRetryDelay)
	log.Printf("  DB DSN: %s", cfg.getMaskedDSN()) // Логируем DSN с маской пароля
	log.Printf("  DB Max Conns: %d", cfg.DBMaxConns)
	log.Printf("  DB Idle Timeout: %v", cfg.DBIdleTimeout)
	log.Println("  AI API Key: [ЗАГРУЖЕН]")

	return &cfg, nil
}

// getMaskedDSN возвращает DSN с замаскированным паролем для логирования
func (c *Config) getMaskedDSN() string {
	dsn := c.GetDSN()
	parts := strings.Split(dsn, "@")
	if len(parts) != 2 {
		return "[invalid dsn format]"
	}
	userInfo := strings.Split(parts[0], ":")
	if len(userInfo) >= 2 {
		userInfo[len(userInfo)-1] = "********" // Маскируем пароль
	}
	return strings.Join(userInfo, ":") + "@" + parts[1]
}
