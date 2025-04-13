package database

import (
	"context"
	"errors"
	"fmt"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// Compile-time check to ensure pgUserRepository implements UserRepository
var _ interfaces.UserRepository = (*pgUserRepository)(nil)

type pgUserRepository struct {
	db     interfaces.DBTX
	logger *zap.Logger
}

// NewPgUserRepository creates a new PostgreSQL-backed UserRepository.
func NewPgUserRepository(db interfaces.DBTX, logger *zap.Logger) interfaces.UserRepository {
	return &pgUserRepository{
		db:     db,
		logger: logger.Named("PgUserRepo"),
	}
}

// CreateUser inserts a new user into the database.
func (r *pgUserRepository) CreateUser(ctx context.Context, user *models.User) error {
	query := `INSERT INTO users (username, email, password_hash) VALUES ($1, $2, $3) RETURNING id`
	r.logger.Debug("Executing query", zap.String("query", query), zap.String("username", user.Username), zap.String("email", user.Email))
	err := r.db.QueryRow(ctx, query, user.Username, user.Email, user.Password).Scan(&user.ID)

	if err != nil {
		var pgErr *pgconn.PgError
		// Check for unique constraint violation (duplicate username or email)
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // 23505 is unique_violation
			logFields := []zap.Field{zap.String("username", user.Username), zap.String("email", user.Email)}
			var returnErr error
			if pgErr.ConstraintName == "users_username_key" {
				r.logger.Warn("Attempted to create duplicate user by username", logFields...)
				returnErr = models.ErrUserAlreadyExists
			} else if pgErr.ConstraintName == "users_email_key" { // Предполагаем такое имя constraint
				r.logger.Warn("Attempted to create duplicate user by email", logFields...)
				returnErr = models.ErrEmailAlreadyExists
			} else {
				r.logger.Warn("Attempted to create user with unique constraint violation", append(logFields, zap.String("constraint", pgErr.ConstraintName))...)
				// Возвращаем более общую ошибку, если имя constraint неизвестно
				returnErr = models.ErrUserAlreadyExists // Или ErrEmailAlreadyExists?
			}
			return returnErr
		}
		r.logger.Error("Failed to create user in postgres", zap.Error(err), zap.String("username", user.Username), zap.String("email", user.Email))
		return fmt.Errorf("failed to create user in postgres: %w", err)
	}
	r.logger.Info("User created successfully", zap.Uint64("userID", user.ID), zap.String("username", user.Username), zap.String("email", user.Email))
	return nil
}

// GetUserByUsername retrieves a user by their username.
func (r *pgUserRepository) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	query := `SELECT id, username, email, password_hash FROM users WHERE username = $1`
	user := &models.User{}
	r.logger.Debug("Executing query", zap.String("query", query), zap.String("username", username))
	err := r.db.QueryRow(ctx, query, username).Scan(&user.ID, &user.Username, &user.Email, &user.Password)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Debug("User not found by username", zap.String("username", username))
			return nil, models.ErrUserNotFound
		}
		r.logger.Error("Failed to get user by username from postgres", zap.Error(err), zap.String("username", username))
		return nil, fmt.Errorf("failed to get user by username from postgres: %w", err)
	}
	return user, nil
}

// GetUserByEmail retrieves a user by their email.
func (r *pgUserRepository) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	query := `SELECT id, username, email, password_hash FROM users WHERE email = $1`
	user := &models.User{}
	r.logger.Debug("Executing query", zap.String("query", query), zap.String("email", email))
	err := r.db.QueryRow(ctx, query, email).Scan(&user.ID, &user.Username, &user.Email, &user.Password)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Debug("User not found by email", zap.String("email", email))
			// Важно: Возвращаем ErrUserNotFound, а не специфичную для email ошибку,
			// чтобы вызывающий код мог унифицированно обрабатывать отсутствие пользователя.
			return nil, models.ErrUserNotFound
		}
		r.logger.Error("Failed to get user by email from postgres", zap.Error(err), zap.String("email", email))
		return nil, fmt.Errorf("failed to get user by email from postgres: %w", err)
	}
	return user, nil
}

// GetUserByID retrieves a user by their ID.
func (r *pgUserRepository) GetUserByID(ctx context.Context, id uint64) (*models.User, error) {
	query := `SELECT id, username, email, password_hash FROM users WHERE id = $1`
	user := &models.User{}
	r.logger.Debug("Executing query", zap.String("query", query), zap.Uint64("id", id))
	err := r.db.QueryRow(ctx, query, id).Scan(&user.ID, &user.Username, &user.Email, &user.Password)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Debug("User not found by ID", zap.Uint64("id", id))
			return nil, models.ErrUserNotFound
		}
		r.logger.Error("Failed to get user by id from postgres", zap.Error(err), zap.Uint64("id", id))
		return nil, fmt.Errorf("failed to get user by id from postgres: %w", err)
	}
	return user, nil
}

// --- Database Schema ---
// Ensure the following table exists in your PostgreSQL database.
// It's recommended to use a migration tool (e.g., goose, migrate) to manage schema changes.
/*
-- Migration Up
CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    username VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_users_username ON users (username);

-- Migration Down
DROP INDEX IF EXISTS idx_users_username;
DROP TABLE IF EXISTS users;
*/
