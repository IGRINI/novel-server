package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	// "novel-server/gameplay-service/internal/messaging"
	messagingMocks "novel-server/gameplay-service/internal/messaging/mocks"
	// "novel-server/gameplay-service/internal/models" // <<< Удаляем старый импорт
	// repositoryMocks "novel-server/gameplay-service/internal/repository/mocks" // <<< Удаляем старый импорт мока
	"novel-server/gameplay-service/internal/service"
	repositoryMocks "novel-server/shared/interfaces/mocks" // <<< Добавляем новый импорт мока
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

// TestGenerateInitialStory tests the GenerateInitialStory method
func TestGenerateInitialStory(t *testing.T) {
	// userID := uint64(123)
	userID := uuid.New() // <<< Use UUID
	initialPrompt := "Хочу историю про космос"
	ctx := context.Background()

	t.Run("Successful initial generation", func(t *testing.T) {
		mockRepo := new(repositoryMocks.StoryConfigRepository) // Используем мок из shared/interfaces/mocks
		mockPublisher := new(messagingMocks.TaskPublisher)     // Используем мок из пакета
		gameplayService := service.NewGameplayService(mockRepo, nil, nil, nil, nil, mockPublisher, nil, zap.NewNop())

		// Ожидаем вызов CountActiveGenerations, возвращаем 0
		mockRepo.On("CountActiveGenerations", ctx, userID).Return(0, nil).Once()

		// Ожидаем вызов Create
		mockRepo.On("Create", ctx, mock.MatchedBy(func(cfg *sharedModels.StoryConfig) bool { // <<< Используем sharedModels
			// Проверяем основные поля создаваемого конфига
			assert.Equal(t, userID, cfg.UserID)
			assert.Equal(t, sharedModels.StatusGenerating, cfg.Status) // <<< Используем sharedModels
			assert.Nil(t, cfg.Config)                                  // Config должен быть nil
			// Проверяем, что UserInput - это JSON массив с одним элементом
			var userInputs []string
			err := json.Unmarshal(cfg.UserInput, &userInputs)
			assert.NoError(t, err)
			assert.Equal(t, []string{initialPrompt}, userInputs)
			return true
		})).Return(nil).Once()

		// Ожидаем вызов PublishGenerationTask
		mockPublisher.On("PublishGenerationTask", ctx, mock.MatchedBy(func(payload sharedMessaging.GenerationTaskPayload) bool {
			assert.Equal(t, initialPrompt, payload.UserInput)
			assert.Equal(t, sharedMessaging.PromptTypeNarrator, payload.PromptType)
			assert.Empty(t, payload.InputData)        // InputData должен быть пустым
			assert.NotEmpty(t, payload.StoryConfigID) // StoryConfigID должен быть
			assert.NotEmpty(t, payload.TaskID)
			// assert.Equal(t, strconv.FormatUint(userID, 10), payload.UserID)
			assert.Equal(t, userID.String(), payload.UserID) // <<< Check UUID string
			return true
		})).Return(nil).Once()

		// Вызываем тестируемый метод
		createdConfig, err := gameplayService.GenerateInitialStory(ctx, userID, initialPrompt, "en")

		// Проверяем результат
		assert.NoError(t, err)
		assert.NotNil(t, createdConfig)
		assert.Equal(t, userID, createdConfig.UserID)
		assert.Equal(t, sharedModels.StatusGenerating, createdConfig.Status) // <<< Используем sharedModels
		assert.NotEmpty(t, createdConfig.ID)

		// Убеждаемся, что все ожидаемые вызовы были сделаны
		mockRepo.AssertExpectations(t)
		mockPublisher.AssertExpectations(t)
	})

	t.Run("Generation limit reached", func(t *testing.T) {
		mockRepo := new(repositoryMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.TaskPublisher)
		gameplayService := service.NewGameplayService(mockRepo, nil, nil, nil, nil, mockPublisher, nil, zap.NewNop())

		// Ожидаем вызов CountActiveGenerations, возвращаем лимит (1)
		mockRepo.On("CountActiveGenerations", ctx, userID).Return(1, nil).Once()

		// Вызываем тестируемый метод
		createdConfig, err := gameplayService.GenerateInitialStory(ctx, userID, initialPrompt, "en")

		// Проверяем результат
		assert.Error(t, err)
		assert.Nil(t, createdConfig)
		assert.True(t, errors.Is(err, sharedModels.ErrUserHasActiveGeneration))

		// Убеждаемся, что Create и Publish не вызывались
		mockRepo.AssertExpectations(t)
		mockPublisher.AssertNotCalled(t, "PublishGenerationTask", mock.Anything, mock.Anything)
	})

	t.Run("Error counting active generations", func(t *testing.T) {
		mockRepo := new(repositoryMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.TaskPublisher)
		gameplayService := service.NewGameplayService(mockRepo, nil, nil, nil, nil, mockPublisher, nil, zap.NewNop())
		dbError := errors.New("database error")

		// Ожидаем вызов CountActiveGenerations, возвращаем ошибку
		mockRepo.On("CountActiveGenerations", ctx, userID).Return(0, dbError).Once()

		// Вызываем тестируемый метод
		createdConfig, err := gameplayService.GenerateInitialStory(ctx, userID, initialPrompt, "en")

		// Проверяем результат
		assert.Error(t, err)
		assert.Nil(t, createdConfig)
		assert.Contains(t, err.Error(), "ошибка проверки статуса генерации")
		assert.True(t, errors.Is(err, dbError)) // Проверяем, что исходная ошибка обернута

		// Убеждаемся, что Create и Publish не вызывались
		mockRepo.AssertExpectations(t)
		mockPublisher.AssertNotCalled(t, "PublishGenerationTask", mock.Anything, mock.Anything)
	})

	t.Run("Error creating draft", func(t *testing.T) {
		mockRepo := new(repositoryMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.TaskPublisher)
		gameplayService := service.NewGameplayService(mockRepo, nil, nil, nil, nil, mockPublisher, nil, zap.NewNop())
		createError := errors.New("failed to create")

		// Ожидаем вызов CountActiveGenerations, возвращаем 0
		mockRepo.On("CountActiveGenerations", ctx, userID).Return(0, nil).Once()

		// Ожидаем вызов Create, возвращаем ошибку
		mockRepo.On("Create", ctx, mock.AnythingOfType("*sharedmodels.StoryConfig")).Return(createError).Once() // <<< Используем sharedModels

		// Вызываем тестируемый метод
		createdConfig, err := gameplayService.GenerateInitialStory(ctx, userID, initialPrompt, "en")

		// Проверяем результат
		assert.Error(t, err)
		assert.Nil(t, createdConfig) // Конфиг не должен возвращаться при ошибке Create
		assert.Contains(t, err.Error(), "ошибка сохранения начального драфта")
		assert.True(t, errors.Is(err, createError))

		// Убеждаемся, что Publish не вызывался
		mockRepo.AssertExpectations(t)
		mockPublisher.AssertNotCalled(t, "PublishGenerationTask", mock.Anything, mock.Anything)
	})

	t.Run("Error publishing task", func(t *testing.T) {
		mockRepo := new(repositoryMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.TaskPublisher)
		gameplayService := service.NewGameplayService(mockRepo, nil, nil, nil, nil, mockPublisher, nil, zap.NewNop())
		publishError := errors.New("failed to publish")

		// Ожидаем вызов CountActiveGenerations, возвращаем 0
		mockRepo.On("CountActiveGenerations", ctx, userID).Return(0, nil).Once()

		// Ожидаем успешный вызов Create
		var capturedConfig *sharedModels.StoryConfig                                         // <<< Используем sharedModels
		mockRepo.On("Create", ctx, mock.MatchedBy(func(cfg *sharedModels.StoryConfig) bool { // <<< Используем sharedModels
			capturedConfig = cfg // Захватываем созданный конфиг
			return true
		})).Return(nil).Once()

		// Ожидаем вызов PublishGenerationTask, возвращаем ошибку
		mockPublisher.On("PublishGenerationTask", ctx, mock.AnythingOfType("sharedMessaging.GenerationTaskPayload")).Return(publishError).Once() // <<< Используем sharedMessaging

		// Ожидаем вызов Update для отката статуса на Error
		mockRepo.On("Update", mock.Anything, mock.MatchedBy(func(cfg *sharedModels.StoryConfig) bool { // <<< Используем sharedModels
			return cfg.ID == capturedConfig.ID && cfg.Status == sharedModels.StatusError // <<< Используем sharedModels
		})).Return(nil).Once()

		// Вызываем тестируемый метод
		returnedConfig, err := gameplayService.GenerateInitialStory(ctx, userID, initialPrompt, "en")

		// Проверяем результат
		assert.Error(t, err)
		assert.NotNil(t, returnedConfig)
		assert.Equal(t, capturedConfig.ID, returnedConfig.ID)
		assert.Equal(t, sharedModels.StatusError, returnedConfig.Status) // <<< Используем sharedModels
		assert.Contains(t, err.Error(), "ошибка отправки задачи на генерацию")
		assert.True(t, errors.Is(err, publishError) || strings.Contains(err.Error(), publishError.Error()))

		// Убеждаемся, что все ожидаемые вызовы были сделаны
		mockRepo.AssertExpectations(t)
		mockPublisher.AssertExpectations(t)
	})
}

