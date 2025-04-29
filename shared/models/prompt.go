package models

import "time"

// Prompt represents a prompt configuration in the system.
type Prompt struct {
	ID        int64     `db:"id" json:"id"`
	Key       string    `db:"key" json:"key"`
	Language  string    `db:"language" json:"language"`
	Content   string    `db:"content" json:"content"`
	CreatedAt time.Time `db:"created_at" json:"createdAt"`
	UpdatedAt time.Time `db:"updated_at" json:"updatedAt"`
}
