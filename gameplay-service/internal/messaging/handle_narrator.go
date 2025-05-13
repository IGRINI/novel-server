package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"novel-server/shared/database"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/notifications"

	"github.com/jackc/pgx/v5"
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
	dbCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	p.logger.Info("Processing Narrator", zap.String("task_id", taskID), zap.String("story_config_id", storyConfigID.String()))

	var config *sharedModels.StoryConfig
	var parseErr error
	var genResultErrorDetail string
	var finalErrorDetails *string
	var commitSuccessful bool = false

	tx, errTx := p.db.Begin(dbCtx)
	if errTx != nil {
		p.logger.Error("Failed to begin transaction for Narrator update", zap.Error(errTx))
		errStr := fmt.Sprintf("internal server error (db transaction): %v", errTx)
		tempConfigForError, _ := p.repo.GetByIDInternal(context.Background(), storyConfigID)
		if tempConfigForError != nil {
			go p.publishClientDraftUpdate(context.Background(), tempConfigForError, &errStr)
		}
		return fmt.Errorf("failed to begin db transaction: %w", errTx)
	}

	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("Panic recovered during Narrator transaction", zap.Any("panic", r))
			_ = tx.Rollback(context.Background())
		} else if errTx != nil {
			p.logger.Warn("Rolling back transaction due to error during Narrator processing", zap.Error(errTx))
			_ = tx.Rollback(dbCtx)
		} else if commitSuccessful {
		} else {
			p.logger.Info("Committing transaction for Narrator processing (no prior error, no explicit commit yet)")
			if errCommit := tx.Commit(dbCtx); errCommit != nil {
				p.logger.Error("Failed to commit Narrator transaction", zap.Error(errCommit))
				errTx = errCommit
			}
		}
	}()

	errTx = func(tx pgx.Tx) error {
		txRepo := database.NewPgStoryConfigRepository(tx, p.logger)
		txGenResultRepo := database.NewPgGenerationResultRepository(tx, p.logger)

		var errGetConfig error
		config, errGetConfig = txRepo.GetByIDInternal(dbCtx, storyConfigID)
		if errGetConfig != nil {
			p.logger.Error("Error getting StoryConfig for Narrator update within transaction", zap.Error(errGetConfig))
			return fmt.Errorf("error getting StoryConfig %s: %w", storyConfigID, errGetConfig)
		}

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
			genResult, genErr := txGenResultRepo.GetByTaskID(dbCtx, taskID)

			if genErr != nil {
				p.logger.Error("DB ERROR (Tx): Could not get GenerationResult by TaskID", zap.Error(genErr))
				config.Status = sharedModels.StatusError
				parseErr = fmt.Errorf("failed to fetch generation result: %w", genErr)
				genResultErrorDetail = parseErr.Error()
			} else if genResult.Error != "" {
				p.logger.Error("TASK ERROR (Tx): GenerationResult indicates an error", zap.String("gen_error", genResult.Error))
				config.Status = sharedModels.StatusError
				parseErr = errors.New(genResult.Error)
				genResultErrorDetail = genResult.Error
			} else {
				rawGeneratedText = genResult.GeneratedText
			}

			if parseErr == nil {
				var tempConfigForValidation sharedModels.Config
				if errValidate := json.Unmarshal([]byte(rawGeneratedText), &tempConfigForValidation); errValidate != nil {
					p.logger.Error("NARRATOR UNMARSHAL (Full Config) (Tx): Failed to validate generated JSON", zap.Error(errValidate))
					config.Status = sharedModels.StatusError
					parseErr = fmt.Errorf("generated JSON validation failed: %w", errValidate)
					genResultErrorDetail = parseErr.Error()
				} else {
					config.Config = json.RawMessage(rawGeneratedText)
					config.Status = sharedModels.StatusDraft
					config.Title = tempConfigForValidation.Title
					config.Description = tempConfigForValidation.ShortDescription
					p.logger.Info("Successfully validated and updated StoryConfig Title, Description (Tx)", zap.String("new_title", config.Title))
				}
				config.UpdatedAt = time.Now().UTC()
			} else {
				p.logger.Error("Processing Narrator failed before JSON handling (Tx)", zap.Error(parseErr))
				config.Status = sharedModels.StatusError
				config.UpdatedAt = time.Now().UTC()
			}
		} else if notification.Status == sharedMessaging.NotificationStatusError {
			p.logger.Warn("Narrator Error notification received (Tx)", zap.String("error_details", notification.ErrorDetails))
			config.Status = sharedModels.StatusError
			config.UpdatedAt = time.Now().UTC()
			genResultErrorDetail = notification.ErrorDetails
		} else {
			p.logger.Warn("Unknown notification status for Narrator StoryConfig. Ignoring (Tx).")
			return nil
		}

		if parseErr != nil {
			finalErrorDetails = &genResultErrorDetail
		} else if notification.Status == sharedMessaging.NotificationStatusError {
			finalErrorDetails = &notification.ErrorDetails
		}

		updateErr := txRepo.Update(dbCtx, config)
		if updateErr != nil {
			p.logger.Error("CRITICAL ERROR (Tx): Failed to save StoryConfig updates (Narrator)", zap.Error(updateErr))
			return fmt.Errorf("error saving StoryConfig %s: %w", storyConfigID, updateErr)
		}
		p.logger.Info("StoryConfig (Narrator) updated in DB (Tx)", zap.String("new_status", string(config.Status)))
		return nil
	}(tx)

	if errTx != nil {
		if finalErrorDetails == nil {
			errStr := errTx.Error()
			finalErrorDetails = &errStr
		}
		if config != nil {
			go p.publishClientDraftUpdate(context.Background(), config, finalErrorDetails)
		}
		return errTx
	}

	if errCommit := tx.Commit(dbCtx); errCommit != nil {
		p.logger.Error("Failed to commit Narrator transaction", zap.Error(errCommit))
		errStr := errCommit.Error()
		finalErrorDetails = &errStr
		if config != nil {
			go p.publishClientDraftUpdate(context.Background(), config, finalErrorDetails)
		}
		return fmt.Errorf("failed to commit Narrator transaction: %w", errCommit)
	}
	commitSuccessful = true
	p.logger.Info("Narrator transaction committed successfully")

	if config.Status == sharedModels.StatusDraft {
		go p.publishClientDraftUpdate(context.Background(), config, nil)
		p.publishPushNotificationForDraft(context.Background(), config)
	} else if config.Status == sharedModels.StatusError {
		go p.publishClientDraftUpdate(context.Background(), config, finalErrorDetails)
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
