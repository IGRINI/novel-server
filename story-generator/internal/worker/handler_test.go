package worker_test

import (
	// "context" // Не используется
	// "errors" // Не используется
	"os"
	"path/filepath"
	"testing"
	"time"

	"novel-server/shared/messaging"
	"novel-server/story-generator/internal/config"
	"novel-server/story-generator/internal/mocks"
	"novel-server/story-generator/internal/model"
	"novel-server/story-generator/internal/worker"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TODO: Добавить тесты для TaskHandler.Handle

// setupTest создает временную директорию для промтов и возвращает ее путь и функцию очистки.
func setupTest(t *testing.T) (string, func()) {
	tmpDir, err := os.MkdirTemp("", "prompts_test")
	assert.NoError(t, err)
	return tmpDir, func() { os.RemoveAll(tmpDir) }
}

// copyAndReadPrompt копирует файл промта из исходного пути во временную директорию
// и возвращает его исходное содержимое как строку.
func copyAndReadPrompt(t *testing.T, sourcePath, destDir, promptFilename string) string {
	destPath := filepath.Join(destDir, promptFilename)

	contentBytes, err := os.ReadFile(sourcePath)
	assert.NoError(t, err, "Failed to read source prompt file: %s", sourcePath)

	err = os.WriteFile(destPath, contentBytes, 0644)
	assert.NoError(t, err, "Failed to write prompt file to temp dir: %s", destPath)

	// Возвращаем содержимое как есть, без нормализации концов строк
	return string(contentBytes)
}

// Константы для тестов
const (
	testUserID            = "user-123"
	testTaskID            = "task-456"
	testGeneratedText     = "Generated text result."
	testNarratorUserInput = "Generate a simple fantasy story about a knight."
	// Корректный путь от story-generator/internal/worker к корню проекта и затем к prompts
	sourceNarratorPath = "../../../prompts/narrator.md"
)

func TestTaskHandler_Handle_NarratorPrompt(t *testing.T) {
	cfg := &config.Config{
		AITimeout:        5 * time.Second,
		AIMaxAttempts:    1,
		AIBaseRetryDelay: 100 * time.Millisecond,
		// PromptsDir будет установлен внутри теста
	}

	t.Run("Successful processing with narrator prompt", func(t *testing.T) {
		promptsDir, cleanup := setupTest(t)
		defer cleanup()

		// Копируем и читаем содержимое промта narrator.md
		expectedNarratorContent := copyAndReadPrompt(t, sourceNarratorPath, promptsDir, "narrator.md")

		cfg.PromptsDir = promptsDir

		mockAI := mocks.NewMockAIClient(t)
		mockRepo := mocks.NewMockResultRepository(t)
		mockNotifier := mocks.NewMockNotifier(t)

		handler := worker.NewTaskHandler(cfg, mockAI, mockRepo, mockNotifier)

		// Configure mock expectations
		mockAI.On("GenerateText",
			mock.Anything,           // context
			expectedNarratorContent, // Ожидаем содержимое файла как system prompt
			testNarratorUserInput,   // Ожидаем фиксированный user input
		).Return(testGeneratedText, nil).Once()

		mockRepo.On("Save",
			mock.Anything, // context
			mock.AnythingOfType("*model.GenerationResult"),
		).Return(nil).Once().Run(func(args mock.Arguments) {
			result := args.Get(1).(*model.GenerationResult)
			// Используем testTaskID для ID результата, т.к. payload его содержит
			assert.Equal(t, testTaskID, result.ID)
			assert.Equal(t, testUserID, result.UserID)
			assert.Equal(t, testGeneratedText, result.GeneratedText)
			assert.Empty(t, result.Error) // No error expected
		})

		mockNotifier.On("Notify",
			mock.Anything, // context
			mock.AnythingOfType("messaging.NotificationPayload"),
		).Return(nil).Once().Run(func(args mock.Arguments) {
			notification := args.Get(1).(messaging.NotificationPayload)
			assert.Equal(t, testUserID, notification.UserID)
			assert.Equal(t, testTaskID, notification.TaskID)
			// Проверяем статус успеха
			assert.Equal(t, messaging.NotificationStatusSuccess, notification.Status)
			assert.Equal(t, testGeneratedText, notification.GeneratedText)
			assert.Empty(t, notification.ErrorDetails) // Ожидаем пустые детали ошибки
		})

		// Create payload
		payload := messaging.GenerationTaskPayload{
			TaskID:     testTaskID, // Используем TaskID из констант
			UserID:     testUserID,
			PromptType: messaging.PromptTypeNarrator, // Use narrator prompt type
			UserInput:  testNarratorUserInput,        // Use predefined user input
			InputData:  nil,                          // No template data needed for narrator
			// ShouldNotify: true, // Поле отсутствует в структуре, уведомление отправляется всегда при успехе
		}

		// Execute the handler
		err := handler.Handle(payload)

		// Проверяем результат
		assert.NoError(t, err)

		// Убеждаемся, что все ожидания моков были выполнены
		mockAI.AssertExpectations(t)
		mockRepo.AssertExpectations(t)
		mockNotifier.AssertExpectations(t)
	})
}
