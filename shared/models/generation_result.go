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
	PromptTypeNarrator             PromptType = "narrator"                    // from narrator.md
	PromptTypeProtagonistGoal      PromptType = "protagonist_goal_prompt"     // from protagonist_goal_prompt.md
	PromptTypeScenePlanner         PromptType = "scene_planner_prompt"        // from scene_planner_prompt.md
	PromptTypeStoryContinuation    PromptType = "story_continuation_prompt"   // from story_continuation_prompt.md
	PromptTypeStorySetup           PromptType = "story_setup_prompt"          // from story_setup_prompt.md
	PromptTypeJsonGeneration       PromptType = "json_generation_prompt"      // from json_generation_prompt.md
	PromptTypeCharacterGeneration  PromptType = "character_generation_prompt" // from character_generation_prompt.md
	PromptTypeContentModeration    PromptType = "content_moderation_prompt"   // from content_moderation_prompt.md
	PromptTypeNovelGameOverCreator PromptType = "novel_gameover_creator"      // from novel_gameover_creator.md
	PromptTypeCharacterImage       PromptType = "character_image"             // Генерация изображения персонажа
	PromptTypeStoryPreviewImage    PromptType = "story_preview_image"         // Генерация превью-изображения истории
	PromptTypeImageGeneration      PromptType = "image_generation_prompt"     // Общая генерация изображений по тексту (например, для карточек)
	// Другие типы, не основанные на предоставленных файлах, были удалены согласно запросу.
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
	case PromptTypeNarrator,
		PromptTypeProtagonistGoal,
		PromptTypeScenePlanner,
		PromptTypeStoryContinuation,
		PromptTypeStorySetup,
		PromptTypeJsonGeneration,
		PromptTypeCharacterGeneration,
		PromptTypeContentModeration,
		PromptTypeNovelGameOverCreator,
		PromptTypeCharacterImage,
		PromptTypeStoryPreviewImage,
		PromptTypeImageGeneration:
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
