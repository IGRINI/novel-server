package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	sharedInterfaces "novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models"
)

type PgGenerationResultRepository struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// NewPgGenerationResultRepository создает новый экземпляр PgGenerationResultRepository.
func NewPgGenerationResultRepository(pool *pgxpool.Pool, logger *zap.Logger) sharedInterfaces.GenerationResultRepository {
	return &PgGenerationResultRepository{
		pool:   pool,
		logger: logger.Named("PgGenerationResultRepo"),
	}
}

// Save сохраняет или обновляет результат генерации в базе данных.
func (r *PgGenerationResultRepository) Save(ctx context.Context, result *sharedModels.GenerationResult) error {
	query := `
		INSERT INTO generation_results (
			id, prompt_type, user_id, generated_text, error, 
			created_at, completed_at, processing_time_ms, 
			prompt_tokens, completion_tokens, estimated_cost_usd
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE SET
			prompt_type = EXCLUDED.prompt_type,
			user_id = EXCLUDED.user_id,
			generated_text = EXCLUDED.generated_text,
			error = EXCLUDED.error,
			created_at = EXCLUDED.created_at,
			completed_at = EXCLUDED.completed_at,
			processing_time_ms = EXCLUDED.processing_time_ms,
			prompt_tokens = EXCLUDED.prompt_tokens,
			completion_tokens = EXCLUDED.completion_tokens,
			estimated_cost_usd = EXCLUDED.estimated_cost_usd
	`
	tag, err := r.pool.Exec(ctx, query,
		result.ID,
		result.PromptType,
		result.UserID,
		result.GeneratedText,
		result.Error,
		result.CreatedAt,
		result.CompletedAt,
		result.ProcessingTimeMs,
		result.PromptTokens,
		result.CompletionTokens,
		result.EstimatedCostUSD,
	)
	if err != nil {
		r.logger.Error("Failed to save GenerationResult",
			zap.String("task_id", result.ID),
			zap.Error(err),
		)
		return fmt.Errorf("error saving generation result: %w", err)
	}
	r.logger.Debug("GenerationResult saved successfully", zap.String("task_id", result.ID), zap.Int64("rows_affected", tag.RowsAffected()))
	return nil
}

// GetByTaskID получает результат генерации по ID задачи.
func (r *PgGenerationResultRepository) GetByTaskID(ctx context.Context, taskID string) (*sharedModels.GenerationResult, error) {
	query := `
		SELECT 
			id, prompt_type, user_id, generated_text, error, 
			created_at, completed_at, processing_time_ms, 
			prompt_tokens, completion_tokens, estimated_cost_usd
		FROM generation_results
		WHERE id = $1
	`
	var result sharedModels.GenerationResult
	err := r.pool.QueryRow(ctx, query, taskID).Scan(
		&result.ID,
		&result.PromptType,
		&result.UserID,
		&result.GeneratedText,
		&result.Error,
		&result.CreatedAt,
		&result.CompletedAt,
		&result.ProcessingTimeMs,
		&result.PromptTokens,
		&result.CompletionTokens,
		&result.EstimatedCostUSD,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			r.logger.Warn("GenerationResult not found by TaskID", zap.String("task_id", taskID))
			return nil, sharedModels.ErrNotFound
		}
		r.logger.Error("Failed to get GenerationResult by TaskID",
			zap.String("task_id", taskID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("error getting generation result by task id: %w", err)
	}

	r.logger.Debug("GenerationResult retrieved successfully by TaskID", zap.String("task_id", taskID))
	return &result, nil
}

// FindOlderThan находит результаты генерации старше указанной даты (не реализовано).
func (r *PgGenerationResultRepository) FindOlderThan(ctx context.Context, threshold time.Time) ([]*sharedModels.GenerationResult, error) {
	// TODO: Реализовать при необходимости (для очистки старых записей)
	r.logger.Warn("FindOlderThan method is not implemented")
	return nil, fmt.Errorf("FindOlderThan is not implemented")
}

// DeleteByTaskID удаляет результат генерации по ID задачи (не реализовано).
func (r *PgGenerationResultRepository) DeleteByTaskID(ctx context.Context, taskID string) error {
	// TODO: Реализовать при необходимости (для очистки старых записей)
	r.logger.Warn("DeleteByTaskID method is not implemented")
	return fmt.Errorf("DeleteByTaskID is not implemented")
}
