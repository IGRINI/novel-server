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
	"novel-server/shared/notifications"
	"novel-server/shared/utils"
)

const (
	consumeConcurrencyNarrator = 5
	retryCount                 = 3
	retryDelay                 = 5 * time.Second
	maxGeneratedImageCount     = 10
	maxSceneImageCount         = 4
	maxSetupImageCount         = 4

	DefaultImagePrompt = "fantasy art, ((masterpiece)), illustration, epic composition, high quality, detailed, BREAK Cinematic, dramatic lighting, vibrant colors, perfect anatomy, beautiful face"
)

func (p *NotificationProcessor) publishClientDraftUpdate(ctx context.Context, storyConfig *sharedModels.StoryConfig, errorMsg *string) {
	if storyConfig == nil {
		p.logger.Error("publishClientDraftUpdate called with nil StoryConfig")
		return
	}

	update := sharedModels.ClientStoryUpdate{
		ID:         storyConfig.ID.String(),
		UserID:     storyConfig.UserID.String(),
		UpdateType: sharedModels.UpdateTypeDraft,
		Status:     string(storyConfig.Status),
	}

	if errorMsg != nil && *errorMsg != "" {
		update.ErrorDetails = errorMsg
	}

	if err := p.clientPub.PublishClientUpdate(ctx, update); err != nil {
		p.logger.Error("Failed to publish client draft update event",
			zap.String("updateType", string(update.UpdateType)),
			zap.String("status", update.Status),
			zap.String("storyConfigID", update.ID),
			zap.String("userID", update.UserID),
			zap.Error(err),
		)
	} else {
		p.logger.Info("Client draft update event published successfully",
			zap.String("updateType", string(update.UpdateType)),
			zap.String("status", update.Status),
			zap.String("storyConfigID", update.ID),
			zap.String("userID", update.UserID),
		)
	}
}

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
			jsonToParse := utils.ExtractJsonContent(rawGeneratedText)
			if jsonToParse == "" {
				p.logger.Error("PARSING ERROR: Could not extract JSON from Narrator text (fetched)",
					zap.String("task_id", taskID),
					zap.String("story_config_id", storyConfigID.String()),
					zap.String("raw_text_snippet", utils.StringShort(rawGeneratedText, 100)),
				)
				config.Status = sharedModels.StatusError
				parseErr = errors.New("failed to extract JSON block from Narrator text")
			} else {
				var configJSON json.RawMessage
				configJSON = json.RawMessage(jsonToParse)
				config.Config = configJSON
				config.Status = sharedModels.StatusDraft
				p.logger.Info("Successfully parsed and updated StoryConfig.Config", zap.String("task_id", taskID), zap.String("story_config_id", storyConfigID.String()))
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

	var errorDetails *string
	if parseErr != nil {
		errStr := parseErr.Error()
		errorDetails = &errStr
	} else if notification.Status == sharedMessaging.NotificationStatusError {
		errorDetails = &notification.ErrorDetails
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
		go p.publishClientDraftUpdate(ctx, config, nil)
		p.publishPushNotificationForDraft(ctx, config)
	} else if config.Status == sharedModels.StatusError {
		go p.publishClientDraftUpdate(ctx, config, errorDetails)
		p.logger.Warn("Draft generation resulted in error, skipping push notification.",
			zap.String("storyConfigID", config.ID.String()),
			zap.String("task_id", taskID),
		)
	}

	return nil
}

func (p *NotificationProcessor) publishPushNotificationForDraft(ctx context.Context, config *sharedModels.StoryConfig) {
	payload, err := notifications.BuildDraftReadyPushPayload(config)
	if err != nil {
		p.logger.Error("Failed to build push notification payload for draft", zap.Error(err))
		return
	}

	if err := p.pushPub.PublishPushNotification(ctx, *payload); err != nil {
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
