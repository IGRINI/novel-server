package database

import (
	// "auth/internal/domain" - No longer needed directly
	// "auth/internal/repository" - No longer needed directly
	"context"
	"fmt"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap" // Добавляем zap
)

// Compile-time check to ensure redisTokenRepository implements TokenRepository
var _ interfaces.TokenRepository = (*redisTokenRepository)(nil) // Check against shared interface

type redisTokenRepository struct {
	client *redis.Client
	logger *zap.Logger // Добавляем поле логгера
}

// NewRedisTokenRepository creates a new Redis-backed TokenRepository.
func NewRedisTokenRepository(client *redis.Client, logger *zap.Logger) interfaces.TokenRepository { // Добавляем логгер
	return &redisTokenRepository{
		client: client,
		logger: logger.Named("RedisTokenRepo"), // Имя для контекста
	}
}

// SetToken stores token details in Redis.
// We store two key-value pairs for each token pair:
// 1. AccessUUID -> UserID (with AccessTokenTTL)
// 2. RefreshUUID -> UserID (with RefreshTokenTTL)
func (r *redisTokenRepository) SetToken(ctx context.Context, userID uuid.UUID, td *models.TokenDetails) error { // <<< Меняем тип userID
	at := time.Unix(td.AtExpires, 0) // Access Token expiration time
	rt := time.Unix(td.RtExpires, 0) // Refresh Token expiration time
	now := time.Now()

	accessKey := fmt.Sprintf("access_uuid:%s", td.AccessUUID)
	refreshKey := fmt.Sprintf("refresh_uuid:%s", td.RefreshUUID)
	userIDStr := userID.String() // <<< Преобразуем UUID в строку

	accessTTL := at.Sub(now)
	refreshTTL := rt.Sub(now)

	// Use pipeline for atomic operations
	pipe := r.client.Pipeline()

	pipe.Set(ctx, accessKey, userIDStr, accessTTL)
	pipe.Set(ctx, refreshKey, userIDStr, refreshTTL)

	r.logger.Debug("Setting tokens in Redis",
		zap.String("userID", userIDStr), // <<< Логируем строку
		zap.String("accessUUID", td.AccessUUID),
		zap.String("refreshUUID", td.RefreshUUID),
		zap.Duration("accessTTL", accessTTL),
		zap.Duration("refreshTTL", refreshTTL),
	)

	_, err := pipe.Exec(ctx)
	if err != nil {
		r.logger.Error("Failed to set token details in redis", zap.Error(err), zap.String("userID", userIDStr)) // <<< Логируем строку
		return fmt.Errorf("failed to set token details in redis: %w", err)
	}
	return nil
}

// DeleteTokens removes tokens from Redis based on their UUIDs.
// It attempts to delete both access and refresh tokens.
func (r *redisTokenRepository) DeleteTokens(ctx context.Context, accessUUID, refreshUUID string) (int64, error) {
	keysToDelete := []string{}
	logFields := []zap.Field{} // Собираем поля для лога

	if accessUUID != "" {
		keysToDelete = append(keysToDelete, fmt.Sprintf("access_uuid:%s", accessUUID))
		logFields = append(logFields, zap.String("accessUUID", accessUUID))
	}
	if refreshUUID != "" {
		keysToDelete = append(keysToDelete, fmt.Sprintf("refresh_uuid:%s", refreshUUID))
		logFields = append(logFields, zap.String("refreshUUID", refreshUUID))
	}

	if len(keysToDelete) == 0 {
		r.logger.Warn("DeleteTokens called with no UUIDs")
		return 0, nil // Nothing to delete
	}

	r.logger.Debug("Deleting tokens from Redis", logFields...)
	deletedCount, err := r.client.Del(ctx, keysToDelete...).Result()
	if err != nil {
		logFields = append(logFields, zap.Error(err))
		r.logger.Error("Failed to delete tokens from redis", logFields...)
		return 0, fmt.Errorf("failed to delete tokens from redis: %w", err)
	}
	logFields = append(logFields, zap.Int64("deletedCount", deletedCount))
	r.logger.Info("Tokens deleted from Redis", logFields...)
	return deletedCount, nil
}

// GetUserIDByAccessUUID retrieves the UserID associated with an AccessUUID.
func (r *redisTokenRepository) GetUserIDByAccessUUID(ctx context.Context, accessUUID string) (uuid.UUID, error) { // <<< Меняем тип возврата
	key := fmt.Sprintf("access_uuid:%s", accessUUID)
	r.logger.Debug("Getting token from Redis", zap.String("key", key))
	userIDStr, err := r.client.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			r.logger.Debug("Access token not found in Redis", zap.String("accessUUID", accessUUID))
			return uuid.Nil, models.ErrTokenNotFound // <<< Возвращаем uuid.Nil
		}
		r.logger.Error("Failed to get token from redis", zap.Error(err), zap.String("key", key))
		return uuid.Nil, fmt.Errorf("failed to get token from redis: %w", err) // <<< Возвращаем uuid.Nil
	}

	userID, err := uuid.Parse(userIDStr) // <<< Парсим строку в UUID
	if err != nil {
		// Эта ошибка серьезная - данные в Redis повреждены
		r.logger.Error("Failed to parse userID (UUID) from redis data for access token",
			zap.Error(err),
			zap.String("accessUUID", accessUUID),
			zap.String("value", userIDStr),
		)
		return uuid.Nil, fmt.Errorf("corrupted userID data in redis for access token %s: %w", accessUUID, err) // <<< Возвращаем uuid.Nil
	}
	return userID, nil
}

