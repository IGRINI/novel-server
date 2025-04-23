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

	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
)

func (p *NotificationProcessor) handleNarratorNotification(ctx context.Context, notification sharedMessaging.NotificationPayload, storyConfigID uuid.UUID) error {
	taskID := notification.TaskID // Получаем taskID из уведомления
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
		rawGeneratedText := notification.GeneratedText
		jsonToParse := extractJsonContent(rawGeneratedText) // Helper from consumer.go
		if jsonToParse == "" {
			p.logger.Error("PARSING ERROR: Could not extract JSON from Narrator text",
				zap.String("task_id", taskID),
				zap.String("story_config_id", storyConfigID.String()),
				zap.String("raw_text_snippet", stringShort(rawGeneratedText, 100)), // Helper from consumer.go
			)
			config.Status = sharedModels.StatusError
			parseErr = errors.New("failed to extract JSON block")
		} else {
			configBytes := []byte(jsonToParse)
			var generatedConfig map[string]interface{}
			parseErr = json.Unmarshal(configBytes, &generatedConfig)
			if parseErr == nil {
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
				}
			} else {
				p.logger.Error("PARSING ERROR: Failed to parse Narrator JSON",
					zap.String("task_id", taskID),
					zap.String("story_config_id", storyConfigID.String()),
					zap.Error(parseErr),
					zap.String("json_to_parse", jsonToParse),
				)
				config.Status = sharedModels.StatusError
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

	if config.Status == sharedModels.StatusDraft || config.Status == sharedModels.StatusError {
		pushPayload := PushNotificationPayload{UserID: config.UserID, Notification: PushNotification{}, Data: map[string]string{"type": UpdateTypeDraft, "entity_id": config.ID.String(), "status": string(config.Status)}}
		pushCtx, pushCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if errPush := p.pushPub.PublishPushNotification(pushCtx, pushPayload); errPush != nil {
			p.logger.Error("Error sending Push notification (Narrator)", zap.String("task_id", taskID), zap.String("story_id", config.ID.String()), zap.Error(errPush))
		} else {
			p.logger.Info("Push notification sent (Narrator)", zap.String("task_id", taskID), zap.String("story_id", config.ID.String()))
		}
		pushCancel()
	}

	clientUpdate = ClientStoryUpdate{ID: config.ID.String(), UserID: config.UserID.String(), UpdateType: UpdateTypeDraft, Status: string(config.Status), Title: config.Title, Description: config.Description}
	if config.Status == sharedModels.StatusError {
		if notification.ErrorDetails != "" {
			errDetails := notification.ErrorDetails
			clientUpdate.ErrorDetails = &errDetails
		} else if parseErr != nil {
			errDetails := fmt.Sprintf("JSON parsing error: %v", parseErr)
			clientUpdate.ErrorDetails = &errDetails
		} else {
			clientUpdate.ErrorDetails = nil
		}
	}
	if parseErr == nil && notification.Status == sharedMessaging.NotificationStatusSuccess {
		var generatedConfig map[string]interface{}
		if err := json.Unmarshal(config.Config, &generatedConfig); err == nil {
			if pDesc, ok := generatedConfig["p_desc"].(string); ok {
				clientUpdate.PlayerDescription = pDesc
			}
			if pp, ok := generatedConfig["pp"].(map[string]interface{}); ok {
				if thRaw, ok := pp["th"].([]interface{}); ok {
					clientUpdate.Themes = castToStringSlice(thRaw) // Helper from consumer.go
				}
				if wlRaw, ok := pp["wl"].([]interface{}); ok {
					clientUpdate.WorldLore = castToStringSlice(wlRaw) // Helper from consumer.go
				}
			}
		} else {
			log.Printf("[processor][TaskID: %s] Ошибка повторного парсинга JSON Narrator для StoryConfig %s: %v", taskID, storyConfigID, err) // Keep old log for now
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
