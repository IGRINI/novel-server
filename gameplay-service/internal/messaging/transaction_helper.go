package messaging

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	interfaces "novel-server/shared/interfaces"
)

// TransactionHelper предоставляет унифицированные методы для работы с транзакциями
type TransactionHelper struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

// NewTransactionHelper создает новый помощник транзакций
func NewTransactionHelper(db *pgxpool.Pool, logger *zap.Logger) *TransactionHelper {
	return &TransactionHelper{
		db:     db,
		logger: logger,
	}
}

// WithTransaction выполняет функцию в транзакции с автоматическим rollback при ошибке
func (h *TransactionHelper) WithTransaction(
	ctx context.Context,
	fn func(ctx context.Context, tx interfaces.DBTX) error,
) error {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
				h.logger.Error("Failed to rollback transaction after panic",
					zap.Error(rollbackErr),
					zap.Any("panic", p))
			}
			panic(p) // re-throw panic after rollback
		}
	}()

	if err := fn(ctx, tx); err != nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			h.logger.Error("Failed to rollback transaction",
				zap.Error(rollbackErr),
				zap.NamedError("original_error", err))
		}
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// BeginTransaction начинает новую транзакцию (для случаев, когда нужен ручной контроль)
func (h *TransactionHelper) BeginTransaction(ctx context.Context) (pgx.Tx, error) {
	return h.db.Begin(ctx)
}

// CommitWithLogging коммитит транзакцию с логированием ошибок
func (h *TransactionHelper) CommitWithLogging(ctx context.Context, tx pgx.Tx, operation string) error {
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("Failed to commit transaction",
			zap.String("operation", operation),
			zap.Error(err))
		return fmt.Errorf("failed to commit %s transaction: %w", operation, err)
	}
	return nil
}

// RollbackWithLogging откатывает транзакцию с логированием ошибок
func (h *TransactionHelper) RollbackWithLogging(ctx context.Context, tx pgx.Tx, operation string) {
	if err := tx.Rollback(ctx); err != nil {
		h.logger.Error("Failed to rollback transaction",
			zap.String("operation", operation),
			zap.Error(err))
	}
}
