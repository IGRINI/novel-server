package interfaces

import "context"

// EventType defines the type of event.
type EventType string

// PromptEventType defines the type of prompt event.
type PromptEventType string

const (
	PromptEventTypeCreated PromptEventType = "created"
	PromptEventTypeUpdated PromptEventType = "updated"
	PromptEventTypeDeleted PromptEventType = "deleted"
)

// PromptEvent represents an event related to prompt changes.
type PromptEvent struct {
	EventType PromptEventType `json:"event_type"`
	Key       string          `json:"key"`
	Language  string          `json:"language"`
	Content   string          `json:"content,omitempty"` // Content is optional for delete events
	ID        int64           `json:"id"`                // ID измененного промпта
}

// --- УДАЛЕНО: События для Dynamic Config ---
// // ConfigEventType defines the type of dynamic configuration event.
// type ConfigEventType string

// const (
// 	ConfigEventTypeCreated ConfigEventType = "created"
// 	ConfigEventTypeUpdated ConfigEventType = "updated"
// 	ConfigEventTypeDeleted ConfigEventType = "deleted"
// )

// // DynamicConfigEvent represents an event related to dynamic configuration changes.
// type DynamicConfigEvent struct {
// 	EventType ConfigEventType `json:"event_type"`
// 	Key       string          `json:"key"`
// 	Value     string          `json:"value,omitempty"` // Value is optional for delete events
// }

// Publisher интерфейсы

// PushEventPublisher defines the interface for publishing push notification events.
type PushEventPublisher interface {
	PublishPushEvent(ctx context.Context, event PushNotificationEvent) error
}

// PromptEventPublisher defines the interface for publishing prompt change events.
type PromptEventPublisher interface {
	PublishPromptEvent(ctx context.Context, event PromptEvent) error
}

// <<< УДАЛЕНО: Интерфейс для публикации событий конфигов >>>
// // DynamicConfigEventPublisher defines the interface for publishing dynamic config change events.
// type DynamicConfigEventPublisher interface {
// 	PublishDynamicConfigEvent(ctx context.Context, event DynamicConfigEvent) error
// }

// Consumer интерфейсы

// PushEventConsumer defines the interface for consuming push notification events.
type PushEventConsumer interface {
	Start(ctx context.Context) error
	Stop() error
}

// PromptEventConsumer defines the interface for consuming prompt change events.
type PromptEventConsumer interface {
	Start(ctx context.Context) error
	Stop() error
}

// DynamicConfigEventConsumer defines the interface for consuming dynamic config change events.
type DynamicConfigEventConsumer interface {
	Start(ctx context.Context) error
	Stop() error
}

// Сообщения (перенести в shared/messaging?)

// PushNotificationEvent represents a push notification message.
type PushNotificationEvent struct {
	UserID string            `json:"user_id"`
	Title  string            `json:"title"`
	Body   string            `json:"body"`
	Data   map[string]string `json:"data,omitempty"`
}
