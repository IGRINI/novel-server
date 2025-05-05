package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"novel-server/shared/constants"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/notifications"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// stringRef возвращает указатель на строку.
func stringRef(s string) *string {
	return &s
}

func (p *NotificationProcessor) handleSceneGenerationNotification(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	dbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	gameStateID, errParseStateID := uuid.Parse(notification.GameStateID)
	if errParseStateID != nil {
		p.logger.Error("ERROR: Failed to parse GameStateID from scene notification", zap.Error(errParseStateID))
		return fmt.Errorf("invalid GameStateID: %w", errParseStateID)
	}
	_ = gameStateID

	logWithState := p.logger.With(
		zap.String("task_id", taskID),
		zap.String("published_story_id", publishedStoryID.String()),
		zap.String("state_hash", notification.StateHash),
	)
	logWithState.Info("Processing Scene/GameOver")

	var parseErr error

	if notification.Status == sharedMessaging.NotificationStatusSuccess {
		logWithState.Info("Scene/GameOver Success notification")

		var rawGeneratedText string

		genResultCtx, genResultCancel := context.WithTimeout(ctx, 10*time.Second)
		genResult, genErr := p.genResultRepo.GetByTaskID(genResultCtx, taskID)
		genResultCancel()

		if genErr != nil {
			logWithState.Error("DB ERROR (Scene): Could not get GenerationResult by TaskID", zap.Error(genErr))
			parseErr = fmt.Errorf("failed to fetch generation result: %w", genErr)
		} else if genResult.Error != "" {
			logWithState.Error("TASK ERROR (Scene): GenerationResult indicates an error", zap.String("gen_error", genResult.Error))
			parseErr = errors.New(genResult.Error)
		} else {
			rawGeneratedText = genResult.GeneratedText
			logWithState.Debug("Successfully fetched GeneratedText from DB")
		}

		if parseErr == nil {
			// <<< НОВЫЙ БЛОК: Обработка успеха (rawGeneratedText получен) >>>
			sceneContentJSON := json.RawMessage(rawGeneratedText)
			var endingText *string

			// Попытка извлечь endingText, если это GameOver (не критично, если не получится)
			if notification.PromptType == sharedModels.PromptTypeNovelGameOverCreator {
				var endingContent struct {
					EndingText string `json:"et"`
				}
				if err := json.Unmarshal(sceneContentJSON, &endingContent); err == nil {
					endingText = &endingContent.EndingText
					p.logger.Info("Extracted EndingText from GameOver JSON", zap.String("task_id", taskID))
				} else {
					p.logger.Warn("Failed to extract optional EndingText ('et') from GameOver JSON",
						zap.String("task_id", taskID),
						zap.String("published_story_id", publishedStoryID.String()),
						zap.Error(err),
					)
				}
			}

			// Сразу создаем и сохраняем сцену, доверяя данным от генератора
			scene := &sharedModels.StoryScene{
				ID:               uuid.New(),
				PublishedStoryID: publishedStoryID,
				StateHash:        notification.StateHash,
				Content:          sceneContentJSON, // Сохраняем pre-validated JSON
				CreatedAt:        time.Now().UTC(),
			}

			upsertErr := p.sceneRepo.Upsert(dbCtx, scene)
			if upsertErr != nil {
				logWithState.Error("CRITICAL ERROR: Failed to upsert scene (post-generator validation)", zap.Error(upsertErr))
				parseErr = fmt.Errorf("error upserting scene: %w", upsertErr)
				errDetails := parseErr.Error()
				dbCtxUpdateStory, cancelUpdateStory := context.WithTimeout(ctx, 10*time.Second)
				if errUpdateStory := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtxUpdateStory, publishedStoryID, sharedModels.StatusError, false, false, &errDetails); errUpdateStory != nil {
					logWithState.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after scene upsert failure", zap.Error(errUpdateStory))
				} else {
					logWithState.Info("PublishedStory status updated to Error successfully after scene upsert failure")
				}
				cancelUpdateStory()

				storyForWsUpdate, errGetStory := p.publishedRepo.GetByID(dbCtx, publishedStoryID)
				if errGetStory != nil {
					logWithState.Error("Failed to get PublishedStory for WebSocket update after scene upsert error", zap.Error(errGetStory))
				} else {
					clientUpdateError := sharedModels.ClientStoryUpdate{
						ID:           publishedStoryID.String(),
						UserID:       storyForWsUpdate.UserID.String(),
						UpdateType:   sharedModels.UpdateTypeStory,
						Status:       string(sharedModels.StatusError),
						ErrorDetails: stringRef(errDetails),
						StateHash:    notification.StateHash,
					}
					wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
					if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdateError); errWs != nil {
						logWithState.Error("Error sending ClientStoryUpdate (Scene Upsert Error)", zap.Error(errWs))
					} else {
						logWithState.Info("ClientStoryUpdate sent (Scene Upsert Error)")
					}
					wsCancel()
				}

				if notification.GameStateID != "" {
					gameStateIDOnError, errParseStateIDOnError := uuid.Parse(notification.GameStateID)
					if errParseStateIDOnError != nil {
						logWithState.Error("ERROR: Failed to parse GameStateID from notification during scene upsert error handling", zap.Error(errParseStateIDOnError))
					} else {
						gameState, errGetState := p.playerGameStateRepo.GetByID(ctx, gameStateIDOnError)
						if errGetState != nil {
							logWithState.Error("ERROR: Failed to get PlayerGameState by ID to update error after scene upsert failure", zap.Error(errGetState))
						} else {
							gameState.PlayerStatus = sharedModels.PlayerStatusError
							gameState.ErrorDetails = stringRef(errDetails)
							gameState.LastActivityAt = time.Now().UTC()
							if _, errSaveState := p.playerGameStateRepo.Save(ctx, gameState); errSaveState != nil {
								logWithState.Error("ERROR: Failed to save PlayerGameState with Error status after scene upsert failure", zap.Error(errSaveState))
							} else {
								logWithState.Info("PlayerGameState updated to Error status successfully after scene upsert failure")
							}
						}
					}
				} else {
					logWithState.Warn("GameStateID missing in scene upsert error notification.")
				}
				return parseErr // Возвращаем ошибку upsert
			} else {
				logWithState.Info("Scene upserted successfully", zap.String("scene_id", scene.ID.String()))
				var finalStatusToSet *sharedModels.StoryStatus
				var currentStoryState *sharedModels.PublishedStory
				currentStoryState, errState := p.publishedRepo.GetByID(dbCtx, publishedStoryID)
				if errState != nil {
					logWithState.Error("Failed to re-fetch story state after scene upsert", zap.Error(errState))
				} else {
					isGameOverScene := notification.PromptType == sharedModels.PromptTypeNovelGameOverCreator
					if !isGameOverScene && currentStoryState.Status != sharedModels.StatusError && !currentStoryState.IsFirstScenePending && !currentStoryState.AreImagesPending {
						status := sharedModels.StatusReady
						finalStatusToSet = &status
						logWithState.Info("Story is ready after scene generation (flags and status checked)")
						if notification.StateHash == sharedModels.InitialStateHash {
							go p.publishPushNotificationForStoryReady(context.WithoutCancel(ctx), currentStoryState)
						}
					} else if !isGameOverScene {
						logWithState.Info("Story is not yet ready after scene generation",
							zap.String("current_status", string(currentStoryState.Status)),
							zap.Bool("is_first_scene_pending", currentStoryState.IsFirstScenePending),
							zap.Bool("are_images_pending", currentStoryState.AreImagesPending),
						)
					}
				}

				var statusToUpdate sharedModels.StoryStatus
				needsStatusUpdate := false
				if finalStatusToSet != nil {
					statusToUpdate = *finalStatusToSet
					needsStatusUpdate = true
				}

				if needsStatusUpdate {
					isFirstPending := false
					areImagesPending := false
					errUpdate := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtx, publishedStoryID, statusToUpdate, isFirstPending, areImagesPending, nil)
					if errUpdate != nil {
						logWithState.Error("CRITICAL ERROR (Data Inconsistency!): Scene upserted, but failed to update PublishedStory status/flags", zap.Error(errUpdate))
					} else {
						logWithState.Info("PublishedStory status/flags updated after scene generation",
							zap.String("updated_status", string(statusToUpdate)),
							zap.Bool("is_first_scene_pending", isFirstPending),
							zap.Bool("are_images_pending", areImagesPending),
						)
					}
				}

				if notification.GameStateID != "" {
					gameStateIDSuccess, errParseSuccess := uuid.Parse(notification.GameStateID)
					if errParseSuccess != nil {
						logWithState.Error("ERROR: Failed to parse GameStateID from notification after successful Scene Upsert", zap.Error(errParseSuccess))
					} else {
						gameState, errGetState := p.playerGameStateRepo.GetByID(ctx, gameStateIDSuccess)
						if errGetState != nil {
							logWithState.Error("ERROR: Failed to get PlayerGameState by ID after successful Scene Upsert", zap.Error(errGetState))
						} else {
							if notification.PromptType == sharedModels.PromptTypeNovelGameOverCreator {
								gameState.PlayerStatus = sharedModels.PlayerStatusCompleted
								gameState.EndingText = endingText // Используем извлеченный ранее
								now := time.Now().UTC()
								gameState.CompletedAt = sql.NullTime{Time: now, Valid: true}
								gameState.CurrentSceneID = uuid.NullUUID{UUID: scene.ID, Valid: true}
							} else {
								gameState.PlayerStatus = sharedModels.PlayerStatusPlaying
								gameState.CurrentSceneID = uuid.NullUUID{UUID: scene.ID, Valid: true}
							}
							gameState.ErrorDetails = nil
							gameState.LastActivityAt = time.Now().UTC()

							if _, errSaveState := p.playerGameStateRepo.Save(ctx, gameState); errSaveState != nil {
								logWithState.Error("ERROR: Failed to save updated PlayerGameState after successful Scene Upsert", zap.Error(errSaveState))
							} else {
								logWithState.Info("PlayerGameState updated successfully after successful Scene Upsert", zap.String("new_player_status", string(gameState.PlayerStatus)))

								if gameState.PlayerProgressID != uuid.Nil {
									progressID := gameState.PlayerProgressID
									var sceneSummaryContent struct {
										Summary *string `json:"sssf"`
									}
									if errUnmarshal := json.Unmarshal(sceneContentJSON, &sceneSummaryContent); errUnmarshal != nil {
										logWithState.Warn("Failed to unmarshal scene JSON to extract summary (sssf field) for PlayerProgress update", zap.Error(errUnmarshal))
									} else if sceneSummaryContent.Summary != nil {
										updates := map[string]interface{}{"current_scene_summary": *sceneSummaryContent.Summary}
										if errUpdateProgress := p.playerProgressRepo.UpdateFields(ctx, progressID, updates); errUpdateProgress != nil {
											logWithState.Error("ERROR: Failed to update current_scene_summary in PlayerProgress", zap.String("progress_id", progressID.String()), zap.Error(errUpdateProgress))
										} else {
											logWithState.Info("PlayerProgress current_scene_summary updated successfully", zap.String("progress_id", progressID.String()))
										}
									}
								}

								finalPubStory, errGetStory := p.publishedRepo.GetByID(dbCtx, publishedStoryID)
								if errGetStory != nil {
									logWithState.Error("Failed to get PublishedStory for WebSocket update after Scene Success", zap.Error(errGetStory))
								} else {
									wsEvent := sharedModels.ClientStoryUpdate{
										ID:         publishedStoryID.String(),
										UserID:     finalPubStory.UserID.String(),
										UpdateType: sharedModels.UpdateTypeStory,
										Status:     string(finalPubStory.Status),
										SceneID:    stringRef(scene.ID.String()),
										StateHash:  notification.StateHash,
										EndingText: endingText,
									}
									wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
									if errWs := p.clientPub.PublishClientUpdate(wsCtx, wsEvent); errWs != nil {
										logWithState.Error("Error sending ClientStoryUpdate (Scene Success)", zap.Error(errWs))
									} else {
										logWithState.Info("ClientStoryUpdate sent (Scene Success)", zap.String("status", wsEvent.Status))
									}
									wsCancel()
								}
								p.publishPushNotificationForScene(ctx, gameState, scene, publishedStoryID)
							}
						}
					}
				}
			}
		}

	} else {
		logWithState.Warn("Scene/GameOver Error notification received",
			zap.String("task_id", taskID),
			zap.String("published_story_id", publishedStoryID.String()),
			zap.String("state_hash", notification.StateHash),
			zap.String("error_details", notification.ErrorDetails),
		)

		var storyForWsUpdate *sharedModels.PublishedStory
		if errUpdateStory := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtx, publishedStoryID, sharedModels.StatusError, false, false, &notification.ErrorDetails); errUpdateStory != nil {
			logWithState.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after generator error", zap.Error(errUpdateStory))
		} else {
			logWithState.Info("PublishedStory status updated to Error successfully after generator error")
			storyForWsUpdate, _ = p.publishedRepo.GetByID(dbCtx, publishedStoryID)
		}

		if notification.GameStateID != "" {
			gameStateIDError, errParseError := uuid.Parse(notification.GameStateID)
			if errParseError != nil {
				logWithState.Error("ERROR: Failed to parse GameStateID from error notification", zap.Error(errParseError))
			} else {
				gameState, errGetState := p.playerGameStateRepo.GetByID(ctx, gameStateIDError)
				if errGetState != nil {
					logWithState.Error("ERROR: Failed to get PlayerGameState by ID to update error after generator error", zap.Error(errGetState))
				} else {
					gameState.PlayerStatus = sharedModels.PlayerStatusError
					gameState.ErrorDetails = stringRef(notification.ErrorDetails)
					gameState.LastActivityAt = time.Now().UTC()
					if _, errSaveState := p.playerGameStateRepo.Save(ctx, gameState); errSaveState != nil {
						logWithState.Error("ERROR: Failed to save PlayerGameState with Error status after generator error", zap.Error(errSaveState))
					} else {
						logWithState.Info("PlayerGameState updated to Error status successfully after generator error")
					}
				}
			}
		} else {
			logWithState.Warn("GameStateID missing in generator error notification, player status was not updated.")
		}

		if storyForWsUpdate != nil {
			clientUpdateError := sharedModels.ClientStoryUpdate{
				ID:           publishedStoryID.String(),
				UserID:       storyForWsUpdate.UserID.String(),
				UpdateType:   sharedModels.UpdateTypeStory,
				Status:       string(sharedModels.StatusError),
				ErrorDetails: stringRef(notification.ErrorDetails),
				StateHash:    notification.StateHash,
			}
			wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
			if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdateError); errWs != nil {
				logWithState.Error("Error sending ClientStoryUpdate (Generator Error)", zap.Error(errWs))
			} else {
				logWithState.Info("ClientStoryUpdate sent (Generator Error)")
			}
			wsCancel()
		}
	}

	return nil
}

