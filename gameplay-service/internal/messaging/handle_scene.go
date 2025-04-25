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
		var endingText *string

		// <<< НАЧАЛО: Валидация JSON перед сохранением >>>
		var tempSceneContentData interface{} // Используем interface{} для простой валидации структуры JSON
		if err := json.Unmarshal(sceneContentJSON, &tempSceneContentData); err != nil {
			p.logger.Error("INVALID SCENE JSON: Failed to parse Scene/GameOver JSON content before saving",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.String("state_hash", notification.StateHash),
				zap.Error(err),
				zap.String("json_to_parse", stringShort(jsonToParse, 500)), // Логируем часть невалидного JSON
			)

			// <<< НАЧАЛО: Обновляем статус PublishedStory на Error >>>
			dbCtxUpdateStory, cancelUpdateStory := context.WithTimeout(ctx, 10*time.Second)
			defer cancelUpdateStory()
			errorDetailStory := fmt.Sprintf("Scene generation failed: invalid JSON format from generator - %s", err.Error())
			if errUpdateStory := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtxUpdateStory, publishedStoryID, sharedModels.StatusError, false, false, &errorDetailStory); errUpdateStory != nil {
				p.logger.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after scene JSON parsing error",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.Error(errUpdateStory),
				)
				// Не прерываем, но логируем критическую ошибку обновления статуса
			} else {
				p.logger.Info("PublishedStory status updated to Error due to invalid scene JSON",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
				)
			}
			// <<< КОНЕЦ: Обновляем статус PublishedStory на Error >>>

			// <<< НАЧАЛО: Отправка Web Socket обновления (Ошибка парсинга JSON) >>>
			storyForWsUpdate, errGetStory := p.publishedRepo.GetByID(dbCtx, publishedStoryID) // Используем dbCtx, т.к. он еще активен
			if errGetStory != nil {
				p.logger.Error("Failed to get PublishedStory for WebSocket update after JSON parsing error", zap.String("task_id", taskID), zap.Error(errGetStory))
				// Не можем получить UserID, пропускаем WS
			} else {
				clientUpdateError := ClientStoryUpdate{
					ID:           publishedStoryID.String(),
					UserID:       storyForWsUpdate.UserID.String(),
					UpdateType:   UpdateTypeStory,
					Status:       string(sharedModels.StatusError),
					ErrorDetails: &errorDetailStory,      // Используем детали ошибки парсинга
					StateHash:    notification.StateHash, // Передаем хеш, чтобы клиент знал, какой переход не удался
				}
				wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer wsCancel()
				if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdateError); errWs != nil {
					p.logger.Error("Error sending ClientStoryUpdate (JSON Parsing Error)", zap.String("task_id", taskID), zap.Error(errWs))
				} else {
					p.logger.Info("ClientStoryUpdate sent (JSON Parsing Error)", zap.String("task_id", taskID), zap.String("story_id", publishedStoryID.String()))
				}
			}
			// <<< КОНЕЦ: Отправка Web Socket обновления (Ошибка парсинга JSON) >>>

			// Попытка обновить PlayerGameState на статус ошибки, если ID есть
			if notification.GameStateID != "" {
				gameStateID, errParseGID := uuid.Parse(notification.GameStateID)
				if errParseGID != nil {
					p.logger.Error("Failed to parse GameStateID while handling scene JSON error",
						zap.String("task_id", taskID),
						zap.String("invalid_game_state_id", notification.GameStateID),
						zap.Error(errParseGID),
					)
					// Продолжаем, чтобы вернуть основную ошибку парсинга JSON
				} else {
					dbCtxUpdate, cancelUpdate := context.WithTimeout(ctx, 10*time.Second)
					defer cancelUpdate()
					gameState, errGetState := p.playerGameStateRepo.GetByID(dbCtxUpdate, gameStateID)
					if errGetState != nil {
						p.logger.Error("Failed to get PlayerGameState to set error status after scene JSON error",
							zap.String("task_id", taskID),
							zap.String("game_state_id", notification.GameStateID),
							zap.Error(errGetState),
						)
					} else {
						gameState.PlayerStatus = sharedModels.PlayerStatusError // Устанавливаем статус ошибки
						errorDetail := fmt.Sprintf("Scene generation failed: invalid JSON content received - %s", err.Error())
						gameState.ErrorDetails = &errorDetail
						gameState.LastActivityAt = time.Now().UTC()
						if _, errSaveState := p.playerGameStateRepo.Save(dbCtxUpdate, gameState); errSaveState != nil {
							p.logger.Error("Failed to save PlayerGameState with error status after scene JSON error",
								zap.String("task_id", taskID),
								zap.String("game_state_id", notification.GameStateID),
								zap.Error(errSaveState),
							)
						} else {
							p.logger.Info("PlayerGameState updated to Error status due to invalid scene JSON",
								zap.String("task_id", taskID),
								zap.String("game_state_id", notification.GameStateID),
							)
						}
					}
				}
			}
			// Возвращаем ошибку, чтобы сообщение было Nack'ed
			return fmt.Errorf("invalid scene JSON format: %w", err)
		}
		p.logger.Info("Scene JSON content validated successfully", zap.String("task_id", taskID))
		// <<< КОНЕЦ: Валидация JSON перед сохранением >>>

		// Парсинг EndingText (только если это GameOver и JSON валиден)
		if notification.PromptType == sharedMessaging.PromptTypeNovelGameOverCreator {
			var endingContent struct {
				EndingText string `json:"et"`
			}
			// Используем уже валидированный jsonToParse
			if err := json.Unmarshal([]byte(jsonToParse), &endingContent); err != nil {
				// Логируем ошибку парсинга именно EndingText, но не прерываем процесс,
				// так как основная структура JSON сцены валидна.
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
			return fmt.Errorf("error upserting scene for PublishedStory %s, Hash %s: %w", publishedStoryID, notification.StateHash, upsertErr)
		}
		p.logger.Info("Scene upserted successfully",
			zap.String("task_id", taskID),
			zap.String("published_story_id", publishedStoryID.String()),
			zap.String("state_hash", notification.StateHash),
			zap.String("scene_id", scene.ID.String()),
		)

		// --- Начало изменений: Логика обновления статуса и флагов ---
		var finalStatusToSet *sharedModels.StoryStatus     // Используем указатель, чтобы обновлять статус только когда нужно
		var currentStoryState *sharedModels.PublishedStory // Для хранения загруженного состояния
		setIsFirstScenePendingToFalse := false             // Флаг, нужно ли сбросить is_first_scene_pending

		if notification.StateHash == sharedModels.InitialStateHash {
			p.logger.Info("Processing result for the FIRST scene", zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()))
			setIsFirstScenePendingToFalse = true // Отмечаем, что первая сцена готова

			// Проверяем, нужно ли установить статус Ready
			// Загружаем текущее состояние флагов из БД (GetByID теперь возвращает флаги)
			var errState error
			currentStoryState, errState = p.publishedRepo.GetByID(dbCtx, publishedStoryID)
			if errState != nil {
				p.logger.Error("CRITICAL ERROR: Failed to get current story state after first scene generation", zap.Error(errState), zap.String("publishedStoryID", publishedStoryID.String()))
				// Не можем проверить флаг are_images_pending, но первую сцену обработали. Не устанавливаем Ready.
			} else {
				p.logger.Info("Current story state before final check",
					zap.String("publishedStoryID", publishedStoryID.String()),
					zap.Bool("is_first_scene_pending", currentStoryState.IsFirstScenePending), // Должен быть true здесь
					zap.Bool("are_images_pending", currentStoryState.AreImagesPending),
				)
				// Устанавливаем Ready, только если картинки НЕ ожидаются (т.е. флаг are_images_pending уже false)
				if !currentStoryState.AreImagesPending {
					p.logger.Info("First scene generated AND images are ready (or not needed). Setting status to Ready.", zap.String("publishedStoryID", publishedStoryID.String()))
					readyStatus := sharedModels.StatusReady
					finalStatusToSet = &readyStatus
				} else {
					p.logger.Info("First scene generated, but images are still pending. Status remains unchanged for now.", zap.String("publishedStoryID", publishedStoryID.String()))
				}
			}
		}

		// Обновляем статус и/или флаг в БД, если нужно
		if finalStatusToSet != nil || setIsFirstScenePendingToFalse {
			// Определяем статус для обновления
			statusToUpdate := sharedModels.StatusReady                    // По умолчанию Ready, если не первая сцена или если и она и картинки готовы
			if setIsFirstScenePendingToFalse && finalStatusToSet == nil { // Если первая сцена готова, но картинки нет
				// Берем текущий статус из загруженного состояния, если оно есть
				if currentStoryState != nil {
					statusToUpdate = currentStoryState.Status
				} else {
					// Если не удалось загрузить состояние, оставляем Ready как запасной вариант
					p.logger.Warn("Could not determine current status, defaulting to Ready for flag update", zap.String("publishedStoryID", publishedStoryID.String()))
				}
			} else if finalStatusToSet != nil { // Если и первая сцена и картинки готовы
				statusToUpdate = *finalStatusToSet
			}

			// Определяем флаг are_images_pending для записи (он не меняется в этом обработчике)
			currentAreImagesPending := false // Default to false if GetByID failed
			if currentStoryState != nil {
				currentAreImagesPending = currentStoryState.AreImagesPending
			}

			errUpdate := p.publishedRepo.UpdateStatusFlagsAndDetails(dbCtx, publishedStoryID, statusToUpdate, false, currentAreImagesPending, nil) // isFirstScenePending=false
			if errUpdate != nil {
				p.logger.Error("CRITICAL ERROR (Data Inconsistency!): Scene upserted, but failed to update PublishedStory status/flags",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.String("state_hash", notification.StateHash),
					zap.String("scene_id", scene.ID.String()),
					zap.Error(errUpdate),
				)
				// Не возвращаем ошибку, т.к. сцена сохранена
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
		// --- Конец изменений: Логика обновления статуса и флагов ---

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

		// --- Отправка Web Socket обновления (Успех) ---
		// Получаем UserID (лучше получить его один раз в начале функции)
		finalPubStory, errGetStory := p.publishedRepo.GetByID(dbCtx, publishedStoryID)
		if errGetStory != nil {
			p.logger.Error("Failed to get PublishedStory for WebSocket update after success", zap.String("task_id", taskID), zap.Error(errGetStory))
			// Не возвращаем ошибку, продолжаем без WS обновления
		} else {
			clientUpdateSuccess := ClientStoryUpdate{
				ID:         publishedStoryID.String(),
				UserID:     finalPubStory.UserID.String(),
				UpdateType: UpdateTypeStory,
				Status:     string(finalPubStory.Status), // Используем актуальный статус из БД
				SceneID:    scene.ID.String(),
				StateHash:  notification.StateHash,
				EndingText: endingText, // Будет nil, если не GameOver
			}
			wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer wsCancel()
			if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdateSuccess); errWs != nil {
				p.logger.Error("Error sending ClientStoryUpdate (Scene Success)", zap.String("task_id", taskID), zap.Error(errWs))
			} else {
				p.logger.Info("ClientStoryUpdate sent (Scene Success)", zap.String("task_id", taskID), zap.String("story_id", publishedStoryID.String()), zap.String("status", clientUpdateSuccess.Status))
			}
		}
		// --- Конец отправки Web Socket обновления (Успех) ---

		// --- Отправка Push уведомления (если нужно) ---
		// (Перемещено сюда, чтобы использовать finalPubStory)
		if finalPubStory != nil {
			// Определяем статус игрока для типа Push уведомления
			playerStatus := sharedModels.PlayerStatusPlaying // Инициализируем здесь
			if notification.PromptType == sharedMessaging.PromptTypeNovelGameOverCreator {
				playerStatus = sharedModels.PlayerStatusCompleted
			}

			// Определяем, отправлять ли Push на основе финального статуса
			if finalPubStory.Status == sharedModels.StatusReady || playerStatus == sharedModels.PlayerStatusCompleted {
				pushPayload := PushNotificationPayload{
					UserID:       finalPubStory.UserID,
					Notification: PushNotification{},
					Data: map[string]string{
						"type":       UpdateTypeStory,
						"entity_id":  publishedStoryID.String(),
						"status":     string(finalPubStory.Status), // Финальный статус
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
				} else { // StatusReady
					pushPayload.Notification.Title = fmt.Sprintf("'%s': Новая сцена готова!", storyTitle)
					pushPayload.Notification.Body = "Продолжите ваше приключение."
				}

				pushCtx, pushCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer pushCancel() // Используем defer для pushCancel
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
						zap.String("player_status", string(playerStatus)),        // Используем статус игрока, т.к. он определяет тип пуша
						zap.String("story_status", string(finalPubStory.Status)), // Логируем финальный статус истории
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
		// --- Конец отправки Push уведомления ---

	} else {
		p.logger.Warn("Scene/GameOver Error notification received",
			zap.String("task_id", taskID),
			zap.String("published_story_id", publishedStoryID.String()),
			zap.String("state_hash", notification.StateHash),
			zap.String("error_details", notification.ErrorDetails),
		)

		// --- Обновление статуса PublishedStory на Error ---
		var storyForWsUpdate *sharedModels.PublishedStory // Для получения UserID
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
			// Пытаемся получить историю для отправки WS после успешного обновления статуса
			var errGetStory error
			storyForWsUpdate, errGetStory = p.publishedRepo.GetByID(dbCtx, publishedStoryID)
			if errGetStory != nil {
				p.logger.Error("Failed to get PublishedStory for WebSocket update after generator error", zap.String("task_id", taskID), zap.Error(errGetStory))
			}
		}
		// --- Конец обновления PublishedStory ---

		// --- Обновление PlayerGameState (если есть ID) ---
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
					// Используем новый метод для обновления статуса Error и сброса флагов
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
			// Логируем, что GameStateID отсутствовал, но статус PublishedStory уже обновлен
			p.logger.Warn("GameStateID missing in error notification, player status was not updated (PublishedStory status was updated to Error).",
				zap.String("task_id", taskID),
				zap.String("prompt_type", string(notification.PromptType)),
			)
		}
		// --- Конец обновления PlayerGameState ---

		// --- Отправка Web Socket обновления (Ошибка генератора) ---
		if storyForWsUpdate != nil { // Отправляем только если удалось получить историю
			clientUpdateError := ClientStoryUpdate{
				ID:           publishedStoryID.String(),
				UserID:       storyForWsUpdate.UserID.String(),
				UpdateType:   UpdateTypeStory,
				Status:       string(sharedModels.StatusError),
				ErrorDetails: &notification.ErrorDetails,
				StateHash:    notification.StateHash, // Передаем хеш, чтобы клиент знал, какой переход не удался
			}
			wsCtx, wsCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer wsCancel()
			if errWs := p.clientPub.PublishClientUpdate(wsCtx, clientUpdateError); errWs != nil {
				p.logger.Error("Error sending ClientStoryUpdate (Generator Error)", zap.String("task_id", taskID), zap.Error(errWs))
			} else {
				p.logger.Info("ClientStoryUpdate sent (Generator Error)", zap.String("task_id", taskID), zap.String("story_id", publishedStoryID.String()))
			}
		} else {
			p.logger.Warn("Skipping ClientStoryUpdate (Generator Error) because PublishedStory could not be fetched", zap.String("task_id", taskID))
		}
	}
	return nil
}

// Определение структуры ClientStoryUpdate удалено из этого файла,
// так как оно уже есть в handle_narrator.go (в том же пакете).
// В идеале, эту структуру нужно вынести в общее место.
