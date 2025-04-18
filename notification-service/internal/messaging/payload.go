package messaging

import "github.com/google/uuid"

// PushNotificationPayload - структура сообщения, получаемого из очереди.
// Важно: Эта структура должна быть идентична той, что отправляет gameplay-service.
// В идеале, вынести ее в общий shared пакет.
type PushNotificationPayload struct {
	UserID       uuid.UUID         `json:"user_id"`
	Notification PushNotification  `json:"notification"`
	Data         map[string]string `json:"data,omitempty"`
}

// PushNotification содержит видимые части push-сообщения.
type PushNotification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}
