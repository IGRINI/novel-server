package interfaces

import (
	"context"
)

// PromptEventType represents the type of prompt event.
type PromptEventType string

const (
	PromptEventTypeCreated PromptEventType = "created"
	PromptEventTypeUpdated PromptEventType = "updated"
	PromptEventTypeDeleted PromptEventType = "deleted"
)

// PromptEvent represents an event related to a prompt change.
type PromptEvent struct {
	EventType PromptEventType `json:"eventType"`
	Key       string          `json:"key"`
	Language  string          `json:"language"`
	Content   string          `json:"content,omitempty"` // Content is omitted for delete events
	ID        int64           `json:"id,omitempty"`      // Include ID for potential direct reference
}

// PromptEventPublisher defines the interface for publishing prompt change events.
type PromptEventPublisher interface {
	PublishPromptEvent(ctx context.Context, event PromptEvent) error
}
