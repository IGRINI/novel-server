package database

import (
	"context"
	"errors"
	"fmt"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"

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

// ListUsers retrieves a list of users.
// TODO: Add pagination (LIMIT, OFFSET)
func (r *pgUserRepository) ListUsers(ctx context.Context) ([]models.User, error) {
	query := `SELECT id, username, display_name, email, roles, is_banned FROM users ORDER BY id ASC`
	r.logger.Debug("Executing query", zap.String("query", query))
	rows, err := r.db.Query(ctx, query)
	if err != nil {
		r.logger.Error("Failed to query users from postgres", zap.Error(err))
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	defer rows.Close()

	users := make([]models.User, 0)
	for rows.Next() {
		var user models.User
		if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &user.Email, &user.Roles, &user.IsBanned); err != nil {
			r.logger.Error("Failed to scan user row", zap.Error(err))
			// Продолжаем сканировать другие строки, но логируем ошибку
			continue
		}
		users = append(users, user)
	}

	if err = rows.Err(); err != nil {
		r.logger.Error("Error iterating user rows", zap.Error(err))
		return nil, fmt.Errorf("error iterating user rows: %w", err)
	}

	return users, nil
}

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

// UpdateUserFields обновляет указанные поля пользователя в базе данных.
// Принимает ID пользователя и указатели на обновляемые значения.
// Если указатель равен nil, соответствующее поле не обновляется.
func (r *pgUserRepository) UpdateUserFields(ctx context.Context, userID uuid.UUID, email *string, roles []string, isBanned *bool) error {
	queryBase := "UPDATE users SET updated_at = CURRENT_TIMESTAMP"
	args := []interface{}{} // Слайс для аргументов запроса
	argID := 1              // Счетчик для плейсхолдеров ($1, $2, ...)

	// Добавляем поля для обновления, если они переданы (не nil)
	if email != nil {
		queryBase += fmt.Sprintf(", email = $%d", argID)
		args = append(args, *email)
		argID++
	}
	if roles != nil { // Обновляем роли, если слайс передан (даже если пустой)
		queryBase += fmt.Sprintf(", roles = $%d", argID)
		args = append(args, roles) // Передаем сам слайс
		argID++
	}
	if isBanned != nil {
		queryBase += fmt.Sprintf(", is_banned = $%d", argID)
		args = append(args, *isBanned)
		argID++
	}
	/* // Пока убираем возможность обновления DisplayName через этот метод
	if displayName != nil {
		queryBase += fmt.Sprintf(", display_name = $%d", argID)
		args = append(args, *displayName)
		argID++
	}
	*/

	// Если нечего обновлять, просто выходим
	if len(args) == 0 {
		r.logger.Info("UpdateUserFields called with no fields to update", zap.String("userID", userID.String()))
		return nil
	}

	// Добавляем условие WHERE и последний аргумент (userID)
	query := queryBase + fmt.Sprintf(" WHERE id = $%d", argID)
	args = append(args, userID)

	r.logger.Debug("Executing update user query", zap.String("query", query), zap.String("userID", userID.String()), zap.Any("args", args))
	cmdTag, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		// Проверяем на ошибку уникальности email
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "users_email_key" {
			r.logger.Warn("Attempted to update user with duplicate email", zap.String("userID", userID.String()), zap.Stringp("email", email))
			return models.ErrEmailAlreadyExists
		}
		r.logger.Error("Failed to update user fields in postgres", zap.Error(err), zap.String("userID", userID.String()))
		return fmt.Errorf("failed to update user fields: %w", err)
	}

	if cmdTag.RowsAffected() == 0 {
		r.logger.Warn("Attempted to update non-existent user", zap.String("userID", userID.String()))
		return models.ErrUserNotFound
	}

	r.logger.Info("User fields updated successfully", zap.String("userID", userID.String()))
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
