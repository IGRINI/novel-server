package mocks

import (
	"context"
	"novel-server/gameplay-service/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

// Mock StoryConfigRepository
type StoryConfigRepository struct {
	mock.Mock
}

func (m *StoryConfigRepository) Create(ctx context.Context, config *models.StoryConfig) error {
	args := m.Called(ctx, config)
	return args.Error(0)
}
func (m *StoryConfigRepository) GetByID(ctx context.Context, id uuid.UUID, userID uint64) (*models.StoryConfig, error) {
	args := m.Called(ctx, id, userID)
	cfg, _ := args.Get(0).(*models.StoryConfig)
	return cfg, args.Error(1)
}
func (m *StoryConfigRepository) Update(ctx context.Context, config *models.StoryConfig) error {
	args := m.Called(ctx, config)
	return args.Error(0)
}
func (m *StoryConfigRepository) GetByIDInternal(ctx context.Context, id uuid.UUID) (*models.StoryConfig, error) {
	args := m.Called(ctx, id)
	cfg, _ := args.Get(0).(*models.StoryConfig)
	return cfg, args.Error(1)
}
func (m *StoryConfigRepository) CountActiveGenerations(ctx context.Context, userID uint64) (int, error) {
	args := m.Called(ctx, userID)
	return args.Int(0), args.Error(1)
}
