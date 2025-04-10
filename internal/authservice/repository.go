package authservice

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// User представляет пользователя в системе
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"display_name"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// RefreshToken представляет токен обновления в системе
type RefreshToken struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expires_at"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Revoked    bool      `json:"revoked"`
	DeviceInfo string    `json:"device_info,omitempty"`
}

// Repository предоставляет доступ к данным пользователей и токенов
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository создает новый экземпляр репозитория
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// CreateUser создает нового пользователя
func (r *Repository) CreateUser(ctx context.Context, user *User) error {
	if user.ID == "" {
		user.ID = uuid.New().String()
	}

	if user.DisplayName == "" {
		user.DisplayName = user.Username
	}

	query := `
		INSERT INTO users (id, username, display_name, email, password_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`

	_, err := r.db.Exec(ctx, query,
		user.ID,
		user.Username,
		user.DisplayName,
		user.Email,
		user.PasswordHash,
		user.CreatedAt,
		user.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("ошибка при создании пользователя: %w", err)
	}

	return nil
}

// GetUserByUsername находит пользователя по имени пользователя
func (r *Repository) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	query := `
		SELECT id, username, display_name, email, password_hash, created_at, updated_at
		FROM users
		WHERE LOWER(username) = LOWER($1)
	`

	user := &User{}
	err := r.db.QueryRow(ctx, query, username).Scan(
		&user.ID,
		&user.Username,
		&user.DisplayName,
		&user.Email,
		&user.PasswordHash,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, pgx.ErrNoRows
		}
		return nil, fmt.Errorf("ошибка при поиске пользователя по username: %w", err)
	}

	return user, nil
}

// GetUserByEmail находит пользователя по email
func (r *Repository) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	query := `
		SELECT id, username, display_name, email, password_hash, created_at, updated_at
		FROM users
		WHERE LOWER(email) = LOWER($1)
	`

	user := &User{}
	err := r.db.QueryRow(ctx, query, email).Scan(
		&user.ID,
		&user.Username,
		&user.DisplayName,
		&user.Email,
		&user.PasswordHash,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, pgx.ErrNoRows
		}
		return nil, fmt.Errorf("ошибка при поиске пользователя по email: %w", err)
	}

	return user, nil
}

// GetUserByID находит пользователя по ID
func (r *Repository) GetUserByID(ctx context.Context, userID string) (*User, error) {
	query := `
		SELECT id, username, display_name, email, password_hash, created_at, updated_at
		FROM users
		WHERE id = $1
	`

	user := &User{}
	err := r.db.QueryRow(ctx, query, userID).Scan(
		&user.ID,
		&user.Username,
		&user.DisplayName,
		&user.Email,
		&user.PasswordHash,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, pgx.ErrNoRows
		}
		return nil, fmt.Errorf("ошибка при поиске пользователя по ID: %w", err)
	}

	return user, nil
}

// CreateRefreshToken создает новый токен обновления
func (r *Repository) CreateRefreshToken(ctx context.Context, token *RefreshToken) error {
	query := `
		INSERT INTO refresh_tokens (id, user_id, token, expires_at, created_at, updated_at, revoked, device_info)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	if token.ID == "" {
		token.ID = uuid.New().String()
	}

	_, err := r.db.Exec(ctx, query,
		token.ID,
		token.UserID,
		token.Token,
		token.ExpiresAt,
		token.CreatedAt,
		token.UpdatedAt,
		token.Revoked,
		token.DeviceInfo,
	)

	if err != nil {
		return fmt.Errorf("ошибка при создании токена обновления: %w", err)
	}

	return nil
}

// GetRefreshTokenByToken находит токен обновления по строке токена
func (r *Repository) GetRefreshTokenByToken(ctx context.Context, tokenStr string) (*RefreshToken, error) {
	query := `
		SELECT id, user_id, token, expires_at, created_at, updated_at, revoked, device_info
		FROM refresh_tokens
		WHERE token = $1
	`

	token := &RefreshToken{}
	err := r.db.QueryRow(ctx, query, tokenStr).Scan(
		&token.ID,
		&token.UserID,
		&token.Token,
		&token.ExpiresAt,
		&token.CreatedAt,
		&token.UpdatedAt,
		&token.Revoked,
		&token.DeviceInfo,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, pgx.ErrNoRows
		}
		return nil, fmt.Errorf("ошибка при поиске токена обновления: %w", err)
	}

	return token, nil
}

// RevokeAllUserTokens отзывает все токены пользователя
func (r *Repository) RevokeAllUserTokens(ctx context.Context, userID string) error {
	query := `
		UPDATE refresh_tokens
		SET revoked = true, updated_at = NOW()
		WHERE user_id = $1 AND revoked = false
	`

	_, err := r.db.Exec(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("ошибка при отзыве токенов пользователя: %w", err)
	}

	return nil
}

// RevokeRefreshToken отзывает конкретный токен обновления
func (r *Repository) RevokeRefreshToken(ctx context.Context, tokenStr string) error {
	query := `
		UPDATE refresh_tokens
		SET revoked = true, updated_at = NOW()
		WHERE token = $1
	`

	_, err := r.db.Exec(ctx, query, tokenStr)
	if err != nil {
		return fmt.Errorf("ошибка при отзыве токена: %w", err)
	}

	return nil
}

// DeleteExpiredTokens удаляет просроченные токены
func (r *Repository) DeleteExpiredTokens(ctx context.Context) error {
	query := `
		DELETE FROM refresh_tokens
		WHERE expires_at < NOW()
	`

	_, err := r.db.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("ошибка при удалении просроченных токенов: %w", err)
	}

	return nil
}

// UpdateDisplayName обновляет отображаемое имя пользователя
func (r *Repository) UpdateDisplayName(ctx context.Context, userID string, displayName string) error {
	query := `
		UPDATE users
		SET display_name = $1, updated_at = NOW()
		WHERE id = $2
	`

	_, err := r.db.Exec(ctx, query, displayName, userID)
	if err != nil {
		return fmt.Errorf("ошибка при обновлении display_name: %w", err)
	}

	return nil
}
