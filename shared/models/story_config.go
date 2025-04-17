package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Используем тип StoryStatus, определенный в published_story.go

// Дополнительные статусы, специфичные для StoryConfig, если нужны:
const (
	// StatusDraft - Черновик, можно редактировать (уже есть в published_story.go, но оставляем для ясности области применения)
	StatusDraft StoryStatus = "draft"
	// StatusGenerating - Отправлено на генерацию (уже есть в published_story.go, но оставляем для ясности области применения)
	StatusGenerating StoryStatus = "generating"
	// StatusError - Ошибка генерации (уже есть в published_story.go)
	// StatusReady - Готово к прохождению (уже есть в published_story.go)
)

// StoryConfig представляет собой конфигурацию (драфт) истории пользователя.
type StoryConfig struct {
	ID          uuid.UUID       `json:"id" db:"id"`                   // Уникальный ID конфигурации
	UserID      uint64          `json:"user_id" db:"user_id"`         // ID пользователя-владельца
	Title       string          `json:"title" db:"title"`             // Название (извлекается из последнего сгенерированного JSON, поле "t")
	Description string          `json:"description" db:"description"` // Краткое описание (извлекается из последнего сгенерированного JSON, поле "sd")
	UserInput   json.RawMessage `json:"user_input" db:"user_input"`   // История текстовых запросов пользователя (JSON массив строк)
	Config      json.RawMessage `json:"config" db:"config"`           // Актуальный JSON конфиг от Narrator (структура из narrator.md)
	Status      StoryStatus     `json:"status" db:"status"`           // Статус конфигурации (использует тип из published_story.go)
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`   // Время создания
	UpdatedAt   time.Time       `json:"updated_at" db:"updated_at"`   // Время последнего обновления
}