// TestReviseDraft tests the ReviseDraft method
func TestReviseDraft(t *testing.T) {
	// baseUserID := uint64(456)
	baseUserID := uuid.New() // <<< Use UUID
	baseStoryID := uuid.New()
	baseRevisionPrompt := "Сделать главного героя магом"
	ctx := context.Background()

	// Базовые данные для конфига - НЕ использовать напрямую в t.Run!
	baseInitialUserInputJSON, _ := json.Marshal([]string{"Начальный промпт"})
	baseCurrentConfigJSON, _ := json.Marshal(map[string]string{"t": "Старый тайтл", "sd": "Старое описание"})

	t.Run("Successful revision", func(t *testing.T) {
		// Создаем копии внутри теста!
		userID := baseUserID
		storyID := baseStoryID
		revisionPrompt := baseRevisionPrompt
		existingConfig := &sharedModels.StoryConfig{ // <<< Используем sharedModels
			ID:        storyID,
			UserID:    userID,
			UserInput: baseInitialUserInputJSON,
			Config:    baseCurrentConfigJSON,
			Status:    sharedModels.StatusDraft, // <<< Используем sharedModels
		}
		currentConfigJSONString := string(baseCurrentConfigJSON)

		mockRepo := new(repositoryMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.TaskPublisher)
		gameplayService := service.NewGameplayService(mockRepo, nil, nil, nil, nil, mockPublisher, nil, zap.NewNop())

		// Ожидаем GetByID
		mockRepo.On("GetByID", ctx, storyID, userID).Return(existingConfig, nil).Once()
		// Ожидаем CountActiveGenerations, возвращаем 0
		mockRepo.On("CountActiveGenerations", ctx, userID).Return(0, nil).Once()
		// Ожидаем Update (статус generating, UserInput обновлен)
		mockRepo.On("Update", ctx, mock.MatchedBy(func(cfg *sharedModels.StoryConfig) bool { // <<< Используем sharedModels
			assert.Equal(t, storyID, cfg.ID)
			assert.Equal(t, sharedModels.StatusGenerating, cfg.Status) // <<< Используем sharedModels
			var userInputs []string
			err := json.Unmarshal(cfg.UserInput, &userInputs)
			assert.NoError(t, err)
			assert.Equal(t, []string{"Начальный промпт", revisionPrompt}, userInputs) // Проверяем добавление
			return true
		})).Return(nil).Once()
		// Ожидаем PublishGenerationTask
		mockPublisher.On("PublishGenerationTask", ctx, mock.MatchedBy(func(payload sharedMessaging.GenerationTaskPayload) bool { // <<< Используем sharedMessaging
			assert.Equal(t, revisionPrompt, payload.UserInput)
			assert.Equal(t, sharedMessaging.PromptTypeNarrator, payload.PromptType)
			assert.NotEmpty(t, payload.InputData)
			assert.Contains(t, payload.InputData, "current_config") // Должен быть current_config
			assert.Equal(t, currentConfigJSONString, payload.InputData["current_config"])
			assert.Equal(t, storyID.String(), payload.StoryConfigID)
			assert.Equal(t, userID.String(), payload.UserID) // <<< Check UUID string
			return true
		})).Return(nil).Once()

		// Вызываем метод
		err := gameplayService.ReviseDraft(ctx, storyID, userID, revisionPrompt)

		// Проверяем результат
		assert.NoError(t, err)
		mockRepo.AssertExpectations(t)
		mockPublisher.AssertExpectations(t)
	})

	t.Run("Revision with invalid status", func(t *testing.T) {
		userID := baseUserID
		storyID := baseStoryID
		revisionPrompt := baseRevisionPrompt
		// Создаем конфиг с невалидным статусом внутри теста
		invalidStatusConfig := &sharedModels.StoryConfig{ // <<< Используем sharedModels
			ID:        storyID,
			UserID:    userID,
			UserInput: baseInitialUserInputJSON,
			Config:    baseCurrentConfigJSON,
			Status:    sharedModels.StatusGenerating, // Невалидный статус <<< Используем sharedModels
		}

		mockRepo := new(repositoryMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.TaskPublisher)
		gameplayService := service.NewGameplayService(mockRepo, nil, nil, nil, nil, mockPublisher, nil, zap.NewNop())

		// Ожидаем GetByID
		mockRepo.On("GetByID", ctx, storyID, userID).Return(invalidStatusConfig, nil).Once()

		// Вызываем метод
		err := gameplayService.ReviseDraft(ctx, storyID, userID, revisionPrompt)

		// Проверяем результат
		assert.Error(t, err)
		assert.True(t, errors.Is(err, sharedModels.ErrCannotRevise))
		mockRepo.AssertExpectations(t)
		mockPublisher.AssertNotCalled(t, "PublishGenerationTask", mock.Anything, mock.Anything)
	})

	t.Run("Revision when generation limit reached", func(t *testing.T) {
		userID := baseUserID
		storyID := baseStoryID
		revisionPrompt := baseRevisionPrompt
		existingConfig := &sharedModels.StoryConfig{ // <<< Используем sharedModels
			ID:        storyID,
			UserID:    userID,
			UserInput: baseInitialUserInputJSON,
			Config:    baseCurrentConfigJSON,
			Status:    sharedModels.StatusDraft, // Валидный статус <<< Используем sharedModels
		}

		mockRepo := new(repositoryMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.TaskPublisher)
		gameplayService := service.NewGameplayService(mockRepo, nil, nil, nil, nil, mockPublisher, nil, zap.NewNop())

		// Ожидаем GetByID (успешно)
		mockRepo.On("GetByID", ctx, storyID, userID).Return(existingConfig, nil).Once()
		// Ожидаем CountActiveGenerations, возвращаем лимит (1)
		mockRepo.On("CountActiveGenerations", ctx, userID).Return(1, nil).Once()

		// Вызываем метод
		err := gameplayService.ReviseDraft(ctx, storyID, userID, revisionPrompt)

		// Проверяем результат
		assert.Error(t, err)
		assert.True(t, errors.Is(err, sharedModels.ErrUserHasActiveGeneration))
		mockRepo.AssertExpectations(t)
		mockPublisher.AssertNotCalled(t, "PublishGenerationTask", mock.Anything, mock.Anything)
	})

	t.Run("Error getting config for revision", func(t *testing.T) {
		userID := baseUserID
		storyID := baseStoryID
		revisionPrompt := baseRevisionPrompt
		getError := errors.New("get failed")

		mockRepo := new(repositoryMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.TaskPublisher)
		gameplayService := service.NewGameplayService(mockRepo, nil, nil, nil, nil, mockPublisher, nil, zap.NewNop())

		// Ожидаем ТОЛЬКО GetByID, возвращаем ошибку
		mockRepo.On("GetByID", ctx, storyID, userID).Return(nil, getError).Once()

		// Вызываем метод
		err := gameplayService.ReviseDraft(ctx, storyID, userID, revisionPrompt)

		// Проверяем результат
		assert.Error(t, err)
		assert.True(t, errors.Is(err, getError) || strings.Contains(err.Error(), getError.Error()))
		mockRepo.AssertExpectations(t)
		mockPublisher.AssertNotCalled(t, "PublishGenerationTask", mock.Anything, mock.Anything)
	})

	t.Run("Error updating config before publishing revision task", func(t *testing.T) {
		userID := baseUserID
		storyID := baseStoryID
		revisionPrompt := baseRevisionPrompt
		existingConfig := &sharedModels.StoryConfig{ // <<< Используем sharedModels
			ID:        storyID,
			UserID:    userID,
			UserInput: baseInitialUserInputJSON,
			Config:    baseCurrentConfigJSON,
			Status:    sharedModels.StatusDraft, // <<< Используем sharedModels
		}
		updateError := errors.New("update failed")

		mockRepo := new(repositoryMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.TaskPublisher)
		gameplayService := service.NewGameplayService(mockRepo, nil, nil, nil, nil, mockPublisher, nil, zap.NewNop())

		// Ожидаем GetByID (успешно)
		mockRepo.On("GetByID", ctx, storyID, userID).Return(existingConfig, nil).Once()
		// Ожидаем CountActiveGenerations (успешно, < лимита)
		mockRepo.On("CountActiveGenerations", ctx, userID).Return(0, nil).Once()
		// Ожидаем Update, возвращаем ошибку
		mockRepo.On("Update", ctx, mock.AnythingOfType("*sharedmodels.StoryConfig")).Return(updateError).Once() // <<< Используем sharedModels

		// Вызываем метод
		err := gameplayService.ReviseDraft(ctx, storyID, userID, revisionPrompt)

		// Проверяем результат
		assert.Error(t, err)
		assert.True(t, errors.Is(err, updateError) || strings.Contains(err.Error(), updateError.Error()))
		mockRepo.AssertExpectations(t)
		mockPublisher.AssertNotCalled(t, "PublishGenerationTask", mock.Anything, mock.Anything)
	})

	t.Run("Error publishing revision task", func(t *testing.T) {
		userID := baseUserID
		storyID := baseStoryID
		revisionPrompt := baseRevisionPrompt
		publishError := errors.New("publish failed")

		// Создаем копию внутри теста!
		configToRevise := &sharedModels.StoryConfig{ // <<< Используем sharedModels
			ID:        storyID,
			UserID:    userID,
			UserInput: baseInitialUserInputJSON,
			Config:    baseCurrentConfigJSON,
			Status:    sharedModels.StatusDraft, // <<< Используем sharedModels
		}

		mockRepo := new(repositoryMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.TaskPublisher)
		gameplayService := service.NewGameplayService(mockRepo, nil, nil, nil, nil, mockPublisher, nil, zap.NewNop())

		// Ожидаем GetByID
		mockRepo.On("GetByID", ctx, storyID, userID).Return(configToRevise, nil).Once()
		// Ожидаем CountActiveGenerations, возвращаем 0
		mockRepo.On("CountActiveGenerations", ctx, userID).Return(0, nil).Once()
		// Ожидаем первый успешный Update
		mockRepo.On("Update", ctx, mock.MatchedBy(func(cfg *sharedModels.StoryConfig) bool { return cfg.Status == sharedModels.StatusGenerating })).Return(nil).Once() // <<< Используем sharedModels
		// Ожидаем PublishGenerationTask, возвращаем ошибку
		mockPublisher.On("PublishGenerationTask", ctx, mock.AnythingOfType("sharedMessaging.GenerationTaskPayload")).Return(publishError).Once() // <<< Используем sharedMessaging
		// Ожидаем второй Update (откат)
		mockRepo.On("Update", ctx, mock.MatchedBy(func(cfg *sharedModels.StoryConfig) bool { // <<< Используем sharedModels
			var userInputs []string
			err := json.Unmarshal(cfg.UserInput, &userInputs)
			return cfg.Status == sharedModels.StatusDraft && err == nil && len(userInputs) == 1 && userInputs[0] == "Начальный промпт" // <<< Используем sharedModels
		})).Return(nil).Once()

		// Вызываем метод
		err := gameplayService.ReviseDraft(ctx, storyID, userID, revisionPrompt)

		// Проверяем результат
		assert.Error(t, err)
		assert.True(t, errors.Is(err, publishError) || strings.Contains(err.Error(), publishError.Error()))
		mockRepo.AssertExpectations(t)
		mockPublisher.AssertExpectations(t)
	})
}

