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
	if s == "" {
		return nil
	}
	return &s
}

// handleSceneGenerationNotification обрабатывает уведомления об успешной или неуспешной генерации сцены/концовки.
// Обеспечивает атомарность операций с БД через ручное управление транзакциями.
func (p *NotificationProcessor) handleSceneGenerationNotification(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	// Увеличиваем общий таймаут операции
	operationCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	gameStateID, errParseStateID := uuid.Parse(notification.GameStateID)
	if errParseStateID != nil && notification.GameStateID != "" {
		p.logger.Error("ERROR: Failed to parse GameStateID from scene notification", zap.Error(errParseStateID), zap.String("gameStateID", notification.GameStateID))
		return fmt.Errorf("invalid GameStateID: %w", errParseStateID)
	}

	logFields := []zap.Field{
		zap.String("task_id", taskID),
		zap.String("published_story_id", publishedStoryID.String()),
		zap.String("state_hash", notification.StateHash),
		zap.String("prompt_type", string(notification.PromptType)),
	}
	if notification.GameStateID != "" {
		logFields = append(logFields, zap.String("game_state_id", gameStateID.String()))
	}
	logWithState := p.logger.With(logFields...)

	logWithState.Info("Processing Scene/GameOver notification")

	if notification.Status == sharedMessaging.NotificationStatusSuccess {
		logWithState.Info("Handling Scene/GameOver SUCCESS notification")

		var rawGeneratedText string
		var fetchErr error

		genResultCtx, genResultCancel := context.WithTimeout(operationCtx, 10*time.Second)
		genResult, genErr := p.genResultRepo.GetByTaskID(genResultCtx, taskID)
		genResultCancel()

		if genErr != nil {
			logWithState.Error("DB ERROR: Could not get GenerationResult by TaskID", zap.Error(genErr))
			fetchErr = fmt.Errorf("failed to fetch generation result: %w", genErr)
		} else if genResult.Error != "" {
			logWithState.Error("TASK ERROR: GenerationResult indicates an error", zap.String("gen_error", genResult.Error))
			fetchErr = errors.New(genResult.Error)
		} else {
			rawGeneratedText = genResult.GeneratedText
			logWithState.Debug("Successfully fetched GeneratedText from DB")
		}

		if fetchErr != nil {
			logWithState.Error("Error during data fetching, handling as generation error", zap.Error(fetchErr))
			return p.handleSceneGenerationError(operationCtx, notification, publishedStoryID, fetchErr.Error(), logWithState)
		}

		sceneContentJSON := json.RawMessage(rawGeneratedText)
		var endingText *string
		isGameOverScene := notification.PromptType == sharedModels.PromptTypeNovelGameOverCreator

		if isGameOverScene {
			var endingContent struct {
				EndingText string `json:"et"`
			}
			if errUnmarshal := json.Unmarshal(sceneContentJSON, &endingContent); errUnmarshal == nil && endingContent.EndingText != "" {
				endingText = stringRef(endingContent.EndingText)
				logWithState.Info("Extracted EndingText from GameOver JSON")
			} else if errUnmarshal != nil {
				logWithState.Warn("Failed to extract optional EndingText ('et') from GameOver JSON", zap.Error(errUnmarshal))
			}
		}

		scene := &sharedModels.StoryScene{
			ID:               uuid.New(),
			PublishedStoryID: publishedStoryID,
			StateHash:        notification.StateHash,
			Content:          sceneContentJSON,
			CreatedAt:        time.Now().UTC(),
		}

		var finalPlayerStatus sharedModels.PlayerStatus
		var finalStoryStatus sharedModels.StoryStatus
		var storyUserID uuid.UUID
		var finalGameState *sharedModels.PlayerGameState
		var isStoryReadyAfterUpdate bool
		var currentStoryState *sharedModels.PublishedStory

		tx, errTxBegin := p.db.Begin(operationCtx)
		if errTxBegin != nil {
			logWithState.Error("DB ERROR: Failed to begin transaction", zap.Error(errTxBegin))
			return p.handleSceneGenerationError(operationCtx, notification, publishedStoryID, fmt.Sprintf("failed to begin transaction: %v", errTxBegin), logWithState)
		}
		defer tx.Rollback(operationCtx)
		logWithState.Info("DB Transaction started")

		logWithState.Debug("TX: Attempting to upsert scene", zap.Any("scene_to_upsert", scene))

		actualSceneID, errUpsert := p.sceneRepo.Upsert(operationCtx, tx, scene)
		if errUpsert != nil {
			logWithState.Error("TX ERROR: Failed to upsert scene", zap.String("scene_id_attempted", scene.ID.String()), zap.Error(errUpsert))
			return p.handleSceneGenerationError(operationCtx, notification, publishedStoryID, fmt.Sprintf("failed to upsert scene: %v", errUpsert), logWithState)
		}
		logWithState.Info("TX: Scene upserted successfully", zap.String("actual_scene_id", actualSceneID.String()))

		fetchedStoryState, errState := p.publishedRepo.GetByID(operationCtx, tx, publishedStoryID)
		if errState != nil {
			logWithState.Error("TX ERROR: Failed to get story state after scene upsert", zap.Error(errState))
			finalStoryStatus = sharedModels.StatusError
			storyUserID = uuid.Nil
		} else {
			logWithState.Debug("TX: Successfully retrieved PublishedStory state within transaction", zap.Any("fetched_story_state", fetchedStoryState))

			currentStoryState = fetchedStoryState
			finalStoryStatus = currentStoryState.Status
			storyUserID = currentStoryState.UserID
			if !isGameOverScene && currentStoryState.Status != sharedModels.StatusError && !currentStoryState.IsFirstScenePending && !currentStoryState.AreImagesPending {
				readyStatus := sharedModels.StatusReady
				if currentStoryState.Status != readyStatus {
					statusToUpdate := &readyStatus
					logWithState.Info("TX: Story status will be set to Ready")
					errUpdate := p.publishedRepo.UpdateStatusFlagsAndDetails(operationCtx, tx, publishedStoryID, *statusToUpdate, false, false, nil)
					if errUpdate != nil {
						logWithState.Error("TX ERROR: Failed to update PublishedStory status/flags", zap.Error(errUpdate))
						return p.handleSceneGenerationError(operationCtx, notification, publishedStoryID, fmt.Sprintf("failed to update story status: %v", errUpdate), logWithState)
					}
					finalStoryStatus = *statusToUpdate
					isStoryReadyAfterUpdate = true
					logWithState.Info("TX: PublishedStory status/flags updated", zap.String("updated_status", string(finalStoryStatus)))
				}
			} else if !isGameOverScene {
				logWithState.Info("TX: Story is not yet ready or is game over",
					zap.String("current_status", string(currentStoryState.Status)),
					zap.Bool("is_first_scene_pending", currentStoryState.IsFirstScenePending),
					zap.Bool("are_images_pending", currentStoryState.AreImagesPending),
					zap.Bool("isGameOverScene", isGameOverScene),
				)
			}
		}

		if notification.GameStateID != "" {
			logWithState.Debug("TX: Attempting to get PlayerGameState by ID within transaction", zap.String("search_gameStateID", gameStateID.String()))

			gameState, errGetState := p.playerGameStateRepo.GetByID(operationCtx, tx, gameStateID)
			if errGetState != nil {
				logWithState.Error("TX ERROR: Failed to get PlayerGameState by ID", zap.Error(errGetState))
				finalPlayerStatus = sharedModels.PlayerStatusError
				return p.handleSceneGenerationError(operationCtx, notification, publishedStoryID, fmt.Sprintf("failed to get gamestate %s: %v", gameStateID.String(), errGetState), logWithState)
			}
			logWithState.Debug("TX: Successfully retrieved PlayerGameState by ID within transaction", zap.Any("retrieved_gameState", gameState))

			if isGameOverScene {
				gameState.PlayerStatus = sharedModels.PlayerStatusCompleted
				gameState.EndingText = endingText
				now := time.Now().UTC()
				gameState.CompletedAt = sql.NullTime{Time: now, Valid: true}
			} else {
				gameState.PlayerStatus = sharedModels.PlayerStatusPlaying
			}
			gameState.CurrentSceneID = uuid.NullUUID{UUID: actualSceneID, Valid: true}
			gameState.ErrorDetails = nil
			gameState.LastActivityAt = time.Now().UTC()

			logWithState.Debug("TX: Attempting to save updated PlayerGameState within transaction",
				zap.String("gameStateID_to_save", gameState.ID.String()),
				zap.String("new_playerStatus", string(gameState.PlayerStatus)),
				zap.String("new_currentSceneID", func() string {
					if gameState.CurrentSceneID.Valid {
						return gameState.CurrentSceneID.UUID.String()
					}
					return "<null>"
				}()),
			)

			gameStateIDResult, errSave := p.playerGameStateRepo.Save(operationCtx, tx, gameState)
			if errSave != nil {
				logWithState.Error("TX ERROR: Failed to save updated PlayerGameState",
					zap.Error(errSave),
					zap.String("referenced_scene_id", actualSceneID.String()),
				)
				return p.handleSceneGenerationError(operationCtx, notification, publishedStoryID, fmt.Sprintf("failed to save gamestate referencing scene %s: %v", actualSceneID.String(), errSave), logWithState)
			}

			finalPlayerStatus = gameState.PlayerStatus
			finalGameState = gameState
			if storyUserID == uuid.Nil {
				storyUserID = gameState.PlayerID
			}
			logWithState.Info("TX: PlayerGameState updated successfully", zap.String("new_player_status", string(finalPlayerStatus)), zap.Stringer("saved_gameStateID", gameStateIDResult))

			if finalPlayerStatus != sharedModels.PlayerStatusError && gameState.PlayerProgressID != uuid.Nil {
				progressID := gameState.PlayerProgressID
				var sceneSummaryContent struct {
					Summary *string `json:"sssf"`
				}
				if errUnmarshal := json.Unmarshal(sceneContentJSON, &sceneSummaryContent); errUnmarshal == nil && sceneSummaryContent.Summary != nil {
					updates := map[string]interface{}{"current_scene_summary": *sceneSummaryContent.Summary}
					if errUpdateProgress := p.playerProgressRepo.UpdateFields(operationCtx, tx, progressID, updates); errUpdateProgress != nil {
						logWithState.Error("TX ERROR: Failed to update current_scene_summary in PlayerProgress", zap.String("progress_id", progressID.String()), zap.Error(errUpdateProgress))
						return p.handleSceneGenerationError(operationCtx, notification, publishedStoryID, fmt.Sprintf("failed to update player progress %s: %v", progressID.String(), errUpdateProgress), logWithState)
					}
					logWithState.Info("TX: PlayerProgress current_scene_summary updated successfully", zap.String("progress_id", progressID.String()))
				} else if errUnmarshal != nil {
					logWithState.Warn("TX: Failed to unmarshal scene JSON to extract summary for PlayerProgress update (non-critical)", zap.Error(errUnmarshal))
				}
			}
		} else {
			logWithState.Info("TX: No GameStateID in notification, skipping PlayerGameState update.")
		}

		logWithState.Debug("TX: All operations within transaction seem successful, attempting commit...")

		if errCommit := tx.Commit(operationCtx); errCommit != nil {
			logWithState.Error("DB ERROR: Failed to commit transaction", zap.Error(errCommit))
			return p.handleSceneGenerationError(operationCtx, notification, publishedStoryID, fmt.Sprintf("failed to commit transaction: %v", errCommit), logWithState)
		}
		logWithState.Info("DB Transaction committed successfully.")

		logWithState.Info("Sending notifications after successful commit.")

		if storyUserID != uuid.Nil {
			wsEvent := sharedModels.ClientStoryUpdate{
				ID:         publishedStoryID.String(),
				UserID:     storyUserID.String(),
				UpdateType: sharedModels.UpdateTypeStory,
				Status:     string(finalStoryStatus),
				StateHash:  notification.StateHash,
			}
			if notification.GameStateID != "" {
				wsEventGameState := sharedModels.ClientStoryUpdate{
					ID:         gameStateID.String(),
					UserID:     storyUserID.String(),
					UpdateType: sharedModels.UpdateTypeGameState,
					Status:     string(finalPlayerStatus),
					SceneID:    stringRef(scene.ID.String()),
					EndingText: endingText,
					StateHash:  notification.StateHash,
				}
				wsCtxState, wsCancelState := context.WithTimeout(context.Background(), 10*time.Second)
				if errWsState := p.clientPub.PublishClientUpdate(wsCtxState, wsEventGameState); errWsState != nil {
					logWithState.Error("Error sending ClientStoryUpdate (GameState Success)", zap.Error(errWsState))
				} else {
					logWithState.Info("ClientStoryUpdate sent (GameState Success)", zap.String("player_status", wsEventGameState.Status))
				}
				wsCancelState()
			} else if isStoryReadyAfterUpdate {
				wsCtxStory, wsCancelStory := context.WithTimeout(context.Background(), 10*time.Second)
				if errWsStory := p.clientPub.PublishClientUpdate(wsCtxStory, wsEvent); errWsStory != nil {
					logWithState.Error("Error sending ClientStoryUpdate (Story Ready)", zap.Error(errWsStory))
				} else {
					logWithState.Info("ClientStoryUpdate sent (Story Ready)", zap.String("story_status", wsEvent.Status))
				}
				wsCancelStory()
			}

		} else {
			logWithState.Warn("Cannot send WebSocket update: UserID is unknown")
		}

		if isStoryReadyAfterUpdate && notification.StateHash == sharedModels.InitialStateHash && currentStoryState != nil {
			go p.publishPushNotificationForStoryReady(context.WithoutCancel(ctx), currentStoryState)
		}
		if notification.GameStateID != "" && finalGameState != nil {
			go p.publishPushNotificationForScene(context.WithoutCancel(ctx), finalGameState, scene, publishedStoryID)
		}

	} else {
		logWithState.Warn("Handling Scene/GameOver ERROR notification",
			zap.String("error_details", notification.ErrorDetails),
		)
		_ = p.handleSceneGenerationError(operationCtx, notification, publishedStoryID, notification.ErrorDetails, logWithState)
	}

	return nil
}

