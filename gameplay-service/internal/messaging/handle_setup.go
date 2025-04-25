package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
)

// MinimalConfigForFirstScene - структура для минимального конфига, отправляемого для PromptTypeNovelFirstSceneCreator
// Содержит только поля, необходимые этому промпту
type MinimalConfigForFirstScene struct {
	Language       string                   `json:"ln,omitempty"`     // Required
	IsAdultContent bool                     `json:"ac,omitempty"`     // Required
	Genre          string                   `json:"gn,omitempty"`     // <<< ДОБАВЛЕНО
	PlayerName     string                   `json:"pn,omitempty"`     // Player Name из конфига
	PlayerGender   string                   `json:"pg,omitempty"`     // <<< ДОБАВЛЕНО
	PlayerDesc     string                   `json:"p_desc,omitempty"` // <<< ДОБАВЛЕНО (основное описание)
	WorldContext   string                   `json:"wc,omitempty"`     // <<< ДОБАВЛЕНО
	StorySummary   string                   `json:"ss,omitempty"`     // <<< ДОБАВЛЕНО
	PlayerPrefs    sharedModels.PlayerPrefs `json:"pp,omitempty"`     // <<< ДОБАВЛЕНО (структура без Style)
}

// ToMinimalConfigForFirstScene преобразует полный конфиг в минимальный
func ToMinimalConfigForFirstScene(configBytes []byte) MinimalConfigForFirstScene {
	var fullConfig sharedModels.Config
	// Игнорируем ошибку, если конфиг некорректный, просто вернем пустые поля
	_ = json.Unmarshal(configBytes, &fullConfig)

	// Создаем минимальную структуру PlayerPrefs
	minimalPrefs := sharedModels.PlayerPrefs{
		Themes:               fullConfig.PlayerPrefs.Themes,
		Tone:                 fullConfig.PlayerPrefs.Tone,
		PlayerDescription:    fullConfig.PlayerPrefs.PlayerDescription,
		WorldLore:            fullConfig.PlayerPrefs.WorldLore,
		DesiredLocations:     fullConfig.PlayerPrefs.DesiredLocations,
		DesiredCharacters:    fullConfig.PlayerPrefs.DesiredCharacters,
		CharacterVisualStyle: fullConfig.PlayerPrefs.CharacterVisualStyle,
		Style:                fullConfig.PlayerPrefs.Style,
	}

	return MinimalConfigForFirstScene{
		Language:       fullConfig.Language,
		IsAdultContent: fullConfig.IsAdultContent,
		Genre:          fullConfig.Genre, // <<< ДОБАВЛЕНО
		PlayerName:     fullConfig.PlayerName,
		PlayerGender:   fullConfig.PlayerGender, // <<< ДОБАВЛЕНО
		PlayerDesc:     fullConfig.PlayerDesc,   // <<< ДОБАВЛЕНО
		WorldContext:   fullConfig.WorldContext, // <<< ДОБАВЛЕНО
		StorySummary:   fullConfig.StorySummary, // <<< ДОБАВЛЕНО
		PlayerPrefs:    minimalPrefs,            // <<< ДОБАВЛЕНО
	}
}

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
		// Разрешаем обработку успеха, если статус SetupGenerating ИЛИ SetupPending
		// (Retry устанавливает SetupPending перед отправкой задачи)
		if publishedStory.Status != sharedModels.StatusSetupGenerating && publishedStory.Status != sharedModels.StatusSetupPending {
			p.logger.Warn("PublishedStory not in SetupGenerating or SetupPending status, Setup Success update cancelled.",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.String("current_status", string(publishedStory.Status)),
			)
			return nil // Не обрабатываем успех, если статус некорректный
		}
		p.logger.Info("Setup Success notification received, proceeding with update", zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()), zap.String("status_when_received", string(publishedStory.Status)))
		rawGeneratedText := notification.GeneratedText
		jsonToParse := extractJsonContent(rawGeneratedText) // Helper from consumer.go
		if jsonToParse == "" {
			p.logger.Error("SETUP PARSING ERROR: Could not extract JSON from Setup text",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.String("raw_text_snippet", stringShort(rawGeneratedText, 100)), // Helper from consumer.go
			)
			errDetails := "Failed to extract JSON block from Setup text"
			if updateErr := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtx, publishedStoryID, sharedModels.StatusError, false, false, &errDetails); updateErr != nil {
				p.logger.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after Setup JSON extraction error" /* zap fields */, zap.Error(updateErr))
				// <<< НАЧАЛО: Отправка WS обновления (Ошибка извлечения JSON) >>>
				clientUpdateErr := ClientStoryUpdate{
					ID:           publishedStoryID.String(),
					UserID:       publishedStory.UserID.String(),
					UpdateType:   UpdateTypeStory,
					Status:       string(sharedModels.StatusError),
					ErrorDetails: &errDetails,
				}
				wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer wsCancel()
				if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdateErr); errWs != nil {
					p.logger.Error("Error sending ClientStoryUpdate (Setup JSON Extraction Error)", zap.String("task_id", taskID), zap.Error(errWs))
				} else {
					p.logger.Info("ClientStoryUpdate sent (Setup JSON Extraction Error)", zap.String("task_id", taskID), zap.String("story_id", publishedStoryID.String()))
				}
				// <<< КОНЕЦ: Отправка WS обновления (Ошибка извлечения JSON) >>>
				return nil // Возвращаем nil, т.к. статус Error уже сохранен
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
			if updateErr := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtx, publishedStoryID, sharedModels.StatusError, false, false, &errDetails); updateErr != nil {
				p.logger.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after Setup parsing error",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.Error(updateErr),
				)
				// <<< НАЧАЛО: Отправка WS обновления (Ошибка парсинга JSON Setup) >>>
				clientUpdateErr := ClientStoryUpdate{
					ID:           publishedStoryID.String(),
					UserID:       publishedStory.UserID.String(),
					UpdateType:   UpdateTypeStory,
					Status:       string(sharedModels.StatusError),
					ErrorDetails: &errDetails,
				}
				wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer wsCancel()
				if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdateErr); errWs != nil {
					p.logger.Error("Error sending ClientStoryUpdate (Setup JSON Parsing Error)", zap.String("task_id", taskID), zap.Error(errWs))
				} else {
					p.logger.Info("ClientStoryUpdate sent (Setup JSON Parsing Error)", zap.String("task_id", taskID), zap.String("story_id", publishedStoryID.String()))
				}
				// <<< КОНЕЦ: Отправка WS обновления (Ошибка парсинга JSON Setup) >>>
				return nil // Возвращаем nil, т.к. статус Error уже сохранен
			}
			return nil
		}

		var setupContent sharedModels.NovelSetupContent
		var fullConfig sharedModels.Config
		var characterVisualStyle string
		var storyStyle string
		var needsPreviewImage bool = false
		var needsCharacterImages bool = false
		imageTasks := make([]sharedMessaging.CharacterImageTaskPayload, 0)
		var errUnmarshalSetup error

		errUnmarshalSetup = json.Unmarshal(setupBytes, &setupContent)
		if errUnmarshalSetup != nil {
			p.logger.Error("SETUP PARSING ERROR (before image check): Failed to unmarshal setup JSON",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(errUnmarshalSetup),
			)
			errDetails := fmt.Sprintf("Failed to unmarshal setup JSON: %v", errUnmarshalSetup)
			if updateErr := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtx, publishedStoryID, sharedModels.StatusError, false, false, &errDetails); updateErr != nil {
				p.logger.Error("CRITICAL ERROR: Failed to update status to Error after setup unmarshal error", zap.Error(updateErr))
				// <<< НАЧАЛО: Отправка WS обновления (Ошибка анмаршалинга Setup) >>>
				clientUpdateErr := ClientStoryUpdate{
					ID:           publishedStoryID.String(),
					UserID:       publishedStory.UserID.String(),
					UpdateType:   UpdateTypeStory,
					Status:       string(sharedModels.StatusError),
					ErrorDetails: &errDetails,
				}
				wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer wsCancel()
				if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdateErr); errWs != nil {
					p.logger.Error("Error sending ClientStoryUpdate (Setup Unmarshal Error)", zap.String("task_id", taskID), zap.Error(errWs))
				} else {
					p.logger.Info("ClientStoryUpdate sent (Setup Unmarshal Error)", zap.String("task_id", taskID), zap.String("story_id", publishedStoryID.String()))
				}
				// <<< КОНЕЦ: Отправка WS обновления (Ошибка анмаршалинга Setup) >>>
				return nil // Возвращаем nil, т.к. статус Error уже сохранен
			}
			return nil
		} else {
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
				_, errCheck := p.imageReferenceRepo.GetImageURLByReference(dbCtx, imageRef)
				if errors.Is(errCheck, sharedModels.ErrNotFound) {
					p.logger.Debug("Character image needs generation", zap.String("image_ref", imageRef))
					needsCharacterImages = true
					characterIDForTask := uuid.New()
					fullCharacterPrompt := charData.Prompt + characterVisualStyle
					imageTask := sharedMessaging.CharacterImageTaskPayload{
						TaskID:           characterIDForTask.String(),
						CharacterID:      characterIDForTask,
						Prompt:           fullCharacterPrompt,
						NegativePrompt:   charData.NegPrompt,
						ImageReference:   imageRef,
						Ratio:            "2:3",
						PublishedStoryID: publishedStoryID,
					}
					imageTasks = append(imageTasks, imageTask)
				} else if errCheck != nil {
					p.logger.Error("Error checking Character ImageRef in DB", zap.String("image_ref", imageRef), zap.Error(errCheck))
				} else {
					p.logger.Debug("Character image already exists", zap.String("image_ref", imageRef))
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
		}
		areImagesPending := needsPreviewImage || needsCharacterImages

		isFirstScenePending := true
		if err := p.publishedRepo.UpdateStatusFlagsAndSetup(dbCtx, publishedStoryID, sharedModels.StatusFirstScenePending, setupBytes, isFirstScenePending, areImagesPending); err != nil {
			p.logger.Error("CRITICAL ERROR: Failed to update status, flags and Setup for PublishedStory",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(err),
			)
			return fmt.Errorf("error updating status/flags/Setup for PublishedStory %s: %w", publishedStoryID, err)
		}
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

		if errUnmarshalSetup == nil {
			var simplifiedSetup map[string]interface{}
			simplifiedChars := make([]map[string]string, 0, len(setupContent.Characters))
			for _, char := range setupContent.Characters {
				simplifiedChar := map[string]string{"n": char.Name, "d": char.Description}
				if char.Personality != "" {
					simplifiedChar["p"] = char.Personality
				}
				simplifiedChars = append(simplifiedChars, simplifiedChar)
			}
			simplifiedSetup = map[string]interface{}{"chars": simplifiedChars}

			combinedInputMap := make(map[string]interface{})
			minimalConfig := ToMinimalConfigForFirstScene(publishedStory.Config)
			combinedInputMap["cfg"] = minimalConfig
			combinedInputMap["stp"] = simplifiedSetup
			combinedInputMap["sv"] = make(map[string]interface{})
			combinedInputMap["gf"] = []string{}
			combinedInputMap["uc"] = []sharedModels.UserChoiceInfo{}
			combinedInputMap["pss"] = ""
			combinedInputMap["pfd"] = ""
			combinedInputMap["pvis"] = ""

			combinedInputBytes, errMarshal := json.Marshal(combinedInputMap)
			if errMarshal != nil {
				p.logger.Error("CRITICAL ERROR: Failed to marshal JSON for FirstScene task, cannot proceed.",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.Error(errMarshal),
				)
				errDetails := fmt.Sprintf("Failed to marshal payload for first scene: %v", errMarshal)
				if updateErr := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtx, publishedStoryID, sharedModels.StatusError, false, false, &errDetails); updateErr != nil {
					p.logger.Error("CRITICAL ERROR: Failed to update status to Error after FirstScene marshal error", zap.Error(updateErr))
					// <<< НАЧАЛО: Отправка WS обновления (Ошибка маршалинга FirstScene Payload) >>>
					clientUpdateErr := ClientStoryUpdate{
						ID:           publishedStoryID.String(),
						UserID:       publishedStory.UserID.String(),
						UpdateType:   UpdateTypeStory,
						Status:       string(sharedModels.StatusError),
						ErrorDetails: &errDetails,
					}
					wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer wsCancel()
					if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdateErr); errWs != nil {
						p.logger.Error("Error sending ClientStoryUpdate (FirstScene Marshal Error)", zap.String("task_id", taskID), zap.Error(errWs))
					} else {
						p.logger.Info("ClientStoryUpdate sent (FirstScene Marshal Error)", zap.String("task_id", taskID), zap.String("story_id", publishedStoryID.String()))
					}
					// <<< КОНЕЦ: Отправка WS обновления (Ошибка маршалинга FirstScene Payload) >>>
					return fmt.Errorf("failed to marshal FirstScene payload for %s: %w", publishedStoryID, errMarshal)
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
						zap.String("next_task_id", nextTaskPayload.TaskID),
						zap.String("published_story_id", publishedStoryID.String()),
						zap.Error(errPub),
					)
					errDetails := fmt.Sprintf("Failed to publish task for first scene: %v", errPub)
					if updateErr := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtx, publishedStoryID, sharedModels.StatusError, false, false, &errDetails); updateErr != nil {
						p.logger.Error("CRITICAL ERROR: Failed to update status to Error after FirstScene publish error", zap.Error(updateErr))
					}
					// <<< НАЧАЛО: Отправка WS обновления (Ошибка публикации FirstScene Task) >>>
					clientUpdateErr := ClientStoryUpdate{
						ID:           publishedStoryID.String(),
						UserID:       publishedStory.UserID.String(),
						UpdateType:   UpdateTypeStory,
						Status:       string(sharedModels.StatusError),
						ErrorDetails: &errDetails,
					}
					wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer wsCancel()
					if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdateErr); errWs != nil {
						p.logger.Error("Error sending ClientStoryUpdate (FirstScene Publish Error)", zap.String("task_id", taskID), zap.Error(errWs))
					} else {
						p.logger.Info("ClientStoryUpdate sent (FirstScene Publish Error)", zap.String("task_id", taskID), zap.String("story_id", publishedStoryID.String()))
					}
					// <<< КОНЕЦ: Отправка WS обновления (Ошибка публикации FirstScene Task) >>>
					// Не возвращаем ошибку здесь, так как статус Error уже установлен
				} else {
					p.logger.Info("First scene generation task sent successfully",
						zap.String("task_id", taskID),
						zap.String("next_task_id", nextTaskPayload.TaskID),
						zap.String("published_story_id", publishedStoryID.String()),
					)
				}
			} else {
				p.logger.Error("Setup content could not be unmarshaled, skipping first scene generation task.",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.Error(errUnmarshalSetup),
				)
			}
		} else {
			p.logger.Error("Setup content could not be unmarshaled, skipping first scene generation task.",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(errUnmarshalSetup),
			)
		}
	} else {
		p.logger.Warn("Setup Error notification received",
			zap.String("task_id", taskID),
			zap.String("published_story_id", publishedStoryID.String()),
			zap.String("error_details", notification.ErrorDetails),
		)
		if err := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtx, publishedStoryID, sharedModels.StatusError, false, false, &notification.ErrorDetails); err != nil {
			p.logger.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after Setup generation error",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(err),
			)
			// <<< НАЧАЛО: Отправка WS обновления (Ошибка генерации Setup от генератора) >>>
			clientUpdateErr := ClientStoryUpdate{
				ID:           publishedStoryID.String(),
				UserID:       publishedStory.UserID.String(),
				UpdateType:   UpdateTypeStory,
				Status:       string(sharedModels.StatusError),
				ErrorDetails: &notification.ErrorDetails,
			}
			wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer wsCancel()
			if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdateErr); errWs != nil {
				p.logger.Error("Error sending ClientStoryUpdate (Setup Generator Error)", zap.String("task_id", taskID), zap.Error(errWs))
			} else {
				p.logger.Info("ClientStoryUpdate sent (Setup Generator Error)", zap.String("task_id", taskID), zap.String("story_id", publishedStoryID.String()))
			}
			// <<< КОНЕЦ: Отправка WS обновления (Ошибка генерации Setup от генератора) >>>
		}
		return nil // Возвращаем nil, т.к. статус Error уже сохранен
	}
	return nil
}
