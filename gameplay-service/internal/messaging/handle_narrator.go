package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"novel-server/shared/constants"
	sharedModels "novel-server/shared/models"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	sharedMessaging "novel-server/shared/messaging"
)

func (p *NotificationProcessor) handleNarratorNotification(ctx context.Context, notification sharedMessaging.NotificationPayload, storyConfigID uuid.UUID) error {
	taskID := notification.TaskID
	dbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	p.logger.Info("Processing Narrator", zap.String("task_id", taskID), zap.String("story_config_id", storyConfigID.String()))

	config, err := p.repo.GetByIDInternal(dbCtx, storyConfigID)
	if err != nil {
		p.logger.Error("Error getting StoryConfig for Narrator update", zap.String("task_id", taskID), zap.String("story_config_id", storyConfigID.String()), zap.Error(err))
		return fmt.Errorf("error getting StoryConfig %s: %w", storyConfigID, err)
	}

	var clientUpdate ClientStoryUpdate
	var parseErr error
	if notification.Status == sharedMessaging.NotificationStatusSuccess {
		if config.Status != sharedModels.StatusGenerating {
			p.logger.Warn("StoryConfig not in Generating status, Narrator Success update cancelled.",
				zap.String("task_id", taskID),
				zap.String("story_config_id", storyConfigID.String()),
				zap.String("current_status", string(config.Status)),
			)
			return nil
		}
		p.logger.Info("Narrator Success notification", zap.String("task_id", taskID), zap.String("story_config_id", storyConfigID.String()))

		var rawGeneratedText string
		genResultCtx, genResultCancel := context.WithTimeout(ctx, 10*time.Second)
		genResult, genErr := p.genResultRepo.GetByTaskID(genResultCtx, taskID)
		genResultCancel()

		if genErr != nil {
			p.logger.Error("DB ERROR: Could not get GenerationResult by TaskID",
				zap.String("task_id", taskID),
				zap.String("story_config_id", storyConfigID.String()),
				zap.Error(genErr),
			)
			config.Status = sharedModels.StatusError
			parseErr = fmt.Errorf("failed to fetch generation result: %w", genErr)
		} else if genResult.Error != "" {
			p.logger.Error("TASK ERROR: GenerationResult indicates an error occurred during generation",
				zap.String("task_id", taskID),
				zap.String("story_config_id", storyConfigID.String()),
				zap.String("gen_error", genResult.Error),
			)
			config.Status = sharedModels.StatusError
			parseErr = errors.New(genResult.Error)
		} else {
			rawGeneratedText = genResult.GeneratedText
		}

		if parseErr == nil {
			jsonToParse := extractJsonContent(rawGeneratedText)
			if jsonToParse == "" {
				p.logger.Error("PARSING ERROR: Could not extract JSON from Narrator text (fetched from DB)",
					zap.String("task_id", taskID),
					zap.String("story_config_id", storyConfigID.String()),
					zap.String("raw_text_snippet", stringShort(rawGeneratedText, 100)),
				)
				config.Status = sharedModels.StatusError
				parseErr = errors.New("failed to extract JSON block from fetched result")
			} else {
				configBytes := []byte(jsonToParse)
				var generatedConfig map[string]interface{}
				jsonUnmarshalErr := json.Unmarshal(configBytes, &generatedConfig)
				if jsonUnmarshalErr == nil {
					title, titleOk := generatedConfig["t"].(string)
					desc, descOk := generatedConfig["sd"].(string)
					if titleOk && descOk && title != "" && desc != "" {
						config.Config = json.RawMessage(configBytes)
						config.Title = title
						config.Description = desc
						config.Status = sharedModels.StatusDraft
						p.logger.Info("Narrator JSON parsed and key fields extracted", zap.String("task_id", taskID), zap.String("story_config_id", storyConfigID.String()))
					} else {
						p.logger.Error("FILLING ERROR: Narrator JSON parsed, but 't' or 'sd' missing/empty",
							zap.String("task_id", taskID),
							zap.String("story_config_id", storyConfigID.String()),
							zap.Bool("title_ok", titleOk),
							zap.Bool("title_empty", title == ""),
							zap.Bool("desc_ok", descOk),
							zap.Bool("desc_empty", desc == ""),
						)
						config.Status = sharedModels.StatusError
						parseErr = errors.New("required fields 't' or 'sd' missing or empty in generated JSON")
					}
				} else {
					p.logger.Error("PARSING ERROR: Failed to parse Narrator JSON (fetched from DB)",
						zap.String("task_id", taskID),
						zap.String("story_config_id", storyConfigID.String()),
						zap.Error(jsonUnmarshalErr),
						zap.String("json_to_parse", jsonToParse),
					)
					config.Status = sharedModels.StatusError
					parseErr = jsonUnmarshalErr
				}
			}
		}
		config.UpdatedAt = time.Now().UTC()
	} else if notification.Status == sharedMessaging.NotificationStatusError {
		p.logger.Warn("Narrator Error notification received",
			zap.String("task_id", taskID),
			zap.String("story_config_id", storyConfigID.String()),
			zap.String("error_details", notification.ErrorDetails),
		)
		config.Status = sharedModels.StatusError
		config.UpdatedAt = time.Now().UTC()
	} else {
		p.logger.Warn("Unknown notification status for Narrator StoryConfig. Ignoring.",
			zap.String("task_id", taskID),
			zap.String("story_config_id", storyConfigID.String()),
			zap.String("status", string(notification.Status)),
		)
		return nil
	}

	updateErr := p.repo.Update(dbCtx, config)
	if updateErr != nil {
		p.logger.Error("CRITICAL ERROR: Failed to save StoryConfig updates (Narrator)",
			zap.String("task_id", taskID),
			zap.String("story_config_id", storyConfigID.String()),
			zap.Error(updateErr),
		)
		return fmt.Errorf("error saving StoryConfig %s: %w", storyConfigID, updateErr)
	}
	p.logger.Info("StoryConfig (Narrator) updated in DB", zap.String("task_id", taskID), zap.String("story_config_id", storyConfigID.String()), zap.String("new_status", string(config.Status)))

	if config.Status == sharedModels.StatusDraft {
		p.publishPushNotificationForDraft(ctx, config)
	} else if config.Status == sharedModels.StatusError {
		p.logger.Warn("Draft generation resulted in error, skipping push notification.",
			zap.String("storyConfigID", config.ID.String()),
			zap.String("task_id", taskID),
		)
	}

	clientUpdate = ClientStoryUpdate{ID: config.ID.String(), UserID: config.UserID.String(), UpdateType: UpdateTypeDraft, Status: string(config.Status), Title: config.Title, Description: config.Description}
	if config.Status == sharedModels.StatusError {
		var errDetails string
		if notification.ErrorDetails != "" {
			errDetails = notification.ErrorDetails
		} else if parseErr != nil {
			errDetails = fmt.Sprintf("Processing error: %v", parseErr)
		} else {
			errDetails = "Unknown processing error"
		}
		clientUpdate.ErrorDetails = &errDetails
	}
	if parseErr == nil && notification.Status == sharedMessaging.NotificationStatusSuccess {
		var generatedConfig map[string]interface{}
		if err := json.Unmarshal(config.Config, &generatedConfig); err == nil {
			if pDesc, ok := generatedConfig["p_desc"].(string); ok {
				clientUpdate.PlayerDescription = pDesc
			}
			if pp, ok := generatedConfig["pp"].(map[string]interface{}); ok {
				if thRaw, ok := pp["th"].([]interface{}); ok {
					clientUpdate.Themes = castToStringSlice(thRaw)
				}
				if wlRaw, ok := pp["wl"].([]interface{}); ok {
					clientUpdate.WorldLore = castToStringSlice(wlRaw)
				}
			}
		} else {
			log.Printf("[processor][TaskID: %s] Ошибка повторного парсинга JSON Narrator для StoryConfig %s: %v", taskID, storyConfigID, err)
		}
	}
	pubCtx, pubCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := p.clientPub.PublishClientUpdate(pubCtx, clientUpdate); err != nil {
		p.logger.Error("Error sending ClientStoryUpdate (Narrator)", zap.String("task_id", taskID), zap.String("story_id", config.ID.String()), zap.Error(err))
	} else {
		p.logger.Info("ClientStoryUpdate sent (Narrator)", zap.String("task_id", taskID), zap.String("story_id", config.ID.String()))
	}
	pubCancel()

	return nil
}

