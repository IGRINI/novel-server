package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"novel-server/shared/utils"

	"github.com/joho/godotenv"
	"go.uber.org/zap"
)

// Config хранит конфигурацию сервиса админки
type Config struct {
	Env                  string
	ServerPort           string
	LogLevel             string
	AuthServiceURL       string
	StoryGeneratorURL    string
	GameplayServiceURL   string
	ClientTimeout        time.Duration
	InterServiceTokenTTL time.Duration
	AuthServiceTimeout   time.Duration
	ServiceID            string
	SupportedLanguages   []string
	// Секреты без тегов
	JWTSecret          string
	InterServiceSecret string
	DBPassword         string // <<< ДОБАВЛЕНО: Пароль БД (из секрета)

	// <<< ДОБАВЛЕНО: Параметры подключения к БД >>>
	DBHost        string        `env:"DB_HOST" env-required:"true"` // Required, т.к. DSN больше не используется
	DBPort        string        `env:"DB_PORT" env-default:"5432"`
	DBUser        string        `env:"DB_USER" env-required:"true"`
	DBName        string        `env:"DB_NAME" env-required:"true"`
	DBSSLMode     string        `env:"DB_SSL_MODE" env-default:"disable"`
	DBMaxConns    int           `env:"DB_MAX_CONNECTIONS" env-default:"10"`
	DBIdleTimeout time.Duration `env:"DB_MAX_IDLE_MINUTES" env-default:"5m"`
	// <<< КОНЕЦ ДОБАВЛЕНИЯ ПАРАМЕТРОВ БД >>>

	// <<< Настройки RabbitMQ >>>
	RabbitMQ RabbitMQConfig

	// УДАЛЕНО: PostgresDSN string
}

// <<< Структура для настроек RabbitMQ >>>
type RabbitMQConfig struct {
	URL           string `env:"RABBITMQ_URL" env-required:"true"`
	PushQueueName string `env:"PUSH_QUEUE_NAME" env-default:"push_notifications"` // Имя очереди для пушей
}

