package models

import "github.com/google/uuid"

// PushNotificationPayload определяет структуру для отправки Push-уведомлений.
type PushNotificationPayload struct {
	UserID       uuid.UUID         `json:"user_id"`        // ID пользователя (обязательно)
	DeviceTokens []string          `json:"device_tokens"`  // Токены устройств (если нужны конкретные)
	Notification PushNotification  `json:"notification"`   // Основное тело уведомления
	Data         map[string]string `json:"data,omitempty"` // Дополнительные данные
}

// PushNotification содержит основные данные для отображения уведомления.
type PushNotification struct {
	Title string `json:"title"`           // Заголовок уведомления
	Body  string `json:"body"`            // Текст уведомления
	Image string `json:"image,omitempty"` // URL изображения (опционально)
}

// ClientStoryUpdate содержит данные для обновления состояния истории на клиенте через WebSocket.
type ClientStoryUpdate struct {
	ID            string         `json:"id"`                       // ID PublishedStory или GameState
	UserID        string         `json:"user_id"`                  // ID пользователя
	UpdateType    UpdateType     `json:"update_type"`              // Тип обновления (Story, GameState)
	Status        string         `json:"status"`                   // Новый статус (string из StoryStatus или PlayerStatus)
	SceneID       *string        `json:"scene_id,omitempty"`       // ID текущей сцены (для GameState)
	StateHash     string         `json:"state_hash,omitempty"`     // Хеш текущего состояния (для GameState)
	ErrorDetails  *string        `json:"error_details,omitempty"`  // Детали ошибки, если есть
	EndingText    *string        `json:"ending_text,omitempty"`    // Текст концовки (если GameOver)
	StoryTitle    *string        `json:"story_title,omitempty"`    // Название истории (для UpdateTypeStory)
	StoryGenre    *string        `json:"story_genre,omitempty"`    // Жанр истории (для UpdateTypeStory)
	PreviewImage  *string        `json:"preview_image,omitempty"`  // Превью-изображение истории (для UpdateTypeStory)
	CharacterInfo *CharacterInfo `json:"character_info,omitempty"` // Информация о персонаже (для UpdateTypeStory)
}

// UpdateType определяет тип обновления для ClientStoryUpdate
type UpdateType string

const (
	UpdateTypeStory     UpdateType = "story"
	UpdateTypeGameState UpdateType = "game_state"
	UpdateTypeDraft     UpdateType = "draft"
)

// CharacterInfo содержит основную информацию о персонаже для отображения на клиенте.
type CharacterInfo struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	Gender          string `json:"gender"`
	ProfileImageURL string `json:"profile_image_url,omitempty"`
	// Дополнительные поля при необходимости
}
