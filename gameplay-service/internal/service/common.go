package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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

// WrapNotFound преобразует ошибку pgx.ErrNoRows в models.ErrNotFound.
func WrapNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return models.ErrNotFound
	}
	return err
}