func (p *NotificationProcessor) publishPushNotificationForScene(ctx context.Context, gameState *sharedModels.PlayerGameState, scene *sharedModels.StoryScene, publishedStoryID uuid.UUID) {

	story, err := p.publishedRepo.GetByID(ctx, publishedStoryID)
	if err != nil {
		p.logger.Error("Failed to get PublishedStory for push notification",
			zap.String("publishedStoryID", publishedStoryID.String()),
			zap.String("gameStateID", gameState.ID.String()),
			zap.Error(err),
		)
		return
	}

	getAuthorName := func(userID uuid.UUID) string {
		authorName := "Unknown Author"
		if p.authClient != nil {
			authCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			userInfoMap, err := p.authClient.GetUsersInfo(authCtx, []uuid.UUID{userID})
			cancel()
			if err != nil {
				p.logger.Error("Failed to get author info map for push notification (scene/gameover)", zap.Stringer("userID", userID), zap.Error(err))
			} else if userInfo, ok := userInfoMap[userID]; ok {
				if userInfo.DisplayName != "" {
					authorName = userInfo.DisplayName
				} else {
					authorName = userInfo.Username
				}
			} else {
				p.logger.Warn("Author info not found in map from auth service for push notification (scene/gameover)", zap.Stringer("userID", userID))
			}
		} else {
			p.logger.Warn("authClient is nil in NotificationProcessor, cannot fetch author name for push notification")
		}
		return authorName
	}

	var payload *sharedModels.PushNotificationPayload
	var buildErr error
	var locKey string

	if gameState.PlayerStatus == sharedModels.PlayerStatusCompleted {
		locKey = constants.PushLocKeyGameOver
		endingText := ""
		if gameState.EndingText != nil {
			endingText = *gameState.EndingText
		}
		payload, buildErr = notifications.BuildGameOverPushPayload(story, gameState.ID, scene.ID, endingText, getAuthorName)

	} else {
		locKey = constants.PushLocKeySceneReady
		payload, buildErr = notifications.BuildSceneReadyPushPayload(story, gameState.ID, scene.ID, getAuthorName)
	}

	if buildErr != nil {
		p.logger.Error("Failed to build push notification payload",
			zap.String("publishedStoryID", publishedStoryID.String()),
			zap.String("gameStateID", gameState.ID.String()),
			zap.String("loc_key", locKey),
			zap.Error(buildErr),
		)
		return
	}

	if err := p.pushPub.PublishPushNotification(ctx, *payload); err != nil {
		p.logger.Error("Failed to publish push notification for scene/gameover",
			zap.String("userID", payload.UserID.String()),
			zap.String("publishedStoryID", publishedStoryID.String()),
			zap.String("gameStateID", gameState.ID.String()),
			zap.String("loc_key", locKey),
			zap.Error(err),
		)
	} else {
		p.logger.Info("Push notification for scene/gameover published successfully",
			zap.String("userID", payload.UserID.String()),
			zap.String("publishedStoryID", publishedStoryID.String()),
			zap.String("gameStateID", gameState.ID.String()),
			zap.String("loc_key", locKey),
		)
	}
}

