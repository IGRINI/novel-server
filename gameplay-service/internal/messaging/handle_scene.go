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
	"novel-server/shared/utils"

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
			// Use rawGeneratedText directly
			jsonToParse := rawGeneratedText
			if jsonToParse == "" { // Check if the generator already determined it's empty/invalid
				logWithState.Error("SCENE PARSING ERROR: Generator returned empty text for Scene/GameOver",
					zap.String("raw_text_snippet", utils.StringShort(rawGeneratedText, 100)), // Still log snippet
				)
				parseErr = errors.New("generator returned empty text for scene")
			} else {
				logWithState.Debug("JSON to parse (length)", zap.Int("json_length", len(jsonToParse)))
				if len(jsonToParse) < 500 {
					logWithState.Debug("JSON to parse (full)", zap.String("json", jsonToParse))
				} else {
					logWithState.Debug("JSON to parse (trimmed)",
						zap.String("json_start", jsonToParse[0:200]),
						zap.String("json_end", jsonToParse[len(jsonToParse)-200:]),
					)
				}

				sceneContentJSON := json.RawMessage(jsonToParse)
				var endingText *string

				var tempSceneContentData interface{}
				if err := json.Unmarshal(sceneContentJSON, &tempSceneContentData); err != nil {
					logWithState.Error("INVALID SCENE JSON: Failed to parse Scene/GameOver JSON content before saving (fetched)",
						zap.Error(err),
						zap.String("json_to_parse", utils.StringShort(jsonToParse, 500)),
					)
					parseErr = fmt.Errorf("invalid scene JSON format: %w", err)
				} else {
					logWithState.Info("Scene JSON content validated successfully")

					contentMap, ok := tempSceneContentData.(map[string]interface{})
					if !ok {
						logWithState.Error("SCENE STRUCTURE ERROR: Parsed JSON is not a map",
							zap.String("type_found", fmt.Sprintf("%T", tempSceneContentData)),
						)
						parseErr = errors.New("parsed scene JSON is not an object")
					} else {
						keys := utils.GetMapKeys(contentMap)
						logWithState.Debug("Parsed JSON keys", zap.Strings("keys", keys))

						if notification.PromptType == sharedModels.PromptTypeNovelGameOverCreator {
							etData, etExists := contentMap["et"]
							choicesData, chExists := contentMap["ch"]

							if !etExists && !chExists {
								logWithState.Error("SCENE STRUCTURE ERROR: Game Over JSON missing both 'et' and 'ch' keys",
									zap.Any("parsed_keys", keys),
								)
								parseErr = errors.New("missing required keys in game over JSON")
							} else if etExists {
								_, isString := etData.(string)
								if !isString {
									logWithState.Warn("Game Over 'et' field is not a string",
										zap.String("type_found", fmt.Sprintf("%T", etData)),
									)
								}
							} else {
								_, isArray := choicesData.([]interface{})
								if !isArray {
									logWithState.Error("SCENE STRUCTURE ERROR: Value for 'ch' key is not an array",
										zap.String("type_found", fmt.Sprintf("%T", choicesData)),
									)
									parseErr = errors.New("value for 'ch' key is not an array")
								}
							}
						} else {
							choicesData, keyExists := contentMap["ch"]
							if !keyExists {
								logWithState.Error("SCENE STRUCTURE ERROR: Missing 'ch' key in scene JSON",
									zap.Any("parsed_keys", utils.GetMapKeys(contentMap)),
								)
								parseErr = errors.New("missing 'ch' key in scene JSON")
							} else {
								_, isArray := choicesData.([]interface{})
								if !isArray {
									logWithState.Error("SCENE STRUCTURE ERROR: Value for 'ch' key is not an array",
										zap.String("type_found", fmt.Sprintf("%T", choicesData)),
									)
									parseErr = errors.New("value for 'ch' key is not an array")
								}
							}
						}

						if notification.PromptType == sharedModels.PromptTypeNovelGameOverCreator && parseErr == nil {
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

						if parseErr == nil {
							type sceneValidator struct {
								Choices []struct {
									Char    string        `json:"char"`
									Desc    string        `json:"desc"`
									Options []interface{} `json:"opts"`
								} `json:"ch"`
								EndingText *string `json:"et"`
							}
							var validator sceneValidator
							if errValidate := json.Unmarshal(sceneContentJSON, &validator); errValidate != nil {
								logWithState.Error("STRICT VALIDATION FAILED: Scene JSON structure is invalid or incomplete",
									zap.Error(errValidate),
									zap.String("json_to_parse", utils.StringShort(jsonToParse, 500)),
								)
								parseErr = fmt.Errorf("strict validation failed: %w", errValidate)
							} else {
								if notification.PromptType != sharedModels.PromptTypeNovelGameOverCreator && len(validator.Choices) == 0 {
									logWithState.Error("STRICT VALIDATION FAILED: 'ch' array is empty for a non-gameover scene")
									parseErr = errors.New("choices array 'ch' cannot be empty for regular scene")
								} else {
									logWithState.Info("Strict JSON structure validation passed.")
								}
							}
						}

						if parseErr == nil {
							scene := &sharedModels.StoryScene{
								ID:               uuid.New(),
								PublishedStoryID: publishedStoryID,
								StateHash:        notification.StateHash,
								Content:          sceneContentJSON,
								CreatedAt:        time.Now().UTC(),
							}

							upsertErr := p.sceneRepo.Upsert(dbCtx, scene)
							if upsertErr != nil {
								logWithState.Error("CRITICAL ERROR: Failed to upsert scene after validation", zap.Error(upsertErr))
								parseErr = fmt.Errorf("error upserting scene: %w", upsertErr)
								errDetails := parseErr.Error()
								dbCtxUpdateStory, cancelUpdateStory := context.WithTimeout(ctx, 10*time.Second)
								if errUpdateStory := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtxUpdateStory, publishedStoryID, sharedModels.StatusError, false, false, &errDetails); errUpdateStory != nil {
									logWithState.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after upsert failure", zap.Error(errUpdateStory))
								} else {
									logWithState.Info("PublishedStory status updated to Error successfully after upsert failure")
								}
								cancelUpdateStory()

								storyForWsUpdate, errGetStory := p.publishedRepo.GetByID(dbCtx, publishedStoryID)
								if errGetStory != nil {
									logWithState.Error("Failed to get PublishedStory for WebSocket update after upsert error", zap.Error(errGetStory))
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
										logWithState.Error("Error sending ClientStoryUpdate (Upsert Error)", zap.Error(errWs))
									} else {
										logWithState.Info("ClientStoryUpdate sent (Upsert Error)")
									}
									wsCancel()
								}

								if notification.GameStateID != "" {
									gameStateIDOnError, errParseStateIDOnError := uuid.Parse(notification.GameStateID)
									if errParseStateIDOnError != nil {
										logWithState.Error("ERROR: Failed to parse GameStateID from notification during upsert error handling", zap.Error(errParseStateIDOnError))
									} else {
										gameState, errGetState := p.playerGameStateRepo.GetByID(ctx, gameStateIDOnError)
										if errGetState != nil {
											logWithState.Error("ERROR: Failed to get PlayerGameState by ID to update error after upsert failure", zap.Error(errGetState))
										} else {
											gameState.PlayerStatus = sharedModels.PlayerStatusError
											gameState.ErrorDetails = stringRef(errDetails)
											gameState.LastActivityAt = time.Now().UTC()
											if _, errSaveState := p.playerGameStateRepo.Save(ctx, gameState); errSaveState != nil {
												logWithState.Error("ERROR: Failed to save PlayerGameState with Error status after upsert failure", zap.Error(errSaveState))
											} else {
												logWithState.Info("PlayerGameState updated to Error status successfully after upsert failure")
											}
										}
									}
								} else {
									logWithState.Warn("GameStateID missing in validation error notification.")
								}
								return parseErr
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
										logWithState.Error("ERROR: Failed to parse GameStateID from notification after successful Upsert", zap.Error(errParseSuccess))
									} else {
										gameState, errGetState := p.playerGameStateRepo.GetByID(ctx, gameStateIDSuccess)
										if errGetState != nil {
											logWithState.Error("ERROR: Failed to get PlayerGameState by ID after successful Upsert", zap.Error(errGetState))
										} else {
											if notification.PromptType == sharedModels.PromptTypeNovelGameOverCreator {
												gameState.PlayerStatus = sharedModels.PlayerStatusCompleted
												gameState.EndingText = endingText
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
												logWithState.Error("ERROR: Failed to save updated PlayerGameState after successful Upsert", zap.Error(errSaveState))
											} else {
												logWithState.Info("PlayerGameState updated successfully after successful Upsert", zap.String("new_player_status", string(gameState.PlayerStatus)))

												if gameState.PlayerProgressID != uuid.Nil {
													progressID := gameState.PlayerProgressID
													var sceneSummaryContent struct {
														Summary *string `json:"sssf"`
													}
													if errUnmarshal := json.Unmarshal(sceneContentJSON, &sceneSummaryContent); errUnmarshal != nil {
														logWithState.Warn("Failed to unmarshal scene JSON to extract summary (sssf field)", zap.Error(errUnmarshal))
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
													logWithState.Error("Failed to get PublishedStory for WebSocket update after success", zap.Error(errGetStory))
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
								} else {
									logWithState.Warn("GameStateID missing in notification, player status not updated.")
								}
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
