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

	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/utils"
)

// protagonistGoalResultPayload - ожидаемая структура JSON ответа от AI для цели протагониста.
// Может содержать как саму цель, так и начальные элементы setup.
// Например: {"goal": "найти артефакт", "initial_setup_elements": {"key_item": "старая карта"}}
// ИСПРАВЛЕННАЯ СТРУКТУРА В СООТВЕТСТВИИ С protagonist_goal_prompt.md
type protagonistGoalResultPayload struct {
	Result string `json:"result"` // Промпт возвращает только это поле
}

func (p *NotificationProcessor) handleProtagonistGoalResult(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	log := p.logger.With(zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()), zap.String("prompt_type", string(notification.PromptType)))

	log.Info("Processing protagonist goal result")

	dbCtx, cancel := context.WithTimeout(ctx, 20*time.Second) // Увеличим таймаут для потенциально более сложной логики
	defer cancel()

	publishedStory, err := p.publishedRepo.GetByID(dbCtx, p.db, publishedStoryID)
	if err != nil {
		log.Error("Failed to get PublishedStory for protagonist goal update", zap.Error(err))
		return fmt.Errorf("error getting PublishedStory %s: %w", publishedStoryID, err)
	}

	if publishedStory.Status != sharedModels.StatusProtagonistGoalPending {
		log.Warn("PublishedStory not in ProtagonistGoalPending status, update skipped.", zap.String("current_status", string(publishedStory.Status)))
		return nil // Дублирующее или запоздавшее сообщение
	}

	// Переменная для хранения payload следующей задачи
	var scenePlannerPayload *sharedMessaging.GenerationTaskPayload
	var processingError error // Для обработки ошибок и предотвращения запуска задачи

	if notification.Status == sharedMessaging.NotificationStatusError {
		log.Warn("Protagonist goal task failed", zap.String("error_details", notification.ErrorDetails))
		// Используем UpdateAfterModeration, так как он уже умеет обновлять статус и ошибку.
		// IsAdultContent здесь не меняется, передаем текущее значение.
		// Передаем nil для internalStep при ошибке
		if errUpdate := p.publishedRepo.UpdateAfterModeration(dbCtx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &notification.ErrorDetails, nil); errUpdate != nil {
			log.Error("Failed to update PublishedStory to Error status after protagonist goal failure", zap.Error(errUpdate))
			processingError = fmt.Errorf("failed to update story status to Error: %w", errUpdate) // Сохраняем ошибку
		} else {
			log.Info("PublishedStory status updated to Error due to protagonist goal failure.")
			// Уведомляем клиента об ошибке протагониста
			if uid, errUID := uuid.Parse(notification.UserID); errUID == nil {
				p.publishClientStoryUpdateOnError(publishedStoryID, uid, notification.ErrorDetails)
			}
		}
	} else if notification.Status == sharedMessaging.NotificationStatusSuccess {
		log.Info("Protagonist goal task successful, processing results.")

		genResult, genErr := p.genResultRepo.GetByTaskID(dbCtx, taskID)
		if genErr != nil {
			log.Error("Failed to get GenerationResult by TaskID for protagonist goal", zap.Error(genErr))
			errMsg := fmt.Sprintf("failed to fetch protagonist goal result from gen_results: %v", genErr)
			// Передаем nil для internalStep при ошибке
			if errUpdate := p.publishedRepo.UpdateAfterModeration(dbCtx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpdate != nil {
				log.Error("Failed to update PublishedStory to Error status after failing to fetch gen_result", zap.Error(errUpdate))
				processingError = fmt.Errorf("failed to update story status to Error: %w", errUpdate)
			} else {
				// Уведомляем клиента об ошибке протагониста
				if uid, errUID := uuid.Parse(notification.UserID); errUID == nil {
					p.publishClientStoryUpdateOnError(publishedStoryID, uid, errMsg)
				}
			}
		} else if genResult.Error != "" {
			log.Warn("GenerationResult for protagonist goal indicates an error", zap.String("gen_error", genResult.Error))
			// Передаем nil для internalStep при ошибке
			if errUpdate := p.publishedRepo.UpdateAfterModeration(dbCtx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &genResult.Error, nil); errUpdate != nil {
				log.Error("Failed to update PublishedStory to Error status due to gen_result error", zap.Error(errUpdate))
				processingError = fmt.Errorf("failed to update story status to Error: %w", errUpdate)
			} else {
				// Уведомляем клиента об ошибке протагониста
				if uid, errUID := uuid.Parse(notification.UserID); errUID == nil {
					p.publishClientStoryUpdateOnError(publishedStoryID, uid, genResult.Error)
				}
			}
		} else {
			genResultText := genResult.GeneratedText
			var goalOutcome protagonistGoalResultPayload
			if err := utils.DecodeStrict([]byte(genResultText), &goalOutcome); err != nil {
				log.Error("Failed to unmarshal protagonist goal result JSON", zap.Error(err), zap.String("json_text", genResultText))
				var raw map[string]interface{}
				if errMap := json.Unmarshal([]byte(genResultText), &raw); errMap == nil {
					if res, ok := raw["result"].(string); ok {
						goalOutcome.Result = res
						log.Warn("Использован fallback при разборе protagonist goal", zap.String("result", res))
					} else {
						errMsg := fmt.Sprintf("fallback parsing failed: поле 'result' отсутствует или не строка: %v", raw)
						// Передаем nil для internalStep при ошибке
						if errUpd := p.publishedRepo.UpdateAfterModeration(dbCtx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpd != nil {
							log.Error("Failed to update story status to Error after fallback parse failure", zap.Error(errUpd))
						}
						processingError = errors.New(errMsg) // Устанавливаем ошибку
					}
				} else {
					errMsg := fmt.Sprintf("failed to parse protagonist goal result: %v", err)
					// Передаем nil для internalStep при ошибке
					if errUpd := p.publishedRepo.UpdateAfterModeration(dbCtx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpd != nil {
						log.Error("Failed to update story status to Error after fallback parse failure", zap.Error(errUpd))
					}
					processingError = errors.New(errMsg) // Устанавливаем ошибку
				}
			}

			if processingError == nil && strings.TrimSpace(goalOutcome.Result) == "" {
				errMsg := "empty 'result' field in protagonist goal JSON"
				// Передаем nil для internalStep при ошибке
				if errUpd := p.publishedRepo.UpdateAfterModeration(dbCtx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpd != nil {
					log.Error("Failed to update story status to Error due to empty result field", zap.Error(errUpd))
				}
				processingError = errors.New(errMsg) // Устанавливаем ошибку
			}

			if processingError == nil { // Продолжаем, только если ошибок не было
				currentSetup := make(map[string]interface{})
				if len(publishedStory.Setup) > 0 && string(publishedStory.Setup) != "null" && string(publishedStory.Setup) != "{}" {
					if err := json.Unmarshal(publishedStory.Setup, &currentSetup); err != nil {
						log.Error("Failed to unmarshal existing PublishedStory.Setup, will overwrite", zap.Error(err), zap.String("existing_setup", string(publishedStory.Setup)))
						currentSetup = make(map[string]interface{}) // Сбрасываем, если не можем распарсить
					}
				}
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
					publishedStory.Setup = newSetupBytes
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
		return fmt.Errorf("failed to update story after protagonist goal: %w", errUpdate) // NACK
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

	// Отправляем клиенту уведомление об успешном обновлении истории после цели протагониста
	p.publishClientStoryUpdateOnReady(publishedStory)

	return nil // Ack
}