func (p *NotificationProcessor) publishPushNotificationForDraft(ctx context.Context, config *sharedModels.StoryConfig) {
	if config == nil {
		p.logger.Error("Attempted to send push notification for nil StoryConfig")
		return
	}

	data := map[string]string{
		"storyConfigId":                config.ID.String(),
		"eventType":                    string(sharedModels.StatusDraft),
		constants.PushLocKey:           constants.PushLocKeyDraftReady,
		constants.PushLocArgStoryTitle: config.Title,
		constants.PushFallbackTitleKey: "Draft Ready!",
		constants.PushFallbackBodyKey:  fmt.Sprintf("Your draft \"%s\" is ready for setup.", config.Title),
		"title":                        config.Title,
	}

	payload := PushNotificationPayload{
		UserID: config.UserID,
		Notification: PushNotification{
			Title: data[constants.PushFallbackTitleKey],
			Body:  data[constants.PushFallbackBodyKey],
		},
		Data: data,
	}

	if err := p.pushPub.PublishPushNotification(ctx, payload); err != nil {
		p.logger.Error("Failed to publish push notification event for draft",
			zap.String("userID", payload.UserID.String()),
			zap.String("storyConfigID", config.ID.String()),
			zap.Error(err),
		)
	} else {
		p.logger.Info("Push notification event for draft published successfully",
			zap.String("userID", payload.UserID.String()),
			zap.String("storyConfigID", config.ID.String()),
		)
	}
}
