package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"novel-server/shared/constants"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"

	"github.com/google/uuid"
	// "novel-server/shared/utils" // Может понадобиться позже
)

// publishTask публикует задачу генерации и возвращает ошибку при неудаче
func (p *NotificationProcessor) publishTask(payload sharedMessaging.GenerationTaskPayload) error {
	if payload.UserInput == "" {
		p.logger.Error("CRITICAL: Cannot publish task because UserInput is empty after formatting",
			zap.String("publishedStoryID", payload.PublishedStoryID),
			zap.String("taskID", payload.TaskID),
			zap.String("promptType", string(payload.PromptType)))
		return fmt.Errorf("empty UserInput for TaskID %s", payload.TaskID)
	}
	taskCtx, taskCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer taskCancel()
	if errPub := p.taskPub.PublishGenerationTask(taskCtx, payload); errPub != nil {
		p.logger.Error("CRITICAL: Failed to publish task", zap.Error(errPub),
			zap.String("task_id", payload.TaskID),
			zap.String("promptType", string(payload.PromptType)))
		return fmt.Errorf("failed to publish task %s: %w", payload.TaskID, errPub)
	}
	p.logger.Info("Task published successfully", zap.String("task_id", payload.TaskID), zap.String("promptType", string(payload.PromptType)))
	return nil
}

// Handler for CharacterGeneration result is already registered in NotificationProcessor.

