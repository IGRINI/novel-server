package messaging

import (
	"context"
	"novel-server/shared/models"

	"github.com/google/uuid"
)

// GenerationTaskPayload defines the structure for AI generation tasks.
// This is sent TO the story-generator service.
// It contains all necessary context marshalled into UserInput.
type GenerationTaskPayload struct {
	TaskID           string            `json:"task_id"`
	UserID           string            `json:"user_id"`                      // User ID as string
	PublishedStoryID string            `json:"published_story_id,omitempty"` // Story ID as string (for scene/game over)
	StoryConfigID    string            `json:"story_config_id,omitempty"`    // Draft ID as string (for narrator/setup)
	PromptType       models.PromptType `json:"prompt_type"`
	UserInput        string            `json:"user_input"`            // JSON string containing cfg, stp, cs, uc, pss, pfd, pvis, sv, gf
	StateHash        string            `json:"state_hash,omitempty"`  // Required for PromptTypeNovelCreator
	GameStateID      string            `json:"gameStateId,omitempty"` // Required for subsequent scene/game over results processing
	Language         string            `json:"language"`              // <<< НОВОЕ ПОЛЕ: Язык истории >>>
}

// GameOverReason details why the game ended.
// This structure is used *within* GameOverTaskPayload.
type GameOverReason struct {
	StatName  string `json:"sn"`   // stat_name
	Condition string `json:"cond"` // "min" or "max"
	Value     int    `json:"val"`  // final value
}

// GameOverTaskPayload defines the structure for requesting game over text generation.
// This is sent TO the story-generator service.
type GameOverTaskPayload struct {
	TaskID           string                `json:"task_id"`
	UserID           string                `json:"user_id"`
	PublishedStoryID string                `json:"published_story_id"`
	GameStateID      string                `json:"gameStateId"`
	LastState        models.PlayerProgress `json:"lst"` // The final PlayerProgress node
	Reason           GameOverReason        `json:"rsn"` // Reason for game over
	// --- MODIFIED FIELDS --- Use minimal structs for context needed by AI ---
	NovelConfig models.MinimalConfigForGameOver `json:"cfg"` // Minimal Config (language, genre, player prefs)
	NovelSetup  models.MinimalSetupForGameOver  `json:"stp"` // Minimal Setup (character names)
	// CanContinue field might be needed if continuation logic exists
	// CanContinue      bool                            `json:"can_continue,omitempty"`
}

// CharacterImageTaskPayload defines the structure for a single image generation task.
type CharacterImageTaskPayload struct {
	TaskID           string    `json:"task_id"`            // Unique ID for this specific task
	UserID           string    `json:"user_id"`            // <<< ДОБАВЛЕНО: User ID as string
	PublishedStoryID uuid.UUID `json:"published_story_id"` // Story context
	CharacterID      uuid.UUID `json:"character_id"`       // Character context (optional, can be zero UUID)
	CharacterName    string    `json:"character_name"`     // Character name from setup
	ImageReference   string    `json:"image_reference"`    // Unique reference ID for the image (e.g., character_{id}_{taskid})
	Prompt           string    `json:"prompt"`             // Image generation prompt
	NegativePrompt   string    `json:"negative_prompt,omitempty"`
	Width            int       `json:"width,omitempty"`
	Height           int       `json:"height,omitempty"`
	Ratio            string    `json:"ratio"` // <<< ДОБАВЛЕНО: Соотношение сторон ("2:3" или "3:2")
}

// CharacterImageTaskBatchPayload defines a batch of image generation tasks.
type CharacterImageTaskBatchPayload struct {
	BatchID          string                      `json:"batch_id"` // Unique ID for this batch
	PublishedStoryID uuid.UUID                   `json:"published_story_id"`
	Tasks            []CharacterImageTaskPayload `json:"tasks"` // Array of individual tasks
}

// CharacterImageResultPayload defines the result of an image generation task.
type CharacterImageResultPayload struct {
	TaskID           string    `json:"task_id"`            // Matches the ID from CharacterImageTaskPayload
	PublishedStoryID uuid.UUID `json:"published_story_id"` // <<< ДОБАВЛЕНО: ID истории, к которой относится изображение
	ImageReference   string    `json:"image_reference"`    // Matches the reference from the task
	Success          bool      `json:"success"`
	ErrorMessage     *string   `json:"error,omitempty"`     // Error message if success is false
	ImageURL         *string   `json:"image_url,omitempty"` // URL to the generated image (e.g., S3/MinIO URL) if success is true
}

// NotificationStatus defines the status of a notification.
type NotificationStatus string

const (
	NotificationStatusSuccess NotificationStatus = "success"
	NotificationStatusError   NotificationStatus = "error"
)

// NotificationPayload is the structure for notifications sent FROM generation services back TO gameplay-service.
type NotificationPayload struct {
	TaskID           string             `json:"task_id"`                 // ID of the original task
	Status           NotificationStatus `json:"status"`                  // success or error
	PromptType       models.PromptType  `json:"prompt_type"`             // <<< Используем models.PromptType
	ErrorDetails     string             `json:"error_details,omitempty"` // Details if status is error
	UserID           string             `json:"user_id,omitempty"`       // Added UserID
	StoryConfigID    string             `json:"story_config_id,omitempty"`
	PublishedStoryID string             `json:"published_story_id,omitempty"`
	StateHash        string             `json:"state_hash,omitempty"`
	GameStateID      string             `json:"game_state_id,omitempty"`

	// <<< ДОБАВЛЕНО: Поля для результатов генерации изображений >>>
	ImageReference string  `json:"image_reference,omitempty"`
	ImageURL       *string `json:"image_url,omitempty"` // Pointer for optional URL

	// TODO: Поле Data может понадобиться для доп. информации (например, метаданные или URL картинки?)
	Data map[string]interface{} `json:"data,omitempty"` // Flexible field for additional data
}

// Publisher - интерфейс для отправки сообщений в очередь.
// Это позволяет использовать разные реализации (RabbitMQ, моки для тестов).
type Publisher interface {
	// Publish отправляет сообщение.
	// payload - структура сообщения, которая будет сериализована в JSON.
	// correlationID - опциональный ID для связывания запроса и ответа.
	Publish(ctx context.Context, payload interface{}, correlationID string) error
	// Close закрывает соединение/канал паблишера.
	Close() error
}

// <<< НОВОЕ: Структура сообщения для обновлений конфигурации >>>
// ConfigUpdatePayload определяет структуру сообщения, отправляемого при изменении динамической конфигурации.
// Это сообщение рассылается из admin-service всем заинтересованным сервисам.
type ConfigUpdatePayload struct {
	Key   string `json:"key"`   // Ключ измененной конфигурации
	Value string `json:"value"` // Новое значение конфигурации
}

// <<< КОНЕЦ >>>
