package repository

import (
	"context"
	"fmt"
	"log"

	"novel-server/story-generator/internal/model"

	"github.com/jackc/pgx/v5/pgxpool"
)

// postgresResultRepository реализует ResultRepository для PostgreSQL.
type postgresResultRepository struct {
	db *pgxpool.Pool
}

// NewPostgresResultRepository создает новый экземпляр репозитория для PostgreSQL.
func NewPostgresResultRepository(db *pgxpool.Pool) ResultRepository {
	return &postgresResultRepository{db: db}
}

// Save сохраняет результат генерации в базу данных.
func (r *postgresResultRepository) Save(ctx context.Context, result *model.GenerationResult) error {
	query := `
        INSERT INTO generation_results 
        (id, user_id, prompt_type, input_data, generated_text, processing_time_ms, created_at, completed_at, error)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
        ON CONFLICT (id) DO UPDATE SET
            user_id = EXCLUDED.user_id,
            prompt_type = EXCLUDED.prompt_type,
            input_data = EXCLUDED.input_data,
            generated_text = EXCLUDED.generated_text,
            processing_time_ms = EXCLUDED.processing_time_ms,
            completed_at = EXCLUDED.completed_at,
            error = EXCLUDED.error;
    `
	// Конвертируем duration в миллисекунды для хранения
	processingTimeMs := result.ProcessingTime.Milliseconds()

	_, err := r.db.Exec(ctx, query,
		result.ID,
		result.UserID,
		result.PromptType,
		result.InputData,
		result.GeneratedText,
		processingTimeMs,
		result.CreatedAt,
		result.CompletedAt,
		result.Error, // Передаем пустую строку, если ошибки нет
	)

	if err != nil {
		log.Printf("[TaskID: %s] Ошибка сохранения результата в БД: %v", result.ID, err)
		return fmt.Errorf("ошибка сохранения результата '%s' в БД: %w", result.ID, err)
	}

	log.Printf("[TaskID: %s] Результат успешно сохранен в БД.", result.ID)
	return nil
}

// --- TODO: Реализовать методы GetByID, ListByUserID, если они понадобятся ---