// TestGetStoryConfig tests the GetStoryConfig method
func TestGetStoryConfig(t *testing.T) {
	// userID := uint64(789)
	userID := uuid.New() // <<< Use UUID
	storyID := uuid.New()
	ctx := context.Background()

	existingConfig := &sharedModels.StoryConfig{ // <<< Используем sharedModels
		ID:     storyID,
		UserID: userID,
		Title:  "Test Title",
		Status: sharedModels.StatusDraft, // <<< Используем sharedModels
	}

	t.Run("Successful get", func(t *testing.T) {
		mockRepo := new(repositoryMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.TaskPublisher) // Publisher не используется в Get
		gameplayService := service.NewGameplayService(mockRepo, nil, nil, nil, nil, mockPublisher, nil, zap.NewNop())

		// Ожидаем GetByID
		mockRepo.On("GetByID", ctx, storyID, userID).Return(existingConfig, nil).Once()

		// Вызываем метод
		config, err := gameplayService.GetStoryConfig(ctx, storyID, userID)

		// Проверяем результат
		assert.NoError(t, err)
		assert.NotNil(t, config)
		assert.Equal(t, existingConfig, config)
		mockRepo.AssertExpectations(t)
	})

	t.Run("Config not found", func(t *testing.T) {
		mockRepo := new(repositoryMocks.StoryConfigRepository)
		mockPublisher := new(messagingMocks.TaskPublisher)
		gameplayService := service.NewGameplayService(mockRepo, nil, nil, nil, nil, mockPublisher, nil, zap.NewNop())
		notFoundError := errors.New("not found") // Можно использовать sharedModels.ErrNotFound

		// Ожидаем GetByID, возвращаем ошибку
		mockRepo.On("GetByID", ctx, storyID, userID).Return(nil, notFoundError).Once()

		// Вызываем метод
		config, err := gameplayService.GetStoryConfig(ctx, storyID, userID)

		// Проверяем результат
		assert.Error(t, err)
		assert.Nil(t, config)
		assert.True(t, errors.Is(err, notFoundError) || strings.Contains(err.Error(), notFoundError.Error()))
		mockRepo.AssertExpectations(t)
	})
}

// TODO: Добавить тесты для ReviseDraft // Эти TODO уже неактуальны?
// TODO: Добавить тесты для GetStoryConfig // Эти TODO уже неактуальны?
