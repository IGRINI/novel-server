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

		var parseErr error
		var rawGeneratedText string

		genResultCtx, genResultCancel := context.WithTimeout(ctx, 10*time.Second)
		genResult, genErr := p.genResultRepo.GetByTaskID(genResultCtx, taskID)
		genResultCancel()

		if genErr != nil {
			p.logger.Error("DB ERROR (Scene): Could not get GenerationResult by TaskID",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.String("state_hash", notification.StateHash),
				zap.Error(genErr),
			)
			parseErr = fmt.Errorf("failed to fetch generation result: %w", genErr)
		} else if genResult.Error != "" {
			p.logger.Error("TASK ERROR (Scene): GenerationResult indicates an error",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.String("state_hash", notification.StateHash),
				zap.String("gen_error", genResult.Error),
			)
			parseErr = errors.New(genResult.Error)
		} else {
			rawGeneratedText = genResult.GeneratedText
			p.logger.Debug("Successfully fetched GeneratedText from DB", zap.String("task_id", taskID))
		}

		if parseErr == nil {
			jsonToParse := extractJsonContent(rawGeneratedText)
			if jsonToParse == "" {
				p.logger.Error("SCENE PARSING ERROR: Could not extract JSON from Scene/GameOver text (fetched)",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.String("state_hash", notification.StateHash),
					zap.String("raw_text_snippet", stringShort(rawGeneratedText, 100)),
				)
				parseErr = errors.New("failed to extract JSON block from Scene/GameOver text")
			} else {
				sceneContentJSON := json.RawMessage(jsonToParse)
				var endingText *string

				var tempSceneContentData interface{}
				if err := json.Unmarshal(sceneContentJSON, &tempSceneContentData); err != nil {
					p.logger.Error("INVALID SCENE JSON: Failed to parse Scene/GameOver JSON content before saving (fetched)",
						zap.String("task_id", taskID),
						zap.String("published_story_id", publishedStoryID.String()),
						zap.String("state_hash", notification.StateHash),
						zap.Error(err),
						zap.String("json_to_parse", stringShort(jsonToParse, 500)),
					)
					parseErr = fmt.Errorf("invalid scene JSON format: %w", err)
				} else {
					p.logger.Info("Scene JSON content validated successfully", zap.String("task_id", taskID))

					if notification.PromptType == sharedModels.PromptTypeNovelGameOverCreator {
						var endingContent struct {
							EndingText string `json:"et"`
						}
						if err := json.Unmarshal([]byte(jsonToParse), &endingContent); err != nil {
							p.logger.Error("Failed to parse EndingText from game over JSON (optional field)",
								zap.String("task_id", taskID),
								zap.String("published_story_id", publishedStoryID.String()),
								zap.Error(err),
								zap.String("json_to_parse", jsonToParse),
							)
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
						parseErr = fmt.Errorf("error upserting scene for PublishedStory %s, Hash %s: %w", publishedStoryID, notification.StateHash, upsertErr)
					} else {
						p.logger.Info("Scene upserted successfully",
							zap.String("task_id", taskID),
							zap.String("published_story_id", publishedStoryID.String()),
							zap.String("state_hash", notification.StateHash),
							zap.String("scene_id", scene.ID.String()),
						)

						var finalStatusToSet *sharedModels.StoryStatus
						var currentStoryState *sharedModels.PublishedStory
						setIsFirstScenePendingToFalse := false

						if notification.StateHash == sharedModels.InitialStateHash {
							p.logger.Info("Processing result for the FIRST scene", zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()))
							setIsFirstScenePendingToFalse = true

							var errState error
							currentStoryState, errState = p.publishedRepo.GetByID(dbCtx, publishedStoryID)
							if errState != nil {
								p.logger.Error("CRITICAL ERROR: Failed to get current story state after first scene generation", zap.Error(errState), zap.String("publishedStoryID", publishedStoryID.String()))
							} else {
								p.logger.Info("Current story state before final check",
									zap.String("publishedStoryID", publishedStoryID.String()),
									zap.Bool("is_first_scene_pending", currentStoryState.IsFirstScenePending),
									zap.Bool("are_images_pending", currentStoryState.AreImagesPending),
								)
								if !currentStoryState.AreImagesPending {
									p.logger.Info("First scene generated AND images are ready (or not needed). Setting status to Ready.", zap.String("publishedStoryID", publishedStoryID.String()))
									readyStatus := sharedModels.StatusReady
									finalStatusToSet = &readyStatus
								} else {
									p.logger.Info("First scene generated, but images are still pending. Status remains unchanged for now.", zap.String("publishedStoryID", publishedStoryID.String()))
								}
							}
						}

						if finalStatusToSet != nil || setIsFirstScenePendingToFalse {
							statusToUpdate := sharedModels.StatusReady // По умолчанию Ready, если finalStatusToSet установлен
							if setIsFirstScenePendingToFalse && finalStatusToSet == nil {
								// Первая сцена сгенерирована, но картинки еще ожидаются.
								// Всегда ставим FirstScenePending в этом случае
								statusToUpdate = sharedModels.StatusFirstScenePending
								p.logger.Info("First scene done, images pending. Setting status to FirstScenePending.", zap.String("publishedStoryID", publishedStoryID.String()))
							} else if finalStatusToSet != nil {
								statusToUpdate = *finalStatusToSet
							}

							currentAreImagesPending := true // Знаем, что картинки еще ожидаются, если finalStatusToSet == nil
							if finalStatusToSet != nil {    // Если finalStatusToSet != nil, значит картинки готовы
								currentAreImagesPending = false
							}

							errUpdate := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtx, publishedStoryID, statusToUpdate, false, currentAreImagesPending, nil) // isFirstScenePending всегда false здесь
							if errUpdate != nil {
								p.logger.Error("CRITICAL ERROR (Data Inconsistency!): Scene upserted, but failed to update PublishedStory status/flags",
									zap.String("task_id", taskID),
									zap.String("published_story_id", publishedStoryID.String()),
									zap.String("state_hash", notification.StateHash),
									zap.String("scene_id", scene.ID.String()),
									zap.Error(errUpdate),
								)
							} else {
								p.logger.Info("PublishedStory status/flags updated after scene generation",
									zap.String("task_id", taskID),
									zap.String("published_story_id", publishedStoryID.String()),
									zap.String("updated_status", string(statusToUpdate)),
									zap.Bool("is_first_scene_pending", false),
									zap.Bool("are_images_pending", currentAreImagesPending),
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
									if notification.PromptType == sharedModels.PromptTypeNovelGameOverCreator {
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

										if gameState.PlayerProgressID != nil {
											progressID := *gameState.PlayerProgressID

											var sceneSummaryContent struct {
												Summary *string `json:"ss"`
											}
											if errUnmarshal := json.Unmarshal(sceneContentJSON, &sceneSummaryContent); errUnmarshal != nil {
												p.logger.Warn("Failed to unmarshal scene JSON to extract summary (ss field)",
													zap.String("task_id", taskID),
													zap.String("game_state_id", notification.GameStateID),
													zap.Error(errUnmarshal),
												)
											} else if sceneSummaryContent.Summary != nil {
												updates := map[string]interface{}{
													"current_scene_summary": *sceneSummaryContent.Summary,
												}
												if errUpdateProgress := p.playerProgressRepo.UpdateFields(ctx, progressID, updates); errUpdateProgress != nil {
													p.logger.Error("ERROR: Failed to update current_scene_summary in PlayerProgress",
														zap.String("task_id", taskID),
														zap.String("progress_id", progressID.String()),
														zap.Error(errUpdateProgress),
													)
												} else {
													p.logger.Info("PlayerProgress current_scene_summary updated successfully",
														zap.String("task_id", taskID),
														zap.String("progress_id", progressID.String()),
													)
												}
											}
										}
									}
								}
							}
						} else {
							p.logger.Warn("GameStateID missing in notification, player status not updated.",
								zap.String("task_id", taskID),
								zap.String("prompt_type", string(notification.PromptType)),
							)
						}

						finalPubStory, errGetStory := p.publishedRepo.GetByID(dbCtx, publishedStoryID)
						if errGetStory != nil {
							p.logger.Error("Failed to get PublishedStory for WebSocket update after success", zap.String("task_id", taskID), zap.Error(errGetStory))
						} else {
							clientUpdateSuccess := ClientStoryUpdate{
								ID:         publishedStoryID.String(),
								UserID:     finalPubStory.UserID.String(),
								UpdateType: UpdateTypeStory,
								Status:     string(finalPubStory.Status),
								SceneID:    scene.ID.String(),
								StateHash:  notification.StateHash,
								EndingText: endingText,
							}
							wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
							defer wsCancel()
							if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdateSuccess); errWs != nil {
								p.logger.Error("Error sending ClientStoryUpdate (Scene Success)", zap.String("task_id", taskID), zap.Error(errWs))
							} else {
								p.logger.Info("ClientStoryUpdate sent (Scene Success)", zap.String("task_id", taskID), zap.String("story_id", publishedStoryID.String()), zap.String("status", clientUpdateSuccess.Status))
							}
						}

						if finalPubStory != nil {
							playerStatus := sharedModels.PlayerStatusPlaying
							if notification.PromptType == sharedModels.PromptTypeNovelGameOverCreator {
								playerStatus = sharedModels.PlayerStatusCompleted
							}
							if finalPubStory.Status == sharedModels.StatusReady || playerStatus == sharedModels.PlayerStatusCompleted {
								pushPayload := PushNotificationPayload{
									UserID:       finalPubStory.UserID,
									Notification: PushNotification{},
									Data: map[string]string{
										"type":       UpdateTypeStory,
										"entity_id":  publishedStoryID.String(),
										"status":     string(finalPubStory.Status),
										"scene_id":   scene.ID.String(),
										"state_hash": notification.StateHash,
									},
								}
								storyTitle := "История"
								if finalPubStory.Title != nil && *finalPubStory.Title != "" {
									storyTitle = *finalPubStory.Title
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
								defer pushCancel()
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
										zap.String("story_status", string(finalPubStory.Status)),
									)
								}
							} else {
								p.logger.Info("PUSH notification skipped for Scene/GameOver",
									zap.String("task_id", taskID),
									zap.String("published_story_id", publishedStoryID.String()),
									zap.String("story_status", string(finalPubStory.Status)),
									zap.String("player_status", string(playerStatus)),
								)
							}
						} else {
							p.logger.Error("Push notification skipped because PublishedStory could not be fetched", zap.String("task_id", taskID))
						}
					}
				}
			}
		}

		if parseErr != nil {
			p.logger.Error("Processing Scene/GameOver failed",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.String("state_hash", notification.StateHash),
				zap.Error(parseErr),
			)
			errDetails := parseErr.Error()

			dbCtxUpdateStory, cancelUpdateStory := context.WithTimeout(ctx, 10*time.Second)
			defer cancelUpdateStory()
			if errUpdateStory := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtxUpdateStory, publishedStoryID, sharedModels.StatusError, false, false, &errDetails); errUpdateStory != nil {
				p.logger.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after scene/gameover generation error",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.Error(errUpdateStory),
				)
			} else {
				p.logger.Info("PublishedStory status updated to Error successfully after scene/gameover generation error",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
				)
			}

			storyForWsUpdate, errGetStory := p.publishedRepo.GetByID(dbCtx, publishedStoryID)
			if errGetStory != nil {
				p.logger.Error("Failed to get PublishedStory for WebSocket update after processing error", zap.String("task_id", taskID), zap.Error(errGetStory))
			} else {
				clientUpdateError := ClientStoryUpdate{
					ID:           publishedStoryID.String(),
					UserID:       storyForWsUpdate.UserID.String(),
					UpdateType:   UpdateTypeStory,
					Status:       string(sharedModels.StatusError),
					ErrorDetails: &errDetails,
					StateHash:    notification.StateHash,
				}
				wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer wsCancel()
				if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdateError); errWs != nil {
					p.logger.Error("Error sending ClientStoryUpdate (Processing Error)", zap.String("task_id", taskID), zap.Error(errWs))
				} else {
					p.logger.Info("ClientStoryUpdate sent (Processing Error)", zap.String("task_id", taskID), zap.String("story_id", publishedStoryID.String()))
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
				p.logger.Warn("GameStateID missing in error notification, player status was not updated (PublishedStory status was updated to Error).",
					zap.String("task_id", taskID),
					zap.String("prompt_type", string(notification.PromptType)),
				)
			}

			return parseErr
		}

	} else {
		p.logger.Warn("Scene/GameOver Error notification received",
			zap.String("task_id", taskID),
			zap.String("published_story_id", publishedStoryID.String()),
			zap.String("state_hash", notification.StateHash),
			zap.String("error_details", notification.ErrorDetails),
		)

		var storyForWsUpdate *sharedModels.PublishedStory
		if errUpdateStory := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtx, publishedStoryID, sharedModels.StatusError, false, false, &notification.ErrorDetails); errUpdateStory != nil {
			p.logger.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after scene/gameover generation error",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(errUpdateStory),
			)
		} else {
			p.logger.Info("PublishedStory status updated to Error successfully after scene/gameover generation error",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
			)
			storyForWsUpdate, _ = p.publishedRepo.GetByID(dbCtx, publishedStoryID)
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
			p.logger.Warn("GameStateID missing in error notification, player status was not updated (PublishedStory status was updated to Error).",
				zap.String("task_id", taskID),
				zap.String("prompt_type", string(notification.PromptType)),
			)
		}

		if storyForWsUpdate != nil {
			clientUpdateError := ClientStoryUpdate{
				ID:           publishedStoryID.String(),
				UserID:       storyForWsUpdate.UserID.String(),
				UpdateType:   UpdateTypeStory,
				Status:       string(sharedModels.StatusError),
				ErrorDetails: &notification.ErrorDetails,
				StateHash:    notification.StateHash,
			}
			wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer wsCancel()
			if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdateError); errWs != nil {
				p.logger.Error("Error sending ClientStoryUpdate (Generator Error)", zap.String("task_id", taskID), zap.Error(errWs))
			} else {
				p.logger.Info("ClientStoryUpdate sent (Generator Error)", zap.String("task_id", taskID), zap.String("story_id", publishedStoryID.String()))
			}
		}
	}
	return nil
}

// Определение структуры ClientStoryUpdate и PushNotificationPayload
// предполагается существующим в пакете (возможно, в другом файле,
// например, handle_narrator.go или в отдельном файле types.go)
// или должно быть добавлено/перенесено.

// Helper function (example, already exists in consumer.go)
// func extractJsonContent(text string) string { ... }

// Helper function (example, already exists in consumer.go)
// func stringShort(s string, maxLen int) string { ... }
