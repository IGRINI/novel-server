package configservice

import (
	"context"
	"novel-server/shared/interfaces" // Updated import path
	"novel-server/shared/models"     // Updated import path
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"
)

// <<< ДОБАВЛЕНЫ ЭКСПОРТИРУЕМЫЕ КЛЮЧИ КОНФИГУРАЦИИ И ЗНАЧЕНИЯ ПО УМОЛЧАНИЮ >>>
const (
	// Экспортируем ключи и дефолты, которые могут использоваться в других пакетах (например, main.go)
	ConfigKeyAIMaxAttempts    = "ai.max_attempts"
	ConfigKeyAIBaseRetryDelay = "ai.base_retry_delay"
	ConfigKeyAITimeout        = "ai.timeout"
	ConfigKeyInputCost        = "generation.token_input_cost"  // Exported
	ConfigKeyOutputCost       = "generation.token_output_cost" // Exported
	ConfigKeyAIModel          = "ai.model"                     // Exported
	ConfigKeyAIBaseURL        = "ai.base_url"                  // Exported
	ConfigKeyAIClientType     = "ai.client_type"               // Exported
	ConfigKeyAIAPIKey         = "ai.api_key"                   // Exported
	ConfigKeyAITemperature    = "ai.temperature"               // Exported (Новый ключ)

	DefaultAIMaxAttempts    = 3
	DefaultAIBaseRetryDelay = 1 * time.Second
	DefaultAITimeout        = 120 * time.Second
	DefaultInputCost        = 0.1                             // Exported
	DefaultOutputCost       = 0.4                             // Exported
	DefaultAIModel          = "meta-llama/llama-4-scout:free" // Exported
	DefaultAIBaseURL        = "https://openrouter.ai/api/v1"  // Exported
	DefaultAIClientType     = "openai"                        // Exported
	DefaultAITemperature    = 0.7                             // Exported (Новое значение по умолчанию)
)

// <<< КОНЕЦ ДОБАВЛЕНИЯ >>>

// ConfigService управляет динамическими конфигурациями, загруженными из БД.
// Он обеспечивает потокобезопасный доступ к этим конфигурациям.
type ConfigService struct {
	logger  *zap.Logger
	repo    interfaces.DynamicConfigRepository // Use shared interface
	mu      sync.RWMutex                       // Мьютекс для защиты доступа к configs
	configs map[string]string                  // Кэш конфигураций: ключ -> значение
	db      interfaces.DBTX                    // <<< ДОБАВЛЕНО: Пул соединений >>>
}

// NewConfigService создает новый экземпляр ConfigService и загружает начальные конфигурации.
func NewConfigService(repo interfaces.DynamicConfigRepository, logger *zap.Logger, dbPool interfaces.DBTX) (*ConfigService, error) { // <<< ДОБАВЛЕНО: dbPool >>>
	cs := &ConfigService{
		logger:  logger.Named("ConfigService"),
		repo:    repo,
		configs: make(map[string]string),
		db:      dbPool, // <<< ДОБАВЛЕНО: Сохраняем пул >>>
	}

	cs.logger.Info("Загрузка начальных динамических конфигураций...")
	if err := cs.loadAllConfigs(); err != nil {
		cs.logger.Error("Не удалось загрузить начальные динамические конфигурации", zap.Error(err))
		// Считаем, что ошибка критична, если БД недоступна при старте.
		return nil, err
	}
	cs.logger.Info("Динамические конфигурации загружены", zap.Int("count", len(cs.configs)))

	return cs, nil
}

