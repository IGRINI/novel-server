package mocks

import (
	"context"
	"novel-server/shared/messaging"
	"novel-server/story-generator/internal/service"

	"github.com/stretchr/testify/mock"
)

// MockNotifier is a mock type for the Notifier type
type MockNotifier struct {
	mock.Mock
}

// Notify provides a mock function with given fields: ctx, payload
func (_m *MockNotifier) Notify(ctx context.Context, payload messaging.NotificationPayload) error {
	ret := _m.Called(ctx, payload)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, messaging.NotificationPayload) error); ok {
		r0 = rf(ctx, payload)
	} else {
		err := ret.Error(0)
		if err != nil {
			r0 = err
		}
	}

	return r0
}

// NewMockNotifier creates a new instance of MockNotifier. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewMockNotifier(t interface {
	mock.TestingT
	Helper()
}) *MockNotifier {
	m := &MockNotifier{}
	m.Mock.Test(t)
	t.Helper()
	return m
}

var _ service.Notifier = (*MockNotifier)(nil)
