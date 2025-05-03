package handler

import (
	"github.com/google/uuid"
)

// DTOs for scene responses will be added here

// --- DTOs для ответа GET /api/published-stories/:id/scene ---

// GameSceneResponseDTO представляет структурированный ответ для текущей сцены.
type GameSceneResponseDTO struct {
	ID               uuid.UUID        `json:"id"`                      // ID текущей сцены
	PublishedStoryID uuid.UUID        `json:"published_story_id"`      // camelCase -> snake_case
	GameStateID      uuid.UUID        `json:"game_state_id"`           // camelCase -> snake_case
	Choices          []ChoiceBlockDTO `json:"choices,omitempty"`       // Блоки выбора
	EndingText       *string          `json:"ending_text,omitempty"`   // camelCase -> snake_case
	Continuation     *ContinuationDTO `json:"continuation,omitempty"`  // Данные для продолжения
	CurrentStats     map[string]int   `json:"current_stats,omitempty"` // camelCase -> snake_case
}

// ChoiceBlockDTO представляет блок выбора в сцене.
type ChoiceBlockDTO struct {
	Shuffleable   bool              `json:"shuffleable"`              // Можно ли перемешивать опции (sh: 1 = true, 0 = false)
	CharacterName string            `json:"character_name,omitempty"` // camelCase -> snake_case
	Description   string            `json:"description"`              // Описание ситуации/вопроса (desc)
	Options       []ChoiceOptionDTO `json:"options"`                  // Массив из ДВУХ опций (opts)
}

// ChoiceOptionDTO представляет одну опцию выбора.
type ChoiceOptionDTO struct {
	Text         string           `json:"text"`                   // Текст опции (txt)
	Consequences *ConsequencesDTO `json:"consequences,omitempty"` // ПОЛНЫЕ последствия (cons)
}

// ConsequencesDTO представляет ПОЛНЫЕ последствия для отображения клиенту.
type ConsequencesDTO struct {
	ResponseText *string        `json:"response_text,omitempty"` // camelCase -> snake_case
	StatChanges  map[string]int `json:"stat_changes,omitempty"`  // camelCase -> snake_case
	// TODO: Добавить поля для sv и gf, если они нужны клиенту
	// StoryVariables map[string]interface{} `json:"sv,omitempty"`
	// GlobalFlags    []string               `json:"gf,omitempty"`
}

// ContinuationDTO содержит данные для сцены типа "продолжение".
type ContinuationDTO struct {
	NewPlayerDescription string         `json:"new_player_description"` // camelCase -> snake_case
	EndingTextPrevious   string         `json:"ending_text_previous"`   // camelCase -> snake_case
	CoreStatsReset       map[string]int `json:"core_stats_reset"`       // camelCase -> snake_case
}

// --- DTO для ответа GET /api/published-stories/:story_id/gamestates ---
