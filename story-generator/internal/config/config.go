package config

import (
	"fmt"
	"log"
	"novel-server/shared/utils"
	"os"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Config содержит конфигурацию для воркера генерации историй
type Config struct {
	// Настройки HTTP API (если сервис его предоставляет)
	HTTPServerPort string `envconfig:"HTTP_SERVER_PORT" default:"8083"`

	// Настройки логгера
	LogLevel    string `envconfig:"LOG_LEVEL" default:"info"`    // Уровень логирования
	LogEncoding string `envconfig:"LOG_ENCODING" default:"json"` // Формат: json или console
	LogOutput   string `envconfig:"LOG_OUTPUT" default:"stdout"` // Вывод: stdout или путь к файлу

	// Настройки RabbitMQ
	RabbitMQURL              string `envconfig:"RABBITMQ_URL" default:"amqp://guest:guest@localhost:5672/"`
	InternalUpdatesQueueName string `envconfig:"INTERNAL_UPDATES_QUEUE_NAME" default:"internal_updates"`

	// Настройки Pushgateway
	PushgatewayURL string `envconfig:"PUSHGATEWAY_URL" default:"http://localhost:9091"`

	// Настройки AI (остался только ключ)
	// Секретное поле БЕЗ envconfig тега
	AIAPIKey string

	// Настройки PostgreSQL
	DBHost        string        `envconfig:"DB_HOST" default:"localhost"`
	DBPort        string        `envconfig:"DB_PORT" default:"5432"`
	DBUser        string        `envconfig:"DB_USER" default:"novel_user"`
	DBName        string        `envconfig:"DB_NAME" default:"novel_db"`
	DBSSLMode     string        `envconfig:"DB_SSL_MODE" default:"disable"`
	DBMaxConns    int           `envconfig:"DB_MAX_CONNECTIONS" default:"10"`
	DBIdleTimeout time.Duration `envconfig:"DB_MAX_IDLE_MINUTES" default:"5m"`
	// Секретное поле БЕЗ envconfig тега
	DBPassword string

	// Настройки CORS
	AllowedOrigins []string `envconfig:"ALLOWED_ORIGINS"` // Список разрешенных источников (разделенных запятой)

	// Дополнительные настройки можно добавить сюда
}

// LoadConfig загружает конфигурацию из переменных окружения и секретов
func LoadConfig() (*Config, error) {
	var cfg Config

	// Загружаем НЕсекретные переменные
	err := envconfig.Process("", &cfg)

	log.Printf("[DIAG] Ошибка envconfig.Process: %v", err)

	if err != nil {
		return nil, fmt.Errorf("ошибка загрузки конфигурации: %w", err)
	}

	// Загружаем ОБЯЗАТЕЛЬНЫЕ секреты
	var loadErr error
	// Получаем AIAPIKey только если AI_CLIENT_TYPE в окружении == openai (по дефолту он openai)
	// Используем os.Getenv, так как cfg.AIClientType еще не определен надежно
	aiClientTypeEnv := os.Getenv("AI_CLIENT_TYPE")
	if aiClientTypeEnv == "" { // Если переменная не установлена, берем дефолт
		aiClientTypeEnv = "openai"
	}
	if aiClientTypeEnv == "openai" {
		cfg.AIAPIKey, loadErr = utils.ReadSecret("ai_api_key")
		if loadErr != nil {
			return nil, fmt.Errorf("ошибка чтения секрета ai_api_key (требуется для openai): %w", loadErr)
		}
		log.Printf("[DEBUG] Loaded AIAPIKey (length %d): '%s'", len(cfg.AIAPIKey), cfg.AIAPIKey)
		log.Println("  AI API Key: [ЗАГРУЖЕН]")
	} else {
		log.Printf("[INFO] AI_CLIENT_TYPE установлен в '%s', секрет ai_api_key не загружается.", aiClientTypeEnv)
	}

	cfg.DBPassword, loadErr = utils.ReadSecret("db_password")
	if loadErr != nil {
		return nil, loadErr
	}
	log.Println("  DB Password: [ЗАГРУЖЕН]")

	// Логируем загруженные (не секретные) параметры
	log.Println("Конфигурация загружена:")
	log.Printf("  HTTP Server Port: %s", cfg.HTTPServerPort)
	log.Printf("  Log Level: %s", cfg.LogLevel)
	log.Printf("  Log Encoding: %s", cfg.LogEncoding)
	log.Printf("  Log Output: %s", cfg.LogOutput)
	log.Printf("  RabbitMQ URL: %s", cfg.RabbitMQURL)
	log.Printf("  Internal Updates Queue: %s", cfg.InternalUpdatesQueueName)
	log.Printf("  Pushgateway URL: %s", cfg.PushgatewayURL)
	// Параметры AI теперь читаются из dynamic_configs, здесь не логируем
	log.Printf("  DB Host: %s", cfg.DBHost)
	log.Printf("  DB Port: %s", cfg.DBPort)
	log.Printf("  DB User: %s", cfg.DBUser)
	log.Printf("  DB Name: %s", cfg.DBName)
	log.Printf("  DB SSL Mode: %s", cfg.DBSSLMode)
	log.Printf("  DB Max Connections: %d", cfg.DBMaxConns)
	log.Printf("  DB Idle Timeout: %v", cfg.DBIdleTimeout)
	log.Printf("  Allowed Origins: %v", cfg.AllowedOrigins)

	return &cfg, nil
}

// GetDSN возвращает строку Data Source Name для подключения к PostgreSQL
func (c *Config) GetDSN() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode)
}
