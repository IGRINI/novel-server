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
	// Используем универсальную транзакционную обёртку
	return WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		// 1. Строим payload
		payload, err := buildPayload(ctx, tx)
		if err != nil {
			return err
		}
		// 2. После сохранения статусов вызываем postProcess для публикации
		if postProcess != nil {
			if err2 := postProcess(ctx, tx, payload); err2 != nil {
				return fmt.Errorf("post-process retry failed: %w", err2)
			}
		}
		return nil
	})
}
