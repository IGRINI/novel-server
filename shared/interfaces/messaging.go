package interfaces

import (
	"context"
)

// TokenDeletionPublisher defines the interface for publishing messages
// indicating that a device token should be deleted.
type TokenDeletionPublisher interface {
	// PublishTokenDeletion sends the token string to the designated queue/topic.
	PublishTokenDeletion(ctx context.Context, token string) error
}

// Добавь сюда другие интерфейсы, связанные с messaging, если нужно
