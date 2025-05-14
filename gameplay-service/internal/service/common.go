package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"novel-server/shared/models"
)

// WithTx выполняет fn в рамках транзакции, коммитит при успехе или откатывает при ошибке.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}
	// Откат при панике
	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback(context.Background())
			panic(r)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit tx: %w", err)
	}
	return nil
}

// SanitizeLimit проверяет и корректирует значение limit, устанавливая defaultVal, если оно вне [1, max].
func SanitizeLimit(limit *int, defaultVal, max int) {
	if *limit <= 0 || *limit > max {
		*limit = defaultVal
	}
}

// WrapRepoError обрабатывает ошибку репозитория: преобразует ErrNoRows в models.ErrNotFound и другие ошибки в models.ErrInternalServer, логгируя их.
func WrapRepoError(logger *zap.Logger, err error, entity string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		logger.Warn(fmt.Sprintf("%s not found", entity), zap.Error(err))
		return models.ErrNotFound
	}
	if err != nil {
		logger.Error(fmt.Sprintf("error querying %s", entity), zap.Error(err))
		return models.ErrInternalServer
	}
	return nil
}

// DecodeStrictJSON проверяет корректность JSON и десериализует его в v.
// Возвращает ErrBadRequest при некорректном формате или ErrInternalServer при ошибке Unmarshal.
func DecodeStrictJSON(data []byte, v interface{}) error {
	if !json.Valid(data) {
		return fmt.Errorf("%w: invalid JSON format", models.ErrBadRequest)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%w: %v", models.ErrInternalServer, err)
	}
	return nil
}

// PaginateList применяет проверку limit, вызывает функцию listFn для получения limit+1 элементов,
// возвращает усечённый список длины limit и корректный nextCursor.
// listFn принимает скорректированный limit+1 и возвращает ([]T, nextCursor, error).
func PaginateList[T any](limit *int, defaultVal, maxVal int, listFn func(int) ([]T, string, error)) ([]T, string, error) {
	SanitizeLimit(limit, defaultVal, maxVal)
	items, nextCursor, err := listFn(*limit + 1)
	if err != nil {
		return nil, "", err
	}
	if len(items) > *limit {
		items = items[:*limit]
	} else {
		nextCursor = ""
	}
	return items, nextCursor, nil
}
