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
	"novel-server/shared/notifications"
)

func (p *NotificationProcessor) publishStoryUpdateViaRabbitMQ(ctx context.Context, story *sharedModels.PublishedStory, eventType string, errorMsg *string) {
	if story == nil {
		p.logger.Error("Attempted to publish story update for nil PublishedStory")
		return
	}

	clientUpdate := sharedModels.ClientStoryUpdate{
		ID:           story.ID.String(), // Всегда ID PublishedStory
		UserID:       story.UserID.String(),
		UpdateType:   sharedModels.UpdateTypeStory, // Всегда Story
		Status:       string(story.Status),
		ErrorDetails: errorMsg,
		StoryTitle:   story.Title,
	}

	// Логирование перед отправкой
	p.logger.Info("Attempting to publish client story update via RabbitMQ...",
		zap.String("update_type", string(clientUpdate.UpdateType)),
		zap.String("id", clientUpdate.ID),
		zap.String("status", clientUpdate.Status),
		zap.String("ws_event", eventType), // Передаем исходное событие для логов
	)

	// Отправка через RabbitMQ паблишер
	wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer wsCancel()
	if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdate); errWs != nil {
		p.logger.Error("Error sending ClientStoryUpdate (Story) via RabbitMQ", zap.Error(errWs),
			zap.String("storyID", clientUpdate.ID),
		)
	} else {
		p.logger.Info("ClientStoryUpdate (Story) sent successfully via RabbitMQ",
			zap.String("storyID", clientUpdate.ID),
			zap.String("status", clientUpdate.Status),
			zap.String("ws_event", eventType),
		)
	}

	// Отправка через NATS не требуется, т.к. websocket-service слушает RabbitMQ.
}

