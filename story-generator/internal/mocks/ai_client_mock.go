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

// GenerateText provides a mock function with given fields: ctx, systemPrompt, userInput, params
func (_m *MockAIClient) GenerateText(ctx context.Context, systemPrompt string, userInput string, params service.GenerationParams) (string, error) {
	ret := _m.Called(ctx, systemPrompt, userInput, params)

	var r0 string
	if rf, ok := ret.Get(0).(func(context.Context, string, string, service.GenerationParams) string); ok {
		r0 = rf(ctx, systemPrompt, userInput, params)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(string)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, string, string, service.GenerationParams) error); ok {
		r1 = rf(ctx, systemPrompt, userInput, params)
	} else {
		err := ret.Error(1)
		if err != nil {
			r1 = err
		}
	}

	return r0, r1
}

// GenerateTextStream provides a mock function to match the updated interface
func (_m *MockAIClient) GenerateTextStream(ctx context.Context, systemPrompt string, userInput string, params service.GenerationParams, chunkHandler func(string) error) error {
	// Вызываем мок с новыми аргументами
	ret := _m.Called(ctx, systemPrompt, userInput, params, chunkHandler)

	// Обрабатываем возвращаемое значение (только ошибка)
	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string, string, service.GenerationParams, func(string) error) error); ok {
		r0 = rf(ctx, systemPrompt, userInput, params, chunkHandler)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

/* <<< Старая реализация мока закомментирована >>>
// GenerateTextStream provides a mock function with given fields: ctx, systemPrompt, userInput
func (_m *MockAIClient) GenerateTextStream(ctx context.Context, systemPrompt string, userInput string, params service.GenerationParams) (*openaigo.ChatCompletionStream, error) {
	ret := _m.Called(ctx, systemPrompt, userInput, params)

	var r0 *openaigo.ChatCompletionStream
	if rf, ok := ret.Get(0).(func(context.Context, string, string, service.GenerationParams) *openaigo.ChatCompletionStream); ok {
		r0 = rf(ctx, systemPrompt, userInput, params)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*openaigo.ChatCompletionStream)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, string, string, service.GenerationParams) error); ok {
		r1 = rf(ctx, systemPrompt, userInput, params)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}
*/

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
