package database

import (
	"context"
	"errors"
	"fmt"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"
	"strings"
	"time"

	"encoding/base64"

	"github.com/google/uuid"
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
	query := `INSERT INTO users (username, email, password_hash, display_name) VALUES ($1, $2, $3, $4) RETURNING id`
	r.logger.Debug("Executing query", zap.String("query", query), zap.String("username", user.Username), zap.String("email", user.Email), zap.String("displayName", user.DisplayName))
	err := r.db.QueryRow(ctx, query, user.Username, user.Email, user.PasswordHash, user.DisplayName).Scan(&user.ID)

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
	r.logger.Info("User created successfully", zap.String("userID", user.ID.String()), zap.String("username", user.Username), zap.String("email", user.Email))
	return nil
}

// GetUserByUsername retrieves a user by their username.
func (r *pgUserRepository) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	query := `SELECT id, username, display_name, email, password_hash, roles, is_banned FROM users WHERE username = $1`
	user := &models.User{}
	r.logger.Debug("Executing query", zap.String("query", query), zap.String("username", username))
	err := r.db.QueryRow(ctx, query, username).Scan(&user.ID, &user.Username, &user.DisplayName, &user.Email, &user.PasswordHash, &user.Roles, &user.IsBanned)

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
	query := `SELECT id, username, display_name, email, password_hash, roles, is_banned FROM users WHERE email = $1`
	user := &models.User{}
	r.logger.Debug("Executing query", zap.String("query", query), zap.String("email", email))
	err := r.db.QueryRow(ctx, query, email).Scan(&user.ID, &user.Username, &user.DisplayName, &user.Email, &user.PasswordHash, &user.Roles, &user.IsBanned)

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
func (r *pgUserRepository) GetUserByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	query := `SELECT id, username, display_name, email, password_hash, roles, is_banned FROM users WHERE id = $1`
	user := &models.User{}
	r.logger.Debug("Executing query", zap.String("query", query), zap.String("id", id.String()))
	err := r.db.QueryRow(ctx, query, id).Scan(&user.ID, &user.Username, &user.DisplayName, &user.Email, &user.PasswordHash, &user.Roles, &user.IsBanned)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Debug("User not found by ID", zap.String("id", id.String()))
			return nil, models.ErrUserNotFound
		}
		r.logger.Error("Failed to get user by id from postgres", zap.Error(err), zap.String("id", id.String()))
		return nil, fmt.Errorf("failed to get user by id from postgres: %w", err)
	}
	return user, nil
}

// GetUserCount retrieves the total number of users.
func (r *pgUserRepository) GetUserCount(ctx context.Context) (int64, error) {
	query := `SELECT COUNT(*) FROM users`
	var count int64
	r.logger.Debug("Executing query", zap.String("query", query))
	err := r.db.QueryRow(ctx, query).Scan(&count)
	if err != nil {
		r.logger.Error("Failed to get user count from postgres", zap.Error(err))
		return 0, fmt.Errorf("failed to get user count: %w", err)
	}
	return count, nil
}

