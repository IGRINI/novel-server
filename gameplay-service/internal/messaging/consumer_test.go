package messaging_test

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"novel-server/gameplay-service/internal/messaging"
	messagingMocks "novel-server/gameplay-service/internal/messaging/mocks"
	"novel-server/gameplay-service/internal/models"
	repoMocks "novel-server/gameplay-service/internal/repository/mocks"
	sharedMessaging "novel-server/shared/messaging"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	// amqp "github.com/rabbitmq/amqp091-go" - Для тестов сам коннект не нужен
)

// --- Моки (УДАЛЕНО) ---

// --- Тесты для NotificationProcessor.Process ---

func TestNotificationProcessor_Process(t *testing.T) {
	ctx := context.Background()
	storyID := uuid.New()
	taskID := "task-abc"

	// Базовый конфиг - НЕ использовать напрямую в t.Run!
	baseConfigGenerating := &models.StoryConfig{
		ID:        storyID,
		UserID:    123, // Важно: UserID здесь не используется напрямую
		Status:    models.StatusGenerating,
		UserInput: []byte(`["Начало"]`),
	}

	t.Run("Successful processing", func(t *testing.T) {
		// Копируем конфиг
		configGenerating := *baseConfigGenerating
		mockRepo := new(repoMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.ClientUpdatePublisher)
		processor := messaging.NewNotificationProcessor(mockRepo, mockPublisher)
		generatedText := `{"t":"Новый тайтл","sd":"Новое описание"}`
		notification := sharedMessaging.NotificationPayload{
			TaskID: taskID, StoryConfigID: storyID.String(),
			Status: sharedMessaging.NotificationStatusSuccess, GeneratedText: generatedText,
		}
		body, _ := json.Marshal(notification)

		// Ожидаем вызовы
		mockRepo.On("GetByIDInternal", mock.Anything, storyID).Return(&configGenerating, nil).Once()
		mockRepo.On("Update", mock.Anything, mock.MatchedBy(func(cfg *models.StoryConfig) bool {
			// Проверяем обновление статуса, конфига, тайтла, описания
			return cfg.ID == storyID && cfg.Status == models.StatusDraft &&
				string(cfg.Config) == generatedText && cfg.Title == "Новый тайтл" && cfg.Description == "Новое описание"
		})).Return(nil).Once()
		mockPublisher.On("PublishClientUpdate", mock.Anything, mock.MatchedBy(func(payload messaging.ClientStoryUpdate) bool {
			// Проверяем данные для клиента
			return payload.ID == storyID.String() && payload.Status == string(models.StatusDraft) &&
				payload.Title == "Новый тайтл" && payload.Description == "Новое описание"
		})).Return(nil).Once()

		err := processor.Process(ctx, body, storyID)

		assert.NoError(t, err)
		mockRepo.AssertExpectations(t)
		mockPublisher.AssertExpectations(t)
	})

	t.Run("Config not generating status", func(t *testing.T) {
		// Копируем конфиг и меняем статус
		configNotGenerating := *baseConfigGenerating
		configNotGenerating.Status = models.StatusDraft
		mockRepo := new(repoMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.ClientUpdatePublisher)
		processor := messaging.NewNotificationProcessor(mockRepo, mockPublisher)
		notification := sharedMessaging.NotificationPayload{ /* ... */ }
		body, _ := json.Marshal(notification)

		// Ожидаем ТОЛЬКО GetByIDInternal
		mockRepo.On("GetByIDInternal", mock.Anything, storyID).Return(&configNotGenerating, nil).Once()

		err := processor.Process(ctx, body, storyID)

		assert.NoError(t, err) // Ошибки нет, просто выход
		mockRepo.AssertExpectations(t)
		mockPublisher.AssertNotCalled(t, "PublishClientUpdate", mock.Anything, mock.Anything)
	})

	t.Run("GetByIDInternal returns error", func(t *testing.T) {
		// Копируем конфиг - Не нужен здесь
		// configGenerating := *baseConfigGenerating
		mockRepo := new(repoMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.ClientUpdatePublisher)
		processor := messaging.NewNotificationProcessor(mockRepo, mockPublisher)
		dbError := errors.New("db error")
		notification := sharedMessaging.NotificationPayload{ /* ... */ }
		body, _ := json.Marshal(notification)

		// Ожидаем GetByIDInternal с ошибкой
		mockRepo.On("GetByIDInternal", mock.Anything, storyID).Return(nil, dbError).Once()

		err := processor.Process(ctx, body, storyID)

		assert.Error(t, err)
		assert.True(t, errors.Is(err, dbError) || strings.Contains(err.Error(), dbError.Error()))
		mockRepo.AssertExpectations(t)
		mockPublisher.AssertNotCalled(t, "PublishClientUpdate", mock.Anything, mock.Anything)
	})

	t.Run("Error notification processing", func(t *testing.T) {
		// Копируем конфиг
		configGenerating := *baseConfigGenerating
		mockRepo := new(repoMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.ClientUpdatePublisher)
		processor := messaging.NewNotificationProcessor(mockRepo, mockPublisher)
		// errorDetails не используется в проверках
		// errorDetails := "AI model failed"
		notification := sharedMessaging.NotificationPayload{
			TaskID: taskID, StoryConfigID: storyID.String(),
			Status: sharedMessaging.NotificationStatusError, ErrorDetails: "AI model failed", // Укажем здесь
		}
		body, _ := json.Marshal(notification)

		// Ожидаем вызовы
		mockRepo.On("GetByIDInternal", mock.Anything, storyID).Return(&configGenerating, nil).Once()
		mockRepo.On("Update", mock.Anything, mock.MatchedBy(func(cfg *models.StoryConfig) bool {
			return cfg.ID == storyID && cfg.Status == models.StatusError
		})).Return(nil).Once()
		mockPublisher.On("PublishClientUpdate", mock.Anything, mock.AnythingOfType("messaging.ClientStoryUpdate")).Return(nil).Once()

		err := processor.Process(ctx, body, storyID)

		assert.NoError(t, err)
		mockRepo.AssertExpectations(t)
		mockPublisher.AssertExpectations(t)
	})

	t.Run("Update returns error", func(t *testing.T) {
		// Копируем конфиг
		configGenerating := *baseConfigGenerating
		mockRepo := new(repoMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.ClientUpdatePublisher)
		processor := messaging.NewNotificationProcessor(mockRepo, mockPublisher)
		dbError := errors.New("update failed")
		notification := sharedMessaging.NotificationPayload{ /* Успешное уведомление */
			TaskID: taskID, StoryConfigID: storyID.String(),
			Status: sharedMessaging.NotificationStatusSuccess, GeneratedText: `{"t":"T"}`,
		}
		body, _ := json.Marshal(notification)

		// Настраиваем ожидания
		mockRepo.On("GetByIDInternal", mock.Anything, storyID).Return(&configGenerating, nil).Once()
		mockRepo.On("Update", mock.Anything, mock.AnythingOfType("*models.StoryConfig")).Return(dbError).Once()

		err := processor.Process(ctx, body, storyID)

		// СНАЧАЛА проверяем ожидания моков
		mockRepo.AssertExpectations(t)
		mockPublisher.AssertNotCalled(t, "PublishClientUpdate", mock.Anything, mock.Anything)

		// ПОТОМ проверяем ошибку
		if err == nil {
			t.Errorf("Expected an error, but got nil")
		} else {
			// Опционально проверяем текст ошибки
			if !strings.Contains(err.Error(), dbError.Error()) {
				t.Errorf("Error message '%v' does not contain expected '%v'", err, dbError.Error())
			}
			log.Printf("DEBUG: Test received error as expected: %v", err) // Лог для подтверждения
		}
	})

	t.Run("PublishClientUpdate returns error", func(t *testing.T) {
		// Копируем конфиг
		configGenerating := *baseConfigGenerating
		mockRepo := new(repoMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.ClientUpdatePublisher)
		processor := messaging.NewNotificationProcessor(mockRepo, mockPublisher)
		pubError := errors.New("publish failed")
		notification := sharedMessaging.NotificationPayload{ /* Успешное уведомление */ }
		body, _ := json.Marshal(notification)

		// Ожидаем вызовы
		mockRepo.On("GetByIDInternal", mock.Anything, storyID).Return(&configGenerating, nil).Once()
		mockRepo.On("Update", mock.Anything, mock.AnythingOfType("*models.StoryConfig")).Return(nil).Once()
		mockPublisher.On("PublishClientUpdate", mock.Anything, mock.AnythingOfType("messaging.ClientStoryUpdate")).Return(pubError).Once()

		err := processor.Process(ctx, body, storyID)

		assert.NoError(t, err) // Process логирует, но не возвращает ошибку паблишера
		mockRepo.AssertExpectations(t)
		mockPublisher.AssertExpectations(t)
	})

	t.Run("Error parsing generated JSON for Title/Desc", func(t *testing.T) {
		// Копируем конфиг
		configGenerating := *baseConfigGenerating
		mockRepo := new(repoMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.ClientUpdatePublisher)
		processor := messaging.NewNotificationProcessor(mockRepo, mockPublisher)
		notification := sharedMessaging.NotificationPayload{
			TaskID: taskID, StoryConfigID: storyID.String(),
			Status: sharedMessaging.NotificationStatusSuccess, GeneratedText: "невалидный json",
		}
		body, _ := json.Marshal(notification)

		// Ожидаем вызовы
		mockRepo.On("GetByIDInternal", mock.Anything, storyID).Return(&configGenerating, nil).Once()
		mockRepo.On("Update", mock.Anything, mock.AnythingOfType("*models.StoryConfig")).Return(nil).Once()
		mockPublisher.On("PublishClientUpdate", mock.Anything, mock.AnythingOfType("messaging.ClientStoryUpdate")).Return(nil).Once()

		err := processor.Process(ctx, body, storyID)

		assert.NoError(t, err)
		mockRepo.AssertExpectations(t)
		mockPublisher.AssertExpectations(t)
	})
}