func (p *NotificationProcessor) handleNovelSetupNotification(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	dbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	p.logger.Info("Processing NovelSetup", zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()))

	publishedStory, err := p.publishedRepo.GetByID(dbCtx, p.db, publishedStoryID)
	if err != nil {
		p.logger.Error("CRITICAL ERROR: Error getting PublishedStory for Setup update", zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()), zap.Error(err))
		return fmt.Errorf("error getting PublishedStory %s: %w", publishedStoryID, err)
	}

	if notification.Status == sharedMessaging.NotificationStatusSuccess {
		if publishedStory.Status != sharedModels.StatusSetupPending {
			p.logger.Warn("PublishedStory not in SetupPending status, Setup Success update cancelled.",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.String("current_status", string(publishedStory.Status)),
			)
			return nil
		}
		p.logger.Info("Setup Success notification received, proceeding with update", zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()), zap.String("status_when_received", string(publishedStory.Status)))

		var parseErr error
		var rawGeneratedText string

		genResultCtx, genResultCancel := context.WithTimeout(ctx, 10*time.Second)
		genResult, genErr := p.genResultRepo.GetByTaskID(genResultCtx, taskID)
		genResultCancel()

		if genErr != nil {
			p.logger.Error("DB ERROR (Setup): Could not get GenerationResult by TaskID",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(genErr),
			)
			parseErr = fmt.Errorf("failed to fetch generation result: %w", genErr)
		} else if genResult.Error != "" {
			p.logger.Error("TASK ERROR (Setup): GenerationResult indicates an error",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.String("gen_error", genResult.Error),
			)
			parseErr = errors.New(genResult.Error)
		} else {
			rawGeneratedText = genResult.GeneratedText
		}

		if parseErr != nil {
			p.logger.Error("Processing NovelSetup failed before JSON handling",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(parseErr),
			)
			errDetails := parseErr.Error()

			dbCtxUpdateStory, cancelUpdateStory := context.WithTimeout(ctx, 10*time.Second)
			defer cancelUpdateStory()
			if errUpdateStory := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtxUpdateStory, p.db, publishedStoryID, sharedModels.StatusError, false, false, &errDetails); errUpdateStory != nil {
				p.logger.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after initial setup processing error",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.Error(errUpdateStory),
				)
			} else {
				p.logger.Info("PublishedStory status updated to Error due to initial setup processing error",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
				)
			}

			// Отправка WebSocket уведомления об ошибке обработки
			go p.publishStoryUpdateViaRabbitMQ(ctx, publishedStory, constants.WSEventSetupError, &errDetails)
			return parseErr
		}

		var setupContent sharedModels.NovelSetupContent
		setupBytes := []byte(rawGeneratedText)
		p.logger.Debug("Attempting to unmarshal setupBytes into NovelSetupContent (assuming pre-validated JSON)",
			zap.String("task_id", taskID),
			zap.String("published_story_id", publishedStoryID.String()),
			zap.ByteString("json_bytes", setupBytes),
		)
		if errUnmarshalSetup := json.Unmarshal(setupBytes, &setupContent); errUnmarshalSetup != nil {
			p.logger.Error("SETUP UNMARSHAL ERROR (post-generator): Failed to decode setup JSON into NovelSetupContent struct (should have been validated by generator)",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(errUnmarshalSetup),
				zap.ByteString("problematic_bytes", setupBytes),
			)
			parseErr = fmt.Errorf("internal error processing setup: unexpected JSON structure from generator: %w", errUnmarshalSetup)
			errDetails := parseErr.Error()

			dbCtxUpdateStory, cancelUpdateStory := context.WithTimeout(ctx, 10*time.Second)
			defer cancelUpdateStory()
			if errUpdateStory := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtxUpdateStory, p.db, publishedStoryID, sharedModels.StatusError, false, false, &errDetails); errUpdateStory != nil {
				p.logger.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after initial setup processing error",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.Error(errUpdateStory),
				)
			} else {
				p.logger.Info("PublishedStory status updated to Error due to initial setup processing error",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
				)
			}

			// Отправка WebSocket уведомления об ошибке обработки
			go p.publishStoryUpdateViaRabbitMQ(ctx, publishedStory, constants.WSEventSetupError, &errDetails)
			return parseErr
		} else {
			p.logger.Info("Successfully unmarshalled setup JSON (post-generator)", zap.String("task_id", taskID))

			var fullConfig sharedModels.Config
			var characterVisualStyle string
			var storyStyle string
			var needsPreviewImage bool = false
			var needsCharacterImages bool = false
			var imageTasks []sharedMessaging.CharacterImageTaskPayload

			if errCfg := json.Unmarshal(publishedStory.Config, &fullConfig); errCfg != nil {
				p.logger.Warn("Failed to unmarshal config JSON to get CharacterVisualStyle/Style", zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()), zap.Error(errCfg))
			} else {
				characterVisualStyle = fullConfig.PlayerPrefs.CharacterVisualStyle
				storyStyle = fullConfig.PlayerPrefs.Style
				if characterVisualStyle != "" {
					characterVisualStyle = ", " + characterVisualStyle
				}
				if storyStyle != "" {
					storyStyle = ", " + storyStyle
				}
			}

			p.logger.Info("Checking which images need generation", zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()))
			imageTasks = make([]sharedMessaging.CharacterImageTaskPayload, 0, len(setupContent.Characters))

			for _, charData := range setupContent.Characters {
				if charData.ImageRef == "" || charData.Prompt == "" {
					p.logger.Debug("Skipping character image check: missing ImageRef or Prompt", zap.String("char_name", charData.Name))
					continue
				}
				imageRef := charData.ImageRef
				correctedRef := imageRef

				if !strings.HasPrefix(imageRef, "ch_") {
					if strings.HasPrefix(imageRef, "character_") {
						correctedRef = strings.TrimPrefix(imageRef, "character_")
					} else if strings.HasPrefix(imageRef, "char_") {
						correctedRef = strings.TrimPrefix(imageRef, "char_")
					} else {
						correctedRef = imageRef
					}

					correctedRef = "ch_" + strings.TrimPrefix(correctedRef, "ch_")

					p.logger.Info("Ensured ImageRef prefix is 'ch_'.", zap.String("original_ref", imageRef), zap.String("new_ref", correctedRef))
				}

				_, errCheck := p.imageReferenceRepo.GetImageURLByReference(dbCtx, correctedRef)
				if errors.Is(errCheck, sharedModels.ErrNotFound) {
					p.logger.Debug("Character image needs generation", zap.String("image_ref", correctedRef))
					needsCharacterImages = true
					characterIDForTask := uuid.New()
					fullCharacterPrompt := charData.Prompt + characterVisualStyle
					imageTask := sharedMessaging.CharacterImageTaskPayload{
						TaskID:           characterIDForTask.String(),
						CharacterID:      characterIDForTask,
						Prompt:           fullCharacterPrompt,
						NegativePrompt:   charData.NegPrompt,
						ImageReference:   correctedRef,
						Ratio:            "2:3",
						PublishedStoryID: publishedStoryID,
					}
					imageTasks = append(imageTasks, imageTask)
				} else if errCheck != nil {
					p.logger.Error("Error checking Character ImageRef in DB", zap.String("image_ref", correctedRef), zap.Error(errCheck))
				} else {
					p.logger.Debug("Character image already exists", zap.String("image_ref", correctedRef))
				}
			}

			if setupContent.StoryPreviewImagePrompt != "" {
				previewImageRef := fmt.Sprintf("history_preview_%s", publishedStoryID.String())
				_, errCheck := p.imageReferenceRepo.GetImageURLByReference(dbCtx, previewImageRef)
				if errors.Is(errCheck, sharedModels.ErrNotFound) {
					p.logger.Debug("Preview image needs generation", zap.String("image_ref", previewImageRef))
					needsPreviewImage = true
				} else if errCheck != nil {
					p.logger.Error("Error checking Preview ImageRef in DB", zap.String("image_ref", previewImageRef), zap.Error(errCheck))
				} else {
					p.logger.Debug("Preview image already exists", zap.String("image_ref", previewImageRef))
				}
			} else {
				p.logger.Info("StoryPreviewImagePrompt (spi) is empty in setup, no preview generation needed.")
			}

			areImagesPending := needsPreviewImage || needsCharacterImages
			isFirstScenePending := true
			if errUpdateSetup := p.publishedRepo.UpdateStatusFlagsAndSetup(dbCtx, p.db, publishedStoryID, sharedModels.StatusFirstScenePending, setupBytes, isFirstScenePending, areImagesPending); errUpdateSetup != nil {
				p.logger.Error("CRITICAL ERROR: Failed to update status, flags and Setup for PublishedStory",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.Error(errUpdateSetup),
				)
				parseErr = fmt.Errorf("error updating status/flags/Setup for PublishedStory %s: %w", publishedStoryID, errUpdateSetup)
			} else {
				p.logger.Info("PublishedStory status, flags updated and Setup saved",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.String("new_status", string(sharedModels.StatusFirstScenePending)),
					zap.Bool("is_first_scene_pending", isFirstScenePending),
					zap.Bool("are_images_pending", areImagesPending),
				)

				if areImagesPending {
					p.logger.Info("Publishing image generation tasks",
						zap.String("task_id", taskID),
						zap.String("published_story_id", publishedStoryID.String()),
						zap.Bool("preview_needed", needsPreviewImage),
						zap.Int("character_images_needed", len(imageTasks)),
					)
					if len(imageTasks) > 0 {
						batchPayload := sharedMessaging.CharacterImageTaskBatchPayload{BatchID: uuid.New().String(), Tasks: imageTasks}
						if errPub := p.characterImageTaskBatchPub.PublishCharacterImageTaskBatch(ctx, batchPayload); errPub != nil {
							p.logger.Error("Failed to publish character image task batch", zap.Error(errPub), zap.String("batch_id", batchPayload.BatchID), zap.String("published_story_id", publishedStoryID.String()))
						} else {
							p.logger.Info("Character image task batch published successfully", zap.String("batch_id", batchPayload.BatchID), zap.String("published_story_id", publishedStoryID.String()))
						}
					}
					if needsPreviewImage {
						previewImageRef := fmt.Sprintf("history_preview_%s", publishedStoryID.String())
						basePreviewPrompt := setupContent.StoryPreviewImagePrompt
						fullPreviewPromptWithStyles := basePreviewPrompt + storyStyle + characterVisualStyle
						previewTask := sharedMessaging.CharacterImageTaskPayload{
							TaskID:           uuid.New().String(),
							CharacterID:      publishedStoryID,
							Prompt:           fullPreviewPromptWithStyles,
							NegativePrompt:   "",
							ImageReference:   previewImageRef,
							Ratio:            "3:2",
							PublishedStoryID: publishedStoryID,
						}
						previewBatchPayload := sharedMessaging.CharacterImageTaskBatchPayload{BatchID: uuid.New().String(), Tasks: []sharedMessaging.CharacterImageTaskPayload{previewTask}}
						if errPub := p.characterImageTaskBatchPub.PublishCharacterImageTaskBatch(ctx, previewBatchPayload); errPub != nil {
							p.logger.Error("Failed to publish story preview image task", zap.Error(errPub), zap.String("preview_batch_id", previewBatchPayload.BatchID), zap.String("published_story_id", publishedStoryID.String()))
						} else {
							p.logger.Info("Story preview image task published successfully", zap.String("preview_batch_id", previewBatchPayload.BatchID), zap.String("published_story_id", publishedStoryID.String()))
						}
					}
				} else {
					p.logger.Info("No image generation needed.", zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()))
				}

				if !areImagesPending {
					p.logger.Info("No images pending, attempting to publish first scene task using internal helper...")
					if errPub := p.publishFirstSceneTaskInternal(ctx, publishedStory); errPub != nil {
						p.logger.Error("CRITICAL ERROR: Failed to send first scene generation task (using internal helper)",
							zap.String("task_id", taskID),
							zap.String("published_story_id", publishedStoryID.String()),
							zap.Error(errPub),
						)
						parseErr = fmt.Errorf("failed to publish task for first scene: %w", errPub)
					}
				}
			}

			if parseErr != nil {
				p.logger.Error("Processing NovelSetup failed",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.Error(parseErr),
				)
				errDetails := parseErr.Error()

				dbCtxUpdateStory, cancelUpdateStory := context.WithTimeout(ctx, 10*time.Second)
				defer cancelUpdateStory()
				if errUpdateStory := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtxUpdateStory, p.db, publishedStoryID, sharedModels.StatusError, false, false, &errDetails); errUpdateStory != nil {
					p.logger.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after setup processing error",
						zap.String("task_id", taskID),
						zap.String("published_story_id", publishedStoryID.String()),
						zap.Error(errUpdateStory),
					)
				} else {
					p.logger.Info("PublishedStory status updated to Error due to setup processing error",
						zap.String("task_id", taskID),
						zap.String("published_story_id", publishedStoryID.String()),
					)
				}

				// Отправка WebSocket уведомления об ошибке обработки
				go p.publishStoryUpdateViaRabbitMQ(ctx, publishedStory, constants.WSEventSetupError, &errDetails)
			}

			// Отправка WebSocket уведомления об успехе Setup
			finalStoryState, errGetFinal := p.publishedRepo.GetByID(ctx, p.db, publishedStoryID)
			if errGetFinal != nil {
				p.logger.Error("Failed to get final story state for WS notification after setup success", zap.Error(errGetFinal))
			} else {
				// Отправляем обновление с текущим статусом (вероятно, FirstScenePending или ImageGenerationPending)
				go p.publishStoryUpdateViaRabbitMQ(ctx, finalStoryState, constants.WSEventSetupGenerated, nil)
			}

			// Отправляем Push уведомление
			go p.publishPushNotificationForSetupPending(ctx, publishedStory)
		}
	} else if notification.Status == sharedMessaging.NotificationStatusError {
		p.logger.Warn("Setup Error notification received",
			zap.String("task_id", taskID),
			zap.String("published_story_id", publishedStoryID.String()),
			zap.String("error_details", notification.ErrorDetails),
		)

		dbCtxUpdateStory, cancelUpdateStory := context.WithTimeout(ctx, 10*time.Second)
		defer cancelUpdateStory()
		if errUpdateStory := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtxUpdateStory, p.db, publishedStoryID, sharedModels.StatusError, false, false, &notification.ErrorDetails); errUpdateStory != nil {
			p.logger.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after Setup generation error",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(errUpdateStory),
			)
		} else {
			p.logger.Info("PublishedStory status updated to Error due to setup generation error",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
			)
		}

		// Отправка WebSocket уведомления об ошибке генерации
		go p.publishStoryUpdateViaRabbitMQ(ctx, publishedStory, constants.WSEventSetupError, &notification.ErrorDetails)
	}
	return nil
}

// publishPushNotificationForSetupPending использует notifications.BuildSetupPendingPushPayload
func (p *NotificationProcessor) publishPushNotificationForSetupPending(ctx context.Context, story *sharedModels.PublishedStory) {
	payload, err := notifications.BuildSetupPendingPushPayload(story)
	if err != nil {
		p.logger.Error("Failed to build push notification payload for setup pending", zap.Error(err))
		return
	}

	if err := p.pushPub.PublishPushNotification(ctx, *payload); err != nil {
		p.logger.Error("Failed to publish push notification event for setup pending",
			zap.String("userID", payload.UserID.String()),
			zap.String("publishedStoryID", story.ID.String()),
			zap.Error(err),
		)
	} else {
		p.logger.Info("Push notification event for setup pending published successfully",
			zap.String("userID", payload.UserID.String()),
			zap.String("publishedStoryID", story.ID.String()),
		)
	}
}
