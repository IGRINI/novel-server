package messaging

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	interfaces "novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models"
)

// GenerationStepManager управляет логикой определения следующих шагов генерации
// и обеспечивает атомарные операции обновления состояния
type GenerationStepManager struct {
	publishedRepo interfaces.PublishedStoryRepository
	logger        *zap.Logger
}

// NewGenerationStepManager создает новый менеджер шагов генерации
func NewGenerationStepManager(
	publishedRepo interfaces.PublishedStoryRepository,
	logger *zap.Logger,
) *GenerationStepManager {
	return &GenerationStepManager{
		publishedRepo: publishedRepo,
		logger:        logger,
	}
}

// DetermineNextStep определяет следующий шаг генерации на основе текущего состояния истории
func (m *GenerationStepManager) DetermineNextStep(story *sharedModels.PublishedStory) *sharedModels.InternalGenerationStep {
	// Проверяем задачи генерации персонажей
	if story.PendingCharGenTasks > 0 {
		step := sharedModels.StepCharacterGeneration
		return &step
	}

	// Проверяем задачи генерации изображений карт
	if story.PendingCardImgTasks > 0 {
		step := sharedModels.StepCardImageGeneration
		return &step
	}

	// Проверяем задачи генерации изображений персонажей
	if story.PendingCharImgTasks > 0 {
		step := sharedModels.StepCharacterImageGeneration
		return &step
	}

	// Если нет ожидающих задач, переходим к генерации setup
	step := sharedModels.StepSetupGeneration
	return &step
}

// DetermineStatusFromStep определяет статус истории на основе шага генерации
func (gsm *GenerationStepManager) DetermineStatusFromStep(step *sharedModels.InternalGenerationStep) sharedModels.StoryStatus {
	if step == nil {
		return sharedModels.StatusReady // Финальное состояние
	}

	switch *step {
	case sharedModels.StepModeration:
		return sharedModels.StatusModerationPending
	case sharedModels.StepProtagonistGoal:
		return sharedModels.StatusProtagonistGoalPending
	case sharedModels.StepScenePlanner:
		return sharedModels.StatusScenePlannerPending
	case sharedModels.StepCharacterGeneration:
		return sharedModels.StatusSubTasksPending
	case sharedModels.StepCardImageGeneration:
		return sharedModels.StatusImageGenerationPending
	case sharedModels.StepSetupGeneration:
		return sharedModels.StatusSetupPending
	case sharedModels.StepCoverImageGeneration:
		return sharedModels.StatusImageGenerationPending
	case sharedModels.StepCharacterImageGeneration:
		return sharedModels.StatusImageGenerationPending
	case sharedModels.StepInitialSceneJSON:
		return sharedModels.StatusJsonGenerationPending
	case sharedModels.StepComplete:
		return sharedModels.StatusReady
	default:
		gsm.logger.Warn("Unknown generation step, defaulting to Ready status",
			zap.Any("step", step))
		return sharedModels.StatusReady
	}
}

// AtomicUpdateStepAndStatus атомарно обновляет шаг и статус истории с использованием SELECT FOR UPDATE
func (m *GenerationStepManager) AtomicUpdateStepAndStatus(
	ctx context.Context,
	tx interfaces.DBTX,
	storyID uuid.UUID,
	expectedCurrentStep *sharedModels.InternalGenerationStep,
	newStep *sharedModels.InternalGenerationStep,
	newStatus sharedModels.StoryStatus,
) (*sharedModels.PublishedStory, error) {
	// Получаем текущее состояние с блокировкой
	story, err := m.getStoryForUpdate(ctx, tx, storyID)
	if err != nil {
		return nil, fmt.Errorf("failed to get story for update: %w", err)
	}

	// Проверяем, что текущий шаг соответствует ожидаемому
	if !m.stepsEqual(story.InternalGenerationStep, expectedCurrentStep) {
		return nil, fmt.Errorf("step mismatch: expected %v, got %v",
			expectedCurrentStep, story.InternalGenerationStep)
	}

	// Обновляем шаг и статус
	err = m.publishedRepo.UpdateStatusFlagsAndDetails(ctx, tx, storyID, newStatus,
		story.IsFirstScenePending, story.AreImagesPending,
		story.PendingCharGenTasks, story.PendingCardImgTasks, story.PendingCharImgTasks,
		nil, newStep)
	if err != nil {
		return nil, fmt.Errorf("failed to update status and step: %w", err)
	}

	// Возвращаем обновленную историю
	story.Status = newStatus
	story.InternalGenerationStep = newStep
	return story, nil
}

