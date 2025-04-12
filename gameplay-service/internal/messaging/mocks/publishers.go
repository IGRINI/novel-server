package mocks

import (
	"context"
	"novel-server/gameplay-service/internal/messaging"
	sharedMessaging "novel-server/shared/messaging"

	"github.com/stretchr/testify/mock"
)

// Mock TaskPublisher
type TaskPublisher struct {
	mock.Mock
}

func (m *TaskPublisher) PublishGenerationTask(ctx context.Context, payload sharedMessaging.GenerationTaskPayload) error {
	args := m.Called(ctx, payload)
	return args.Error(0)
}

// Mock ClientUpdatePublisher
type ClientUpdatePublisher struct {
	mock.Mock
}

func (m *ClientUpdatePublisher) PublishClientUpdate(ctx context.Context, payload messaging.ClientStoryUpdate) error {
	args := m.Called(ctx, payload)
	return args.Error(0)
}