func (p *NotificationProcessor) handleSceneGenerationError(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID, errorDetails string, logger *zap.Logger) error {
	logger.Warn("Entering handleSceneGenerationError", zap.String("reason", errorDetails))

	dbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var storyUserID uuid.UUID
	var updateErr error
	var parsedGameStateID uuid.UUID
	var gameStateUpdatedSuccessfully bool

	if notification.GameStateID != "" {
		gameStateID, errParse := uuid.Parse(notification.GameStateID)
		if errParse != nil {
			logger.Error("ERROR: Failed to parse GameStateID from error notification", zap.Error(errParse), zap.String("gameStateID", notification.GameStateID))
		} else {
			parsedGameStateID = gameStateID
			gameState, errGetState := p.playerGameStateRepo.GetByID(dbCtx, p.db, gameStateID)
			if errGetState != nil {
				if errors.Is(errGetState, sharedModels.ErrNotFound) {
					logger.Warn("PlayerGameState not found, cannot update status to Error", zap.String("game_state_id", gameStateID.String()))
				} else {
					logger.Error("DB ERROR: Failed to get PlayerGameState by ID to update status to Error", zap.Error(errGetState))
					updateErr = fmt.Errorf("failed to get game state %s during error handling: %w", gameStateID.String(), errGetState)
				}
			} else {
				storyUserID = gameState.PlayerID

				if gameState.PlayerStatus != sharedModels.PlayerStatusError {
					gameState.PlayerStatus = sharedModels.PlayerStatusError
					gameState.ErrorDetails = stringRef(errorDetails)
					gameState.LastActivityAt = time.Now().UTC()
					_, errSaveState := p.playerGameStateRepo.Save(dbCtx, p.db, gameState)
					if errSaveState != nil {
						logger.Error("DB ERROR: Failed to save PlayerGameState with Error status", zap.Error(errSaveState))
						if updateErr == nil {
							updateErr = fmt.Errorf("failed to save game state %s during error handling: %w", gameStateID.String(), errSaveState)
						}
					} else {
						logger.Info("PlayerGameState updated to Error status successfully")
						gameStateUpdatedSuccessfully = true
					}
				} else {
					logger.Info("PlayerGameState already in Error status, skipping update.")
					gameStateUpdatedSuccessfully = true
					storyUserID = gameState.PlayerID
				}
			}
		}
	}

	if !gameStateUpdatedSuccessfully {
		logger.Info("Updating PublishedStory status to Error (either no GameStateID or GameState update failed/skipped)")
		errUpdateStory := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtx, p.db, publishedStoryID, sharedModels.StatusError, false, false, stringRef(errorDetails))
		if errUpdateStory != nil {
			logger.Error("CRITICAL DB ERROR: Failed to update PublishedStory status to Error", zap.Error(errUpdateStory))
			updateErr = fmt.Errorf("failed to update story %s status to Error: %w", publishedStoryID.String(), errUpdateStory)
		} else {
			logger.Info("PublishedStory status updated to Error successfully")
		}
	}

	if storyUserID == uuid.Nil {
		story, errGet := p.publishedRepo.GetByID(dbCtx, p.db, publishedStoryID)
		if errGet != nil {
			logger.Error("Failed to get story details for WS update after setting Error status", zap.Error(errGet))
		} else {
			storyUserID = story.UserID
		}
	}

	if storyUserID != uuid.Nil {
		clientUpdateError := sharedModels.ClientStoryUpdate{
			UserID:       storyUserID.String(),
			ErrorDetails: stringRef(errorDetails),
			StateHash:    notification.StateHash,
		}
		logMessage := ""
		wsLogFields := []zap.Field{
			zap.Stringer("userID", storyUserID),
			zap.Stringer("publishedStoryID", publishedStoryID),
			zap.String("stateHash", notification.StateHash),
		}

		if parsedGameStateID != uuid.Nil && gameStateUpdatedSuccessfully {
			clientUpdateError.ID = parsedGameStateID.String()
			clientUpdateError.UpdateType = sharedModels.UpdateTypeGameState
			clientUpdateError.Status = string(sharedModels.PlayerStatusError)
			logMessage = "ClientStoryUpdate sent (GameState Error)"
			wsLogFields = append(wsLogFields, zap.Stringer("gameStateID", parsedGameStateID))
		} else {
			clientUpdateError.ID = publishedStoryID.String()
			clientUpdateError.UpdateType = sharedModels.UpdateTypeStory
			clientUpdateError.Status = string(sharedModels.StatusError)
			logMessage = "ClientStoryUpdate sent (Story Error)"
		}

		wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdateError); errWs != nil {
			logger.Error("Error sending ClientStoryUpdate on error", append(wsLogFields, zap.Error(errWs))...)
		} else {
			logger.Info(logMessage, wsLogFields...)
		}
		wsCancel()
	} else {
		logger.Warn("Cannot send WebSocket error update: UserID is unknown",
			zap.Stringer("publishedStoryID", publishedStoryID),
			zap.Stringer("parsedGameStateID", parsedGameStateID),
			zap.Bool("gameStateUpdatedSuccessfully", gameStateUpdatedSuccessfully),
		)
	}

	return updateErr
}

