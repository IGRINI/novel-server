package interfaces

// Определения PromptEventType, PromptEvent и констант
// перенесены в events.go

// Интерфейс PromptEventPublisher остается здесь (или переносим в events.go?)
// Пока оставим здесь, но его определение дублируется в events.go
// Лучше удалить и его отсюда, оставив только в events.go

// // PromptEventType represents the type of prompt event.
// type PromptEventType string
// const (
// 	PromptEventTypeCreated PromptEventType = "created"
// 	PromptEventTypeUpdated PromptEventType = "updated"
// 	PromptEventTypeDeleted PromptEventType = "deleted"
// )
// // PromptEvent represents an event related to a prompt update or creation.
// type PromptEvent struct {
// 	EventType PromptEventType `json:"event_type"`
// 	ID        int64           `json:"id"` // ID of the prompt
// 	Key       string          `json:"key"`
// 	Language  string          `json:"language"`
// 	Content   string          `json:"content"`
// }

// // PromptEventPublisher defines the interface for publishing prompt change events.
// type PromptEventPublisher interface {
// 	PublishPromptEvent(ctx context.Context, event PromptEvent) error
// }

// Этот файл становится пустым, если все перенести в events.go
// Возможно, его стоит удалить?
// Или оставить здесь только интерфейс Publisher?

// ОСТАВИМ ПОКА ТОЛЬКО ИНТЕРФЕЙС ЗДЕСЬ, УДАЛИВ ОСТАЛЬНОЕ

// PromptEventPublisher defines the interface for publishing prompt change events.
// ПРИМЕЧАНИЕ: Это определение дублирует то, что в events.go.
// Нужно выбрать одно место для определения.
// Предпочтительно оставить в events.go и удалить этот файл.
// Пока комментирую для устранения ошибки компиляции.
/*
type PromptEventPublisher interface {
	PublishPromptEvent(ctx context.Context, event PromptEvent) error // PromptEvent должен быть импортирован или определен
}
*/

// Фактически, после переноса всего в events.go, этот файл можно удалить.
