package repository

import (
	"context"
	"errors"
	"fmt"
	"log"
	"novel-server/gameplay-service/internal/models"
	sharedModels "novel-server/shared/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// StoryConfigRepository определяет интерфейс для работы с хранилищем конфигураций историй.
// Используем интерфейс для возможности мокирования в тестах.
type StoryConfigRepository interface {
	Create(ctx context.Context, config *models.StoryConfig) error
	GetByID(ctx context.Context, id uuid.UUID, userID uint64) (*models.StoryConfig, error)
	Update(ctx context.Context, config *models.StoryConfig) error
	GetByIDInternal(ctx context.Context, id uuid.UUID) (*models.StoryConfig, error)
	CountActiveGenerations(ctx context.Context, userID uint64) (int, error)
	// Можно добавить ListByUser, Delete и т.д. при необходимости
}

type postgresStoryConfigRepository struct {
	db *pgxpool.Pool
}

func NewPostgresStoryConfigRepository(db *pgxpool.Pool) StoryConfigRepository {
	return &postgresStoryConfigRepository{db: db}
}

func (r *postgresStoryConfigRepository) Create(ctx context.Context, config *models.StoryConfig) error {
	query := `
        INSERT INTO story_configs (id, user_id, title, description, user_input, config, status, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
    `
	_, err := r.db.Exec(ctx, query,
		config.ID,
		config.UserID,
		config.Title,
		config.Description,
		config.UserInput,
		config.Config,
		config.Status,
		config.CreatedAt,
		config.UpdatedAt,
	)

	if err != nil {
		// Проверка на уникальность (если нужно)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			log.Printf("Ошибка создания StoryConfig: конфликт уникальности (ID: %s)", config.ID)
			// Можно вернуть специфическую ошибку, например, ErrConflict
			return fmt.Errorf("конфликт уникальности: %w", err)
		}
		log.Printf("Ошибка выполнения INSERT для StoryConfig (ID: %s): %v", config.ID, err)
		return fmt.Errorf("ошибка создания StoryConfig в БД: %w", err)
	}

	log.Printf("StoryConfig успешно создан (ID: %s, UserID: %d)", config.ID, config.UserID)
	return nil
}

func (r *postgresStoryConfigRepository) GetByID(ctx context.Context, id uuid.UUID, userID uint64) (*models.StoryConfig, error) {
	query := `
        SELECT id, user_id, title, description, user_input, config, status, created_at, updated_at
        FROM story_configs
        WHERE id = $1 AND user_id = $2
    `
	config := &models.StoryConfig{}
	err := r.db.QueryRow(ctx, query, id, userID).Scan(
		&config.ID,
		&config.UserID,
		&config.Title,
		&config.Description,
		&config.UserInput,
		&config.Config,
		&config.Status,
		&config.CreatedAt,
		&config.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("StoryConfig не найден (ID: %s, UserID: %d)", id, userID)
			// Возвращаем стандартную ошибку "не найдено" из shared
			return nil, sharedModels.ErrNotFound // Используем ошибку из shared
		}
		log.Printf("Ошибка выполнения SELECT для StoryConfig (ID: %s, UserID: %d): %v", id, userID, err)
		return nil, fmt.Errorf("ошибка получения StoryConfig из БД: %w", err)
	}

	log.Printf("StoryConfig успешно получен (ID: %s)", config.ID)
	return config, nil
}

func (r *postgresStoryConfigRepository) Update(ctx context.Context, config *models.StoryConfig) error {
	query := `
        UPDATE story_configs
        SET title = $1, description = $2, user_input = $3, config = $4, status = $5, updated_at = $6
        WHERE id = $7 AND user_id = $8
    `
	commandTag, err := r.db.Exec(ctx, query,
		config.Title,
		config.Description,
		config.UserInput,
		config.Config,
		config.Status,
		config.UpdatedAt,
		config.ID,
		config.UserID, // Важно проверять UserID при обновлении
	)

	if err != nil {
		log.Printf("Ошибка выполнения UPDATE для StoryConfig (ID: %s): %v", config.ID, err)
		return fmt.Errorf("ошибка обновления StoryConfig в БД: %w", err)
	}

	if commandTag.RowsAffected() == 0 {
		log.Printf("Попытка обновления несуществующего или чужого StoryConfig (ID: %s, UserID: %d)", config.ID, config.UserID)
		// Запись не найдена для обновления (либо по ID, либо по UserID)
		return sharedModels.ErrNotFound // Используем ошибку из shared
	}

	log.Printf("StoryConfig успешно обновлен (ID: %s)", config.ID)
	return nil
}

// GetByIDInternal получает StoryConfig по ID без проверки UserID (для внутреннего использования)
func (r *postgresStoryConfigRepository) GetByIDInternal(ctx context.Context, id uuid.UUID) (*models.StoryConfig, error) {
	query := `
        SELECT id, user_id, title, description, user_input, config, status, created_at, updated_at
        FROM story_configs
        WHERE id = $1
    `
	config := &models.StoryConfig{}
	err := r.db.QueryRow(ctx, query, id).Scan(
		&config.ID,
		&config.UserID,
		&config.Title,
		&config.Description,
		&config.UserInput,
		&config.Config,
		&config.Status,
		&config.CreatedAt,
		&config.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[Repo] StoryConfig не найден (ID: %s) при внутреннем запросе", id)
			return nil, sharedModels.ErrNotFound
		}
		log.Printf("[Repo] Ошибка выполнения SELECT для StoryConfig (ID: %s) при внутреннем запросе: %v", id, err)
		return nil, fmt.Errorf("ошибка получения StoryConfig (internal) из БД: %w", err)
	}

	log.Printf("[Repo] StoryConfig успешно получен (ID: %s) при внутреннем запросе", config.ID)
	return config, nil
}

// CountActiveGenerations возвращает количество конфигов пользователя в статусе 'generating'.
func (r *postgresStoryConfigRepository) CountActiveGenerations(ctx context.Context, userID uint64) (int, error) {
	query := `
        SELECT COUNT(*) FROM story_configs WHERE user_id = $1 AND status = $2
    `
	var count int
	err := r.db.QueryRow(ctx, query, userID, models.StatusGenerating).Scan(&count)
	if err != nil {
		// Отдельно обрабатываем pgx.ErrNoRows? Нет, COUNT(*) всегда вернет строку, даже если 0.
		log.Printf("[Repo] Ошибка подсчета активных генераций для UserID %d: %v", userID, err)
		return 0, fmt.Errorf("ошибка подсчета активных генераций в БД: %w", err)
	}
	return count, nil
}

// Добавьте сюда реализацию других методов интерфейса, если они появятся

// Добавим импорт fmt, если он еще не используется
