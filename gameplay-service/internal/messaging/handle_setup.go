package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
)

func (p *NotificationProcessor) handleNovelSetupNotification(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	dbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	p.logger.Info("Processing NovelSetup", zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()))

	publishedStory, err := p.publishedRepo.GetByID(dbCtx, publishedStoryID)
	if err != nil {
		p.logger.Error("Error getting PublishedStory for Setup update", zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()), zap.Error(err))
		return fmt.Errorf("error getting PublishedStory %s: %w", publishedStoryID, err)
	}

	if notification.Status == sharedMessaging.NotificationStatusSuccess {
		if publishedStory.Status != sharedModels.StatusSetupGenerating {
			p.logger.Warn("PublishedStory not in SetupGenerating status, Setup Success update cancelled.",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.String("current_status", string(publishedStory.Status)),
			)
			return nil
		}
		p.logger.Info("Setup Success notification", zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()))
		rawGeneratedText := notification.GeneratedText
		jsonToParse := extractJsonContent(rawGeneratedText) // Helper from consumer.go
		if jsonToParse == "" {
			p.logger.Error("SETUP PARSING ERROR: Could not extract JSON from Setup text",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.String("raw_text_snippet", stringShort(rawGeneratedText, 100)), // Helper from consumer.go
			)
			errDetails := "Failed to extract JSON block from Setup text"
			if updateErr := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, sharedModels.StatusError, nil, nil, nil, &errDetails); updateErr != nil {
				p.logger.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after Setup JSON extraction error" /* zap fields */, zap.Error(updateErr))
				return fmt.Errorf("error updating Error status for PublishedStory %s: %w", publishedStoryID, updateErr)
			}
			return nil
		}

		setupBytes := []byte(jsonToParse)
		var temp map[string]interface{}
		if err := json.Unmarshal(setupBytes, &temp); err != nil {
			p.logger.Error("SETUP PARSING ERROR: Invalid JSON received for Setup",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(err),
				zap.String("json_to_parse", jsonToParse),
			)
			errDetails := fmt.Sprintf("Invalid JSON received for Setup: %v", err)
			if updateErr := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, sharedModels.StatusError, nil, nil, nil, &errDetails); updateErr != nil {
				p.logger.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after Setup parsing error",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.Error(updateErr),
				)
				return fmt.Errorf("error updating Error status for PublishedStory %s: %w", publishedStoryID, updateErr)
			}
			return nil
		}

		if err := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, sharedModels.StatusFirstScenePending, setupBytes, nil, nil, nil); err != nil {
			p.logger.Error("CRITICAL ERROR: Failed to update status and Setup for PublishedStory",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(err),
			)
			return fmt.Errorf("error updating status/Setup for PublishedStory %s: %w", publishedStoryID, err)
		}
		p.logger.Info("PublishedStory status updated -> FirstScenePending and Setup saved", zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()))

		var setupContent sharedModels.NovelSetupContent
		if err := json.Unmarshal(setupBytes, &setupContent); err != nil {
			p.logger.Warn("Failed to unmarshal setup JSON to NovelSetupContent for image task trigger",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(err),
			)
		} else {
			p.logger.Info("Triggering image generation check",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
			)
			// Собираем задачи для батча
			imageTasks := make([]sharedMessaging.CharacterImageTaskPayload, 0, len(setupContent.Characters))

			for _, charData := range setupContent.Characters {
				if charData.ImageRef == "" {
					continue
				}
				imageRef := charData.ImageRef
				prompt := charData.Prompt
				characterIDForTask := uuid.New().String() // Генерируем ID для задачи

				logFieldsImg := []zap.Field{
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.String("image_ref", imageRef),
					zap.String("character_task_id", characterIDForTask),
				}
				p.logger.Info("Checking image for character", logFieldsImg...)
				_, errCheck := p.imageReferenceRepo.GetImageURLByReference(dbCtx, imageRef)
				if errCheck == nil {
					p.logger.Info("Found existing URL for ImageRef. Generation not required.", logFieldsImg...)
				} else if errors.Is(errCheck, interfaces.ErrNotFound) {
					p.logger.Info("URL for ImageRef not found. Adding image generation task to batch.", logFieldsImg...)
					// Добавляем задачу в слайс
					imageTask := sharedMessaging.CharacterImageTaskPayload{
						TaskID:         characterIDForTask, // Используем сгенерированный ID
						UserID:         publishedStory.UserID.String(),
						CharacterID:    characterIDForTask, // Используем сгенерированный ID задачи как ID персонажа для этой задачи
						Prompt:         prompt,
						NegativePrompt: "", // TODO: Get from charData when available
						ImageReference: imageRef,
						// Опциональные параметры (seed, steps, guidance) можно добавить сюда, если они есть в charData
					}
					imageTasks = append(imageTasks, imageTask)

				} else {
					p.logger.Error("Error checking ImageRef in DB. Skipping generation.", append(logFieldsImg, zap.Error(errCheck))...)
				}
			}

			// Отправляем батч, если есть задачи
			if len(imageTasks) > 0 {
				batchPayload := sharedMessaging.CharacterImageTaskBatchPayload{
					BatchID: uuid.New().String(),
					Tasks:   imageTasks,
				}
				p.logger.Info("Publishing character image task batch",
					zap.String("batch_id", batchPayload.BatchID),
					zap.Int("task_count", len(batchPayload.Tasks)),
					zap.String("published_story_id", publishedStoryID.String()),
				)
				if errPub := p.characterImageTaskBatchPub.PublishCharacterImageTaskBatch(ctx, batchPayload); errPub != nil {
					p.logger.Error("Failed to publish character image task batch", zap.Error(errPub),
						zap.String("batch_id", batchPayload.BatchID),
						zap.String("published_story_id", publishedStoryID.String()),
					)
					// TODO: Подумать о стратегии отката или повторной попытки для всего батча?
					// Пока просто логируем ошибку.
				} else {
					p.logger.Info("Character image task batch published successfully",
						zap.String("batch_id", batchPayload.BatchID),
						zap.String("published_story_id", publishedStoryID.String()),
					)
				}
			}

			// <<< НАЧАЛО: Логика генерации превью истории >>>
			if setupContent.StoryPreviewImagePrompt != "" {
				p.logger.Info("Generating story preview image",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
				)
				// Формируем ImageReference для превью
				previewImageRef := fmt.Sprintf("history_preview_%s", publishedStoryID.String())

				// Формируем полный промпт с суффиксом из конфига
				fullPreviewPrompt := setupContent.StoryPreviewImagePrompt + p.cfg.StoryPreviewPromptStyleSuffix // Убедимся, что cfg доступен в p

				// Создаем задачу для генерации превью
				previewTask := sharedMessaging.CharacterImageTaskPayload{
					TaskID:         uuid.New().String(),            // Новый ID для этой задачи
					UserID:         publishedStory.UserID.String(), // ID пользователя истории
					CharacterID:    publishedStoryID.String(),      // Используем ID истории как CharacterID для превью
					Prompt:         fullPreviewPrompt,
					NegativePrompt: "", // Не используется
					ImageReference: previewImageRef,
				}

				// Оборачиваем в батч из одной задачи
				previewBatchPayload := sharedMessaging.CharacterImageTaskBatchPayload{
					BatchID: uuid.New().String(),
					Tasks:   []sharedMessaging.CharacterImageTaskPayload{previewTask},
				}

				// Отправляем задачу
				if errPub := p.characterImageTaskBatchPub.PublishCharacterImageTaskBatch(ctx, previewBatchPayload); errPub != nil {
					p.logger.Error("Failed to publish story preview image task", zap.Error(errPub),
						zap.String("preview_batch_id", previewBatchPayload.BatchID),
						zap.String("published_story_id", publishedStoryID.String()),
					)
					// Не критично для основного потока, просто логируем
				} else {
					p.logger.Info("Story preview image task published successfully",
						zap.String("preview_batch_id", previewBatchPayload.BatchID),
						zap.String("published_story_id", publishedStoryID.String()),
					)
					// <<< Опционально: Обновить поле PreviewImageReference в PublishedStory >>>
					// previewRef := previewImageRef // Создаем копию
					// if updateRefErr := p.publishedRepo.UpdatePreviewImageReference(dbCtx, publishedStoryID, &previewRef); updateRefErr != nil {
					// 	p.logger.Error("Failed to update preview image reference in DB", zap.Error(updateRefErr), zap.String("published_story_id", publishedStoryID.String()))
					// }
				}
			} else {
				p.logger.Warn("StoryPreviewImagePrompt (spi) is empty in setup, skipping preview generation.",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
				)
			}
			// <<< КОНЕЦ: Логика генерации превью истории >>>
		}

		configBytes := publishedStory.Config
		combinedInputMap := make(map[string]interface{})
		if len(configBytes) > 0 && string(configBytes) != "null" {
			if err := json.Unmarshal(configBytes, &combinedInputMap); err != nil {
				log.Printf("[processor][TaskID: %s] ПРЕДУПРЕЖДЕНИЕ: Не удалось распарсить Config для задачи FirstScene PublishedStory %s: %v. Задача будет отправлена без Config.", taskID, publishedStoryID, err)
				combinedInputMap = make(map[string]interface{})
			}
		} else {
			log.Printf("[processor][TaskID: %s] ПРЕДУПРЕЖДЕНИЕ: Config отсутствует или null для задачи FirstScene PublishedStory %s. Задача будет отправлена без Config.", taskID, publishedStoryID)
		}
		var setupMapForTask map[string]interface{}
		_ = json.Unmarshal(setupBytes, &setupMapForTask)
		combinedInputMap["stp"] = setupMapForTask
		initialCoreStats := make(map[string]int)
		if csd, ok := setupMapForTask["csd"].(map[string]interface{}); ok {
			for key, val := range csd {
				if statDef, okDef := val.(map[string]interface{}); okDef {
					if initVal, okVal := statDef["iv"].(float64); okVal {
						initialCoreStats[key] = int(initVal)
					}
				}
			}
		}
		combinedInputMap["cs"] = initialCoreStats
		combinedInputMap["sv"] = make(map[string]interface{})
		combinedInputMap["gf"] = []string{}
		combinedInputMap["uc"] = []sharedModels.UserChoiceInfo{}
		combinedInputMap["pss"] = ""
		combinedInputMap["pfd"] = ""
		combinedInputMap["pvis"] = ""
		delete(combinedInputMap, "t")
		delete(combinedInputMap, "sd")
		delete(combinedInputMap, "gn")
		delete(combinedInputMap, "ln")

		combinedInputBytes, errMarshal := json.Marshal(combinedInputMap)
		if errMarshal != nil {
			p.logger.Error("ERROR: Failed to marshal JSON for FirstScene task",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(errMarshal),
			)
			return nil
		}
		combinedInputJSON := string(combinedInputBytes)
		nextTaskPayload := sharedMessaging.GenerationTaskPayload{
			TaskID:           uuid.New().String(),
			UserID:           publishedStory.UserID.String(),
			PromptType:       sharedMessaging.PromptTypeNovelFirstSceneCreator,
			PublishedStoryID: publishedStoryID.String(),
			UserInput:        combinedInputJSON,
			StateHash:        sharedModels.InitialStateHash,
		}

		if errPub := p.taskPub.PublishGenerationTask(ctx, nextTaskPayload); errPub != nil {
			p.logger.Error("CRITICAL ERROR: Failed to send first scene generation task",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(errPub),
			)
		} else {
			p.logger.Info("First scene generation task sent successfully",
				zap.String("task_id", taskID),
				zap.String("next_task_id", nextTaskPayload.TaskID),
				zap.String("published_story_id", publishedStoryID.String()),
			)
		}
	} else {
		p.logger.Warn("Setup Error notification received",
			zap.String("task_id", taskID),
			zap.String("published_story_id", publishedStoryID.String()),
			zap.String("error_details", notification.ErrorDetails),
		)
		if err := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, sharedModels.StatusError, nil, nil, nil, &notification.ErrorDetails); err != nil {
			p.logger.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after Setup generation error",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(err),
			)
			return fmt.Errorf("error updating Error status for PublishedStory %s: %w", publishedStoryID, err)
		}
	}
	return nil
}