// ListUsers retrieves a list of users with cursor pagination.
func (r *pgUserRepository) ListUsers(ctx context.Context, cursor string, limit int) ([]models.User, string, error) {
	if limit <= 0 {
		limit = 20 // Default limit
	}
	fetchLimit := limit + 1

	cursorID, err := decodeUUIDCursor(cursor)
	if err != nil {
		r.logger.Warn("Invalid cursor for ListUsers", zap.String("cursor", cursor), zap.Error(err))
		return nil, "", fmt.Errorf("invalid cursor: %w", err)
	}

	query := `SELECT id, username, display_name, email, roles, is_banned FROM users `
	args := []interface{}{}
	paramIndex := 1

	if cursorID != uuid.Nil {
		// Sorting ASC by id, so we need items with id > cursorID
		query += fmt.Sprintf("WHERE id > $%d ", paramIndex)
		args = append(args, cursorID)
		paramIndex++
	}

	query += fmt.Sprintf("ORDER BY id ASC LIMIT $%d", paramIndex)
	args = append(args, fetchLimit)

	logFields := []zap.Field{
		zap.Int("limit", limit),
		zap.String("cursor", cursor),
		zap.Stringer("cursorID", cursorID),
	}
	r.logger.Debug("Executing ListUsers query", append(logFields, zap.String("query", query))...)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		r.logger.Error("Failed to query users from postgres", zap.Error(err))
		return nil, "", fmt.Errorf("failed to query users: %w", err)
	}
	defer rows.Close()

	users := make([]models.User, 0, limit)
	var lastUserID uuid.UUID
	for rows.Next() {
		var user models.User
		if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &user.Email, &user.Roles, &user.IsBanned); err != nil {
			r.logger.Error("Failed to scan user row", zap.Error(err))
			// Продолжаем сканировать другие строки, но логируем ошибку
			continue
		}
		users = append(users, user)
		lastUserID = user.ID // Keep track of the last ID for cursor encoding
	}

	if err = rows.Err(); err != nil {
		r.logger.Error("Error iterating user rows", zap.Error(err))
		return nil, "", fmt.Errorf("error iterating user rows: %w", err)
	}

	var nextCursor string
	if len(users) == fetchLimit {
		// Encode cursor from the *last* item fetched (which is limit+1 th item)
		nextCursor = encodeUUIDCursor(lastUserID)
		// Return only the requested number of items
		users = users[:limit]
	} // No next cursor if fewer items than fetchLimit were returned

	r.logger.Debug("Users listed successfully", append(logFields, zap.Int("count", len(users)), zap.Bool("hasNext", nextCursor != ""))...)
	return users, nextCursor, nil
}

// --- Cursor Encoding/Decoding Helpers for UUID ---

// encodeUUIDCursor encodes the UUID into a base64 string.
func encodeUUIDCursor(id uuid.UUID) string {
	return base64.StdEncoding.EncodeToString([]byte(id.String()))
}

// decodeUUIDCursor decodes the base64 cursor string back into a UUID.
func decodeUUIDCursor(cursor string) (uuid.UUID, error) {
	if cursor == "" {
		return uuid.Nil, nil // Empty cursor is valid
	}
	decodedBytes, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid base64 cursor: %w", err)
	}

	cursorID, err := uuid.Parse(string(decodedBytes))
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid uuid format in cursor: %w", err)
	}

	return cursorID, nil
}

// --- End Cursor Helpers ---

// SetUserBanStatus updates the is_banned status for a user.
func (r *pgUserRepository) SetUserBanStatus(ctx context.Context, userID uuid.UUID, isBanned bool) error {
	query := `UPDATE users SET is_banned = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`
	r.logger.Debug("Executing query", zap.String("query", query), zap.String("userID", userID.String()), zap.Bool("isBanned", isBanned))

	cmdTag, err := r.db.Exec(ctx, query, isBanned, userID)
	if err != nil {
		r.logger.Error("Failed to update user ban status in postgres", zap.Error(err), zap.String("userID", userID.String()), zap.Bool("isBanned", isBanned))
		return fmt.Errorf("failed to update user ban status: %w", err)
	}

	if cmdTag.RowsAffected() == 0 {
		r.logger.Warn("Attempted to update ban status for non-existent user", zap.String("userID", userID.String()))
		return models.ErrUserNotFound // Возвращаем ошибку, если пользователь не найден
	}

	r.logger.Info("User ban status updated successfully", zap.String("userID", userID.String()), zap.Bool("isBanned", isBanned))
	return nil
}

