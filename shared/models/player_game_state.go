package models

import (
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
	ID               uuid.UUID    `json:"id" db:"id"`                                           // Уникальный ID состояния игры
	PlayerID         uuid.UUID    `json:"player_id" db:"player_id"`                             // ID игрока (из сервиса auth)
	PublishedStoryID uuid.UUID    `json:"published_story_id" db:"published_story_id"`           // ID опубликованной истории
	CurrentSceneID   *uuid.UUID   `json:"current_scene_id,omitempty" db:"current_scene_id"`     // ID текущей сцены, на которой находится игрок
	PlayerProgressID *uuid.UUID   `json:"player_progress_id,omitempty" db:"player_progress_id"` // Ссылка на детальное состояние прогресса
	PlayerStatus     PlayerStatus `json:"player_status" db:"player_status"`                     // Текущий статус игрока в этой игре
	EndingText       *string      `json:"ending_text,omitempty" db:"ending_text"`               // Текст концовки (если StatusCompleted)
	ErrorDetails     *string      `json:"error_details,omitempty" db:"error_details"`           // Детали ошибки генерации для игрока
	StartedAt        time.Time    `json:"started_at" db:"started_at"`                           // Время начала игры игроком
	LastActivityAt   time.Time    `json:"last_activity_at" db:"last_activity_at"`               // Время последнего действия игрока (выбор, генерация)
	CompletedAt      *time.Time   `json:"completed_at,omitempty" db:"completed_at"`             // Время завершения игры (если StatusCompleted)
}

// PlayerCoreStats - можно использовать для парсинга CoreStats JSON, если потребуется.
// type PlayerCoreStats map[string]int