func (p *NotificationProcessor) publishPushNotificationForStoryReady(ctx context.Context, story *sharedModels.PublishedStory) {
	getAuthorName := func(userID uuid.UUID) string {
		authorName := "Unknown Author"
		if p.authClient != nil {
			authCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			userInfoMap, err := p.authClient.GetUsersInfo(authCtx, []uuid.UUID{userID})
			cancel()
			if err != nil {
				p.logger.Error("Failed to get author info map for push notification (story ready)", zap.Stringer("userID", userID), zap.Error(err))
			} else if userInfo, ok := userInfoMap[userID]; ok {
				if userInfo.DisplayName != "" {
					authorName = userInfo.DisplayName
				} else {
					authorName = userInfo.Username
				}
			} else {
				p.logger.Warn("Author info not found in map from auth service for push notification (story ready)", zap.Stringer("userID", userID))
			}
		} else {
			p.logger.Warn("authClient is nil in NotificationProcessor, cannot fetch author name for push notification")
		}
		return authorName
	}

	payload, err := notifications.BuildStoryReadyPushPayload(story, getAuthorName)
	if err != nil {
		p.logger.Error("Failed to build push notification payload for story ready", zap.Error(err))
		return
	}

	if err := p.pushPub.PublishPushNotification(ctx, *payload); err != nil {
		p.logger.Error("Failed to publish push notification event for story ready",
			zap.String("userID", payload.UserID.String()),
			zap.String("publishedStoryID", story.ID.String()),
			zap.Error(err),
		)
	} else {
		p.logger.Info("Push notification event for story ready published successfully",
			zap.String("userID", payload.UserID.String()),
			zap.String("publishedStoryID", story.ID.String()),
		)
	}
}