// loadAllConfigs загружает все конфигурации из репозитория в кэш.
func (cs *ConfigService) loadAllConfigs() error {
	ctx := context.Background() // TODO: Consider using a context with timeout
	configs, err := cs.repo.GetAll(ctx, cs.db)
	if err != nil {
		return err
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

	// Очищаем старый кэш перед заполнением
	cs.configs = make(map[string]string)
	for _, cfg := range configs {
		cs.configs[cfg.Key] = cfg.Value
		cs.logger.Debug("Загружена конфигурация", zap.String("key", cfg.Key), zap.String("value", cfg.Value))
	}
	return nil
}

// get возвращает значение конфигурации по ключу (внутренний метод без логов).
// Возвращает значение и true, если ключ найден, иначе "" и false.
func (cs *ConfigService) get(key string) (string, bool) {
	cs.mu.RLock() // Блокировка на чтение
	defer cs.mu.RUnlock()
	val, ok := cs.configs[key]
	return val, ok
}

// GetString возвращает строковое значение конфигурации по ключу или значение по умолчанию.
func (cs *ConfigService) GetString(key string, defaultValue string) string {
	val, ok := cs.get(key)
	if !ok || val == "" {
		cs.logger.Debug("Ключ не найден или значение пустое, используется значение по умолчанию", zap.String("key", key), zap.String("default", defaultValue))
		return defaultValue
	}
	cs.logger.Debug("Получено значение из кэша", zap.String("key", key), zap.String("value", val))
	return val
}

// GetInt возвращает целочисленное значение конфигурации по ключу или значение по умолчанию.
func (cs *ConfigService) GetInt(key string, defaultValue int) int {
	strVal, ok := cs.get(key)
	if !ok {
		cs.logger.Debug("Ключ не найден, используется значение по умолчанию", zap.String("key", key), zap.Int("default", defaultValue))
		return defaultValue
	}
	intVal, err := strconv.Atoi(strVal)
	if err != nil {
		cs.logger.Warn("Ошибка парсинга int, используется значение по умолчанию", zap.String("key", key), zap.String("value", strVal), zap.Error(err), zap.Int("default", defaultValue))
		return defaultValue
	}
	cs.logger.Debug("Получено значение int из кэша", zap.String("key", key), zap.Int("value", intVal))
	return intVal
}

// GetFloat возвращает float64 значение конфигурации по ключу или значение по умолчанию.
func (cs *ConfigService) GetFloat(key string, defaultValue float64) float64 {
	strVal, ok := cs.get(key)
	if !ok {
		cs.logger.Debug("Ключ не найден, используется значение по умолчанию", zap.String("key", key), zap.Float64("default", defaultValue))
		return defaultValue
	}
	floatVal, err := strconv.ParseFloat(strVal, 64)
	if err != nil {
		cs.logger.Warn("Ошибка парсинга float64, используется значение по умолчанию", zap.String("key", key), zap.String("value", strVal), zap.Error(err), zap.Float64("default", defaultValue))
		return defaultValue
	}
	cs.logger.Debug("Получено значение float64 из кэша", zap.String("key", key), zap.Float64("value", floatVal))
	return floatVal
}

// GetDuration возвращает time.Duration значение конфигурации по ключу или значение по умолчанию.
func (cs *ConfigService) GetDuration(key string, defaultValue time.Duration) time.Duration {
	strVal, ok := cs.get(key)
	if !ok {
		cs.logger.Debug("Ключ не найден, используется значение по умолчанию", zap.String("key", key), zap.Duration("default", defaultValue))
		return defaultValue
	}
	durationVal, err := time.ParseDuration(strVal)
	if err != nil {
		cs.logger.Warn("Ошибка парсинга time.Duration, используется значение по умолчанию", zap.String("key", key), zap.String("value", strVal), zap.Error(err), zap.Duration("default", defaultValue))
		return defaultValue
	}
	cs.logger.Debug("Получено значение time.Duration из кэша", zap.String("key", key), zap.Duration("value", durationVal))
	return durationVal
}

// Update обновляет значение конфигурации в кэше.
// Этот метод будет вызываться консьюмером при получении события обновления.
func (cs *ConfigService) Update(config models.DynamicConfig) { // Use shared models type
	cs.mu.Lock() // Блокировка на запись
	defer cs.mu.Unlock()
	cs.logger.Info("Обновление динамической конфигурации в кэше", zap.String("key", config.Key), zap.String("new_value", config.Value))
	cs.configs[config.Key] = config.Value
}
