package service

import (
	"context"
	"novel-server/shared/interfaces"
	"novel-server/shared/messaging"
	"novel-server/shared/models"

	"go.uber.org/zap"
)

const configUpdateTopic = "config.updated" // Имя топика/ключа роутинга для RabbitMQ

// ConfigService определяет методы для управления динамическими настройками.
type ConfigService interface {
	GetAllConfigs(ctx context.Context) ([]*models.DynamicConfig, error)
	GetConfigByKey(ctx context.Context, key string) (*models.DynamicConfig, error)
	UpdateConfig(ctx context.Context, key, value string) error
}

type configServiceImpl struct {
	repo      interfaces.DynamicConfigRepository
	publisher messaging.Publisher // Интерфейс для RabbitMQ
	logger    *zap.Logger
}

// NewConfigService создает новый экземпляр ConfigService.
func NewConfigService(
	repo interfaces.DynamicConfigRepository,
	publisher messaging.Publisher, // Принимаем паблишер
	logger *zap.Logger,
) ConfigService {
	return &configServiceImpl{
		repo:      repo,
		publisher: publisher, // Сохраняем паблишер
		logger:    logger.Named("ConfigService"),
	}
}

func (s *configServiceImpl) GetAllConfigs(ctx context.Context) ([]*models.DynamicConfig, error) {
	configs, err := s.repo.GetAll(ctx)
	if err != nil {
		s.logger.Error("Failed to get all configs from repository", zap.Error(err))
		// Можно вернуть специфичную ошибку сервисного уровня, если нужно
		return nil, err
	}
	return configs, nil
}

func (s *configServiceImpl) GetConfigByKey(ctx context.Context, key string) (*models.DynamicConfig, error) {
	config, err := s.repo.GetByKey(ctx, key)
	if err != nil {
		s.logger.Warn("Failed to get config by key from repository", zap.String("key", key), zap.Error(err))
		// repo.GetByKey уже возвращает models.ErrNotFound
		return nil, err
	}
	return config, nil
}

func (s *configServiceImpl) UpdateConfig(ctx context.Context, key, value string) error {
	log := s.logger.With(zap.String("key", key))

	// 1. Подготавливаем данные для Upsert
	config := &models.DynamicConfig{
		Key:   key,
		Value: value,
		// UpdatedAt обновится автоматически триггером
	}

	// 2. Выполняем Upsert в БД
	err := s.repo.Upsert(ctx, config)
	if err != nil {
		log.Error("Failed to upsert config in repository", zap.Error(err))
		return err // Возвращаем ошибку репозитория
	}

	log.Info("Config updated successfully")

	// <<< НОВОЕ: Публикуем уведомление об обновлении >>>
	if s.publisher != nil {
		configPayload := messaging.ConfigUpdatePayload{
			Key:   key,
			Value: value,
		}
		if publishErr := s.publisher.Publish(ctx, configPayload, ""); publishErr != nil {
			// Логируем критическую ошибку: БД обновлена, но уведомление не отправлено!
			log.Error("CRITICAL: Failed to publish config update notification after DB update", zap.Error(publishErr), zap.Any("payload", configPayload))
			// Можно добавить метрику или другой механизм оповещения об этой ситуации
		} else {
			log.Info("Config update notification published successfully")
		}
	} else {
		log.Warn("Publisher is nil, skipping config update notification")
	}
	// <<< КОНЕЦ НОВОГО БЛОКА >>>

	return nil
}
