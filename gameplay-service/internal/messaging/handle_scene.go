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

func (p *NotificationProcessor) handleSceneGenerationNotification(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	dbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	p.logger.Info("Processing Scene/GameOver",
		zap.String("task_id", taskID),
		zap.String("published_story_id", publishedStoryID.String()),
		zap.String("state_hash", notification.StateHash),
	)

	if notification.Status == sharedMessaging.NotificationStatusSuccess {
		p.logger.Info("Scene/GameOver Success notification",
			zap.String("task_id", taskID),
			zap.String("published_story_id", publishedStoryID.String()),
			zap.String("state_hash", notification.StateHash),
		)
		rawGeneratedText := notification.GeneratedText
		jsonToParse := extractJsonContent(rawGeneratedText) // Helper from consumer.go
		if jsonToParse == "" {
			p.logger.Error("SCENE PARSING ERROR: Could not extract JSON from Scene/GameOver text",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.String("state_hash", notification.StateHash),
				zap.String("raw_text_snippet", stringShort(rawGeneratedText, 100)), // Helper from consumer.go
			)
			return errors.New("failed to extract JSON block from Scene/GameOver text")
		}

		sceneContentJSON := json.RawMessage(jsonToParse)
		newStatus := sharedModels.StatusReady
		var endingText *string

		if notification.PromptType == sharedMessaging.PromptTypeNovelGameOverCreator {
			var endingContent struct {
				EndingText string `json:"et"`
			}
			if err := json.Unmarshal(sceneContentJSON, &endingContent); err != nil {
				p.logger.Error("ERROR: Failed to parse game over JSON",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.Error(err),
					zap.String("json_to_parse", jsonToParse),
				)
				// Don't set endingText if parsing failed
			} else {
				endingText = &endingContent.EndingText
				p.logger.Info("Extracted EndingText", zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()))
			}
		}

		scene := &sharedModels.StoryScene{
			ID:               uuid.New(),
			PublishedStoryID: publishedStoryID,
			StateHash:        notification.StateHash,
			Content:          sceneContentJSON,
			CreatedAt:        time.Now().UTC(),
		}

		upsertErr := p.sceneRepo.Upsert(dbCtx, scene)
		if upsertErr != nil {
			p.logger.Error("CRITICAL ERROR: Failed to upsert scene",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.String("state_hash", notification.StateHash),
				zap.Error(upsertErr),
			)
			return fmt.Errorf("error upserting scene for PublishedStory %s, Hash %s: %w", publishedStoryID, notification.StateHash, upsertErr)
		}
		p.logger.Info("Scene upserted successfully",
			zap.String("task_id", taskID),
			zap.String("published_story_id", publishedStoryID.String()),
			zap.String("state_hash", notification.StateHash),
			zap.String("scene_id", scene.ID.String()),
		)

		if notification.PromptType != sharedMessaging.PromptTypeNovelGameOverCreator {
			if err := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, newStatus, nil, nil, nil, nil); err != nil {
				p.logger.Error("CRITICAL ERROR (Data Inconsistency!): Scene upserted, but failed to update PublishedStory status",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.String("state_hash", notification.StateHash),
					zap.String("scene_id", scene.ID.String()),
					zap.Error(err),
				)
				// Не возвращаем ошибку здесь, т.к. сцена уже сохранена, но логируем как критическую
			} else {
				p.logger.Info("PublishedStory status updated to Ready",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
				)
			}
		}

		if notification.GameStateID != "" {
			gameStateID, errParse := uuid.Parse(notification.GameStateID)
			if errParse != nil {
				p.logger.Error("ERROR: Failed to parse GameStateID from notification",
					zap.String("task_id", taskID),
					zap.String("game_state_id", notification.GameStateID),
					zap.Error(errParse),
				)
			} else {
				gameState, errGetState := p.playerGameStateRepo.GetByID(ctx, gameStateID)
				if errGetState != nil {
					p.logger.Error("ERROR: Failed to get PlayerGameState by ID",
						zap.String("task_id", taskID),
						zap.String("game_state_id", notification.GameStateID),
						zap.Error(errGetState),
					)
				} else {
					if notification.PromptType == sharedMessaging.PromptTypeNovelGameOverCreator {
						gameState.PlayerStatus = sharedModels.PlayerStatusCompleted
						gameState.EndingText = endingText
						now := time.Now().UTC()
						gameState.CompletedAt = &now
						gameState.CurrentSceneID = &scene.ID
					} else {
						gameState.PlayerStatus = sharedModels.PlayerStatusPlaying
						gameState.CurrentSceneID = &scene.ID
					}
					gameState.ErrorDetails = nil
					gameState.LastActivityAt = time.Now().UTC()

					if _, errSaveState := p.playerGameStateRepo.Save(ctx, gameState); errSaveState != nil {
						p.logger.Error("ERROR: Failed to save updated PlayerGameState",
							zap.String("task_id", taskID),
							zap.String("game_state_id", notification.GameStateID),
							zap.Error(errSaveState),
						)
					} else {
						p.logger.Info("PlayerGameState updated successfully",
							zap.String("task_id", taskID),
							zap.String("game_state_id", notification.GameStateID),
							zap.String("new_player_status", string(gameState.PlayerStatus)),
						)
					}
				}
			}
		} else {
			p.logger.Warn("GameStateID missing in notification, player status not updated.",
				zap.String("task_id", taskID),
				zap.String("prompt_type", string(notification.PromptType)),
			)
		}

		pubStory, getErr := p.publishedRepo.GetByID(dbCtx, publishedStoryID)
		if getErr != nil {
			p.logger.Error("ERROR: Failed to get PublishedStory for Push sending",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(getErr),
			)
		} else {
			playerStatus := sharedModels.PlayerStatusPlaying
			if notification.PromptType == sharedMessaging.PromptTypeNovelGameOverCreator {
				playerStatus = sharedModels.PlayerStatusCompleted
			}

			if newStatus == sharedModels.StatusReady || playerStatus == sharedModels.PlayerStatusCompleted {
				if pubStory != nil {
					pushPayload := PushNotificationPayload{
						UserID:       pubStory.UserID,
						Notification: PushNotification{},
						Data: map[string]string{
							"type":       UpdateTypeStory,
							"entity_id":  publishedStoryID.String(),
							"status":     string(newStatus),
							"scene_id":   scene.ID.String(),
							"state_hash": notification.StateHash,
						},
					}
					storyTitle := "История"
					if pubStory.Title != nil && *pubStory.Title != "" {
						storyTitle = *pubStory.Title
					}

					if playerStatus == sharedModels.PlayerStatusCompleted {
						pushPayload.Notification.Title = fmt.Sprintf("История '%s' завершена!", storyTitle)
						if endingText != nil && *endingText != "" {
							pushPayload.Notification.Body = fmt.Sprintf("Ваше приключение подошло к концу: %s", *endingText)
						} else {
							pushPayload.Notification.Body = "Ваше приключение подошло к концу."
						}
						pushPayload.Data["status"] = string(sharedModels.PlayerStatusCompleted)
					} else {
						pushPayload.Notification.Title = fmt.Sprintf("'%s': Новая сцена готова!", storyTitle)
						pushPayload.Notification.Body = "Продолжите ваше приключение."
					}

					pushCtx, pushCancel := context.WithTimeout(context.Background(), 10*time.Second)
					if errPush := p.pushPub.PublishPushNotification(pushCtx, pushPayload); errPush != nil {
						p.logger.Error("Error sending Push notification (Scene/GameOver)",
							zap.String("task_id", taskID),
							zap.String("published_story_id", publishedStoryID.String()),
							zap.Error(errPush),
						)
					} else {
						p.logger.Info("Push notification sent (Scene/GameOver)",
							zap.String("task_id", taskID),
							zap.String("published_story_id", publishedStoryID.String()),
							zap.String("player_status", string(playerStatus)),
						)
					}
					pushCancel()
				}
			} else {
				p.logger.Info("PUSH notification skipped for Scene/GameOver",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.String("story_status", string(newStatus)),
					zap.String("player_status", string(playerStatus)),
				)
			}
		}

	} else {
		p.logger.Warn("Scene/GameOver Error notification received",
			zap.String("task_id", taskID),
			zap.String("published_story_id", publishedStoryID.String()),
			zap.String("state_hash", notification.StateHash),
			zap.String("error_details", notification.ErrorDetails),
		)
		if notification.GameStateID != "" {
			gameStateID, errParse := uuid.Parse(notification.GameStateID)
			if errParse != nil {
				p.logger.Error("ERROR: Failed to parse GameStateID from error notification",
					zap.String("task_id", taskID),
					zap.String("game_state_id", notification.GameStateID),
					zap.Error(errParse),
				)
			} else {
				gameState, errGetState := p.playerGameStateRepo.GetByID(ctx, gameStateID)
				if errGetState != nil {
					p.logger.Error("ERROR: Failed to get PlayerGameState by ID to update error",
						zap.String("task_id", taskID),
						zap.String("game_state_id", notification.GameStateID),
						zap.Error(errGetState),
					)
				} else {
					gameState.PlayerStatus = sharedModels.PlayerStatusError
					gameState.ErrorDetails = &notification.ErrorDetails
					gameState.LastActivityAt = time.Now().UTC()
					if _, errSaveState := p.playerGameStateRepo.Save(ctx, gameState); errSaveState != nil {
						p.logger.Error("ERROR: Failed to save PlayerGameState with Error status",
							zap.String("task_id", taskID),
							zap.String("game_state_id", notification.GameStateID),
							zap.Error(errSaveState),
						)
					} else {
						p.logger.Info("PlayerGameState updated to Error status successfully",
							zap.String("task_id", taskID),
							zap.String("game_state_id", notification.GameStateID),
						)
					}
				}
			}
		} else {
			p.logger.Warn("GameStateID missing in error notification, player status not updated.",
				zap.String("task_id", taskID),
				zap.String("prompt_type", string(notification.PromptType)),
			)
		}
	}
	return nil
}
