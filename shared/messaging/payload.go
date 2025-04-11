package messaging

// PromptType определяет тип задачи для генерации
type PromptType string

const (
	PromptTypeNovelSetup             PromptType = "novel_setup"
	PromptTypeNovelCreator           PromptType = "novel_creator"
	PromptTypeNovelFirstSceneCreator PromptType = "novel_first_scene_creator"
	PromptTypeNarrator               PromptType = "narrator"
	PromptTypeGameOver               PromptType = "gameover"
	// Добавить другие типы по необходимости
)

// GenerationTaskPayload - данные, передаваемые в очередь для воркера генерации
type GenerationTaskPayload struct {
	TaskID     string                 `json:"taskId"`              // Уникальный ID задачи
	UserID     string                 `json:"userId"`              // ID пользователя, инициировавшего задачу
	PromptType PromptType             `json:"promptType"`          // Тип промта/задачи
	InputData  map[string]interface{} `json:"inputData,omitempty"` // Дополнительные данные для шаблона промта (гибкий формат)
	UserInput  string                 `json:"userInput,omitempty"` // Текст от пользователя (для роли user в чате)
}

// --- Добавляем структуры для уведомлений ---

// NotificationStatus - статус завершения задачи для уведомления
type NotificationStatus string

const (
	NotificationStatusSuccess NotificationStatus = "success"
	NotificationStatusError   NotificationStatus = "error"
)

// NotificationPayload - данные, отправляемые в очередь уведомлений
type NotificationPayload struct {
	TaskID        string             `json:"taskId"`                  // ID исходной задачи
	UserID        string             `json:"userId"`                  // ID пользователя для уведомления
	Status        NotificationStatus `json:"status"`                  // Статус (success/error)
	GeneratedText string             `json:"generatedText,omitempty"` // Сгенерированный текст (при успехе)
	ErrorDetails  string             `json:"errorDetails,omitempty"`  // Детали ошибки (при ошибке)
	PromptType    PromptType         `json:"promptType"`              // Тип промта, чтобы клиент знал, что это было
	// Можно добавить любые другие метаданные, полезные клиенту
}
