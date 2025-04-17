package models

import (
	"time"

	"github.com/google/uuid"
)

// User represents a user in the system.
type User struct {
	ID           uuid.UUID `json:"id"`
	Username     string    `json:"username"`
	DisplayName  string    `db:"display_name" json:"display_name"`
	Email        string    `db:"email" json:"email"`
	PasswordHash string    `db:"password_hash" json:"-"`    // Не отдаем хеш пароля
	Roles        []string  `db:"roles" json:"roles"`        // <<< Добавляем роли
	IsBanned     bool      `db:"is_banned" json:"isBanned"` // <<< Добавляем флаг бана
	CreatedAt    time.Time `db:"created_at" json:"createdAt"`
	UpdatedAt    time.Time `db:"updated_at" json:"updatedAt"`
}
