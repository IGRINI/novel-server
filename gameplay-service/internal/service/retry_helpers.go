package service

import (
	"context"
	"fmt"
	sharedMessaging "novel-server/shared/messaging"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// doRetryWithTx обобщает транзакционную обёртку для операций Retry
func (s *gameLoopServiceImpl) doRetryWithTx(
	ctx context.Context,
	userID, storyID uuid.UUID,
	buildPayload func(ctx context.Context, tx pgx.Tx) (*sharedMessaging.GenerationTaskPayload, error),
	postProcess func(ctx context.Context, tx pgx.Tx, payload *sharedMessaging.GenerationTaskPayload) error,
) error {
	// 1. Начинаем транзакцию
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin retry tx: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback(ctx)
		} else {
			err = tx.Commit(ctx)
		}
	}()

	// 2. Строим payload
	payload, err := buildPayload(ctx, tx)
	if err != nil {
		return err
	}

	// 3. После сохранения статусов и данных вызываем postProcess для публикации
	if postProcess != nil {
		if err2 := postProcess(ctx, tx, payload); err2 != nil {
			return fmt.Errorf("post-process retry failed: %w", err2)
		}
	}

	return nil
}
