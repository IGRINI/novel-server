package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// StoryConfig represents a story configuration draft being worked on by a user.
type StoryConfig struct {
	ID          uuid.UUID       `db:"id" json:"id"`
	UserID      uuid.UUID       `db:"user_id" json:"user_id"`
	Title       string          `db:"title" json:"title"`
	Description string          `db:"description" json:"description"`
	UserInput   json.RawMessage `db:"user_input" json:"user_input"`
	Config      json.RawMessage `db:"config" json:"config"`
	Status      StoryStatus     `db:"status" json:"status"`
	CreatedAt   time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time       `db:"updated_at" json:"updated_at"`
}
