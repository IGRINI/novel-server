package messaging

// PromptType определяет тип запроса к AI генератору
type PromptType string

// Константы для типов промптов
const (
	PromptTypeNarrator               PromptType = "narrator"                  // Генерация базовых параметров мира по запросу пользователя
	PromptTypeNovelSetup             PromptType = "novel_setup"               // Генерация стартового состояния мира (статы, персонажи)
	PromptTypeNovelFirstSceneCreator PromptType = "novel_first_scene_creator" // Генерация первой сцены
	PromptTypeNovelCreator           PromptType = "novel_creator"             // Генерация следующей сцены
	PromptTypeNovelGameOverCreator   PromptType = "novel_game_over_creator"   // Генерация финальной сцены (конец игры)
	// Добавить другие типы по необходимости
)

// GenerationTaskPayload - структура сообщения для задачи генерации
type GenerationTaskPayload struct {
	TaskID        string                 `json:"task_id"`                   // Уникальный ID задачи генерации
	UserID        string                 `json:"user_id"`                   // ID пользователя
	PromptType    PromptType             `json:"prompt_type"`               // Тип запроса к AI
	InputData     map[string]interface{} `json:"input_data"`                // Данные для шаблонизации промпта (из StoryConfig.UserInput)
	UserInput     string                 `json:"user_input"`                // Основной ввод пользователя для AI (например, описание из StoryConfig или промт ревизии)
	StoryConfigID string                 `json:"story_config_id,omitempty"` // Опционально: ID конфигурации истории для связи
}

// NotificationStatus определяет статус уведомления
type NotificationStatus string

const (
	NotificationStatusSuccess NotificationStatus = "success"
	NotificationStatusError   NotificationStatus = "error"
)

// NotificationPayload - структура сообщения для уведомления пользователя
type NotificationPayload struct {
	TaskID        string             `json:"task_id"`                   // ID задачи, которая завершилась
	UserID        string             `json:"user_id"`                   // ID пользователя для отправки уведомления
	PromptType    PromptType         `json:"prompt_type"`               // Тип промпта, который выполнялся
	Status        NotificationStatus `json:"status"`                    // Статус выполнения (success/error)
	GeneratedText string             `json:"generated_text,omitempty"`  // Сгенерированный текст (при успехе)
	ErrorDetails  string             `json:"error_details,omitempty"`   // Детали ошибки (при ошибке)
	StoryConfigID string             `json:"story_config_id,omitempty"` // Опционально: ID конфигурации истории, если применимо
}

// IsValidPromptType проверяет, является ли строка допустимым PromptType.
func IsValidPromptType(pt PromptType) bool {
	switch pt {
	case PromptTypeNarrator, PromptTypeNovelSetup, PromptTypeNovelFirstSceneCreator, PromptTypeNovelCreator, PromptTypeNovelGameOverCreator:
		return true
	default:
		return false
	}
}
