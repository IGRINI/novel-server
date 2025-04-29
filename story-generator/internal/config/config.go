package config

import (
	"fmt"
	"log"
	"novel-server/shared/utils"
	"os"
	"strings"
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

	// Настройки воркера
	PromptsDir string `envconfig:"PROMPTS_DIR" default:"prompts"` // Путь относительно корня воркера или WORKDIR в Docker

	// Настройки AI
	AIClientType     string        `envconfig:"AI_CLIENT_TYPE" default:"openai"` // Тип клиента: "openai" или "ollama"
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

	// Настройки CORS
	AllowedOrigins []string `envconfig:"ALLOWED_ORIGINS"` // Список разрешенных источников (разделенных запятой)

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

	// <<< ДИАГНОСТИКА: Логируем переменные ПЕРЕД обработкой >>>
	log.Printf("[DIAG] Перед envconfig.Process: AI_BASE_URL='%s', AI_MODEL='%s'", os.Getenv("AI_BASE_URL"), os.Getenv("AI_MODEL"))

	// Загружаем НЕсекретные переменные
	err := envconfig.Process("", &cfg)

	// <<< ДИАГНОСТИКА: Логируем ошибку envconfig и значения ПОСЛЕ >>>
	log.Printf("[DIAG] Ошибка envconfig.Process: %v", err)
	log.Printf("[DIAG] После envconfig.Process: cfg.AIBaseURL='%s', cfg.AIModel='%s'", cfg.AIBaseURL, cfg.AIModel)

	if err != nil {
		return nil, fmt.Errorf("ошибка загрузки конфигурации: %w", err)
	}

	// Загружаем ОБЯЗАТЕЛЬНЫЕ секреты
	var loadErr error
	cfg.AIAPIKey, loadErr = utils.ReadSecret("ai_api_key")
	// Обработка случая, когда AI_CLIENT_TYPE != 'openai' - не требовать ключ
	if cfg.AIClientType == "openai" && loadErr != nil {
		return nil, fmt.Errorf("ошибка чтения секрета ai_api_key (требуется для openai): %w", loadErr)
	} else if cfg.AIClientType != "openai" && loadErr != nil {
		log.Printf("[WARN] Секрет ai_api_key не найден, но не требуется для AI_CLIENT_TYPE='%s'", cfg.AIClientType)
		cfg.AIAPIKey = "" // Убедимся, что он пустой
	} else if loadErr == nil {
		log.Println("  AI API Key: [ЗАГРУЖЕН]")
	}

	cfg.DBPassword, loadErr = utils.ReadSecret("db_password")
	if loadErr != nil {
		return nil, loadErr
	}

	// <<< Обработка ALLOWED_ORIGINS >>>
	// envconfig уже должен был загрузить строку в cfg.AllowedOrigins[0], если она была одна
	// Если же передано несколько через запятую, envconfig так не умеет по умолчанию.
	// Мы прочитаем переменную окружения заново и разделим ее.
	allowedOriginsStr := os.Getenv("ALLOWED_ORIGINS")
	if allowedOriginsStr != "" {
		cfg.AllowedOrigins = strings.Split(allowedOriginsStr, ",")
		// Удаляем пробелы вокруг каждого origin
		for i := range cfg.AllowedOrigins {
			cfg.AllowedOrigins[i] = strings.TrimSpace(cfg.AllowedOrigins[i])
		}
	} else {
		// Если переменная не установлена, можно установить дефолтное значение,
		// например, разрешить только тот же origin или ничего не разрешать.
		// Пока оставим пустым (CORS будет работать по умолчанию браузера, т.е. запрещено)
		cfg.AllowedOrigins = []string{}
		log.Println("[WARN] Переменная окружения ALLOWED_ORIGINS не установлена. CORS будет запрещен по умолчанию.")
	}
	// <<< Конец обработки ALLOWED_ORIGINS >>>

	// Логируем загруженную конфигурацию (кроме паролей/ключей)
	log.Printf("Конфигурация загружена:")
	log.Printf("  HTTP Server Port: %s", cfg.HTTPServerPort)
	log.Printf("  RabbitMQ URL: %s", cfg.RabbitMQURL)
	log.Printf("  Prompts Dir: %s", cfg.PromptsDir)
	log.Printf("  AI Client Type: %s", cfg.AIClientType)
	log.Printf("  AI Base URL: %s", cfg.AIBaseURL)
	log.Printf("  AI Model: %s", cfg.AIModel)
	log.Printf("  AI Timeout: %v", cfg.AITimeout)
	log.Printf("  AI Max Attempts: %d", cfg.AIMaxAttempts)
	log.Printf("  AI Base Retry Delay: %v", cfg.AIBaseRetryDelay)
	log.Printf("  DB DSN: %s", cfg.getMaskedDSN()) // Логируем DSN с маской пароля
	log.Printf("  DB Max Conns: %d", cfg.DBMaxConns)
	log.Printf("  DB Idle Timeout: %v", cfg.DBIdleTimeout)
	log.Printf("  Pushgateway URL: %s", cfg.PushgatewayURL)
	// <<< ДОБАВЛЕНО: Логирование настроек логгера >>>
	log.Printf("  Log Level: %s", cfg.LogLevel)
	log.Printf("  Log Encoding: %s", cfg.LogEncoding)
	log.Printf("  Log Output: %s", cfg.LogOutput)
	// <<< КОНЕЦ ДОБАВЛЕНИЯ >>>
	// Логируем AI ключ только если он был загружен
	if cfg.AIAPIKey != "" {
		log.Println("  AI API Key: [ЗАГРУЖЕН]")
	} else {
		log.Println("  AI API Key: [НЕ ИСПОЛЬЗУЕТСЯ]")
	}
	log.Printf("  Internal Updates Queue: %s", cfg.InternalUpdatesQueueName)
	log.Printf("  Allowed Origins (CORS): %v", cfg.AllowedOrigins) // Логируем разрешенные origins

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