// AtomicDecrementCounters атомарно декрементирует счетчики и обновляет статус
func (m *GenerationStepManager) AtomicDecrementCounters(
	ctx context.Context,
	tx interfaces.DBTX,
	storyID uuid.UUID,
	decCardImg, decCharImg int,
) (*sharedModels.PublishedStory, error) {
	// Получаем текущее состояние с блокировкой
	story, err := m.getStoryForUpdate(ctx, tx, storyID)
	if err != nil {
		return nil, fmt.Errorf("failed to get story for update: %w", err)
	}

	// Вычисляем новые значения счетчиков
	newCardImgTasks := story.PendingCardImgTasks - decCardImg
	newCharImgTasks := story.PendingCharImgTasks - decCharImg

	// Защита от отрицательных значений
	if newCardImgTasks < 0 {
		newCardImgTasks = 0
	}
	if newCharImgTasks < 0 {
		newCharImgTasks = 0
	}

	// Определяем новый статус и шаг
	story.PendingCardImgTasks = newCardImgTasks
	story.PendingCharImgTasks = newCharImgTasks

	nextStep := m.DetermineNextStep(story)
	newStatus := m.DetermineStatusFromStep(nextStep)

	// Обновляем флаг изображений
	areImagesPending := newCardImgTasks > 0 || newCharImgTasks > 0

	// Обновляем в БД
	err = m.publishedRepo.UpdateStatusFlagsAndDetails(ctx, tx, storyID, newStatus,
		story.IsFirstScenePending, areImagesPending,
		story.PendingCharGenTasks, newCardImgTasks, newCharImgTasks,
		nil, nextStep)
	if err != nil {
		return nil, fmt.Errorf("failed to update counters: %w", err)
	}

	// Возвращаем обновленную историю
	story.Status = newStatus
	story.InternalGenerationStep = nextStep
	story.AreImagesPending = areImagesPending
	return story, nil
}

// getStoryForUpdate получает историю с блокировкой SELECT FOR UPDATE
func (m *GenerationStepManager) getStoryForUpdate(
	ctx context.Context,
	tx interfaces.DBTX,
	storyID uuid.UUID,
) (*sharedModels.PublishedStory, error) {
	query := `
		SELECT id, user_id, config, setup, status, internal_generation_step, language,
		       is_public, is_adult_content, title, description, error_details,
		       likes_count, created_at, updated_at, is_first_scene_pending,
		       are_images_pending, pending_char_gen_tasks, pending_card_img_tasks,
		       pending_char_img_tasks
		FROM published_stories 
		WHERE id = $1 
		FOR UPDATE`

	var story sharedModels.PublishedStory
	err := tx.QueryRow(ctx, query, storyID).Scan(
		&story.ID, &story.UserID, &story.Config, &story.Setup, &story.Status,
		&story.InternalGenerationStep, &story.Language, &story.IsPublic,
		&story.IsAdultContent, &story.Title, &story.Description, &story.ErrorDetails,
		&story.LikesCount, &story.CreatedAt, &story.UpdatedAt, &story.IsFirstScenePending,
		&story.AreImagesPending, &story.PendingCharGenTasks, &story.PendingCardImgTasks,
		&story.PendingCharImgTasks,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("story not found: %s", storyID)
		}
		return nil, err
	}

	return &story, nil
}

// stepsEqual сравнивает два шага генерации (учитывая nil)
func (m *GenerationStepManager) stepsEqual(a, b *sharedModels.InternalGenerationStep) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// ValidateStepTransition проверяет, что переход между шагами валиден
func (m *GenerationStepManager) ValidateStepTransition(
	from, to *sharedModels.InternalGenerationStep,
) error {
	// Если переходим в complete, это всегда валидно
	if to != nil && *to == sharedModels.StepComplete {
		return nil
	}

	// Если from == nil, то это начальное состояние
	if from == nil {
		return nil
	}

	// Определяем валидные переходы
	validTransitions := map[sharedModels.InternalGenerationStep][]sharedModels.InternalGenerationStep{
		sharedModels.StepProtagonistGoal: {
			sharedModels.StepScenePlanner,
		},
		sharedModels.StepScenePlanner: {
			sharedModels.StepCharacterGeneration,
			sharedModels.StepCardImageGeneration,
			sharedModels.StepSetupGeneration,
		},
		sharedModels.StepCharacterGeneration: {
			sharedModels.StepCardImageGeneration,
			sharedModels.StepCharacterImageGeneration,
			sharedModels.StepSetupGeneration,
		},
		sharedModels.StepCardImageGeneration: {
			sharedModels.StepCharacterImageGeneration,
			sharedModels.StepSetupGeneration,
		},
		sharedModels.StepCharacterImageGeneration: {
			sharedModels.StepSetupGeneration,
		},
		sharedModels.StepSetupGeneration: {
			sharedModels.StepInitialSceneJSON,
		},
		sharedModels.StepInitialSceneJSON: {
			sharedModels.StepComplete,
		},
	}

	if to == nil {
		return nil // Переход в nil (complete) всегда валиден
	}

	validNext, exists := validTransitions[*from]
	if !exists {
		return fmt.Errorf("no valid transitions from step %s", *from)
	}

	for _, valid := range validNext {
		if *to == valid {
			return nil
		}
	}

	return fmt.Errorf("invalid transition from %s to %s", *from, *to)
}