// UpdateUserFields обновляет указанные поля пользователя (email, displayName, роли, is_banned).
// Поля со значением nil не обновляются.
func (r *pgUserRepository) UpdateUserFields(ctx context.Context, userID uuid.UUID, email *string, displayName *string, roles []string, isBanned *bool) error {
	setClauses := []string{}
	args := []interface{}{}
	argID := 1

	if email != nil {
		setClauses = append(setClauses, fmt.Sprintf("email = $%d", argID))
		args = append(args, *email)
		argID++
	}
	if displayName != nil {
		setClauses = append(setClauses, fmt.Sprintf("display_name = $%d", argID))
		args = append(args, *displayName)
		argID++
	}
	if roles != nil {
		setClauses = append(setClauses, fmt.Sprintf("roles = $%d", argID))
		args = append(args, roles)
		argID++
	}
	if isBanned != nil {
		setClauses = append(setClauses, fmt.Sprintf("is_banned = $%d", argID))
		args = append(args, *isBanned)
		argID++
	}

	if len(setClauses) == 0 {
		r.logger.Info("UpdateUserFields called with no fields to update", zap.String("userID", userID.String()))
		return nil // Нет полей для обновления
	}

	// Всегда обновляем updated_at
	setClauses = append(setClauses, fmt.Sprintf("updated_at = $%d", argID))
	args = append(args, time.Now())
	argID++

	args = append(args, userID)

	query := fmt.Sprintf("UPDATE users SET %s WHERE id = $%d",
		strings.Join(setClauses, ", "),
		argID,
	)

	r.logger.Debug("Executing user update query", zap.String("query", query), zap.Any("args", args))

	cmdTag, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		// Проверка на уникальность email
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // 23505 - unique_violation
			if strings.Contains(pgErr.ConstraintName, "users_email_key") {
				r.logger.Warn("User update failed: email already exists", zap.String("userID", userID.String()), zap.Stringp("email", email), zap.Error(err))
				return models.ErrEmailAlreadyExists
			}
		}
		r.logger.Error("Failed to execute user update query", zap.String("userID", userID.String()), zap.Error(err))
		return fmt.Errorf("failed to update user: %w", err)
	}

	if cmdTag.RowsAffected() == 0 {
		r.logger.Warn("UpdateUserFields affected 0 rows, user not found?", zap.String("userID", userID.String()))
		return models.ErrUserNotFound
	}

	r.logger.Info("User fields updated successfully", zap.String("userID", userID.String()), zap.Int64("rowsAffected", cmdTag.RowsAffected()))
	return nil
}

// UpdatePasswordHash обновляет хеш пароля пользователя.
func (r *pgUserRepository) UpdatePasswordHash(ctx context.Context, userID uuid.UUID, passwordHash string) error {
	query := `UPDATE users SET password_hash = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`
	r.logger.Debug("Executing query", zap.String("query", query), zap.String("userID", userID.String()))

	cmdTag, err := r.db.Exec(ctx, query, passwordHash, userID)
	if err != nil {
		r.logger.Error("Failed to update user password hash in postgres", zap.Error(err), zap.String("userID", userID.String()))
		return fmt.Errorf("failed to update password hash: %w", err)
	}

	if cmdTag.RowsAffected() == 0 {
		r.logger.Warn("Attempted to update password hash for non-existent user", zap.String("userID", userID.String()))
		return models.ErrUserNotFound
	}

	r.logger.Info("User password hash updated successfully", zap.String("userID", userID.String()))
	return nil
}

// GetUsersByIDs retrieves multiple users by their IDs.
func (r *pgUserRepository) GetUsersByIDs(ctx context.Context, ids []uuid.UUID) ([]models.User, error) {
	// Если список ID пуст, возвращаем пустой срез без запроса к БД
	if len(ids) == 0 {
		return []models.User{}, nil
	}

	query := `SELECT id, username, display_name, email, roles, is_banned FROM users WHERE id = ANY($1)`
	r.logger.Debug("Executing query", zap.String("query", query), zap.Any("ids", ids))

	rows, err := r.db.Query(ctx, query, ids)
	if err != nil {
		r.logger.Error("Failed to query users by IDs from postgres", zap.Error(err), zap.Any("ids", ids))
		return nil, fmt.Errorf("failed to query users by IDs: %w", err)
	}
	defer rows.Close()

	users := make([]models.User, 0, len(ids)) // Предполагаем, что найдем всех
	for rows.Next() {
		var user models.User
		if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &user.Email, &user.Roles, &user.IsBanned); err != nil {
			r.logger.Error("Failed to scan user row in GetUsersByIDs", zap.Error(err))
			// Не прерываем цикл, но логируем ошибку
			continue
		}
		users = append(users, user)
	}

	if err = rows.Err(); err != nil {
		r.logger.Error("Error iterating user rows in GetUsersByIDs", zap.Error(err))
		return nil, fmt.Errorf("error iterating user rows in GetUsersByIDs: %w", err)
	}

	r.logger.Debug("Successfully retrieved users by IDs", zap.Int("foundCount", len(users)), zap.Int("requestedCount", len(ids)))
	return users, nil
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