// GetUserIDByRefreshUUID retrieves the UserID associated with a RefreshUUID.
func (r *redisTokenRepository) GetUserIDByRefreshUUID(ctx context.Context, refreshUUID string) (uuid.UUID, error) { // <<< Меняем тип возврата
	key := fmt.Sprintf("refresh_uuid:%s", refreshUUID)
	r.logger.Debug("Getting token from Redis", zap.String("key", key))
	userIDStr, err := r.client.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			r.logger.Debug("Refresh token not found in Redis", zap.String("refreshUUID", refreshUUID))
			return uuid.Nil, models.ErrTokenNotFound // <<< Возвращаем uuid.Nil
		}
		r.logger.Error("Failed to get token from redis", zap.Error(err), zap.String("key", key))
		return uuid.Nil, fmt.Errorf("failed to get token from redis: %w", err) // <<< Возвращаем uuid.Nil
	}

	userID, err := uuid.Parse(userIDStr) // <<< Парсим строку в UUID
	if err != nil {
		// Эта ошибка серьезная - данные в Redis повреждены
		r.logger.Error("Failed to parse userID (UUID) from redis data for refresh token",
			zap.Error(err),
			zap.String("refreshUUID", refreshUUID),
			zap.String("value", userIDStr),
		)
		return uuid.Nil, fmt.Errorf("corrupted userID data in redis for refresh token %s: %w", refreshUUID, err) // <<< Возвращаем uuid.Nil
	}
	return userID, nil
}

// DeleteRefreshUUID removes only the refresh token UUID from Redis.
func (r *redisTokenRepository) DeleteRefreshUUID(ctx context.Context, refreshUUID string) error {
	key := fmt.Sprintf("refresh_uuid:%s", refreshUUID)
	r.logger.Debug("Deleting refresh token from Redis", zap.String("key", key))

	result, err := r.client.Del(ctx, key).Result()
	if err != nil {
		r.logger.Error("Failed to delete refresh token from redis", zap.Error(err), zap.String("key", key))
		return fmt.Errorf("failed to delete refresh token %s from redis: %w", refreshUUID, err)
	}

	if result == 0 {
		r.logger.Warn("Attempted to delete non-existent refresh token", zap.String("refreshUUID", refreshUUID))
		// Не возвращаем ошибку, если ключ не найден, это идемпотентная операция
	}

	r.logger.Info("Refresh token deleted from Redis", zap.String("refreshUUID", refreshUUID))
	return nil
}

// DeleteTokensByUserID removes all tokens associated with a user ID.
// WARNING: This implementation uses SCAN and might be inefficient on large Redis instances.
// Consider alternative data structures (like sets per user) if performance becomes an issue.
func (r *redisTokenRepository) DeleteTokensByUserID(ctx context.Context, userID uuid.UUID) (int64, error) { // <<< Меняем тип userID
	log := r.logger.With(zap.String("userID", userID.String())) // <<< Используем userID.String()
	log.Info("Attempting to delete all tokens for user")

	userIDStr := userID.String() // <<< Преобразуем UUID в строку
	var cursor uint64
	var keysToDelete []string
	var totalDeleted int64

	// Сканируем ключи access_uuid:*
	log.Debug("Scanning for access tokens")
	for {
		var keys []string
		var err error
		keys, cursor, err = r.client.Scan(ctx, cursor, "access_uuid:*", 100).Result() // Сканируем пачками по 100
		if err != nil {
			log.Error("Redis SCAN failed for access tokens", zap.Error(err))
			return totalDeleted, fmt.Errorf("redis scan failed for access tokens: %w", err)
		}

		// Проверяем найденные ключи
		if len(keys) > 0 {
			values, err := r.client.MGet(ctx, keys...).Result()
			if err != nil {
				log.Error("Redis MGET failed for access tokens", zap.Error(err))
				// Пытаемся продолжить, но это плохой знак
			} else {
				for i, key := range keys {
					if i < len(values) && values[i] != nil && values[i].(string) == userIDStr {
						keysToDelete = append(keysToDelete, key)
					}
				}
			}
		}

		if cursor == 0 { // Сканирование завершено
			break
		}
	}

	// Сканируем ключи refresh_uuid:*
	cursor = 0 // Сбрасываем курсор
	log.Debug("Scanning for refresh tokens")
	for {
		var keys []string
		var err error
		keys, cursor, err = r.client.Scan(ctx, cursor, "refresh_uuid:*", 100).Result()
		if err != nil {
			log.Error("Redis SCAN failed for refresh tokens", zap.Error(err))
			return totalDeleted, fmt.Errorf("redis scan failed for refresh tokens: %w", err)
		}

		// Проверяем найденные ключи
		if len(keys) > 0 {
			values, err := r.client.MGet(ctx, keys...).Result()
			if err != nil {
				log.Error("Redis MGET failed for refresh tokens", zap.Error(err))
			} else {
				for i, key := range keys {
					if i < len(values) && values[i] != nil && values[i].(string) == userIDStr {
						keysToDelete = append(keysToDelete, key)
					}
				}
			}
		}

		if cursor == 0 {
			break
		}
	}

	// Удаляем найденные ключи
	if len(keysToDelete) > 0 {
		log.Debug("Deleting found tokens", zap.Strings("keys", keysToDelete))
		deletedCount, err := r.client.Del(ctx, keysToDelete...).Result()
		if err != nil {
			log.Error("Failed to delete tokens by user ID", zap.Error(err))
			return totalDeleted, fmt.Errorf("failed to delete tokens by user ID: %w", err)
		}
		totalDeleted = deletedCount
		log.Info("Deleted tokens for user", zap.Int64("deletedCount", totalDeleted))
	} else {
		log.Info("No tokens found to delete for user")
	}

	return totalDeleted, nil
}
