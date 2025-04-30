package models

import "time"

// Prompt represents a prompt configuration in the system.
type Prompt struct {
	ID        int64     `json:"id" db:"id"`
	Key       string    `json:"key" db:"key"`
	Language  string    `json:"language" db:"language"`
	Content   string    `json:"content" db:"content"`
	Version   int       `json:"version" db:"version"`
	IsActive  bool      `json:"is_active" db:"is_active"`
	Comment   string    `json:"comment" db:"comment"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}
