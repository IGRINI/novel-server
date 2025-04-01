package repository

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresNovelDraftRepository реализация NovelDraftRepository для PostgreSQL
type PostgresNovelDraftRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresNovelDraftRepository создает новый экземпляр PostgresNovelDraftRepository
func NewPostgresNovelDraftRepository(pool *pgxpool.Pool) *PostgresNovelDraftRepository {
	return &PostgresNovelDraftRepository{
		pool: pool,
	}
}

// SaveDraft сохраняет новый черновик в базу данных
func (r *PostgresNovelDraftRepository) SaveDraft(ctx context.Context, userID string, draftID uuid.UUID, configJSON []byte) error {
	log.Printf("[PostgresNovelDraftRepository] SaveDraft - Saving draft with ID: %s for UserID: %s", draftID, userID)

	query := `
		INSERT INTO novel_drafts (draft_id, user_id, config_json)
		VALUES ($1, $2, $3)
	`

	_, err := r.pool.Exec(ctx, query, draftID, userID, configJSON)
	if err != nil {
		log.Printf("[PostgresNovelDraftRepository] SaveDraft - Error saving draft: %v", err)
		return fmt.Errorf("failed to save draft: %w", err)
	}

	log.Printf("[PostgresNovelDraftRepository] SaveDraft - Successfully saved draft with ID: %s", draftID)
	return nil
}

// GetDraftConfigJSON получает сериализованный конфиг черновика по ID
func (r *PostgresNovelDraftRepository) GetDraftConfigJSON(ctx context.Context, userID string, draftID uuid.UUID) ([]byte, error) {
	log.Printf("[PostgresNovelDraftRepository] GetDraftConfigJSON - Getting draft with ID: %s for UserID: %s", draftID, userID)

	query := `
		SELECT config_json FROM novel_drafts
		WHERE draft_id = $1 AND user_id = $2
	`

	var configJSON []byte
	err := r.pool.QueryRow(ctx, query, draftID, userID).Scan(&configJSON)
	if err != nil {
		log.Printf("[PostgresNovelDraftRepository] GetDraftConfigJSON - Error getting draft: %v", err)
		return nil, fmt.Errorf("failed to get draft: %w", err)
	}

	log.Printf("[PostgresNovelDraftRepository] GetDraftConfigJSON - Successfully retrieved draft with ID: %s", draftID)
	return configJSON, nil
}

// UpdateDraftConfigJSON обновляет сериализованный конфиг существующего черновика
func (r *PostgresNovelDraftRepository) UpdateDraftConfigJSON(ctx context.Context, userID string, draftID uuid.UUID, configJSON []byte) error {
	log.Printf("[PostgresNovelDraftRepository] UpdateDraftConfigJSON - Updating draft with ID: %s for UserID: %s", draftID, userID)

	query := `
		UPDATE novel_drafts
		SET config_json = $3
		WHERE draft_id = $1 AND user_id = $2
	`

	result, err := r.pool.Exec(ctx, query, draftID, userID, configJSON)
	if err != nil {
		log.Printf("[PostgresNovelDraftRepository] UpdateDraftConfigJSON - Error updating draft: %v", err)
		return fmt.Errorf("failed to update draft: %w", err)
	}

	if result.RowsAffected() == 0 {
		log.Printf("[PostgresNovelDraftRepository] UpdateDraftConfigJSON - Draft not found: %s", draftID)
		return errors.New("draft not found")
	}

	log.Printf("[PostgresNovelDraftRepository] UpdateDraftConfigJSON - Successfully updated draft with ID: %s", draftID)
	return nil
}

// DeleteDraft удаляет черновик
func (r *PostgresNovelDraftRepository) DeleteDraft(ctx context.Context, userID string, draftID uuid.UUID) error {
	log.Printf("[PostgresNovelDraftRepository] DeleteDraft - Deleting draft with ID: %s for UserID: %s", draftID, userID)

	query := `
		DELETE FROM novel_drafts
		WHERE draft_id = $1 AND user_id = $2
	`

	result, err := r.pool.Exec(ctx, query, draftID, userID)
	if err != nil {
		log.Printf("[PostgresNovelDraftRepository] DeleteDraft - Error deleting draft: %v", err)
		return fmt.Errorf("failed to delete draft: %w", err)
	}

	if result.RowsAffected() == 0 {
		log.Printf("[PostgresNovelDraftRepository] DeleteDraft - Draft not found: %s", draftID)
		return errors.New("draft not found")
	}

	log.Printf("[PostgresNovelDraftRepository] DeleteDraft - Successfully deleted draft with ID: %s", draftID)
	return nil
}