// LoadConfig загружает конфигурацию из переменных окружения и секретов
func LoadConfig(logger *zap.Logger) (*Config, error) {
	_ = godotenv.Load()

	port := getEnv("ADMIN_SERVER_PORT", "8084")

	// Используем utils.ReadSecret
	jwtSecret, err := utils.ReadSecret("jwt_secret")
	if err != nil {
		logger.Error("Не удалось прочитать JWT_SECRET из секрета Docker", zap.Error(err))
		return nil, err
	}

	interServiceSecret, err := utils.ReadSecret("inter_service_secret")
	if err != nil {
		logger.Error("Не удалось прочитать INTER_SERVICE_SECRET из секрета Docker", zap.Error(err))
		return nil, err
	}

	// <<< ДОБАВЛЕНО: Чтение пароля БД из секрета >>>
	dbPassword, err := utils.ReadSecret("db_password")
	if err != nil {
		// Считаем ошибку чтения пароля БД фатальной
		logger.Error("Не удалось прочитать DB_PASSWORD из секрета Docker", zap.Error(err))
		return nil, fmt.Errorf("не удалось прочитать секрет db_password: %w", err)
	}
	// <<< КОНЕЦ ЧТЕНИЯ ПАРОЛЯ БД >>>

	authServiceURL := getEnv("AUTH_SERVICE_URL", "http://auth-service:8081")
	storyGeneratorURL := getEnv("STORY_GENERATOR_URL", "http://story-generator:8083")
	gameplayServiceURL := getEnv("GAMEPLAY_SERVICE_URL", "http://gameplay-service:8082")

	clientTimeoutStr := getEnv("HTTP_CLIENT_TIMEOUT", "10s")
	clientTimeout, err := time.ParseDuration(clientTimeoutStr)
	if err != nil {
		logger.Warn("Invalid HTTP_CLIENT_TIMEOUT format, using default 10s", zap.String("value", clientTimeoutStr), zap.Error(err))
		clientTimeout = 10 * time.Second
	}
	logger.Info("Client timeout", zap.Duration("clientTimeout", clientTimeout))

	// <<< Загрузка поддерживаемых языков >>>
	supportedLangsStr := getEnv("SUPPORTED_LANGUAGES", "en,ru")
	supportedLangs := strings.Split(supportedLangsStr, ",")
	for i := range supportedLangs {
		supportedLangs[i] = strings.TrimSpace(supportedLangs[i])
	}
	logger.Info("Supported languages loaded", zap.Strings("languages", supportedLangs))

	cfg := &Config{
		Env:        getEnv("ENV", "development"),
		ServerPort: port,
		LogLevel:   getEnv("LOG_LEVEL", "debug"),
		// УДАЛЕНО: PostgresDSN: getEnv("DATABASE_URL", getEnv("POSTGRES_DSN", "")),
		JWTSecret:            jwtSecret,
		DBPassword:           dbPassword, // <<< Сохраняем пароль
		AuthServiceURL:       authServiceURL,
		StoryGeneratorURL:    storyGeneratorURL,
		GameplayServiceURL:   gameplayServiceURL,
		ClientTimeout:        clientTimeout,
		InterServiceSecret:   interServiceSecret,
		InterServiceTokenTTL: getDurationEnv("INTER_SERVICE_TOKEN_TTL", "1h"),
		AuthServiceTimeout:   getDurationEnv("AUTH_SERVICE_TIMEOUT", "5s"),
		ServiceID:            getEnv("SERVICE_ID", "admin-service"),
		SupportedLanguages:   supportedLangs,

		// <<< ДОБАВЛЕНО: Загрузка параметров БД >>>
		DBHost:        getEnv("DB_HOST", ""), // Пусто по умолчанию, будет проверяться в setupDatabase
		DBPort:        getEnv("DB_PORT", "5432"),
		DBUser:        getEnv("DB_USER", ""), // Пусто по умолчанию
		DBName:        getEnv("DB_NAME", ""), // Пусто по умолчанию
		DBSSLMode:     getEnv("DB_SSL_MODE", "disable"),
		DBMaxConns:    getIntEnv("DB_MAX_CONNECTIONS", 10),
		DBIdleTimeout: getDurationEnv("DB_MAX_IDLE_MINUTES", "5m"),
		// <<< КОНЕЦ ЗАГРУЗКИ ПАРАМЕТРОВ БД >>>

		// <<< Настройки RabbitMQ >>>
		RabbitMQ: RabbitMQConfig{
			URL:           getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
			PushQueueName: getEnv("PUSH_QUEUE_NAME", "push_notifications"),
		},
	}

	// Проверим, что обязательные параметры БД загружены (хотя бы не пустые строки)
	// Более строгая проверка будет в setupDatabase при попытке коннекта
	if cfg.DBHost == "" || cfg.DBUser == "" || cfg.DBName == "" {
		logger.Warn("Один или несколько параметров БД (DB_HOST, DB_USER, DB_NAME) не установлены.")
		// Можно решить, фатально ли это. Пока оставим как Warning.
	}

	logger.Info("Конфигурация Admin Service загружена (секреты из файлов)",
		zap.String("env", cfg.Env),
		zap.String("port", cfg.ServerPort),
		zap.String("logLevel", cfg.LogLevel),
		zap.String("dbHost", cfg.DBHost), // <<< ЛОГИРУЕМ ОТДЕЛЬНЫЕ ПАРАМЕТРЫ >>>
		zap.String("dbPort", cfg.DBPort),
		zap.String("dbUser", cfg.DBUser),
		zap.String("dbName", cfg.DBName),
		zap.String("dbSSLMode", cfg.DBSSLMode),
		zap.Int("dbMaxConns", cfg.DBMaxConns),
		zap.Duration("dbIdleTimeout", cfg.DBIdleTimeout),
		zap.Bool("dbPasswordLoaded", cfg.DBPassword != ""), // <<< Проверяем пароль
		zap.String("authServiceURL", cfg.AuthServiceURL),
		zap.String("storyGeneratorURL", cfg.StoryGeneratorURL),
		zap.String("gameplayServiceURL", cfg.GameplayServiceURL),
		zap.Duration("clientTimeout", cfg.ClientTimeout),
		// zap.Bool("postgresDSNLoaded", cfg.PostgresDSN != ""), // <<< УДАЛЕНО ЛОГИРОВАНИЕ DSN >>>
		zap.Bool("jwtSecretLoaded", cfg.JWTSecret != ""),
		zap.Bool("interServiceSecretLoaded", cfg.InterServiceSecret != ""),
		zap.String("serviceID", cfg.ServiceID),
		zap.Strings("supportedLanguages", cfg.SupportedLanguages),
	)

	return cfg, nil
}

// <<< ДОБАВЛЕНО: Метод GetDSN >>>
// GetDSN возвращает строку подключения (DSN) для PostgreSQL
func (c *Config) GetDSN() string {
	// Пароль теперь в c.DBPassword
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName, c.DBSSLMode)
}

// <<< КОНЕЦ ДОБАВЛЕНИЯ GetDSN >>>

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func getDurationEnv(key string, fallback string) time.Duration {
	if value, ok := os.LookupEnv(key); ok {
		duration, err := time.ParseDuration(value)
		if err != nil {
			fmt.Printf("Invalid %s format, using default %s\n", key, fallback)
			// Возвращаем fallback duration, если он парсится
			if fallbackDur, fbErr := time.ParseDuration(fallback); fbErr == nil {
				return fallbackDur
			}
			return time.Duration(0) // Или возвращаем 0, если fallback тоже некорректен
		}
		return duration
	}
	// Возвращаем fallback duration, если переменная не найдена
	duration, err := time.ParseDuration(fallback)
	if err != nil {
		fmt.Printf("Invalid fallback format for %s, using default 0s\n", fallback)
		return time.Duration(0)
	}
	return duration
}

// <<< (НОВОЕ) Вспомогательная функция для получения int из env >>>
func getIntEnv(key string, fallback int) int {
	if valueStr, ok := os.LookupEnv(key); ok {
		if valueInt, err := strconv.Atoi(valueStr); err == nil {
			return valueInt
		}
		fmt.Printf("Invalid integer format for %s, using fallback %d\n", key, fallback)
	}
	return fallback
}
