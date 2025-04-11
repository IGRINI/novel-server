package mocks

import (
	"context"
	"novel-server/story-generator/internal/service"

	"github.com/stretchr/testify/mock"
)

// MockAIClient is a mock type for the AIClient type
type MockAIClient struct {
	mock.Mock
}

// GenerateText provides a mock function with given fields: ctx, systemPrompt, userInput
func (_m *MockAIClient) GenerateText(ctx context.Context, systemPrompt string, userInput string) (string, error) {
	ret := _m.Called(ctx, systemPrompt, userInput)

	var r0 string
	if rf, ok := ret.Get(0).(func(context.Context, string, string) string); ok {
		r0 = rf(ctx, systemPrompt, userInput)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(string)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, string, string) error); ok {
		r1 = rf(ctx, systemPrompt, userInput)
	} else {
		err := ret.Error(1)
		if err != nil {
			r1 = err
		}
	}

	return r0, r1
}

// NewMockAIClient creates a new instance of MockAIClient. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewMockAIClient(t interface {
	mock.TestingT
	Helper()
}) *MockAIClient {
	m := &MockAIClient{}
	m.Mock.Test(t)
	t.Helper()
	return m
}

var _ service.AIClient = (*MockAIClient)(nil)
