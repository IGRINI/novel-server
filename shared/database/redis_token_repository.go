package database

import (
	// "auth/internal/domain" - No longer needed directly
	// "auth/internal/repository" - No longer needed directly
	"context"
	"fmt"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"
	"strings"
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
// And add identifiers to a user-specific set:
// user_tokens:{UserID} -> { "access:{AccessUUID}", "refresh:{RefreshUUID}" }
func (r *redisTokenRepository) SetToken(ctx context.Context, userID uuid.UUID, td *models.TokenDetails) error {
	at := time.Unix(td.AtExpires, 0) // Access Token expiration time
	rt := time.Unix(td.RtExpires, 0) // Refresh Token expiration time
	now := time.Now()

	accessKey := fmt.Sprintf("access_uuid:%s", td.AccessUUID)
	refreshKey := fmt.Sprintf("refresh_uuid:%s", td.RefreshUUID)
	userIDStr := userID.String()
	userSetKey := fmt.Sprintf("user_tokens:%s", userIDStr) // Key for the user's set

	accessTTL := at.Sub(now)
	refreshTTL := rt.Sub(now)

	// Identifiers to store in the set
	accessIdentifier := fmt.Sprintf("access:%s", td.AccessUUID)
	refreshIdentifier := fmt.Sprintf("refresh:%s", td.RefreshUUID)

	// Use pipeline for atomic operations
	pipe := r.client.Pipeline()

	// Set the actual token keys with TTLs
	pipe.Set(ctx, accessKey, userIDStr, accessTTL)
	pipe.Set(ctx, refreshKey, userIDStr, refreshTTL)

	// Add token identifiers to the user's set
	pipe.SAdd(ctx, userSetKey, accessIdentifier, refreshIdentifier)
	// Optional: Set TTL on the set itself? Maybe same as refresh token? Let's keep it without TTL for now.
	// pipe.Expire(ctx, userSetKey, refreshTTL) // Consider implications

	r.logger.Debug("Setting tokens in Redis and adding to user set",
		zap.String("userID", userIDStr),
		zap.String("accessUUID", td.AccessUUID),
		zap.String("refreshUUID", td.RefreshUUID),
		zap.Duration("accessTTL", accessTTL),
		zap.Duration("refreshTTL", refreshTTL),
		zap.String("userSetKey", userSetKey),
	)

	_, err := pipe.Exec(ctx)
	if err != nil {
		r.logger.Error("Failed to set token details in redis", zap.Error(err), zap.String("userID", userIDStr)) // <<< Логируем строку
		return fmt.Errorf("failed to set token details in redis: %w", err)
	}
	return nil
}

// DeleteTokens removes tokens from Redis based on their UUIDs and removes them from the user's set.
func (r *redisTokenRepository) DeleteTokens(ctx context.Context, userID uuid.UUID, accessUUID, refreshUUID string) (int64, error) { // <<< ADDED userID
	keysToDelete := []string{}
	identifiersToRemove := []interface{}{}                          // Use interface{} for SRem arguments
	logFields := []zap.Field{zap.String("userID", userID.String())} // Add userID to logs
	userSetKey := fmt.Sprintf("user_tokens:%s", userID.String())    // Key for the user's set

	if accessUUID != "" {
		accessKey := fmt.Sprintf("access_uuid:%s", accessUUID)
		accessIdentifier := fmt.Sprintf("access:%s", accessUUID)
		keysToDelete = append(keysToDelete, accessKey)
		identifiersToRemove = append(identifiersToRemove, accessIdentifier)
		logFields = append(logFields, zap.String("accessUUID", accessUUID))
	}
	if refreshUUID != "" {
		refreshKey := fmt.Sprintf("refresh_uuid:%s", refreshUUID)
		refreshIdentifier := fmt.Sprintf("refresh:%s", refreshUUID)
		keysToDelete = append(keysToDelete, refreshKey)
		identifiersToRemove = append(identifiersToRemove, refreshIdentifier)
		logFields = append(logFields, zap.String("refreshUUID", refreshUUID))
	}

	if len(keysToDelete) == 0 {
		r.logger.Warn("DeleteTokens called with no UUIDs")
		return 0, nil // Nothing to delete
	}

	r.logger.Debug("Deleting tokens from Redis and removing from set", logFields...)

	pipe := r.client.Pipeline()
	var delCmd *redis.IntCmd
	// Delete the actual token keys
	if len(keysToDelete) > 0 {
		delCmd = pipe.Del(ctx, keysToDelete...)
	}
	// Remove identifiers from the set
	if len(identifiersToRemove) > 0 {
		pipe.SRem(ctx, userSetKey, identifiersToRemove...)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		logFields = append(logFields, zap.Error(err))
		r.logger.Error("Failed to execute pipeline for deleting tokens and removing from set", logFields...)
		// Return 0 deleted count as the operation wasn't fully successful/atomic
		return 0, fmt.Errorf("failed to delete tokens/remove from set: %w", err)
	}

	// Get the result from the DEL command if it was executed
	var deletedCount int64
	if delCmd != nil {
		deletedCount, _ = delCmd.Result() // Ignore error here, pipeline error checked above
	}

	logFields = append(logFields, zap.Int64("deletedCount", deletedCount))
	r.logger.Info("Tokens deleted from Redis and removed from set", logFields...)
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

// DeleteRefreshUUID removes only the refresh token UUID from Redis and its identifier from the user's set.
func (r *redisTokenRepository) DeleteRefreshUUID(ctx context.Context, userID uuid.UUID, refreshUUID string) error { // <<< ADDED userID
	key := fmt.Sprintf("refresh_uuid:%s", refreshUUID)
	identifier := fmt.Sprintf("refresh:%s", refreshUUID)
	userSetKey := fmt.Sprintf("user_tokens:%s", userID.String())
	logFields := []zap.Field{ // <<< ADDED logFields
		zap.String("userID", userID.String()),
		zap.String("refreshUUID", refreshUUID),
		zap.String("key", key),
		zap.String("identifier", identifier),
		zap.String("userSetKey", userSetKey),
	}
	r.logger.Debug("Deleting refresh token from Redis and set", logFields...)

	// Use pipeline for atomicity, although less critical here
	pipe := r.client.Pipeline()
	pipe.Del(ctx, key)
	pipe.SRem(ctx, userSetKey, identifier)

	cmders, err := pipe.Exec(ctx)
	if err != nil {
		logFields = append(logFields, zap.Error(err))
		r.logger.Error("Failed to execute pipeline for deleting refresh token and removing from set", logFields...)
		return fmt.Errorf("failed to delete refresh token %s or remove from set: %w", refreshUUID, err)
	}

	// Optionally check individual command errors if needed, but pipeline error usually suffices
	deletedCount := int64(0)
	if len(cmders) > 0 {
		if delResult, ok := cmders[0].(*redis.IntCmd); ok {
			deletedCount, _ = delResult.Result() // Ignore error, check pipeline error
		}
	}

	if deletedCount == 0 {
		r.logger.Warn("Attempted to delete non-existent refresh token key (or set removal failed)", logFields...)
		// Not returning error, as goal is idempotency (token should be gone)
	}

	r.logger.Info("Refresh token deleted from Redis and removed from set", logFields...)
	return nil
}

// DeleteTokensByUserID removes all tokens associated with a user ID using the user-specific set.
func (r *redisTokenRepository) DeleteTokensByUserID(ctx context.Context, userID uuid.UUID) (int64, error) { // <<< Меняем тип userID
	log := r.logger.With(zap.String("userID", userID.String())) // <<< Используем userID.String()
	log.Info("Attempting to delete all tokens for user using Set")

	userIDStr := userID.String() // <<< Преобразуем UUID в строку
	userSetKey := fmt.Sprintf("user_tokens:%s", userIDStr)

	// 1. Get all token identifiers from the user's set
	tokenIdentifiers, err := r.client.SMembers(ctx, userSetKey).Result()
	if err != nil {
		if err == redis.Nil { // Key (set) doesn't exist
			log.Info("No token set found for user, nothing to delete.")
			return 0, nil
		}
		log.Error("Failed to get token identifiers from user set", zap.Error(err))
		return 0, fmt.Errorf("failed to retrieve token identifiers for user %s: %w", userIDStr, err)
	}

	if len(tokenIdentifiers) == 0 {
		log.Info("Token set for user is empty, nothing to delete.")
		// Attempt to delete the (empty) set key just in case
		r.client.Del(ctx, userSetKey) // Fire and forget potential error here
		return 0, nil
	}

	// 2. Construct the actual token keys to delete
	keysToDelete := make([]string, 0, len(tokenIdentifiers))
	for _, identifier := range tokenIdentifiers {
		parts := strings.SplitN(identifier, ":", 2)
		if len(parts) != 2 {
			log.Warn("Malformed token identifier found in user set", zap.String("identifier", identifier))
			continue // Skip malformed identifier
		}
		tokType := parts[0]
		tokUUID := parts[1]
		switch tokType {
		case "access":
			keysToDelete = append(keysToDelete, fmt.Sprintf("access_uuid:%s", tokUUID))
		case "refresh":
			keysToDelete = append(keysToDelete, fmt.Sprintf("refresh_uuid:%s", tokUUID))
		default:
			log.Warn("Unknown token type found in user set identifier", zap.String("identifier", identifier), zap.String("type", tokType))
		}
	}

	// 3. Delete the actual token keys and the user set
	var totalDeleted int64
	pipe := r.client.Pipeline()

	if len(keysToDelete) > 0 {
		log.Debug("Deleting actual token keys", zap.Strings("keys", keysToDelete))
		pipe.Del(ctx, keysToDelete...)
	}

	log.Debug("Deleting user token set key", zap.String("userSetKey", userSetKey))
	pipe.Del(ctx, userSetKey) // Always delete the set key

	cmders, err := pipe.Exec(ctx)
	if err != nil {
		// Even if exec fails, some commands might have succeeded.
		// It's hard to know the exact state without inspecting cmders results.
		log.Error("Failed to execute pipeline for deleting tokens and set", zap.Error(err))
		// Return 0 deleted count as the operation wasn't fully successful/atomic
		return 0, fmt.Errorf("failed to delete tokens and set for user %s: %w", userIDStr, err)
	}

	// Check the result of the first DEL command (token keys) if it exists
	// Initialize totalDeleted before the block
	if len(keysToDelete) > 0 && len(cmders) > 0 {
		if delResult, ok := cmders[0].(*redis.IntCmd); ok {
			// Assign result directly to totalDeleted if no error
			count, delErr := delResult.Result()
			if delErr == nil {
				totalDeleted = count // Assign directly
			} else {
				log.Warn("Error getting result from token keys DEL command", zap.Error(delErr))
				// totalDeleted remains 0 if there was an error
			}
		}
	}

	log.Info("Deleted tokens for user using Set", zap.Int64("deletedTokenKeys", totalDeleted), zap.Int("tokenIdentifiersFound", len(tokenIdentifiers)))
	return totalDeleted, nil
}
