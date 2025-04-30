package messaging

// Exchange Names
const (
	PushNotificationExchangeName = "push_notifications_exchange"
	ClientUpdateExchangeName     = "client_updates_exchange"
	InternalUpdateExchangeName   = "internal_updates_exchange"
	PromptExchangeName           = "prompts_exchange" // Exchange для событий промптов
	// DynamicConfigExchangeName    = "dynamic_configs_exchange"    // Exchange для событий дин. конфигов
)

// Queue Names (examples, might be service-specific)
const (
	PushNotificationQueueName = "push_notifications_queue"
)

// Notification Statuses
type NotificationStatus string

const (
	NotificationStatusSuccess NotificationStatus = "success"
	NotificationStatusError   NotificationStatus = "error"
)
