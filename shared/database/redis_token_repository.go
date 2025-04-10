package database

import (
	// "auth/internal/domain" - No longer needed directly
	// "auth/internal/repository" - No longer needed directly
	"context"
	"fmt"
	"shared/interfaces" // Import shared interfaces
	"shared/models"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
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
func (r *redisTokenRepository) SetToken(ctx context.Context, userID uint64, td *models.TokenDetails) error { // Use shared TokenDetails
	at := time.Unix(td.AtExpires, 0) // Access Token expiration time
	rt := time.Unix(td.RtExpires, 0) // Refresh Token expiration time
	now := time.Now()

	accessKey := fmt.Sprintf("access_uuid:%s", td.AccessUUID)
	refreshKey := fmt.Sprintf("refresh_uuid:%s", td.RefreshUUID)
	userIDStr := strconv.FormatUint(userID, 10)

	accessTTL := at.Sub(now)
	refreshTTL := rt.Sub(now)

	// Use pipeline for atomic operations
	pipe := r.client.Pipeline()

	pipe.Set(ctx, accessKey, userIDStr, accessTTL)
	pipe.Set(ctx, refreshKey, userIDStr, refreshTTL)

	r.logger.Debug("Setting tokens in Redis",
		zap.Uint64("userID", userID),
		zap.String("accessUUID", td.AccessUUID),
		zap.String("refreshUUID", td.RefreshUUID),
		zap.Duration("accessTTL", accessTTL),
		zap.Duration("refreshTTL", refreshTTL),
	)

	_, err := pipe.Exec(ctx)
	if err != nil {
		r.logger.Error("Failed to set token details in redis", zap.Error(err), zap.Uint64("userID", userID))
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
func (r *redisTokenRepository) GetUserIDByAccessUUID(ctx context.Context, accessUUID string) (uint64, error) {
	key := fmt.Sprintf("access_uuid:%s", accessUUID)
	r.logger.Debug("Getting token from Redis", zap.String("key", key))
	userIDStr, err := r.client.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			r.logger.Debug("Access token not found in Redis", zap.String("accessUUID", accessUUID))
			return 0, models.ErrTokenNotFound // Use shared error
		}
		r.logger.Error("Failed to get token from redis", zap.Error(err), zap.String("key", key))
		return 0, fmt.Errorf("failed to get token from redis: %w", err)
	}

	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		// Эта ошибка серьезная - данные в Redis повреждены
		r.logger.Error("Failed to parse userID from redis data for access token",
			zap.Error(err),
			zap.String("accessUUID", accessUUID),
			zap.String("value", userIDStr),
		)
		return 0, fmt.Errorf("corrupted userID data in redis for access token %s: %w", accessUUID, err)
	}
	return userID, nil
}

// GetUserIDByRefreshUUID retrieves the UserID associated with a RefreshUUID.
func (r *redisTokenRepository) GetUserIDByRefreshUUID(ctx context.Context, refreshUUID string) (uint64, error) {
	key := fmt.Sprintf("refresh_uuid:%s", refreshUUID)
	r.logger.Debug("Getting token from Redis", zap.String("key", key))
	userIDStr, err := r.client.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			r.logger.Debug("Refresh token not found in Redis", zap.String("refreshUUID", refreshUUID))
			return 0, models.ErrTokenNotFound // Use shared error
		}
		r.logger.Error("Failed to get token from redis", zap.Error(err), zap.String("key", key))
		return 0, fmt.Errorf("failed to get token from redis: %w", err)
	}

	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		// Эта ошибка серьезная - данные в Redis повреждены
		r.logger.Error("Failed to parse userID from redis data for refresh token",
			zap.Error(err),
			zap.String("refreshUUID", refreshUUID),
			zap.String("value", userIDStr),
		)
		return 0, fmt.Errorf("corrupted userID data in redis for refresh token %s: %w", refreshUUID, err)
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
