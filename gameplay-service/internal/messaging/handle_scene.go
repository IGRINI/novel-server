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
		return fmt.Errorf("invalid GameStateID: %w", errParseStateID) // Nack
	}
	_ = gameStateID // Используем blank identifier, если ID больше не нужен напрямую

	logWithState := p.logger.With(
		zap.String("task_id", taskID),
		zap.String("published_story_id", publishedStoryID.String()),
		zap.String("state_hash", notification.StateHash),
	)
	logWithState.Info("Processing Scene/GameOver")

	var parseErr error
	var finalGameState *sharedModels.PlayerGameState
	var finalScene *sharedModels.StoryScene
	var wsEventToSend string
	var errorDetailsForWs *string
	var storyForWs *sharedModels.PublishedStory // Для получения UserID при ошибке

	// --- Обработка УСПЕХА генерации ---
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
			jsonToParse := utils.ExtractJsonContent(rawGeneratedText)
			if jsonToParse == "" {
				logWithState.Error("SCENE PARSING ERROR: Could not extract JSON from Scene/GameOver text (fetched)",
					zap.String("raw_text_snippet", utils.StringShort(rawGeneratedText, 100)),
				)
				parseErr = errors.New("failed to extract JSON block from Scene/GameOver text")
			} else {
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
							} else {
								logWithState.Info("Scene JSON structure validated successfully (contains 'ch' array)")

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

												// <<< НАЧАЛО: Отправка Push уведомления о готовности истории >>>
												p.publishPushNotificationForStoryReady(ctx, currentStoryState)
												// <<< КОНЕЦ: Отправка Push уведомления о готовности истории >>>
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
													gameState.CompletedAt = sql.NullTime{Time: now, Valid: true}
													gameState.CurrentSceneID = uuid.NullUUID{UUID: scene.ID, Valid: true}
												} else {
													gameState.PlayerStatus = sharedModels.PlayerStatusPlaying
													gameState.CurrentSceneID = uuid.NullUUID{UUID: scene.ID, Valid: true}
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

													// Обновляем current_scene_summary в PlayerProgress
													if gameState.PlayerProgressID != uuid.Nil {
														progressID := gameState.PlayerProgressID
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

													// <<< ПРАВИЛЬНОЕ МЕСТО для отправки WebSocket и Push >>>
													// Получаем актуальное состояние истории для WebSocket
													finalPubStory, errGetStory := p.publishedRepo.GetByID(dbCtx, publishedStoryID)
													if errGetStory != nil {
														p.logger.Error("Failed to get PublishedStory for WebSocket update after success", zap.String("task_id", taskID), zap.Error(errGetStory))
													} else {
														// Отправляем WebSocket уведомление
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
															p.logger.Error("Error sending ClientStoryUpdate (Scene Success)", zap.String("task_id", taskID), zap.Error(errWs))
														} else {
															p.logger.Info("ClientStoryUpdate sent (Scene Success)", zap.String("task_id", taskID), zap.String("story_id", publishedStoryID.String()), zap.String("status", wsEvent.Status))
														}
														wsCancel() // Закрываем контекст для WebSocket
													}

													// Отправляем Push уведомление (используя gameState, который мы обновили)
													p.publishPushNotificationForScene(ctx, gameState, scene, publishedStoryID)
												}
											}
										}
									} else {
										p.logger.Warn("GameStateID missing in notification, player status not updated.",
											zap.String("task_id", taskID),
											zap.String("prompt_type", string(notification.PromptType)),
										)
									}
								}
							}
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
				clientUpdateError := sharedModels.ClientStoryUpdate{
					ID:           publishedStoryID.String(),
					UserID:       storyForWsUpdate.UserID.String(),
					UpdateType:   sharedModels.UpdateTypeStory,
					Status:       string(sharedModels.StatusError),
					ErrorDetails: stringRef(errDetails),
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
						gameState.ErrorDetails = stringRef(errDetails)
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
					gameState.ErrorDetails = stringRef(notification.ErrorDetails)
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
			clientUpdateError := sharedModels.ClientStoryUpdate{
				ID:           publishedStoryID.String(),
				UserID:       storyForWsUpdate.UserID.String(),
				UpdateType:   sharedModels.UpdateTypeStory,
				Status:       string(sharedModels.StatusError),
				ErrorDetails: stringRef(notification.ErrorDetails),
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

	// <<< ВОССТАНОВЛЕННЫЙ БЛОК: ОТПРАВКА WebSocket УВЕДОМЛЕНИЯ >>>
	if wsEventToSend != "" { // Отправляем, если событие было определено
		var sceneIDToSend *string
		if finalScene != nil {
			sid := finalScene.ID.String()
			sceneIDToSend = &sid
		}
		stateHashForWs := notification.StateHash // Всегда берем из уведомления
		var endingTextForWs *string
		var userIDForWs string
		var storyIDForWs string
		var statusForWs string

		if finalGameState != nil { // Если есть состояние игры
			userIDForWs = finalGameState.PlayerID.String()
			storyIDForWs = finalGameState.PublishedStoryID.String()
			endingTextForWs = finalGameState.EndingText       // Может быть nil
			statusForWs = string(finalGameState.PlayerStatus) // Берем статус из GameState
		} else if storyForWs != nil { // Если состояния нет, но есть история (для ошибки)
			userIDForWs = storyForWs.UserID.String()
			storyIDForWs = storyForWs.ID.String()
			statusForWs = string(sharedModels.StatusError) // Статус Error
		} else { // Не можем определить UserID
			p.logger.Error("Cannot send WS update for scene, failed to determine UserID", zap.Stringer("gameStateID", gameStateID), zap.Stringer("publishedStoryID", publishedStoryID))
			// Не возвращаем ошибку, чтобы не Nack'ать, просто не шлем WS
		}

		if userIDForWs != "" { // Если UserID определен
			// Создаем объект ClientStoryUpdate для отправки
			clientUpdate := sharedModels.ClientStoryUpdate{
				ID:         storyIDForWs,
				UserID:     userIDForWs,
				UpdateType: sharedModels.UpdateTypeStory, // TODO: Определять GameState на основе finalGameState?
				Status:     statusForWs,
				StateHash:  stateHashForWs,
				EndingText: endingTextForWs, // Может быть nil
			}
			if sceneIDToSend != nil {
				clientUpdate.SceneID = sceneIDToSend                       // Просто присваиваем *string
				clientUpdate.UpdateType = sharedModels.UpdateTypeGameState // Устанавливаем тип GameState, если есть SceneID
				if finalGameState != nil {
					clientUpdate.ID = finalGameState.ID.String() // Используем ID GameState, если он есть
				} else {
					p.logger.Warn("SceneID present, but finalGameState is nil. Cannot set ClientUpdate ID to GameStateID.", zap.String("storyID", storyIDForWs))
				}
			}
			if errorDetailsForWs != nil {
				clientUpdate.ErrorDetails = errorDetailsForWs
			}

			// Отправляем в горутине
			go func(updateToSend sharedModels.ClientStoryUpdate) {
				if err := p.clientPub.PublishClientUpdate(context.Background(), updateToSend); err != nil {
					p.logger.Error("Failed to publish client scene update event",
						zap.String("wsEvent", wsEventToSend),
						zap.String("updateID", updateToSend.ID),
						zap.String("updateType", string(updateToSend.UpdateType)), // Преобразуем к string
						zap.String("userID", updateToSend.UserID),
						zap.Error(err),
					)
				} else {
					p.logger.Info("Client scene/gameover update event published successfully via RabbitMQ",
						zap.String("wsEvent", wsEventToSend),
						zap.String("updateID", updateToSend.ID),
						zap.String("updateType", string(updateToSend.UpdateType)),
						zap.String("userID", updateToSend.UserID),
					)
				}
				// Отправка через NATS не требуется, т.к. websocket-service слушает RabbitMQ.
			}(clientUpdate)
		}
	}

	return nil // Подтверждаем сообщение RabbitMQ
}

// Определение структуры ClientStoryUpdate и PushNotificationPayload
// предполагается существующим в пакете (возможно, в другом файле,
// например, handle_narrator.go или в отдельном файле types.go)
// или должно быть добавлено/перенесено.

// Helper function (example, already exists in consumer.go)
// func extractJsonContent(text string) string { ... }

// Helper function (example, already exists in consumer.go)
// func stringShort(s string, maxLen int) string { ... }

// <<< НАЧАЛО: Обновленная функция для отправки Push уведомлений для сцены/концовки >>>
func (p *NotificationProcessor) publishPushNotificationForScene(ctx context.Context, gameState *sharedModels.PlayerGameState, scene *sharedModels.StoryScene, publishedStoryID uuid.UUID) {
	// Получаем детали истории для payload
	story, err := p.publishedRepo.GetByID(ctx, publishedStoryID)
	if err != nil {
		p.logger.Error("Failed to get PublishedStory for push notification",
			zap.String("publishedStoryID", publishedStoryID.String()),
			zap.String("gameStateID", gameState.ID.String()),
			zap.Error(err),
		)
		return // Не отправляем уведомление, если не можем получить детали
	}

	// Функция для получения имени автора (аналогично publishPushNotificationForStoryReady)
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
	var locKey string // Для логирования

	if gameState.PlayerStatus == sharedModels.PlayerStatusCompleted {
		// --- Строим Payload для Game Over ---
		locKey = constants.PushLocKeyGameOver
		endingText := ""
		if gameState.EndingText != nil {
			endingText = *gameState.EndingText
		}
		payload, buildErr = notifications.BuildGameOverPushPayload(story, gameState.ID, scene.ID, endingText, getAuthorName)

	} else {
		// --- Строим Payload для Scene Ready ---
		locKey = constants.PushLocKeySceneReady
		payload, buildErr = notifications.BuildSceneReadyPushPayload(story, gameState.ID, scene.ID, getAuthorName)
	}

	// Проверяем ошибку сборки payload
	if buildErr != nil {
		p.logger.Error("Failed to build push notification payload",
			zap.String("publishedStoryID", publishedStoryID.String()),
			zap.String("gameStateID", gameState.ID.String()),
			zap.String("loc_key", locKey),
			zap.Error(buildErr),
		)
		return
	}

	// Отправляем уведомление
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

// <<< КОНЕЦ: Обновленная функция >>>

// <<< НАЧАЛО: Новая функция для отправки Push уведомлений о готовности истории >>>
func (p *NotificationProcessor) publishPushNotificationForStoryReady(ctx context.Context, story *sharedModels.PublishedStory) {
	// Функция для получения имени автора (передается в конструктор payload)
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

// <<< КОНЕЦ: Новая функция для отправки Push уведомлений о готовности истории >>>
