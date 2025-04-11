package mocks

import (
	"context"
	"novel-server/story-generator/internal/model"
	"novel-server/story-generator/internal/repository"

	"github.com/stretchr/testify/mock"
)

// MockResultRepository is a mock type for the ResultRepository type
type MockResultRepository struct {
	mock.Mock
}

// Save provides a mock function with given fields: ctx, result
func (_m *MockResultRepository) Save(ctx context.Context, result *model.GenerationResult) error {
	ret := _m.Called(ctx, result)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, *model.GenerationResult) error); ok {
		r0 = rf(ctx, result)
	} else {
		err := ret.Error(0)
		if err != nil {
			r0 = err
		}
	}

	return r0
}

// NewMockResultRepository creates a new instance of MockResultRepository. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewMockResultRepository(t interface {
	mock.TestingT
	Helper()
}) *MockResultRepository {
	m := &MockResultRepository{}
	m.Mock.Test(t)
	t.Helper()
	return m
}

var _ repository.ResultRepository = (*MockResultRepository)(nil)
