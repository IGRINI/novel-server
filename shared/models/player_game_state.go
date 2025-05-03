package models

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// PlayerStatus определяет возможные статусы состояния игры для конкретного игрока.
// Совпадает с типом ENUM 'player_game_status' в БД (нужно будет создать).
type PlayerStatus string

const (
	PlayerStatusPlaying         PlayerStatus = "playing"           // Игрок активно играет (находится на какой-то сцене).
	PlayerStatusGeneratingScene PlayerStatus = "generating_scene"  // Идет генерация следующей сцены для игрока.
	PlayerStatusGameOverPending PlayerStatus = "game_over_pending" // Ожидает генерации концовки для игрока.
	PlayerStatusCompleted       PlayerStatus = "completed"         // Игра завершена для этого игрока.
	PlayerStatusError           PlayerStatus = "error"             // Ошибка при генерации сцены/концовки для игрока.
)

// PlayerGameState представляет состояние игры конкретного игрока для опубликованной истории.
// Эта запись создается, когда игрок начинает играть в историю.
type PlayerGameState struct {
	ID               uuid.UUID     `json:"id" db:"id"`
	PlayerID         uuid.UUID     `json:"player_id" db:"player_id"`
	PublishedStoryID uuid.UUID     `json:"published_story_id" db:"published_story_id"`
	CurrentSceneID   uuid.NullUUID `json:"current_scene_id" db:"current_scene_id"`
	PlayerProgressID uuid.UUID     `json:"player_progress_id" db:"player_progress_id"`
	PlayerStatus     PlayerStatus  `json:"player_status" db:"player_status"`
	EndingText       *string       `json:"ending_text,omitempty" db:"ending_text"`
	ErrorDetails     *string       `json:"error_details,omitempty" db:"error_details"`
	StartedAt        time.Time     `json:"started_at" db:"started_at"`
	LastActivityAt   time.Time     `json:"last_activity_at" db:"last_activity_at"`
	CompletedAt      sql.NullTime  `json:"completed_at,omitempty" db:"completed_at"`
}

// PlayerCoreStats - можно использовать для парсинга CoreStats JSON, если потребуется.
// type PlayerCoreStats map[string]int
