package database

import (
	"context"
	"errors"
	"fmt"

	interfaces "novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models"

	"github.com/jackc/pgx/v5"
	"github.com/lib/pq"
	"go.uber.org/zap"
)

// Compile-time check to ensure pgImageReferenceRepository implements the interface
var _ interfaces.ImageReferenceRepository = (*pgImageReferenceRepository)(nil)

// pgImageReferenceRepository реализует интерфейс ImageReferenceRepository для PostgreSQL.
type pgImageReferenceRepository struct {
	db     interfaces.DBTX // Can be *pgxpool.Pool or *pgx.Tx
	logger *zap.Logger
}

// NewPgImageReferenceRepository создает новый экземпляр pgImageReferenceRepository.
func NewPgImageReferenceRepository(db interfaces.DBTX, logger *zap.Logger) interfaces.ImageReferenceRepository {
	return &pgImageReferenceRepository{
		db:     db,
		logger: logger.Named("PgImageReferenceRepo"), // Добавляем имя для логгера
	}
}

// GetImageURLByReference получает URL изображения по его reference.
func (r *pgImageReferenceRepository) GetImageURLByReference(ctx context.Context, reference string) (string, error) {
	query := `SELECT image_url FROM image_references WHERE reference = $1`
	var imageURL string

	err := r.db.QueryRow(ctx, query, reference).Scan(&imageURL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Debug("Image reference not found", zap.String("reference", reference))
			// Используем стандартную ошибку NotFound
			return "", fmt.Errorf("%w: image reference '%s' not found", sharedModels.ErrNotFound, reference)
		}
		r.logger.Error("Error querying image URL by reference", zap.String("reference", reference), zap.Error(err))
		return "", fmt.Errorf("database error querying image reference '%s': %w", reference, err)
	}

	r.logger.Debug("Image reference found", zap.String("reference", reference), zap.String("image_url", imageURL))
	return imageURL, nil
}

// SaveOrUpdateImageReference сохраняет или обновляет URL для данного reference.
func (r *pgImageReferenceRepository) SaveOrUpdateImageReference(ctx context.Context, reference string, imageURL string) error {
	query := `
        INSERT INTO image_references (reference, image_url)
        VALUES ($1, $2)
        ON CONFLICT (reference) DO UPDATE SET
            image_url = EXCLUDED.image_url,
            updated_at = NOW()
    `

	cmdTag, err := r.db.Exec(ctx, query, reference, imageURL)
	if err != nil {
		r.logger.Error("Error executing save/update image reference", zap.String("reference", reference), zap.String("image_url", imageURL), zap.Error(err))
		return fmt.Errorf("database error saving/updating image reference '%s': %w", reference, err)
	}

	if cmdTag.RowsAffected() == 0 {
		// Это может произойти, если ON CONFLICT сработал, но данные не изменились.
		// В нашем случае это маловероятно, т.к. мы всегда обновляем updated_at,
		// но логируем на всякий случай.
		r.logger.Warn("SaveOrUpdateImageReference query executed but no rows were affected", zap.String("reference", reference))
	} else {
		r.logger.Debug("Image reference saved or updated successfully", zap.String("reference", reference), zap.Int64("rows_affected", cmdTag.RowsAffected()))
	}

	return nil
}

// GetImageURLsByReferences retrieves multiple image URLs based on a list of reference strings.
func (r *pgImageReferenceRepository) GetImageURLsByReferences(ctx context.Context, refs []string) (map[string]string, error) {
	if len(refs) == 0 {
		return make(map[string]string), nil // Return empty map if no refs provided
	}

	query := `SELECT reference, image_url FROM image_references WHERE reference = ANY($1::text[])`
	logFields := []zap.Field{zap.Int("ref_count", len(refs))}
	r.logger.Debug("Getting image URLs by references (batch)", logFields...)

	rows, err := r.db.Query(ctx, query, pq.Array(refs))
	if err != nil {
		r.logger.Error("Failed to query image URLs by references", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("failed to query image URLs by references: %w", err)
	}
	defer rows.Close()

	results := make(map[string]string)
	for rows.Next() {
		var ref, url string
		if err := rows.Scan(&ref, &url); err != nil {
			r.logger.Error("Failed to scan image reference row", append(logFields, zap.Error(err))...)
			// Continue scanning other rows even if one fails
			continue
		}
		results[ref] = url
	}

	if err := rows.Err(); err != nil {
		r.logger.Error("Error iterating image reference rows", append(logFields, zap.Error(err))...)
		return nil, fmt.Errorf("error iterating image reference results: %w", err)
	}

	r.logger.Debug("Image URLs retrieved successfully (batch)", append(logFields, zap.Int("found_count", len(results)))...)
	return results, nil
}
