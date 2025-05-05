package repository

import (
	"context"
	"fmt"
	interfaces "novel-server/shared/interfaces"
	"novel-server/shared/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

const (
	saveDeviceTokenQuery = `
		INSERT INTO user_device_tokens (user_id, token, platform, last_used_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id, token)
		DO UPDATE SET
			platform = EXCLUDED.platform,
			last_used_at = NOW();
	`
	getDeviceTokensForUserQuery = `
		SELECT token, platform
		FROM user_device_tokens
		WHERE user_id = $1;
	`
	deleteDeviceTokenQuery         = `DELETE FROM user_device_tokens WHERE token = $1;`
	deleteDeviceTokensForUserQuery = `DELETE FROM user_device_tokens WHERE user_id = $1;`
)

// Убедимся, что pgDeviceTokenRepository реализует интерфейс
var _ interfaces.UserDeviceTokenRepository = (*pgDeviceTokenRepository)(nil)

type pgDeviceTokenRepository struct {
	db     interfaces.DBTX
	logger *zap.Logger
}

func NewDeviceTokenRepository(db interfaces.DBTX, logger *zap.Logger) interfaces.UserDeviceTokenRepository {
	return &pgDeviceTokenRepository{
		db:     db,
		logger: logger.Named("device_token_repo"),
	}
}

// SaveDeviceToken сохраняет или обновляет токен устройства для пользователя.
// Использует INSERT ... ON CONFLICT DO UPDATE для атомарности.
func (r *pgDeviceTokenRepository) SaveDeviceToken(ctx context.Context, userID uuid.UUID, token, platform string) error {
	_, err := r.db.Exec(ctx, saveDeviceTokenQuery, userID, token, platform)
	if err != nil {
		r.logger.Error("Failed to save device token",
			zap.String("userID", userID.String()),
			zap.String("platform", platform),
			zap.Error(err),
		)
		// Здесь не стоит возвращать ErrDuplicateKey, т.к. ON CONFLICT его обрабатывает
		return fmt.Errorf("db error saving device token: %w", err)
	}

	r.logger.Debug("Successfully saved device token",
		zap.String("userID", userID.String()),
		zap.String("platform", platform),
	)
	return nil
}

// GetDeviceTokensForUser возвращает все активные токены для указанного пользователя.
func (r *pgDeviceTokenRepository) GetDeviceTokensForUser(ctx context.Context, userID uuid.UUID) ([]models.DeviceTokenInfo, error) {
	rows, err := r.db.Query(ctx, getDeviceTokensForUserQuery, userID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return []models.DeviceTokenInfo{}, nil // Нет токенов - не ошибка
		}
		r.logger.Error("Failed to query device tokens", zap.String("userID", userID.String()), zap.Error(err))
		return nil, fmt.Errorf("db error querying device tokens: %w", err)
	}
	defer rows.Close()

	tokens := make([]models.DeviceTokenInfo, 0)
	for rows.Next() {
		var tokenInfo models.DeviceTokenInfo
		if err := rows.Scan(&tokenInfo.Token, &tokenInfo.Platform); err != nil {
			r.logger.Error("Failed to scan device token row", zap.String("userID", userID.String()), zap.Error(err))
			// Не прерываем из-за одной плохой строки, но логируем
			continue
		}
		tokens = append(tokens, tokenInfo)
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating device token rows", zap.String("userID", userID.String()), zap.Error(err))
		// Возвращаем то, что успели собрать, и ошибку
		return tokens, fmt.Errorf("db error iterating device tokens: %w", err)
	}

	r.logger.Debug("Successfully fetched device tokens", zap.String("userID", userID.String()), zap.Int("count", len(tokens)))
	return tokens, nil
}

// DeleteDeviceToken удаляет конкретный токен.
// Может быть полезно, если FCM/APNS сообщают, что токен невалиден.
func (r *pgDeviceTokenRepository) DeleteDeviceToken(ctx context.Context, token string) error {
	cmdTag, err := r.db.Exec(ctx, deleteDeviceTokenQuery, token)
	if err != nil {
		r.logger.Error("Failed to delete device token", zap.String("token", token), zap.Error(err))
		return fmt.Errorf("db error deleting device token: %w", err)
	}

	if cmdTag.RowsAffected() == 0 {
		r.logger.Warn("Attempted to delete non-existent device token", zap.String("token", token))
		// Не считаем ошибкой, если токен уже удален
	}

	r.logger.Debug("Successfully deleted (or confirmed absence of) device token", zap.String("token", token))
	return nil
}

// DeleteDeviceTokensForUser удаляет все токены для указанного пользователя.
// Может быть полезно при удалении пользователя или сбросе сессий.
func (r *pgDeviceTokenRepository) DeleteDeviceTokensForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	cmdTag, err := r.db.Exec(ctx, deleteDeviceTokensForUserQuery, userID)
	if err != nil {
		r.logger.Error("Failed to delete device tokens for user", zap.String("userID", userID.String()), zap.Error(err))
		return 0, fmt.Errorf("db error deleting user device tokens: %w", err)
	}

	rowsAffected := cmdTag.RowsAffected()
	r.logger.Debug("Successfully deleted device tokens for user", zap.String("userID", userID.String()), zap.Int64("count", rowsAffected))
	return rowsAffected, nil
}
