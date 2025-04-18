package entities

// UserPushEvent represents a push notification event targetted at a specific user.
type UserPushEvent struct {
	UserID  string `json:"user_id"` // The target user ID for the notification.
	Title   string `json:"title"`   // The title of the push notification.
	Message string `json:"message"` // The message content of the push notification.
}
