package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"novel-server/shared/constants"
	sharedMessaging "novel-server/shared/messaging"
	"novel-server/shared/models"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/utils"
	// "novel-server/shared/utils" // Пока не используется, но может понадобиться
)

// BoolFromInt для кастомного анмаршалинга bool из int (0 или 1)
// или из стандартного bool (true/false)
// или из строки ("0", "1", "true", "false")
type BoolFromInt bool

// UnmarshalJSON реализует кастомную логику для BoolFromInt
func (b *BoolFromInt) UnmarshalJSON(data []byte) error {
	// Попробуем как стандартный bool
	var standardBool bool
	if err := json.Unmarshal(data, &standardBool); err == nil {
		*b = BoolFromInt(standardBool)
		return nil
	}

	// Попробуем как число (int)
	var num_i int
	if err_i := json.Unmarshal(data, &num_i); err_i == nil {
		*b = BoolFromInt(num_i != 0)
		return nil
	}

	// Попробуем как число (float64, так как json.Unmarshal по умолчанию читает числа в float64)
	var num_f float64
	if err_f := json.Unmarshal(data, &num_f); err_f == nil {
		*b = BoolFromInt(num_f != 0)
		return nil
	}

	// Попробуем как строку и сконвертировать
	var str string
	if err_str := json.Unmarshal(data, &str); err_str == nil {
		switch str {
		case "1", "true", "TRUE":
			*b = true
			return nil
		case "0", "false", "FALSE":
			*b = false
			return nil
		}
	}

	return fmt.Errorf("cannot unmarshal %s into BoolFromInt (tried bool, number, and string representations '0','1','true','false')", string(data))
}

// moderationResultPayload - ожидаемая структура JSON ответа от AI для модерации
// Это внутреннее определение, т.к. структура специфична для этого шага.
type moderationResultPayload struct {
	IsAdult BoolFromInt `json:"ac"` // Используем кастомный тип
	Reasons []string    `json:"reasons,omitempty"`
	// Можно добавить другие поля, если AI их возвращает, например, confidence score
}

