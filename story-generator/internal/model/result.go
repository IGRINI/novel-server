package model

import (
	"time"

	"novel-server/shared/messaging"
)

// GenerationResult представляет результат выполнения задачи генерации,
// предназначенный для сохранения в БД.
type GenerationResult struct {
	ID             string               `db:"id"`                 // Обычно совпадает с TaskID
	UserID         string               `db:"user_id"`            // ID пользователя
	PromptType     messaging.PromptType `db:"prompt_type"`        // Тип использованного промта
	GeneratedText  string               `db:"generated_text"`     // Сгенерированный AI текст
	ProcessingTime time.Duration        `db:"processing_time_ms"` // Время обработки в мс
	CreatedAt      time.Time            `db:"created_at"`         // Время создания записи (получения задачи)
	CompletedAt    time.Time            `db:"completed_at"`       // Время завершения обработки
	Error          string               `db:"error,omitempty"`    // Опциональное поле для ошибки
}
