package messaging

import (
	"novel-server/shared/models"
)

// PromptType определяет тип запроса к AI генератору
type PromptType string

// Константы для типов промптов
const (
	PromptTypeNarrator               PromptType = "narrator"                  // Генерация базовых параметров мира по запросу пользователя
	PromptTypeNovelSetup             PromptType = "novel_setup"               // Генерация стартового состояния мира (статы, персонажи)
	PromptTypeNovelFirstSceneCreator PromptType = "novel_first_scene_creator" // Генерация первой сцены
	PromptTypeNovelCreator           PromptType = "novel_creator"             // Генерация следующей сцены
	PromptTypeNovelGameOverCreator   PromptType = "novel_game_over_creator"   // Генерация финальной сцены (конец игры)
	// Добавить другие типы по необходимости
)

// GenerationTaskPayload - структура сообщения для задачи генерации
type GenerationTaskPayload struct {
	TaskID           string     `json:"taskId"`                     // Уникальный ID задачи
	UserID           string     `json:"userId"`                     // ID пользователя
	PromptType       PromptType `json:"promptType"`                 // Тип промпта для AI
	UserInput        string     `json:"userInput"`                  // Входные данные для AI (например, запрос пользователя или JSON)
	StoryConfigID    string     `json:"storyConfigId,omitempty"`    // ID черновика (для Narrator, Setup). Убрали omitempty? Проверить
	PublishedStoryID string     `json:"publishedStoryId,omitempty"` // ID опубликованной истории (для Creator, GameOver)
	StateHash        string     `json:"state_hash,omitempty"`       // Хеш состояния (для Creator, GameOver)
	GameStateID      string     `json:"gameStateId,omitempty"`      // ID состояния игры игрока (для обновления по callback)
}

// NotificationStatus определяет статус уведомления
type NotificationStatus string

const (
	NotificationStatusSuccess NotificationStatus = "success"
	NotificationStatusError   NotificationStatus = "error"
)

// NotificationPayload - структура сообщения для уведомления пользователя
type NotificationPayload struct {
	TaskID           string             `json:"task_id"`                      // ID задачи, которая завершилась
	UserID           string             `json:"user_id"`                      // ID пользователя для отправки уведомления
	PromptType       PromptType         `json:"prompt_type"`                  // Тип промпта, который выполнялся
	Status           NotificationStatus `json:"status"`                       // Статус выполнения (success/error)
	GeneratedText    string             `json:"generated_text,omitempty"`     // Сгенерированный текст (при успехе)
	ErrorDetails     string             `json:"error_details,omitempty"`      // Детали ошибки (при ошибке)
	StoryConfigID    string             `json:"story_config_id,omitempty"`    // Опционально: ID конфигурации истории, если применимо
	PublishedStoryID string             `json:"published_story_id,omitempty"` // !!! ДОБАВЛЕНО: ID опубликованной истории
	StateHash        string             `json:"state_hash,omitempty"`         // <<< Добавлено: Хеш состояния, для которого генерировалась сцена
	GameStateID      string             `json:"gameStateId,omitempty"`        // <<< ДОБАВЛЕНО: ID состояния игры для обновления
}

// IsValidPromptType проверяет, является ли строка допустимым PromptType.
func IsValidPromptType(pt PromptType) bool {
	switch pt {
	case PromptTypeNarrator, PromptTypeNovelSetup, PromptTypeNovelFirstSceneCreator, PromptTypeNovelCreator, PromptTypeNovelGameOverCreator:
		return true
	default:
		return false
	}
}

// GameOverReason details why the game ended.
type GameOverReason struct {
	StatName  string `json:"sn"`   // stat_name
	Condition string `json:"cond"` // "min" or "max"
	Value     int    `json:"val"`  // final value
}

// GameOverTaskPayload defines the data sent to generate a game over ending.
type GameOverTaskPayload struct {
	TaskID           string                   `json:"task_id"`
	UserID           string                   `json:"user_id"` // User ID as string
	PublishedStoryID string                   `json:"published_story_id"`
	GameStateID      string                   `json:"gameStateId,omitempty"` // ID состояния игры игрока (для обновления по callback)
	PromptType       PromptType               `json:"prompt_type"`           // Should be PromptTypeNovelGameOverCreator
	NovelConfig      models.Config            `json:"cfg"`                   // NovelConfig (extracted from PublishedStory.Config)
	NovelSetup       models.NovelSetupContent `json:"setup"`                 // NovelSetup (extracted from PublishedStory.Setup)
	LastState        models.PlayerProgress    `json:"lst"`                   // The final player progress state
	Reason           GameOverReason           `json:"rsn"`                   // Reason for game over
}