func (p *NotificationProcessor) handleContentModerationResult(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	log := p.logger.With(zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()), zap.String("prompt_type", string(notification.PromptType)))

	log.Info("Processing content moderation result")

	dbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	publishedStory, err := p.publishedRepo.GetByID(dbCtx, p.db, publishedStoryID)
	if err != nil {
		log.Error("Failed to get PublishedStory for moderation update", zap.Error(err))
		return fmt.Errorf("error getting PublishedStory %s: %w", publishedStoryID, err)
	}

	if publishedStory.Status != sharedModels.StatusModerationPending {
		log.Warn("PublishedStory not in ModerationPending status, moderation update skipped.", zap.String("current_status", string(publishedStory.Status)))
		return nil // Это может быть дублирующее или запоздавшее сообщение
	}

	var nextTaskPayload *sharedMessaging.GenerationTaskPayload
	var processingError error

	if notification.Status == sharedMessaging.NotificationStatusError {
		// Ошибка модерации контента
		p.handleStoryError(ctx, publishedStoryID, notification.UserID, notification.ErrorDetails, constants.WSEventStoryError)
		return nil
	} else if notification.Status == sharedMessaging.NotificationStatusSuccess {
		log.Info("Content moderation task successful, processing results.")

		var genResultText string
		genResult, genErr := p.genResultRepo.GetByTaskID(dbCtx, taskID)
		if genErr != nil {
			log.Error("Failed to get GenerationResult by TaskID for moderation", zap.Error(genErr))
			errMsg := fmt.Sprintf("failed to fetch moderation result: %v", genErr)
			p.handleStoryError(ctx, publishedStoryID, notification.UserID, errMsg, constants.WSEventStoryError)
			return nil
		} else if genResult.Error != "" {
			log.Warn("GenerationResult for moderation indicates an error", zap.String("gen_error", genResult.Error))
			errDetails := fmt.Sprintf("moderation result error: %s", genResult.Error)
			p.handleStoryError(ctx, publishedStoryID, notification.UserID, errDetails, constants.WSEventStoryError)
			return nil
		} else {
			genResultText = genResult.GeneratedText
			// Строгая проверка и парсинг JSON результата модерации
			moderationOutcome, err := decodeStrictJSON[moderationResultPayload](genResultText)
			if err != nil {
				log.Error("Failed to unmarshal moderation result JSON", zap.Error(err), zap.String("json_text", genResultText))
				errMsg := fmt.Sprintf("failed to parse moderation result: %v", err)
				p.handleStoryError(ctx, publishedStoryID, notification.UserID, errMsg, constants.WSEventStoryError)
				return nil
			} else {
				publishedStory.IsAdultContent = bool(moderationOutcome.IsAdult) // Преобразуем обратно в bool
				publishedStory.Status = sharedModels.StatusProtagonistGoalPending
				publishedStory.ErrorDetails = nil // Сбрасываем предыдущие ошибки, если были
				publishedStory.UpdatedAt = time.Now().UTC()
				// Устанавливаем следующий шаг (больше не нужно, определяется при финальном обновлении)
				// nextStep := sharedModels.StepProtagonistGoal

				// Формируем payload для следующей задачи
				protagonistGoalTaskID := uuid.New().String()
				var formattedUserInput string
				var errFormat error

				if publishedStory.Config != nil {
					var configStruct models.Config
					if errUnmarshal := json.Unmarshal(publishedStory.Config, &configStruct); errUnmarshal != nil {
						log.Error("Failed to unmarshal publishedStory.Config for goal prompt formatting", zap.Error(errUnmarshal))
						errFormat = fmt.Errorf("failed to unmarshal config: %w", errUnmarshal)
						// configContentForGoal останется пустой строкой
					} else {
						// Теперь форматируем с помощью правильной функции
						formattedUserInput = utils.FormatConfigForGoalPrompt(configStruct, publishedStory.IsAdultContent)
					}
				}

				// Проверяем, удалось ли получить/отформатировать ввод
				if formattedUserInput == "" || errFormat != nil {
					log.Error("CRITICAL: Cannot create protagonist goal task because UserInput could not be formatted",
						zap.String("publishedStoryID", publishedStoryID.String()),
						zap.Error(errFormat))
					errMsg := "Cannot create protagonist goal task: failed to format config"
					if errFormat != nil {
						errMsg = fmt.Sprintf("%s (%v)", errMsg, errFormat)
					}
					// Передаем nil для internalStep при ошибке
					if errUpdate := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtx, p.db, publishedStoryID, sharedModels.StatusError, false, false, &errMsg, nil); errUpdate != nil {
						log.Error("Failed to update story status to Error due to empty/invalid config for goal task", zap.Error(errUpdate))
					}
					processingError = errors.New(errMsg)
				} else {
					payload := sharedMessaging.GenerationTaskPayload{
						TaskID:           protagonistGoalTaskID,
						UserID:           publishedStory.UserID.String(),
						PromptType:       sharedModels.PromptTypeProtagonistGoal,
						UserInput:        formattedUserInput,
						PublishedStoryID: publishedStory.ID.String(),
						Language:         publishedStory.Language,
					}
					nextTaskPayload = &payload
				}
				// Уведомляем клиента об успешной публикации предыдущего шага
				if _, perr2 := parseUUIDField(notification.UserID, "UserID"); perr2 == nil {
					p.publishClientStoryUpdateOnReady(publishedStory)
				}
			}
		}
	} else {
		log.Warn("Unknown notification status for ContentModeration. Ignoring.", zap.String("status", string(notification.Status)))
		return nil // Ack
	}

	if processingError != nil {
		log.Error("Error occurred during content moderation processing, final DB update and task publish aborted.", zap.Error(processingError))
		if strings.Contains(processingError.Error(), "failed to update story status to Error") {
			return processingError // NACK
		}
		return nil // ACK, т.к. статус Error уже установлен
	}

	// === Финальное обновление статуса, IsAdultContent и InternalStep ===
	var errorDetailsForUpdate *string
	if publishedStory.ErrorDetails != nil && *publishedStory.ErrorDetails != "" {
		errorDetailsForUpdate = publishedStory.ErrorDetails
	}
	// Определяем следующий шаг в зависимости от статуса
	var finalInternalStep *sharedModels.InternalGenerationStep
	if publishedStory.Status == sharedModels.StatusProtagonistGoalPending {
		step := sharedModels.StepProtagonistGoal
		finalInternalStep = &step
	} else if publishedStory.Status == sharedModels.StatusError {
		// Если статус Error, шаг не меняем (оставляем nil или предыдущее значение, если бы мы его читали)
		finalInternalStep = nil // Явно устанавливаем nil
	}
	// Используем обновленный UpdateAfterModeration
	if errUpdate := p.publishedRepo.UpdateAfterModeration(dbCtx, p.db, publishedStory.ID, publishedStory.Status, publishedStory.IsAdultContent, errorDetailsForUpdate, finalInternalStep); errUpdate != nil {
		log.Error("FINAL DB ERROR: Failed to update PublishedStory after moderation", zap.Error(errUpdate))
		return fmt.Errorf("failed to update story after moderation: %w", errUpdate) // NACK
	}
	log.Info("PublishedStory updated after moderation", zap.Bool("is_adult", publishedStory.IsAdultContent), zap.String("new_status", string(publishedStory.Status)), zap.Any("internal_step", finalInternalStep))

	// === Публикация следующей задачи (только если payload был успешно создан) ===
	if nextTaskPayload != nil {
		taskCtx, taskCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer taskCancel()
		if errPub := p.taskPub.PublishGenerationTask(taskCtx, *nextTaskPayload); errPub != nil {
			p.logger.Error("CRITICAL: Failed to publish protagonist goal task AFTER DB COMMIT",
				zap.String("publishedStoryID", nextTaskPayload.PublishedStoryID),
				zap.String("taskID", nextTaskPayload.TaskID),
				zap.Error(errPub))
			// Ошибка после коммита. Требуется мониторинг.
		} else {
			p.logger.Info("Protagonist goal task published successfully AFTER DB COMMIT",
				zap.String("publishedStoryID", nextTaskPayload.PublishedStoryID),
				zap.String("taskID", nextTaskPayload.TaskID))
		}
	} else {
		log.Warn("Protagonist goal task payload was not generated (e.g. due to empty config), task not published.")
	}

	return nil // Ack
}