// handleCharacterGenerationResult обрабатывает результат задачи генерации персонажей.
// TODO: добавить реальную логику обработки.
func (p *NotificationProcessor) handleCharacterGenerationResult(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	log := p.logger.With(zap.String("task_id", notification.TaskID), zap.String("published_story_id", publishedStoryID.String()), zap.String("prompt_type", string(notification.PromptType)))
	log.Info("Processing CharacterGeneration result")

	dbCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	// Загружаем историю для доступа к флагу IsAdultContent и проверки статуса
	publishedStory, err := p.publishedRepo.GetByID(dbCtx, p.db, publishedStoryID)
	if err != nil {
		log.Error("Failed to get PublishedStory for character generation", zap.Error(err))
		return fmt.Errorf("error getting PublishedStory %s: %w", publishedStoryID, err)
	}

	// <<< НАЧАЛО: Проверка статуса >>>
	if publishedStory.Status != sharedModels.StatusSubTasksPending || !publishedStory.PendingCharGenTasks {
		errMsg := fmt.Sprintf("CRITICAL: CharacterGeneration result received for story %s with unexpected status/flag. Current status: %s, PendingCharGenTasks: %t. Expected: StatusSubTasksPending and PendingCharGenTasks=true.",
			publishedStoryID.String(),
			string(publishedStory.Status),
			publishedStory.PendingCharGenTasks,
		)
		log.Error(errMsg)

		// Немедленно устанавливаем статус Error для истории
		// Используем UpdateAfterModeration как универсальный метод для установки статуса и ошибки
		// Передаем isAdultContent как есть, и nil для internalStep, так как на этом этапе он не важен
		if errUpd := p.publishedRepo.UpdateAfterModeration(dbCtx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpd != nil {
			log.Error("Failed to update story to Error status due to unexpected state during character generation", zap.Error(errUpd))
			// Если не удалось обновить статус в БД, Nack-аем сообщение, чтобы оно было переобработано
			return fmt.Errorf("failed to update story %s to error: %w", publishedStoryID, errUpd)
		}

		// Уведомляем клиента об ошибке истории
		p.publishClientStoryUpdateOnError(publishedStoryID, publishedStory.UserID, errMsg)
		// Отправляем push-уведомление об ошибке
		p.publishPushOnError(ctx, publishedStoryID, publishedStory.UserID, "Unexpected state during character generation.", constants.PushEventTypeStoryError)

		return nil // Ack, так как статус в БД обновлен (или попытка была сделана)
	}
	// <<< КОНЕЦ: Проверка статуса >>>

	// Получаем результат генерации
	genResult, err := p.genResultRepo.GetByTaskID(dbCtx, notification.TaskID)
	if err != nil {
		log.Error("Failed to get GenerationResult for character generation", zap.Error(err))
		return fmt.Errorf("failed to fetch generation result: %w", err)
	}
	if genResult.Error != "" {
		log.Warn("GenerationResult indicates error for character generation", zap.String("gen_error", genResult.Error))
		// Обновляем статус истории на ошибку
		if errUpd := p.publishedRepo.UpdateAfterModeration(ctx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &genResult.Error, nil); errUpd != nil {
			log.Error("Failed to update story to Error status after character generation failure", zap.Error(errUpd))
		}
		return nil // Ack
	}

	raw := genResult.GeneratedText
	// Валидация JSON
	if !json.Valid([]byte(raw)) {
		errMsg := fmt.Sprintf("invalid JSON for CharacterGeneration: %s", raw)
		log.Error(errMsg)
		if errUpd := p.publishedRepo.UpdateAfterModeration(ctx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpd != nil {
			log.Error("Failed to update story status to Error after invalid JSON", zap.Error(errUpd))
		}
		return nil // Ack
	}

	var chars []map[string]interface{}
	if errUn := json.Unmarshal([]byte(raw), &chars); errUn != nil {
		errMsg := fmt.Sprintf("failed to unmarshal CharacterGeneration JSON: %v", errUn)
		log.Error(errMsg)
		if errUpd := p.publishedRepo.UpdateAfterModeration(ctx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpd != nil {
			log.Error("Failed to update story status to Error after unmarshal failure", zap.Error(errUpd))
		}
		return nil // Ack
	}
	if len(chars) == 0 {
		errMsg := "CharacterGeneration JSON array is empty"
		log.Error(errMsg)
		if errUpd := p.publishedRepo.UpdateAfterModeration(ctx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpd != nil {
			log.Error("Failed to update story status to Error after empty characters", zap.Error(errUpd))
		}
		return nil // Ack
	}

	// Дополнительная проверка обязательных полей для каждого персонажа
	for i, c := range chars {
		if id, ok := c["id"].(string); !ok || strings.TrimSpace(id) == "" {
			errMsg := fmt.Sprintf("missing or invalid 'id' field in character at index %d", i)
			log.Error(errMsg)
			if errUpd := p.publishedRepo.UpdateAfterModeration(ctx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpd != nil {
				log.Error("Failed to update story status to Error after missing id", zap.Error(errUpd))
			}
			return nil // Ack
		}
		// Проверка остальных обязательных полей
		if name, ok := c["name"].(string); !ok || strings.TrimSpace(name) == "" {
			errMsg := fmt.Sprintf("missing or invalid 'name' field in character at index %d", i)
			log.Error(errMsg)
			if errUpd := p.publishedRepo.UpdateAfterModeration(ctx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpd != nil {
				log.Error("Failed to update story status to Error after missing name", zap.Error(errUpd))
			}
			return nil // Ack
		}
		if role, ok := c["role"].(string); !ok || strings.TrimSpace(role) == "" {
			errMsg := fmt.Sprintf("missing or invalid 'role' field in character at index %d", i)
			log.Error(errMsg)
			if errUpd := p.publishedRepo.UpdateAfterModeration(ctx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpd != nil {
				log.Error("Failed to update story status to Error after missing role", zap.Error(errUpd))
			}
			return nil // Ack
		}
		if traits, ok := c["traits"].(string); !ok || strings.TrimSpace(traits) == "" {
			errMsg := fmt.Sprintf("missing or invalid 'traits' field in character at index %d", i)
			log.Error(errMsg)
			if errUpd := p.publishedRepo.UpdateAfterModeration(ctx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpd != nil {
				log.Error("Failed to update story status to Error after missing traits", zap.Error(errUpd))
			}
			return nil // Ack
		}
		relRaw, ok := c["relationship"].(map[string]interface{})
		if !ok {
			errMsg := fmt.Sprintf("missing or invalid 'relationship' object in character at index %d", i)
			log.Error(errMsg)
			if errUpd := p.publishedRepo.UpdateAfterModeration(ctx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpd != nil {
				log.Error("Failed to update story status to Error after missing relationship object", zap.Error(errUpd))
			}
			return nil // Ack
		}
		if protag, ok := relRaw["protaghonist"].(string); !ok || strings.TrimSpace(protag) == "" {
			errMsg := fmt.Sprintf("missing or invalid 'protaghonist' relationship in character at index %d", i)
			log.Error(errMsg)
			if errUpd := p.publishedRepo.UpdateAfterModeration(ctx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpd != nil {
				log.Error("Failed to update story status to Error after missing protagonist relationship", zap.Error(errUpd))
			}
			return nil // Ack
		}
		if mem, ok := c["memories"].(string); !ok || strings.TrimSpace(mem) == "" {
			errMsg := fmt.Sprintf("missing or invalid 'memories' field in character at index %d", i)
			log.Error(errMsg)
			if errUpd := p.publishedRepo.UpdateAfterModeration(ctx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpd != nil {
				log.Error("Failed to update story status to Error after missing memories", zap.Error(errUpd))
			}
			return nil // Ack
		}
		if hook, ok := c["plotHook"].(string); !ok || strings.TrimSpace(hook) == "" {
			errMsg := fmt.Sprintf("missing or invalid 'plotHook' field in character at index %d", i)
			log.Error(errMsg)
			if errUpd := p.publishedRepo.UpdateAfterModeration(ctx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpd != nil {
				log.Error("Failed to update story status to Error after missing plotHook", zap.Error(errUpd))
			}
			return nil // Ack
		}
		if ipd, ok := c["image_prompt_descriptor"].(string); !ok || strings.TrimSpace(ipd) == "" {
			errMsg := fmt.Sprintf("missing or invalid 'image_prompt_descriptor' field in character at index %d", i)
			log.Error(errMsg)
			if errUpd := p.publishedRepo.UpdateAfterModeration(ctx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpd != nil {
				log.Error("Failed to update story status to Error after missing image_prompt_descriptor", zap.Error(errUpd))
			}
			return nil // Ack
		}
		if irn, ok := c["image_reference_name"].(string); !ok || strings.TrimSpace(irn) == "" {
			errMsg := fmt.Sprintf("missing or invalid 'image_reference_name' field in character at index %d", i)
			log.Error(errMsg)
			if errUpd := p.publishedRepo.UpdateAfterModeration(ctx, p.db, publishedStoryID, sharedModels.StatusError, publishedStory.IsAdultContent, &errMsg, nil); errUpd != nil {
				log.Error("Failed to update story status to Error after missing image_reference_name", zap.Error(errUpd))
			}
			return nil // Ack
		}
	}

	// Сохраняем персонажей в setup, обновляем счетчики и публикуем задачи для генерации изображений
	var currentSetup map[string]interface{}
	if len(publishedStory.Setup) > 0 {
		if err := json.Unmarshal(publishedStory.Setup, &currentSetup); err != nil {
			log.Error("Failed to unmarshal existing setup for character generation update", zap.Error(err))
			currentSetup = make(map[string]interface{})
		}
	} else {
		currentSetup = make(map[string]interface{})
	}
	currentSetup["chars"] = chars
	// Обновляем флаг задачи генерации персонажей
	publishedStory.PendingCharGenTasks = false
	publishedStory.PendingCharImgTasks += len(chars)
	// Обновляем setup JSON в истории
	newSetupBytes, err := json.Marshal(currentSetup)
	if err != nil {
		log.Error("Failed to marshal updated setup with characters", zap.Error(err))
		return nil // Ack
	}
	publishedStory.Setup = newSetupBytes
	// Атомарно сохраняем setup и счетчики
	txUpdate, err := p.db.Begin(dbCtx)
	if err != nil {
		log.Error("Failed to begin transaction for character generation update", zap.Error(err))
		return nil // Ack
	}
	defer txUpdate.Rollback(dbCtx)
	// Определяем следующий шаг
	nextStep := sharedModels.StepCharacterImageGeneration
	// Вызываем UpdateSetupStatusAndCounters с новым шагом
	if err := p.publishedRepo.UpdateSetupStatusAndCounters(dbCtx, txUpdate, publishedStory.ID, publishedStory.Setup, publishedStory.Status, 0, publishedStory.PendingCardImgTasks, publishedStory.PendingCharImgTasks, &nextStep); err != nil {
		log.Error("Failed to update setup and counters in transaction after character generation", zap.Error(err))
		return nil // Ack
	}
	if err := txUpdate.Commit(dbCtx); err != nil {
		log.Error("Failed to commit transaction for character generation update", zap.Error(err))
		return nil // Ack
	}
	// Публикуем задачи генерации изображений для новых персонажей
	for _, c := range chars {
		refName := c["image_reference_name"].(string)
		prompt := c["image_prompt_descriptor"].(string)
		charTaskID := uuid.New()
		imgPayload := sharedMessaging.CharacterImageTaskPayload{
			TaskID:           charTaskID.String(),
			UserID:           publishedStory.UserID.String(),
			PublishedStoryID: publishedStoryID,
			CharacterID:      charTaskID,
			CharacterName:    c["name"].(string),
			ImageReference:   refName,
			Prompt:           prompt,
			NegativePrompt:   "",
			Ratio:            "2:3",
		}
		go func(payload sharedMessaging.CharacterImageTaskPayload) {
			taskCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if errPub := p.characterImageTaskPub.PublishCharacterImageTask(taskCtx, payload); errPub != nil {
				log.Error("Failed to publish character image generation task", zap.Error(errPub), zap.String("task_id", payload.TaskID))
			} else {
				log.Info("Character image task published successfully", zap.String("task_id", payload.TaskID))
			}
		}(imgPayload)
	}
	// <<< НАЧАЛО: Определение и установка следующего шага ПОСЛЕ коммита и ЗАПУСКА задач >>>
	// Получаем актуальное состояние счетчиков и статуса (могло измениться)
	postCommitStory, errGetPost := p.publishedRepo.GetByID(context.Background(), p.db, publishedStoryID)
	if errGetPost != nil {
		log.Error("Failed to get story state after character generation commit for step update", zap.Error(errGetPost))
		// Не фатально, но следующий шаг не будет установлен
	} else {
		var finalNextStep *sharedModels.InternalGenerationStep
		if postCommitStory.PendingCardImgTasks > 0 {
			step := sharedModels.StepCardImageGeneration
			finalNextStep = &step
		} else if postCommitStory.PendingCharImgTasks > 0 {
			step := sharedModels.StepCharacterImageGeneration
			finalNextStep = &step
		} else if !postCommitStory.PendingCharGenTasks { // Убедимся, что и CharGen завершен
			step := sharedModels.StepSetupGeneration
			finalNextStep = &step
		} // Если еще есть PendingCharGenTasks (маловероятно здесь), шаг не меняем

		if finalNextStep != nil {
			updateStepCtx, cancelStep := context.WithTimeout(context.Background(), 10*time.Second)
			// Обновляем только шаг, статус и флаги были установлены ранее (хотя статус мог быть SubTasksPending)
			if errUpdateStep := p.publishedRepo.UpdateStatusFlagsAndDetails(updateStepCtx, p.db, publishedStoryID, postCommitStory.Status, postCommitStory.IsFirstScenePending, postCommitStory.AreImagesPending, nil, finalNextStep); errUpdateStep != nil {
				log.Error("Failed to update InternalGenerationStep after character generation task dispatch", zap.Error(errUpdateStep), zap.Any("step_to_set", finalNextStep))
			} else {
				log.Info("InternalGenerationStep updated successfully after character generation", zap.Any("new_step", finalNextStep))
			}
			cancelStep()
		} else {
			log.Warn("Could not determine the next generation step after character generation.")
		}
		// Отправляем клиентское обновление о новых задачах
		// Используем postCommitStory, так как он содержит актуальные флаги/статус
		p.publishClientStoryUpdateOnReady(postCommitStory)
	}
	// <<< КОНЕЦ: Определение и установка следующего шага >>>

	return nil // Ack
}