func (p *NotificationProcessor) publishPushNotificationForScene(ctx context.Context, gameState *sharedModels.PlayerGameState, scene *sharedModels.StoryScene, publishedStoryID uuid.UUID) {
	if gameState == nil || scene == nil {
		p.logger.Error("Cannot send push for scene: gameState or scene is nil", zap.String("publishedStoryID", publishedStoryID.String()))
		return
	}

	story, err := p.publishedRepo.GetByID(ctx, p.db, publishedStoryID)
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
			userInfoMap, errAuth := p.authClient.GetUsersInfo(authCtx, []uuid.UUID{userID})
			cancel()
			if errAuth != nil {
				p.logger.Error("Failed to get author info map for push notification (scene/gameover)", zap.Stringer("userID", userID), zap.Error(errAuth))
			} else if userInfo, ok := userInfoMap[userID]; ok {
				if userInfo.DisplayName != "" {
					authorName = userInfo.DisplayName
				} else if userInfo.Username != "" {
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
		endingTextVal := ""
		if gameState.EndingText != nil {
			endingTextVal = *gameState.EndingText
		}
		payload, buildErr = notifications.BuildGameOverPushPayload(story, gameState.ID, scene.ID, endingTextVal, getAuthorName)
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

	if payload == nil {
		p.logger.Error("Push notification payload is nil, cannot publish",
			zap.String("publishedStoryID", publishedStoryID.String()),
			zap.String("gameStateID", gameState.ID.String()),
			zap.String("loc_key", locKey),
		)
		return
	}

	pushCtx, pushCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer pushCancel()
	if err := p.pushPub.PublishPushNotification(pushCtx, *payload); err != nil {
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
	if story == nil {
		p.logger.Error("Cannot send push for story ready: story is nil")
		return
	}

	getAuthorName := func(userID uuid.UUID) string {
		authorName := "Unknown Author"
		if p.authClient != nil {
			authCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			userInfoMap, errAuth := p.authClient.GetUsersInfo(authCtx, []uuid.UUID{userID})
			cancel()
			if errAuth != nil {
				p.logger.Error("Failed to get author info map for push notification (story ready)", zap.Stringer("userID", userID), zap.Error(errAuth))
			} else if userInfo, ok := userInfoMap[userID]; ok {
				if userInfo.DisplayName != "" {
					authorName = userInfo.DisplayName
				} else if userInfo.Username != "" {
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

	if payload == nil {
		p.logger.Error("Push notification payload for story ready is nil, cannot publish", zap.String("publishedStoryID", story.ID.String()))
		return
	}

	pushCtx, pushCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer pushCancel()
	if err := p.pushPub.PublishPushNotification(pushCtx, *payload); err != nil {
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
