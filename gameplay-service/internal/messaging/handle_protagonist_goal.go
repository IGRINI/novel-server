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
	sharedModels "novel-server/shared/models"
	"novel-server/shared/utils"
)

// protagonistGoalResultPayload - ожидаемая структура JSON ответа от AI для цели протагониста.
// Может содержать как саму цель, так и начальные элементы setup.
// Например: {"goal": "найти артефакт", "initial_setup_elements": {"key_item": "старая карта"}}
// ИСПРАВЛЕННАЯ СТРУКТУРА В СООТВЕТСТВИИ С protagonist_goal_prompt.md
type protagonistGoalResultPayload struct {
	Result string `json:"res"` // Промпт возвращает только это поле
}

func (p *NotificationProcessor) handleProtagonistGoalResult(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	log := p.logger.With(zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()), zap.String("prompt_type", string(notification.PromptType)))

	log.Info("Processing protagonist goal result")

	dbCtx, cancel := context.WithTimeout(ctx, 20*time.Second) // Увеличим таймаут для потенциально более сложной логики
	defer cancel()

	publishedStory, err := p.ensureStoryStatus(dbCtx, publishedStoryID, sharedModels.StatusProtagonistGoalPending)
	if err != nil {
		return err // Ошибка получения или несоответствие уже залогировано в ensureStoryStatus
	}
	if publishedStory == nil {
		return nil // Статус не совпал, ACK и выход
	}

	// Переменная для хранения payload следующей задачи
	var scenePlannerPayload *sharedMessaging.GenerationTaskPayload
	var processingError error // Для обработки ошибок и предотвращения запуска задачи

	if notification.Status == sharedMessaging.NotificationStatusError {
		// Ошибка протагониста — используем обёртку
		p.handleStoryError(ctx, publishedStoryID, notification.UserID, notification.ErrorDetails, constants.WSEventStoryError)
		return nil
	} else if notification.Status == sharedMessaging.NotificationStatusSuccess {
		log.Info("Protagonist goal task successful, processing results.")

		genResult, genErr := p.genResultRepo.GetByTaskID(dbCtx, taskID)
		if genErr != nil {
			errMsg := fmt.Sprintf("failed to fetch protagonist goal result: %v", genErr)
			p.handleStoryError(ctx, publishedStoryID, notification.UserID, errMsg, constants.WSEventStoryError)
			return nil
		} else if genResult.Error != "" {
			log.Warn("GenerationResult for protagonist goal indicates an error", zap.String("gen_error", genResult.Error))
			errDetails := fmt.Sprintf("protagonist goal generation error: %s", genResult.Error)
			p.handleStoryError(ctx, publishedStoryID, notification.UserID, errDetails, constants.WSEventStoryError)
			return nil
		} else {
			genResultText := genResult.GeneratedText
			// Строгая проверка и парсинг JSON цели протагониста
			goalOutcome, err := decodeStrictJSON[protagonistGoalResultPayload](genResultText)
			if err != nil {
				log.Error("Failed to parse protagonist goal result JSON", zap.Error(err), zap.String("json_text", genResultText))
				errMsg := fmt.Sprintf("failed to parse protagonist goal result: %v", err)
				p.handleStoryError(ctx, publishedStoryID, notification.UserID, errMsg, constants.WSEventStoryError)
				return nil
			}

			if processingError == nil && strings.TrimSpace(goalOutcome.Result) == "" {
				errMsg := "empty 'result' field in protagonist goal JSON"
				p.handleStoryError(ctx, publishedStoryID, notification.UserID, errMsg, constants.WSEventStoryError)
				return nil
			}

			if processingError == nil { // Продолжаем, только если ошибок не было
				currentSetup := make(map[string]interface{})
				if len(publishedStory.Setup) > 0 && string(publishedStory.Setup) != "null" && string(publishedStory.Setup) != "{}" {
					if err := json.Unmarshal(publishedStory.Setup, &currentSetup); err != nil {
						log.Error("Failed to unmarshal existing PublishedStory.Setup, will overwrite", zap.Error(err), zap.String("existing_setup", string(publishedStory.Setup)))
						currentSetup = make(map[string]interface{}) // Сбрасываем, если не можем распарсить
					}
				}
				// Обновляем или добавляем только цель протагониста
				currentSetup["protagonist_goal"] = goalOutcome.Result

				newSetupBytes, errMarshalSetup := json.Marshal(currentSetup)
				if errMarshalSetup != nil {
					log.Error("Failed to marshal updated Setup JSON for protagonist goal", zap.Error(errMarshalSetup))
					errMsg := fmt.Sprintf("failed to rebuild setup with protagonist goal: %v", errMarshalSetup)
					// Передаем nil для internalStep при ошибке
					if errUpdate := p.publishedRepo.UpdateAfterModeration(dbCtx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpdate != nil {
						log.Error("Failed to update PublishedStory to Error status after setup marshal failure", zap.Error(errUpdate))
						processingError = fmt.Errorf("failed to update story status to Error: %w", errUpdate)
					} else {
						processingError = errors.New(errMsg)
					}
				} else {
					publishedStory.Setup = newSetupBytes // Обновляем Setup для сохранения
					publishedStory.Status = sharedModels.StatusScenePlannerPending
					publishedStory.ErrorDetails = nil // Сбрасываем ошибки
					publishedStory.UpdatedAt = time.Now().UTC()

					// --- ФОРМИРОВАНИЕ PAYLOAD ДЛЯ СЛЕДУЮЩЕЙ ЗАДАЧИ ---
					scenePlannerTaskID := uuid.New().String()
					var finalConfigForTask sharedModels.Config
					if errCfg := json.Unmarshal(publishedStory.Config, &finalConfigForTask); errCfg != nil {
						log.Error("Failed to unmarshal publishedStory.Config for ScenePlanner task", zap.Error(errCfg), zap.String("config_json", string(publishedStory.Config)))
					}
					protagonistGoalString := goalOutcome.Result
					userInputForScenePlanner, errFormat := utils.FormatConfigAndGoalForScenePlanner(finalConfigForTask, protagonistGoalString, publishedStory.IsAdultContent)
					if errFormat != nil {
						log.Error("Failed to format UserInput for ScenePlanner task using FormatConfigAndGoalForScenePlanner", zap.Error(errFormat))
						errMsg := fmt.Sprintf("internal error preparing scene planner task input: %v", errFormat)
						// Передаем nil для internalStep при ошибке
						if errUpdate := p.publishedRepo.UpdateAfterModeration(dbCtx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpdate != nil {
							log.Error("Failed to update PublishedStory to Error status after UserInput formatting failure for scene planner", zap.Error(errUpdate))
						}
						processingError = errors.New(errMsg)
					} else {
						// Сохраняем готовый payload в переменную
						payload := sharedMessaging.GenerationTaskPayload{
							TaskID:           scenePlannerTaskID,
							UserID:           publishedStory.UserID.String(),
							PromptType:       sharedModels.PromptTypeScenePlanner,
							UserInput:        userInputForScenePlanner,
							PublishedStoryID: publishedStory.ID.String(),
							Language:         publishedStory.Language,
						}
						scenePlannerPayload = &payload // Устанавливаем указатель на payload
					}
					// --- КОНЕЦ ФОРМИРОВАНИЯ PAYLOAD ---
				}
			}
		}
	} else {
		log.Warn("Unknown notification status for ProtagonistGoal. Ignoring.", zap.String("status", string(notification.Status)))
		return nil // Ack
	}

	// Если на каком-то этапе возникла ошибка, НЕ обновляем статус и НЕ запускаем задачу
	if processingError != nil {
		log.Error("Error occurred during protagonist goal processing, final DB update and task publish aborted.", zap.Error(processingError))
		// Возвращаем nil, так как ошибка уже обработана (статус Error установлен)
		// Если обновление статуса на Error само по себе упало, то processingError будет содержать ту ошибку,
		// и мы должны вернуть её, чтобы NACK-нуть сообщение.
		if strings.Contains(processingError.Error(), "failed to update story status to Error") {
			return processingError // NACK
		}
		return nil // ACK, т.к. статус Error уже установлен
	}

	// === Финальное обновление статуса, Setup и InternalStep ===
	nextStep := sharedModels.StepScenePlanner
	if errUpdate := p.publishedRepo.UpdateStatusFlagsAndSetup(dbCtx, p.db, publishedStory.ID, publishedStory.Status, publishedStory.Setup, publishedStory.IsFirstScenePending, publishedStory.AreImagesPending, &nextStep); errUpdate != nil {
		log.Error("FINAL DB ERROR: Failed to update PublishedStory after protagonist goal processing", zap.Error(errUpdate))
		// Устанавливаем статус Error и уведомляем клиента
		errMsg := fmt.Sprintf("failed to update story after protagonist goal: %v", errUpdate)
		p.handleStoryError(ctx, publishedStoryID, notification.UserID, errMsg, constants.WSEventStoryError)
		return fmt.Errorf("failed to update story after protagonist goal: %w", errUpdate) // NACK после уведомления
	}
	log.Info("PublishedStory updated after protagonist goal processing", zap.String("new_status", string(publishedStory.Status)), zap.Any("internal_step", nextStep))

	// === Публикация следующей задачи (только если payload был успешно создан) ===
	if scenePlannerPayload != nil {
		taskCtx, taskCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer taskCancel()
		if errPub := p.taskPub.PublishGenerationTask(taskCtx, *scenePlannerPayload); errPub != nil {
			p.logger.Error("CRITICAL: Failed to publish scene planner task AFTER DB COMMIT", zap.Error(errPub), zap.String("task_id", scenePlannerPayload.TaskID))
			// Ошибка после коммита. Требуется мониторинг.
			// Не возвращаем ошибку, чтобы не NACK-нуть успешно обработанное сообщение.
		} else {
			p.logger.Info("Scene planner task published successfully AFTER DB COMMIT", zap.String("task_id", scenePlannerPayload.TaskID))
		}
	} else {
		log.Warn("Scene planner task payload was not generated, task not published.")
	}

	// Уведомляем клиента об обновлении статуса истории после обработки цели протагониста
	if uid, perr := parseUUIDField(notification.UserID, "UserID"); perr == nil {
		p.notifyClient(publishedStory.ID, uid, sharedModels.UpdateTypeStory, string(publishedStory.Status), nil)
	}

	return nil // Ack
}
