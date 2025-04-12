package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// StoryStatus определяет статус конфигурации истории
type StoryStatus string

const (
	StatusDraft      StoryStatus = "draft"      // Черновик, можно редактировать
	StatusGenerating StoryStatus = "generating" // Отправлено на генерацию
	StatusReady      StoryStatus = "ready"      // Готово к прохождению (пока не используется в этом сервисе)
	StatusError      StoryStatus = "error"      // Ошибка генерации
)

// StoryConfig представляет собой конфигурацию (драфт) истории пользователя.
type StoryConfig struct {
	ID          uuid.UUID       `json:"id" db:"id"`                   // Уникальный ID конфигурации
	UserID      uint64          `json:"user_id" db:"user_id"`         // ID пользователя-владельца
	Title       string          `json:"title" db:"title"`             // Название (извлекается из последнего сгенерированного JSON, поле "t")
	Description string          `json:"description" db:"description"` // Краткое описание (извлекается из последнего сгенерированного JSON, поле "sd")
	UserInput   json.RawMessage `json:"user_input" db:"user_input"`   // История текстовых запросов пользователя (JSON массив строк)
	Config      json.RawMessage `json:"config" db:"config"`           // Актуальный JSON конфиг от Narrator (структура из narrator.md)
	Status      StoryStatus     `json:"status" db:"status"`           // Статус конфигурации
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`   // Время создания
	UpdatedAt   time.Time       `json:"updated_at" db:"updated_at"`   // Время последнего обновления
	// Возможно, добавить поле для ссылки на результат генерации, если нужно
	// GenerationResultID string `json:"generation_result_id,omitempty" db:"generation_result_id"`
}
