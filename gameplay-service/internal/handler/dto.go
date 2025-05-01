package handler

import (
	"github.com/google/uuid"
)

// DTOs for scene responses will be added here

// --- DTOs для ответа GET /api/published-stories/:id/scene ---

// GameSceneResponseDTO представляет структурированный ответ для текущей сцены.
type GameSceneResponseDTO struct {
	ID               uuid.UUID        `json:"id"`                     // ID текущей сцены
	PublishedStoryID uuid.UUID        `json:"publishedStoryId"`       // ID истории
	Choices          []ChoiceBlockDTO `json:"choices,omitempty"`      // Блоки выбора
	EndingText       *string          `json:"endingText,omitempty"`   // Текст концовки
	Continuation     *ContinuationDTO `json:"continuation,omitempty"` // Данные для продолжения
	CurrentStats     map[string]int   `json:"currentStats,omitempty"` // Текущие статы игрока
}

// ChoiceBlockDTO представляет блок выбора в сцене.
type ChoiceBlockDTO struct {
	Shuffleable   bool              `json:"shuffleable"`             // Можно ли перемешивать опции (sh: 1 = true, 0 = false)
	CharacterName string            `json:"characterName,omitempty"` // Имя персонажа, представляющего выбор (char)
	Description   string            `json:"description"`             // Описание ситуации/вопроса (desc)
	Options       []ChoiceOptionDTO `json:"options"`                 // Массив из ДВУХ опций (opts)
}

// ChoiceOptionDTO представляет одну опцию выбора.
type ChoiceOptionDTO struct {
	Text         string           `json:"text"`                   // Текст опции (txt)
	Consequences *ConsequencesDTO `json:"consequences,omitempty"` // ПОЛНЫЕ последствия (cons)
}

// ConsequencesDTO представляет ПОЛНЫЕ последствия для отображения клиенту.
type ConsequencesDTO struct {
	ResponseText *string        `json:"responseText,omitempty"` // Текст-реакция на выбор (rt)
	StatChanges  map[string]int `json:"statChanges,omitempty"`  // Изменения статов (cs)
	// TODO: Добавить поля для sv и gf, если они нужны клиенту
	// StoryVariables map[string]interface{} `json:"sv,omitempty"`
	// GlobalFlags    []string               `json:"gf,omitempty"`
}

// ContinuationDTO содержит данные для сцены типа "продолжение".
type ContinuationDTO struct {
	NewPlayerDescription string         `json:"newPlayerDescription"` // npd
	EndingTextPrevious   string         `json:"endingTextPrevious"`   // etp
	CoreStatsReset       map[string]int `json:"coreStatsReset"`       // csr - начальные статы для нового персонажа
}
