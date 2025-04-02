package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"novel-server/internal/model"
)

// UserRepository представляет репозиторий для работы с пользователями
type UserRepository struct {
	pool *pgxpool.Pool
}

// NewUserRepository создает новый экземпляр репозитория для работы с пользователями
func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{
		pool: pool,
	}
}

// Create создает нового пользователя в базе данных
func (r *UserRepository) Create(ctx context.Context, user model.User) (model.User, error) {
	query := `
		INSERT INTO users (id, username, email, password_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		RETURNING id, username, email, password_hash, created_at, updated_at
	`

	now := time.Now()

	// Если ID не указан, генерируем новый
	if user.ID == uuid.Nil {
		user.ID = uuid.New()
	}

	row := r.pool.QueryRow(ctx, query,
		user.ID,
		user.Username,
		user.Email,
		user.Password,
		now,
	)

	var createdUser model.User
	err := row.Scan(
		&createdUser.ID,
		&createdUser.Username,
		&createdUser.Email,
		&createdUser.Password,
		&createdUser.CreatedAt,
		&createdUser.UpdatedAt,
	)
	if err != nil {
		return model.User{}, err
	}

	return createdUser, nil
}

// GetByID получает пользователя по ID
func (r *UserRepository) GetByID(ctx context.Context, id uuid.UUID) (model.User, error) {
	query := `
		SELECT id, username, email, password_hash, created_at, updated_at
		FROM users
		WHERE id = $1
	`

	row := r.pool.QueryRow(ctx, query, id)

	var user model.User
	err := row.Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.Password,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		return model.User{}, err
	}

	return user, nil
}

// GetByEmail получает пользователя по email
func (r *UserRepository) GetByEmail(ctx context.Context, email string) (model.User, error) {
	query := `
		SELECT id, username, email, password_hash, created_at, updated_at
		FROM users
		WHERE email = $1
	`

	row := r.pool.QueryRow(ctx, query, email)

	var user model.User
	err := row.Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.Password,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return model.User{}, errors.New("пользователь не найден")
		}
		return model.User{}, err
	}

	return user, nil
}

// GetByUsername получает пользователя по имени пользователя
func (r *UserRepository) GetByUsername(ctx context.Context, username string) (model.User, error) {
	query := `
		SELECT id, username, email, password_hash, created_at, updated_at
		FROM users
		WHERE username = $1
	`

	row := r.pool.QueryRow(ctx, query, username)

	var user model.User
	err := row.Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.Password,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return model.User{}, errors.New("пользователь не найден")
		}
		return model.User{}, err
	}

	return user, nil
}

// Update обновляет данные пользователя
func (r *UserRepository) Update(ctx context.Context, user model.User) (model.User, error) {
	query := `
		UPDATE users
		SET username = $2, email = $3, updated_at = $4
		WHERE id = $1
		RETURNING id, username, email, password_hash, created_at, updated_at
	`

	now := time.Now()

	row := r.pool.QueryRow(ctx, query,
		user.ID,
		user.Username,
		user.Email,
		now,
	)

	var updatedUser model.User
	err := row.Scan(
		&updatedUser.ID,
		&updatedUser.Username,
		&updatedUser.Email,
		&updatedUser.Password,
		&updatedUser.CreatedAt,
		&updatedUser.UpdatedAt,
	)
	if err != nil {
		return model.User{}, err
	}

	return updatedUser, nil
}

// UpdatePassword обновляет пароль пользователя
func (r *UserRepository) UpdatePassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	query := `
		UPDATE users
		SET password_hash = $2, updated_at = $3
		WHERE id = $1
	`

	now := time.Now()

	_, err := r.pool.Exec(ctx, query, id, passwordHash, now)
	return err
}

// Delete удаляет пользователя
func (r *UserRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM users WHERE id = $1`
	_, err := r.pool.Exec(ctx, query, id)
	return err
}
