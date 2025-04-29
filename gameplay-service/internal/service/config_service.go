package service

import (
	"context"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"
	"sync"

	"go.uber.org/zap"
)

// ConfigService управляет динамическими конфигурациями, загруженными из БД.
// Он обеспечивает потокобезопасный доступ к этим конфигурациям.
type ConfigService struct {
	logger  *zap.Logger
	repo    interfaces.DynamicConfigRepository
	mu      sync.RWMutex      // Мьютекс для защиты доступа к configs
	configs map[string]string // Кэш конфигураций: ключ -> значение
}

// NewConfigService создает новый экземпляр ConfigService и загружает начальные конфигурации.
func NewConfigService(repo interfaces.DynamicConfigRepository, logger *zap.Logger) (*ConfigService, error) {
	cs := &ConfigService{
		logger:  logger.Named("ConfigService"),
		repo:    repo,
		configs: make(map[string]string),
	}

	cs.logger.Info("Загрузка начальных динамических конфигураций...")
	if err := cs.loadAllConfigs(); err != nil {
		cs.logger.Error("Не удалось загрузить начальные динамические конфигурации", zap.Error(err))
		// Можно решить, критична ли ошибка. Если да, вернуть err.
		// Если нет, сервис может работать с пустым кэшем или значениями по умолчанию.
		// Пока считаем, что лучше вернуть ошибку, если БД недоступна при старте.
		return nil, err
	}
	cs.logger.Info("Динамические конфигурации загружены", zap.Int("count", len(cs.configs)))

	return cs, nil
}

// loadAllConfigs загружает все конфигурации из репозитория в кэш.
func (cs *ConfigService) loadAllConfigs() error {
	ctx := context.Background() // TODO: Consider using a context with timeout
	configs, err := cs.repo.GetAll(ctx)
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

// Get возвращает значение конфигурации по ключу.
// Возвращает значение и true, если ключ найден, иначе "" и false.
func (cs *ConfigService) Get(key string) (string, bool) {
	cs.mu.RLock() // Блокировка на чтение
	defer cs.mu.RUnlock()
	val, ok := cs.configs[key]
	return val, ok
}

// Update обновляет значение конфигурации в кэше.
// Этот метод будет вызываться консьюмером при получении события обновления.
func (cs *ConfigService) Update(config models.DynamicConfig) {
	cs.mu.Lock() // Блокировка на запись
	defer cs.mu.Unlock()
	cs.logger.Info("Обновление динамической конфигурации в кэше", zap.String("key", config.Key), zap.String("new_value", config.Value))
	cs.configs[config.Key] = config.Value
}

// TODO: Добавить методы для получения типизированных значений (GetInt, GetBool, GetDuration и т.д.)
// с обработкой ошибок парсинга и значениями по умолчанию.
