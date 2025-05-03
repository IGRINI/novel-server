package constants

// Основной ключ для локализации в data payload
const PushLocKey = "loc_key"

// Event types used in push notification data payload
const (
	PushEventTypeStoryReady   = "story_ready"   // История готова к игре
	PushEventTypeDraftReady   = "draft_ready"   // Черновик готов к настройке
	PushEventTypeSetupPending = "setup_pending" // Setup готов, ожидается первая сцена
	PushEventTypeSceneReady   = "scene_ready"   // Новая сцена готова
	PushEventTypeGameOver     = "game_over"     // Игра завершена
	// TODO: Add other event types as needed (e.g., scene_ready, game_over)
)

// Ключи локализации для Push-уведомлений (для поля loc_key в data payload)
const (
	// Черновик готов
	PushLocKeyDraftReady = "notification_draft_ready"
	// Setup готов, ожидание первой сцены
	PushLocKeySetupReady = "notification_setup_ready"
	// История готова к игре (Setup + 1я сцена + картинки)
	PushLocKeyStoryReady = "notification_story_ready"
	// Новая сцена готова
	PushLocKeySceneReady = "notification_scene_ready"
	// Игра завершена
	PushLocKeyGameOver = "notification_game_over"
)

// Имена аргументов локализации (для полей loc_arg_* в data payload)
const (
	PushLocArgStoryTitle = "storyTitle"
	PushLocArgEndingText = "ending_text"
)

// Ключи для Fallback текста (если локализация на клиенте не сработает)
const (
	PushFallbackTitleKey = "fallback_title"
	PushFallbackBodyKey  = "fallback_body"
)
