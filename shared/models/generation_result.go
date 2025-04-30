package models

import (
	// "novel-server/shared/messaging" // <<< УДАЛЯЕМ ИМПОРТ
	"time"

	"github.com/google/uuid"
)

// <<< ДОБАВЛЕНО: Определение PromptType и констант >>>

// PromptType определяет тип запроса к AI генератору
type PromptType string

// Константы для типов промптов
const (
	PromptTypeNarrator               PromptType = "narrator"                  // Генерация базовых параметров мира по запросу пользователя
	PromptTypeNarratorReviser        PromptType = "narrator_reviser"          // Ревизия базовых параметров мира по запросу пользователя
	PromptTypeNovelSetup             PromptType = "novel_setup"               // Генерация стартового состояния мира (статы, персонажи)
	PromptTypeNovelFirstSceneCreator PromptType = "novel_first_scene_creator" // Генерация первой сцены
	PromptTypeNovelCreator           PromptType = "novel_creator"             // Генерация следующей сцены (или первой)
	PromptTypeNovelGameOverCreator   PromptType = "novel_game_over_creator"   // Генерация финальной сцены (конец игры)
	PromptTypeCharacterImage         PromptType = "character_image"           // Генерация изображения персонажа
	PromptTypeStoryPreviewImage      PromptType = "story_preview_image"       // Генерация превью-изображения истории
	// Добавить другие типы по необходимости
)

// <<< КОНЕЦ ДОБАВЛЕНИЯ >>>

// GenerationResult stores the output and metadata of an AI generation task.
// This is typically saved by the story-generator service for logging, debugging, or retrieval.
type GenerationResult struct {
	ID               string     `json:"id" db:"id"` // Task ID (usually a UUID string)
	UserID           string     `json:"user_id" db:"user_id"`
	PromptType       PromptType `json:"prompt_type" db:"prompt_type"`               // <<< Используем локальный тип
	GeneratedText    string     `json:"generated_text" db:"generated_text"`         // The raw text output from the AI
	ProcessingTimeMs int64      `json:"processing_time_ms" db:"processing_time_ms"` // Duration of the generation task in milliseconds
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`                 // When the task processing started
	CompletedAt      time.Time  `json:"completed_at" db:"completed_at"`             // When the task processing finished
	Error            string     `json:"error,omitempty" db:"error"`                 // Error message if generation failed

	// Дополнительные поля, если нужны (например, токены, стоимость):
	PromptTokens     int     `json:"prompt_tokens,omitempty" db:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens,omitempty" db:"completion_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd,omitempty" db:"estimated_cost_usd"`
}

// IsValidPromptType проверяет, является ли строка допустимым PromptType.
func IsValidPromptType(pt PromptType) bool {
	switch pt {
	case PromptTypeNarrator, PromptTypeNarratorReviser, PromptTypeNovelSetup, PromptTypeNovelFirstSceneCreator, PromptTypeNovelCreator, PromptTypeNovelGameOverCreator, PromptTypeCharacterImage, PromptTypeStoryPreviewImage:
		return true
	default:
		return false
	}
}

// Helper function to convert string to UUID, return zero UUID on error
// (Может быть полезно при работе с репозиторием, если UserID хранится как uuid)
func ParseUUID(s string) uuid.UUID {
	id, _ := uuid.Parse(s)
	return id
}
