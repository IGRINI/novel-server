package interfaces

import (
	"context"
	"novel-server/shared/entities"
)

// PushEventPublisher defines the interface for publishing push notification events.
type PushEventPublisher interface {
	// PublishUserPushEvent sends a push notification event for a specific user.
	PublishUserPushEvent(ctx context.Context, event entities.UserPushEvent) error
}
