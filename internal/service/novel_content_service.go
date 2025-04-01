package service

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"novel-server/internal/deepseek"
	"novel-server/internal/domain"
	"novel-server/internal/repository"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/sashabaranov/go-openai"
)

// NovelContentService предоставляет функциональность для генерации контента новеллы
type NovelContentService struct {
	deepseekClient *deepseek.Client
	novelRepo      repository.NovelRepository
	systemPrompt   string
}

// NewNovelContentService создает новый экземпляр сервиса
func NewNovelContentService(deepseekClient *deepseek.Client, novelRepo repository.NovelRepository) (*NovelContentService, error) {
	// Загружаем системный промпт для генерации новеллы
	promptBytes, err := os.ReadFile("promts/novel_creator.md")
	if err != nil {
		return nil, fmt.Errorf("failed to read novel creator prompt: %w", err)
	}

	return &NovelContentService{
		deepseekClient: deepseekClient,
		novelRepo:      novelRepo,
		systemPrompt:   string(promptBytes),
	}, nil
}

// GenerateNovelContent генерирует или продолжает новеллу на основе запроса
func (s *NovelContentService) GenerateNovelContent(ctx context.Context, request domain.NovelContentRequest) (*domain.NovelContentResponse, error) {
	log.Printf("[GenerateNovelContent] Received request. NovelID: %s, UserID: %s, HasUserChoice: %t, RestartFromSceneIndex: %v",
		request.NovelID, request.UserID, request.UserChoice != nil, request.RestartFromSceneIndex)

	if request.NovelID == uuid.Nil {
		return nil, fmt.Errorf("novel_id is required")
	}

	if request.UserID == "" {
		return nil, fmt.Errorf("user_id is required")
	}

	var state *domain.NovelState
	var sceneIndex int
	var err error

	// Загружаем сетап новеллы (состояние с индексом 0) для получения статических данных
	setupStateData, setupErr := s.novelRepo.GetNovelSetupState(ctx, request.NovelID)
	if setupErr != nil && !errors.Is(setupErr, pgx.ErrNoRows) {
		log.Printf("[GenerateNovelContent] Error getting setup state: %v", setupErr)
		return nil, fmt.Errorf("failed to get novel setup state: %w", setupErr)
	}

	var setupState *domain.NovelState
	if setupStateData != nil {
		// Десериализуем сетап
		var parsedSetupState domain.NovelState
		if err := json.Unmarshal(setupStateData, &parsedSetupState); err != nil {
			log.Printf("[GenerateNovelContent] Error unmarshaling setup state: %v", err)
			return nil, fmt.Errorf("failed to unmarshal setup state: %w", err)
		}
		setupState = &parsedSetupState
	}

	// Получаем последний прогресс пользователя
	progress, latestSceneIndex, err := s.novelRepo.GetLatestUserStoryProgress(ctx, request.NovelID, request.UserID)
	if err != nil {
		log.Printf("[GenerateNovelContent] Error getting latest user story progress: %v", err)
		return nil, fmt.Errorf("failed to get latest user story progress: %w", err)
	}

	// Если у пользователя нет прогресса, но есть предыдущие пользователи, которые уже создали сцены,
	// то мы можем использовать их сцены вместо генерации новых
	if progress == nil && latestSceneIndex == -1 {
		// Проверяем наличие сцены с индексом 0 в таблице novel_states
		existingStateData, err := s.novelRepo.GetNovelStateBySceneIndex(ctx, request.NovelID, 0)
		if err == nil {
			// Если сцена с индексом 0 существует, десериализуем её
			var existingState domain.NovelState
			if err := json.Unmarshal(existingStateData, &existingState); err != nil {
				log.Printf("[GenerateNovelContent] Error unmarshaling existing scene 0 state: %v", err)
			} else {
				log.Printf("[GenerateNovelContent] Using existing scene 0 for new user %s in NovelID %s", request.UserID, request.NovelID)
				sceneIndex = 0
				state = &existingState

				// Формируем ответ на основе существующей сцены
				sceneContent, err := s.extractSceneContent(state, 0)
				if err != nil {
					log.Printf("[GenerateNovelContent] Error extracting scene 0 content from existing state: %v", err)
					sceneContent = nil
				}

				// Обновить хеш состояния для нового пользователя и сохранить прогресс
				stateData, err := json.Marshal(*state)
				if err == nil {
					err = s.novelRepo.SaveNovelState(ctx, request.NovelID, 0, request.UserID, state.StateHash, stateData)
					if err != nil {
						log.Printf("[GenerateNovelContent] Error saving existing state for new user %s: %v", request.UserID, err)
					}
				}

				response := &domain.NovelContentResponse{
					State:      *state,
					NewContent: sceneContent,
				}
				return response, nil
			}
		} else {
			log.Printf("[GenerateNovelContent] No existing scene 0 found for NovelID %s: %v", request.NovelID, err)
		}
	}

	// Продолжаем с обычной логикой получения состояния
	if progress != nil && setupState != nil {
		// Объединяем сетап с прогрессом пользователя
		state = MergeStateWithProgress(setupState, progress)
		sceneIndex = latestSceneIndex
		log.Printf("[GenerateNovelContent] Merged setup with user progress for UserID %s in NovelID %s, SceneIndex: %d",
			request.UserID, request.NovelID, sceneIndex)
	} else if setupState != nil {
		// Если есть сетап, но нет прогресса - новый пользователь в существующей новелле
		state = setupState
		sceneIndex = 0
		log.Printf("[GenerateNovelContent] Using only setup state for new user %s in NovelID %s",
			request.UserID, request.NovelID)
	} else {
		// Ни сетапа, ни прогресса нет
		log.Printf("[GenerateNovelContent] No state or progress found for user %s in novel %s", request.UserID, request.NovelID)
		sceneIndex = -1
		state = nil
	}

	// --- Переменная для хранения JSON запроса к ИИ (если он понадобится) ---
	var requestJSON []byte

	// --- ОБНОВЛЕННАЯ ЛОГИКА: Обработка случая отсутствия состояния у пользователя ---
	if state == nil && request.RestartFromSceneIndex == nil {
		log.Printf("[GenerateNovelContent] No saved state found for user %s. Checking for existing scene 0 (setup state)...", request.UserID)

		// 1. Пытаемся получить общее состояние для сцены 0 (setup)
		setupStateData, err := s.novelRepo.GetNovelSetupState(ctx, request.NovelID)
		if err == nil {
			// --- СЦЕНА 0 НАЙДЕНА В КЕШЕ ---
			log.Printf("[GenerateNovelContent] Found existing setup state for NovelID %s. Using it for user %s. NOT REGENERATING.", request.NovelID, request.UserID)
			var setupState domain.NovelState
			if err := json.Unmarshal(setupStateData, &setupState); err != nil {
				log.Printf("[GenerateNovelContent] Error unmarshaling existing setup state: %v", err)
				// Не можем использовать кеш, переходим к генерации
				goto GenerateInitialRequest // Используем goto для перехода к блоку генерации
			}

			// Применяем личные данные пользователя к загруженному состоянию
			// Загружаем конфиг, чтобы получить имя/пол игрока, если их нет в setupState (что маловероятно, но возможно)
			cfg, cfgErr := s.novelRepo.GetNovelConfigByID(ctx, request.NovelID, request.UserID) // UserID здесь не так важен, конфиг общий
			if cfgErr != nil {
				log.Printf("[GenerateNovelContent] Warning: Could not get config while applying setup state: %v", cfgErr)
				// Продолжаем без PlayerName/Gender, если их нет в setupState
			}

			// Устанавливаем данные пользователя
			state = &setupState // Теперь state указывает на данные из кеша
			if cfg != nil {
				state.PlayerName = cfg.PlayerName
				state.PlayerGender = cfg.PlayerGender
			}
			state.PreviousChoices = []string{} // Очищаем историю выборов для нового пользователя
			state.CurrentSceneIndex = 0        // Убедимся, что индекс правильный
			// state.CurrentStage уже должен быть правильным (StageSceneReady) из кеша

			// Сохраняем это начальное состояние для НОВОГО пользователя
			initialSaveData, err := json.Marshal(*state)
			if err != nil {
				log.Printf("[GenerateNovelContent] Error marshaling initial state for user %s: %v", request.UserID, err)
				// Ошибка не критична для возврата результата, но логируем
			} else {
				// Хеш для сцены 0 обычно не так важен, как для последующих выборов
				initialHash := state.StateHash // Используем хеш из setupState, если он там был
				if initialHash == "" {
					initialHash = "SCENE_0_HASH_PLACEHOLDER"
				}
				err = s.novelRepo.SaveNovelState(ctx, request.NovelID, 0, request.UserID, initialHash, initialSaveData)
				if err != nil {
					log.Printf("[GenerateNovelContent] Error saving initial state for user %s: %v", request.UserID, err)
					// Ошибка не критична для возврата результата
				}
			}

			// Формируем ответ на основе загруженного состояния
			sceneContent, err := s.extractSceneContent(state, 0) // Берем сцену 0
			if err != nil {
				log.Printf("[GenerateNovelContent] Error extracting scene 0 content from setup state: %v", err)
				sceneContent = nil // Возвращаем без контента в случае ошибки
			}
			response := &domain.NovelContentResponse{
				State:      *state,
				NewContent: sceneContent,
			}
			log.Printf("[GenerateNovelContent] Reused existing setup state (scene 0) for user %s.", request.UserID)
			return response, nil // --- ВОЗВРАЩАЕМ ГОТОВУЮ СЦЕНУ 0 ---

		} else if errors.Is(err, pgx.ErrNoRows) {
			// --- СЦЕНА 0 НЕ НАЙДЕНА В КЕШЕ ---
			log.Printf("[GenerateNovelContent] No existing setup state found for NovelID %s. Proceeding to generate initial request.", request.NovelID)
			// Переходим к генерации первоначального запроса
			goto GenerateInitialRequest
		} else {
			// --- ДРУГАЯ ОШИБКА ПРИ ПОЛУЧЕНИИ СЦЕНЫ 0 ---
			log.Printf("[GenerateNovelContent] Error getting setup state: %v", err)
			return nil, fmt.Errorf("failed to get novel setup state: %w", err)
		}

	GenerateInitialRequest: // Метка для goto
		// --- ЛОГИКА ГЕНЕРАЦИИ ПЕРВОНАЧАЛЬНОГО ЗАПРОСА (как было раньше) ---
		log.Printf("[GenerateNovelContent] ВНИМАНИЕ! РЕГЕНЕРАЦИЯ СЕТАПА для NovelID %s и пользователя %s! ЭТО ДОЛЖНО ПРОИСХОДИТЬ ТОЛЬКО ДЛЯ НОВЫХ НОВЕЛЛ!",
			request.NovelID, request.UserID)
		config, err := s.novelRepo.GetNovelConfigByID(ctx, request.NovelID, request.UserID) // UserID здесь не важен
		if err != nil {
			return nil, fmt.Errorf("failed to get novel config for initial request: %w", err)
		}
		// Создаем начальное состояние
		state = &domain.NovelState{
			CurrentStage:         domain.StageSetup,
			SceneCount:           s.determineSceneCount(config.StoryConfig.Length),
			CurrentSceneIndex:    0,
			Language:             config.Language,
			PlayerName:           config.PlayerName,
			PlayerGender:         config.PlayerGender,
			EndingPreference:     config.EndingPreference,
			WorldContext:         config.WorldContext,
			OriginalStorySummary: config.StorySummary,
			StorySummary:         "",
			GlobalFlags:          []string{},
			Relationship:         make(map[string]int),
			StoryVariables:       make(map[string]interface{}),
			PreviousChoices:      []string{},
			StorySummarySoFar:    config.StorySummarySoFar,
			FutureDirection:      config.FutureDirection,
			Backgrounds:          []domain.Background{},
			Characters:           []domain.Character{},
			Scenes:               []domain.Scene{},
			IsAdultContent:       config.IsAdultContent,
		}
		// Формируем первоначальный запрос на основе конфигурации
		requestJSON, err = s.prepareInitialRequest(*config)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare initial request: %w", err)
		}
		log.Printf("[GenerateNovelContent] Prepared initial request for NovelID: %s", request.NovelID)
		// --- КОНЕЦ БЛОКА ГЕНЕРАЦИИ ПЕРВОНАЧАЛЬНОГО ЗАПРОСА ---

	} else if request.RestartFromSceneIndex != nil {
		// --- ОБРАБОТКА ЯВНОГО ЗАПРОСА НА ПЕРЕЗАПУСК ---
		// TODO: Добавить логику перезапуска с конкретной сцены, если она нужна.
		// Сейчас она может попасть в блок ниже (обработка существующего state),
		// но это может быть некорректно, если нужно загружать состояние именно запрошенной сцены.
		log.Printf("[GenerateNovelContent] Handling explicit restart request (index %d) - current logic might proceed to continuation.", *request.RestartFromSceneIndex)
		// Если нужно загружать конкретное состояние:
		// stateData, err := s.novelRepo.GetNovelState(ctx, request.NovelID, request.UserID, *request.RestartFromSceneIndex)
		// ... обработка ошибки и unmarshal ...
		// requestJSON, err = s.prepareContinuationRequest(state, nil) // Формируем запрос на продолжение с загруженного состояния

		// --- Пока что оставляем как есть, попадает в блок ниже ---

	} else {
		// --- ОБРАБОТКА СУЩЕСТВУЮЩЕГО СОСТОЯНИЯ ПОЛЬЗОВАТЕЛЯ (> сцены 0 или при перезапуске) ---
		log.Printf("[GenerateNovelContent] Found saved state for UserID %s, NovelID: %s, SceneIndex: %d, Stage: %s",
			request.UserID, request.NovelID, sceneIndex, state.CurrentStage)

		// Если пользователь сделал выбор, обрабатываем его
		if request.UserChoice != nil && state.CurrentStage == domain.StageSceneReady {
			// Обрабатываем выбор пользователя и применяем последствия к текущему состоянию
			// Важно сделать это до поиска существующих сцен, чтобы иметь актуальное состояние
			updatedState := *state // Копируем состояние

			// Проверяем, был ли выбор сделан в текущей сцене
			if len(updatedState.Scenes) > updatedState.CurrentSceneIndex {
				scene := updatedState.Scenes[updatedState.CurrentSceneIndex]
				// Применяем последствия выбора к состоянию
				processUserChoice(&updatedState, scene, request.UserChoice.ChoiceText)
				log.Printf("[GenerateNovelContent] Processed user choice.")

				// --- DEBUG LOGGING: Состояние после выбора ---
				flagsJSON, _ := json.Marshal(updatedState.GlobalFlags)
				relJSON, _ := json.Marshal(updatedState.Relationship)
				varsJSON, _ := json.Marshal(updatedState.StoryVariables)
				log.Printf("[DEBUG_HASH_STATE] State after choice for UserID %s: Flags=%s, Relationship=%s, Vars=%s",
					request.UserID, string(flagsJSON), string(relJSON), string(varsJSON))
				// --- END DEBUG LOGGING ---

				// ВАЖНО: Увеличиваем индекс текущей сцены после выбора
				updatedState.CurrentSceneIndex++
				log.Printf("[GenerateNovelContent] Incrementing scene index after user choice. New index: %d", updatedState.CurrentSceneIndex)

				// Обновляем переменную state, чтобы использовать обновленное состояние
				*state = updatedState

			} else {
				log.Printf("[GenerateNovelContent] Warning: Could not find scene %d to process user choice.", updatedState.CurrentSceneIndex)
			}

			// Подготавливаем данные текущего состояния для поиска
			nextSceneIndex := sceneIndex + 1

			// Вычисляем хеш состояния, которое *должно* получиться после выбора пользователя
			expectedStateHash, errHash := hashStateKey(
				request.UserChoice.ChoiceText,
				updatedState.GlobalFlags,
				updatedState.Relationship,
				updatedState.StoryVariables,
			)
			// --- DEBUG LOGGING: Результат вычисления хеша ---
			log.Printf("[DEBUG_HASH_CALC] Calculated expected state hash for scene %d: '%s'. Error: %v", nextSceneIndex, expectedStateHash, errHash)
			// --- END DEBUG LOGGING ---

			if errHash != nil {
				log.Printf("[GenerateNovelContent] Error calculating state hash, skipping cache check: %v", errHash)
				// Пропускаем блок поиска по кешу
			} else {
				// --- DEBUG LOGGING: Поиск по хешу ---
				log.Printf("[DEBUG_HASH_SEARCH] Attempting to find state by hash: %s", expectedStateHash)
				// --- END DEBUG LOGGING ---

				// Ищем готовое состояние по хешу, используя новый метод getCachedState
				existingState, err := s.getCachedState(ctx, request.NovelID, expectedStateHash, nextSceneIndex)

				if err == nil {
					// --- DEBUG LOGGING: Кеш найден ---
					log.Printf("[DEBUG_HASH_SEARCH] Found compatible state by hash %s for scene %d.", expectedStateHash, nextSceneIndex)
					// --- END DEBUG LOGGING ---

					// --- ВОССТАНОВЛЕННАЯ ЛОГИКА ИСПОЛЬЗОВАНИЯ КЕША ---
					log.Printf("[GenerateNovelContent] Using cached state for scene %d", nextSceneIndex)

					// Обновляем состояние текущего игрока данными из кеша
					playerName := updatedState.PlayerName
					playerGender := updatedState.PlayerGender
					previousChoices := updatedState.PreviousChoices // Сохраняем историю выборов ТЕКУЩЕГО игрока

					// Применяем состояние мира из кеша
					updatedState = *existingState // Теперь updatedState содержит мир из кеша

					// Восстанавливаем личные данные и историю выборов
					updatedState.PlayerName = playerName
					updatedState.PlayerGender = playerGender
					updatedState.PreviousChoices = previousChoices // Восстанавливаем выборы ТЕКУЩЕГО игрока
					updatedState.StateHash = expectedStateHash     // Сохраняем правильный хеш в состоянии

					// Устанавливаем правильный индекс сцены
					updatedState.CurrentSceneIndex = nextSceneIndex

					// Создаем ответ на основе обновленного состояния
					response := &domain.NovelContentResponse{
						State:      updatedState,
						NewContent: nil, // Контент будет извлечен ниже
					}

					// Извлекаем содержимое сцены из обновленного состояния
					sceneContent, err := s.extractSceneContent(&updatedState, nextSceneIndex)
					if err != nil {
						log.Printf("[GenerateNovelContent] Error extracting scene content from cached state: %v", err)
					} else {
						response.NewContent = sceneContent
					}

					// Сохраняем итоговое ОБЪЕДИНЕННОЕ состояние для ТЕКУЩЕГО пользователя
					finalStateData, err := json.Marshal(updatedState)
					if err != nil {
						log.Printf("[GenerateNovelContent] Error marshaling final state after cache: %v", err)
						// Критическая ошибка, не можем сохранить
						return nil, fmt.Errorf("failed to marshal final state after cache: %w", err)
					}
					err = s.novelRepo.SaveNovelState(ctx, request.NovelID, nextSceneIndex, request.UserID, expectedStateHash, finalStateData)
					if err != nil {
						log.Printf("[GenerateNovelContent] Error saving merged state after cache load for user %s: %v", request.UserID, err)
						// Не критично, возвращаем результат, но логируем ошибку сохранения
					}

					log.Printf("[GenerateNovelContent] Reused existing state for scene %d using hash %s for UserID %s.", nextSceneIndex, expectedStateHash, request.UserID)
					return response, nil // --- ВОЗВРАЩАЕМ РЕЗУЛЬТАТ ИЗ КЕША ---
				} else if errors.Is(err, pgx.ErrNoRows) {
					// --- DEBUG LOGGING: Кеш не найден ---
					log.Printf("[DEBUG_HASH_SEARCH] No compatible state found by hash %s (pgx.ErrNoRows). Proceeding to generate content.", expectedStateHash)
					// --- END DEBUG LOGGING ---
					// Состояние с таким хешом не найдено, продолжаем генерацию
				} else {
					// --- DEBUG LOGGING: Ошибка поиска по хешу ---
					log.Printf("[DEBUG_HASH_SEARCH] Error searching state by hash %s: %v. Proceeding to generate content.", expectedStateHash, err)
					// --- END DEBUG LOGGING ---
					// Другая ошибка при поиске по хешу, продолжаем генерацию через ИИ
				}
			}

			// Если не нашли существующее состояние (или была ошибка хеширования/поиска),
			// используем обновленное состояние для дальнейшей генерации
			*state = updatedState
		} else if request.UserChoice == nil && (state.CurrentStage == domain.StageSetup || state.CurrentStage == domain.StageSceneReady) {
			// Тут обрабатываем случай продолжения БЕЗ выбора пользователя (например, первый запрос после сетапа)
			// ПРОВЕРЯЕМ НА СУЩЕСТВОВАНИЕ СЦЕНЫ 0 В ТАБЛИЦЕ NOVEL_STATES
			if state.CurrentSceneIndex == 0 { // Только если текущая сцена - 0
				log.Printf("[GenerateNovelContent] Checking if scene 0 already exists in novel_states for NovelID: %s", request.NovelID)

				// Получаем существующую сцену 0 (первую сцену истории)
				existingSceneData, existErr := s.novelRepo.GetNovelStateBySceneIndex(ctx, request.NovelID, 0)
				if existErr == nil {
					// Нашли существующую сцену 0, десериализуем
					var existingScene domain.NovelState
					if unmarshalErr := json.Unmarshal(existingSceneData, &existingScene); unmarshalErr == nil {
						log.Printf("[GenerateNovelContent] Found existing scene 0 for NovelID: %s. Using it for user %s instead of generating.",
							request.NovelID, request.UserID)

						// Убедимся, что сцена 0 имеет стадию scene_ready
						if existingScene.CurrentStage == domain.StageSceneReady {
							// Копируем данные пользователя
							existingScene.PlayerName = state.PlayerName
							existingScene.PlayerGender = state.PlayerGender
							existingScene.PreviousChoices = state.PreviousChoices
							existingScene.Relationship = state.Relationship
							existingScene.GlobalFlags = state.GlobalFlags
							existingScene.StoryVariables = state.StoryVariables

							// Формируем ответ
							sceneContent, err := s.extractSceneContent(&existingScene, 0)
							if err != nil {
								log.Printf("[GenerateNovelContent] Error extracting content from existing scene 0: %v", err)
								sceneContent = nil
							}

							// Сохраняем состояние для текущего пользователя (на всякий случай, если GetLatest не вернул полное состояние)
							stateData, err := json.Marshal(existingScene)
							if err == nil {
								err = s.novelRepo.SaveNovelState(ctx, request.NovelID, 0, request.UserID, existingScene.StateHash, stateData)
								if err != nil {
									log.Printf("[GenerateNovelContent] Error saving scene 0 state for user %s: %v", request.UserID, err)
								}
							}

							response := &domain.NovelContentResponse{
								State:      existingScene,
								NewContent: sceneContent,
							}
							return response, nil // --- ВОЗВРАЩАЕМ СУЩЕСТВУЮЩУЮ СЦЕНУ 0 ---
						} else {
							log.Printf("[GenerateNovelContent] Found scene 0 but it's not in scene_ready stage (it's %s). Will generate.",
								existingScene.CurrentStage)
						}
					} else {
						log.Printf("[GenerateNovelContent] Error unmarshaling existing scene 0: %v", unmarshalErr)
					}
				} else {
					log.Printf("[GenerateNovelContent] No existing scene 0 found or error: %v. Will generate.", existErr)
				}
			} // Конец проверки if state.CurrentSceneIndex == 0
		} // Конец блока else if request.UserChoice == nil

		// Если мы дошли сюда, значит:
		// 1. Либо был обработан UserChoice (и state.CurrentSceneIndex увеличен до > 0)
		// 2. Либо UserChoice не было, но сцена 0 не найдена/не готова/не удалось использовать
		// В обоих случаях нам нужно генерировать контент для ТЕКУЩЕГО state.CurrentSceneIndex

		// Формируем запрос на продолжение новеллы для ИИ
		requestJSON, err = s.prepareContinuationRequest(state, request.UserChoice) // Передаем UserChoice, чтобы он попал в previous_choices, но prepareContinuationRequest больше НЕ увеличивает индекс
		if err != nil {
			return nil, fmt.Errorf("failed to prepare continuation request: %w", err)
		}
		log.Printf("[GenerateNovelContent] Prepared continuation request for NovelID: %s, SceneIndex: %d", request.NovelID, state.CurrentSceneIndex)

	} // Конец основного блока else (обработка существующего состояния)

	// --- ОТПРАВКА ЗАПРОСА К ИИ (только если requestJSON был сформирован) ---
	if requestJSON == nil {
		// Эта ситуация не должна возникать, если не было return раньше,
		// но добавим проверку на всякий случай.
		log.Printf("[GenerateNovelContent] ERROR: Reached AI request section but requestJSON is nil. State: %+v", state)
		return nil, fmt.Errorf("internal error: failed to determine AI request type")
	}

	// log.Printf("[GenerateNovelContent] Sending request to AI: %s", string(requestJSON))

	// Создаем сообщения для отправки в DeepSeek
	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleUser,
			Content: string(requestJSON),
		},
	}

	// Устанавливаем системный промпт
	messages = deepseek.SetSystemPrompt(messages, s.systemPrompt)

	// Отправляем запрос к DeepSeek
	// log.Printf("[GenerateNovelContent] Sending request to AI with messages: %+v", messages)
	response, err := s.deepseekClient.ChatCompletion(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("failed to get response from DeepSeek: %w", err)
	}
	log.Printf("[GenerateNovelContent] Raw response from AI: %s", response)

	// Извлекаем JSON из ответа модели
	jsonStr, err := extractJSONFromResponse(response)
	if err != nil {
		return nil, fmt.Errorf("failed to extract JSON from response: %w", err)
	}
	log.Printf("[GenerateNovelContent] Received JSON response from AI: %s", jsonStr)

	// Обрабатываем ответ и обновляем состояние новеллы
	novelResponse, err := s.processModelResponse(jsonStr, state)
	if err != nil {
		return nil, fmt.Errorf("failed to process model response: %w", err)
	}
	log.Printf("[GenerateNovelContent] Processed model response. New Stage: %s, New SceneIndex: %d, Has NewContent: %t",
		novelResponse.State.CurrentStage, novelResponse.State.CurrentSceneIndex, novelResponse.NewContent != nil)

	// Вычисляем хеш для нового сгенерированного состояния
	// Используем последний сделанный выбор (если был) или пустую строку
	lastChoice := ""
	if len(novelResponse.State.PreviousChoices) > 0 {
		lastChoice = novelResponse.State.PreviousChoices[len(novelResponse.State.PreviousChoices)-1]
	}
	finalStateHash, err := hashStateKey(
		lastChoice,
		novelResponse.State.GlobalFlags,
		novelResponse.State.Relationship,
		novelResponse.State.StoryVariables,
	)
	if err != nil {
		log.Printf("[GenerateNovelContent] Error calculating hash for final generated state: %v", err)
		// Не можем сохранить с правильным хешом, но можем вернуть результат
		// Можно использовать пустой хеш или возвращать ошибку?
		// return nil, fmt.Errorf("failed to calculate final state hash: %w", err)
		finalStateHash = "ERROR_HASHING_STATE" // Заглушка
	}
	novelResponse.State.StateHash = finalStateHash // Сохраняем хеш в объекте состояния

	// Сохраняем обновленное состояние новеллы, используя новый метод
	err = s.saveStateProgress(ctx, request.NovelID, novelResponse.State.CurrentSceneIndex, request.UserID, &novelResponse.State)
	if err != nil {
		// Ошибка сохранения не критична для возврата ответа, но важна
		log.Printf("[GenerateNovelContent] Error saving final generated state: %v", err)
		// return nil, fmt.Errorf("failed to save novel state: %w", err)
	}

	return novelResponse, nil
}

// HandleInlineResponse обрабатывает inline_response и применяет изменения к состоянию новеллы
func (s *NovelContentService) HandleInlineResponse(ctx context.Context, userID string, request domain.InlineResponseRequest) (*domain.InlineResponseResult, error) {
	log.Printf("[NovelContentService] HandleInlineResponse called for NovelID: %s, SceneIndex: %d, ChoiceID: %s",
		request.NovelID, request.SceneIndex, request.ChoiceID)

	// Получаем текущее состояние из репозитория
	stateData, sceneIndex, err := s.novelRepo.GetLatestNovelState(ctx, request.NovelID, userID)
	if err != nil {
		log.Printf("[NovelContentService] HandleInlineResponse - Error getting latest state: %v", err)
		return nil, fmt.Errorf("failed to get current state: %w", err)
	}

	// Проверяем, есть ли состояние
	if stateData == nil || sceneIndex < 0 {
		log.Printf("[NovelContentService] HandleInlineResponse - No state found for NovelID: %s, UserID: %s", request.NovelID, userID)
		return nil, fmt.Errorf("no existing state found for this novel")
	}

	if sceneIndex != request.SceneIndex {
		log.Printf("[NovelContentService] HandleInlineResponse - Scene index mismatch: current=%d, requested=%d", sceneIndex, request.SceneIndex)
		return nil, fmt.Errorf("scene index mismatch: current scene is %d, but request is for scene %d", sceneIndex, request.SceneIndex)
	}

	// Распаковываем текущее состояние
	var currentState domain.NovelState
	if err := json.Unmarshal(stateData, &currentState); err != nil {
		log.Printf("[NovelContentService] HandleInlineResponse - Error unmarshaling state: %v", err)
		return nil, fmt.Errorf("failed to unmarshal state data: %w", err)
	}

	// Проверяем, инициализирован ли массив сцен
	if len(currentState.Scenes) == 0 {
		log.Printf("[NovelContentService] HandleInlineResponse - No scenes found in state. NovelID: %s, UserID: %s",
			request.NovelID, userID)
		return nil, fmt.Errorf("no scenes found in state")
	}

	// Получаем текущую сцену
	if request.SceneIndex >= len(currentState.Scenes) {
		log.Printf("[NovelContentService] HandleInlineResponse - Scene index out of bounds: %d (max: %d)",
			request.SceneIndex, len(currentState.Scenes)-1)
		return nil, fmt.Errorf("scene index out of bounds")
	}

	currentScene := currentState.Scenes[request.SceneIndex]

	// Ищем событие inline_response с соответствующим choice_id
	var targetEvent *domain.Event
	for i := range currentScene.Events {
		event := &currentScene.Events[i]
		if event.EventType == "inline_response" && event.Data != nil {
			if choiceID, ok := event.Data["choice_id"].(string); ok && choiceID == request.ChoiceID {
				targetEvent = event
				break
			}
		}
	}

	if targetEvent == nil {
		log.Printf("[NovelContentService] HandleInlineResponse - Event with choice_id '%s' not found in scene %d",
			request.ChoiceID, request.SceneIndex)
		return nil, fmt.Errorf("inline_response event with choice_id '%s' not found", request.ChoiceID)
	}

	// Получаем responses из события
	responses, ok := targetEvent.Data["responses"].([]interface{})
	if !ok || len(responses) <= request.ResponseIdx {
		log.Printf("[NovelContentService] HandleInlineResponse - Response index %d out of bounds or invalid responses", request.ResponseIdx)
		return nil, fmt.Errorf("response index out of bounds or invalid responses array")
	}

	// Получаем выбранный response
	responseMap, ok := responses[request.ResponseIdx].(map[string]interface{})
	if !ok {
		log.Printf("[NovelContentService] HandleInlineResponse - Invalid response format at index %d", request.ResponseIdx)
		log.Printf("[NovelContentService] HandleInlineResponse - Response data type: %T, value: %+v", responses[request.ResponseIdx], responses[request.ResponseIdx])
		return nil, fmt.Errorf("invalid response format")
	}

	// Проверяем соответствие текста выбора
	responseText, ok := responseMap["choice_text"].(string)
	if !ok || responseText != request.ChoiceText {
		log.Printf("[NovelContentService] HandleInlineResponse - Choice text mismatch: '%s' vs '%s'", responseText, request.ChoiceText)
		log.Printf("[NovelContentService] HandleInlineResponse - Response keys available: %+v", getMapKeys(responseMap))
		// Не возвращаем ошибку, а просто логируем предупреждение, так как клиент мог получить устаревшие данные
		log.Printf("[NovelContentService] HandleInlineResponse - WARNING: Proceeding despite text mismatch")
	}

	// Создаем структуру для отслеживания изменений
	stateChanges := domain.NovelStateChanges{
		Relationship:   make(map[string]int),
		StoryVariables: make(map[string]interface{}),
		GlobalFlags:    []string{},
	}

	// Вычисляем изменения от выбранного response и ПРИМЕНЯЕМ их к состоянию
	// 1. Изменения в отношениях (relationship)
	if relationshipChanges, ok := responseMap["relationship_changes"].(map[string]interface{}); ok {
		for character, valueInterface := range relationshipChanges {
			if value, ok := valueInterface.(float64); ok {
				// Обновляем значение в состоянии
				intValue := int(value)
				if currentState.Relationship == nil {
					currentState.Relationship = make(map[string]int)
				}

				// Получаем текущее значение из состояния или 0, если его нет
				currentValue := currentState.Relationship[character]
				// Расчитываем новое значение
				newValue := currentValue + intValue
				// Применяем к состоянию
				currentState.Relationship[character] = newValue

				// Добавляем в stateChanges для отправки клиенту
				stateChanges.Relationship[character] = newValue

				log.Printf("[NovelContentService] HandleInlineResponse - Applied relationship change for '%s': %+d (now %d)",
					character, intValue, newValue)
			} else {
				log.Printf("[NovelContentService] HandleInlineResponse - Invalid relationship change value type for '%s': %T",
					character, valueInterface)
			}
		}
	} else if responseMap["relationship_changes"] != nil {
		log.Printf("[NovelContentService] HandleInlineResponse - relationship_changes has unexpected type: %T",
			responseMap["relationship_changes"])
	}

	// 2. Добавление глобальных флагов
	if flagsToAdd, ok := responseMap["add_global_flags"].([]interface{}); ok {
		for _, flagInterface := range flagsToAdd {
			if flag, ok := flagInterface.(string); ok {
				// Проверяем, не существует ли уже такой флаг
				flagExists := false
				for _, existingFlag := range currentState.GlobalFlags {
					if existingFlag == flag {
						flagExists = true
						break
					}
				}

				if !flagExists {
					// Добавляем флаг в состояние
					currentState.GlobalFlags = append(currentState.GlobalFlags, flag)
					// Добавляем флаг в stateChanges для отправки клиенту
					stateChanges.GlobalFlags = append(stateChanges.GlobalFlags, flag)
					log.Printf("[NovelContentService] HandleInlineResponse - Added global flag to state: '%s'", flag)
				}
			} else {
				log.Printf("[NovelContentService] HandleInlineResponse - Invalid flag type: %T", flagInterface)
			}
		}
	} else if responseMap["add_global_flags"] != nil {
		log.Printf("[NovelContentService] HandleInlineResponse - add_global_flags has unexpected type: %T",
			responseMap["add_global_flags"])
	}

	// 3. Обновление story_variables
	if variableChanges, ok := responseMap["story_variables"].(map[string]interface{}); ok {
		if currentState.StoryVariables == nil {
			currentState.StoryVariables = make(map[string]interface{})
		}
		for key, value := range variableChanges {
			// Применяем изменение к состоянию
			currentState.StoryVariables[key] = value
			// Добавляем переменную в stateChanges для отправки клиенту
			stateChanges.StoryVariables[key] = value
			log.Printf("[NovelContentService] HandleInlineResponse - Updated story variable '%s': %v", key, value)
		}
	} else if responseMap["story_variables"] != nil {
		log.Printf("[NovelContentService] HandleInlineResponse - story_variables has unexpected type: %T",
			responseMap["story_variables"])
	}

	// Получаем события для отображения после выбора
	var nextEvents []domain.SimplifiedEvent
	if responseEvents, ok := responseMap["response_events"].([]interface{}); ok {
		events := make([]domain.Event, 0, len(responseEvents))

		for i, eventInterface := range responseEvents {
			if eventMap, ok := eventInterface.(map[string]interface{}); ok {
				eventType, hasType := eventMap["event_type"].(string)
				if !hasType {
					log.Printf("[NovelContentService] HandleInlineResponse - Response event #%d missing event_type", i)
					continue
				}

				event := domain.Event{
					EventType: eventType,
				}

				// Заполняем поля события в зависимости от их наличия
				if text, ok := eventMap["text"].(string); ok {
					event.Text = text
				}
				if speaker, ok := eventMap["speaker"].(string); ok {
					event.Speaker = speaker
				}
				// И так далее для других полей...

				events = append(events, event)
			} else {
				log.Printf("[NovelContentService] HandleInlineResponse - Invalid response event format at index %d: %T",
					i, eventInterface)
			}
		}

		// Преобразуем события в упрощенную форму для клиента
		nextEvents = convertEventsToSimplified(events)
		log.Printf("[NovelContentService] HandleInlineResponse - Created %d next events from %d response events",
			len(nextEvents), len(responseEvents))
	} else {
		log.Printf("[NovelContentService] HandleInlineResponse - response_events has unexpected type or is missing: %T",
			responseMap["response_events"])
	}

	// Важно: сохраняем изменения в базе данных
	// Добавляем выбор в список предыдущих выборов
	currentState.PreviousChoices = append(currentState.PreviousChoices, request.ChoiceText)
	log.Printf("[NovelContentService] HandleInlineResponse - Added choice '%s' to previous choices", request.ChoiceText)

	// Обновляем хеш состояния
	stateHash := calculateStateHash(&currentState)
	currentState.StateHash = stateHash
	log.Printf("[NovelContentService] HandleInlineResponse - Calculated new state hash: %s", stateHash)

	// Сохраняем обновленное состояние
	err = s.saveStateProgress(ctx, request.NovelID, request.SceneIndex, userID, &currentState)
	if err != nil {
		log.Printf("[NovelContentService] HandleInlineResponse - Error saving updated state: %v", err)
		// Несмотря на ошибку сохранения, продолжаем и возвращаем изменения клиенту
	} else {
		log.Printf("[NovelContentService] HandleInlineResponse - Successfully saved updated state to database")
	}

	log.Printf("[NovelContentService] HandleInlineResponse - Successfully processed inline response for NovelID: %s, SceneIndex: %d",
		request.NovelID, request.SceneIndex)

	// Формируем и возвращаем результат
	return &domain.InlineResponseResult{
		Success:      true,
		UpdatedState: &stateChanges,
		NextEvents:   nextEvents,
	}, nil
}

// getMapKeys вспомогательная функция для получения списка ключей из карты
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// convertEventsToSimplified преобразует события в упрощенную форму для клиента
func convertEventsToSimplified(events []domain.Event) []domain.SimplifiedEvent {
	result := make([]domain.SimplifiedEvent, len(events))

	for i, event := range events {
		result[i] = domain.SimplifiedEvent{
			EventType:   event.EventType,
			Speaker:     event.Speaker,
			Text:        event.Text,
			Character:   event.Character,
			From:        event.From,
			To:          event.To,
			Description: event.Description,
		}

		// Копируем choices, если они есть
		if len(event.Choices) > 0 {
			simplifiedChoices := make([]domain.SimplifiedChoice, len(event.Choices))
			for j, choice := range event.Choices {
				simplifiedChoices[j] = domain.SimplifiedChoice{
					Text: choice.Text,
				}
			}
			result[i].Choices = simplifiedChoices
		}

		// Копируем дополнительные данные для специальных типов событий
		if event.EventType == "inline_choice" && event.Data != nil {
			if choiceID, ok := event.Data["choice_id"].(string); ok {
				result[i].ChoiceID = choiceID
			}
		}
	}

	return result
}

// calculateStateHash вычисляет хеш состояния на основе его динамических элементов
func calculateStateHash(state *domain.NovelState) string {
	if state == nil {
		return ""
	}

	// Создаем временную структуру только с динамическими элементами
	hashStruct := struct {
		GlobalFlags     []string               `json:"global_flags"`
		Relationship    map[string]int         `json:"relationship"`
		StoryVariables  map[string]interface{} `json:"story_variables"`
		PreviousChoices []string               `json:"previous_choices"`
	}{
		GlobalFlags:     state.GlobalFlags,
		Relationship:    state.Relationship,
		StoryVariables:  state.StoryVariables,
		PreviousChoices: state.PreviousChoices,
	}

	data, err := json.Marshal(hashStruct)
	if err != nil {
		log.Printf("[calculateStateHash] Error marshaling state: %v", err)
		return ""
	}

	hash := md5.Sum(data)
	return hex.EncodeToString(hash[:])
}

// getCachedState сначала ищет состояние в user_story_progress, если не находит,
// пытается найти в novel_states (для обратной совместимости).
// Если находит, создает полное состояние, объединяя статический сетап с динамическим прогрессом.
func (s *NovelContentService) getCachedState(ctx context.Context, novelID uuid.UUID, stateHash string, nextSceneIndex int) (*domain.NovelState, error) {
	log.Printf("[GetCachedState] Searching for cached state with hash: %s for scene: %d", stateHash, nextSceneIndex)

	// Сначала пробуем найти в новой таблице user_story_progress
	progress, err := s.novelRepo.GetUserStoryProgressByHash(ctx, stateHash)
	if err == nil {
		// Нашли прогресс по хешу, теперь нужно получить сетап новеллы
		setupStateData, err := s.novelRepo.GetNovelSetupState(ctx, novelID)
		if err != nil {
			log.Printf("[GetCachedState] Error getting setup state for novel %s: %v", novelID, err)
			return nil, fmt.Errorf("failed to get setup state: %w", err)
		}

		// Десериализуем сетап
		var setupState domain.NovelState
		if err := json.Unmarshal(setupStateData, &setupState); err != nil {
			log.Printf("[GetCachedState] Error unmarshaling setup state: %v", err)
			return nil, fmt.Errorf("failed to unmarshal setup state: %w", err)
		}

		// Объединяем сетап с прогрессом
		mergedState := MergeStateWithProgress(&setupState, progress)

		// Устанавливаем правильный индекс сцены
		mergedState.CurrentSceneIndex = nextSceneIndex

		log.Printf("[GetCachedState] Found and merged state from user_story_progress for hash: %s", stateHash)
		return mergedState, nil
	}

	// Если не нашли в user_story_progress и ошибка не "не найдено", возвращаем ошибку
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		log.Printf("[GetCachedState] Error getting user story progress by hash: %v", err)
		return nil, fmt.Errorf("failed to get user story progress: %w", err)
	}

	// Для обратной совместимости пробуем найти в старой таблице novel_states
	log.Printf("[GetCachedState] Trying fallback to novel_states for hash: %s", stateHash)
	existingStateData, err := s.novelRepo.GetNovelStateByHash(ctx, stateHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("[GetCachedState] State not found in both tables for hash: %s", stateHash)
			return nil, pgx.ErrNoRows
		}
		log.Printf("[GetCachedState] Error getting state from novel_states: %v", err)
		return nil, fmt.Errorf("failed to get novel state: %w", err)
	}

	// Десериализуем найденное состояние
	var existingState domain.NovelState
	if err := json.Unmarshal(existingStateData, &existingState); err != nil {
		log.Printf("[GetCachedState] Error unmarshaling state data: %v", err)
		return nil, fmt.Errorf("failed to unmarshal state data: %w", err)
	}

	// Нормализуем индекс сцены
	existingState.CurrentSceneIndex = nextSceneIndex

	log.Printf("[GetCachedState] Found state from novel_states for hash: %s", stateHash)
	return &existingState, nil
}

// saveStateProgress сохраняет состояние и прогресс пользователя.
// Также сохраняет сетап в таблицу novels если current_stage = "setup".
func (s *NovelContentService) saveStateProgress(ctx context.Context, novelID uuid.UUID, sceneIndex int, userID string, state *domain.NovelState) error {
	// Сериализуем полное состояние для сохранения
	stateData, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Проверяем, является ли это сетапом по значению current_stage
	if state.CurrentStage == domain.StageSetup {
		log.Printf("[saveStateProgress] Detected setup state (current_stage='%s'). Saving to novels table. NovelID: %s",
			state.CurrentStage, novelID)
		err = s.novelRepo.SaveNovelSetupState(ctx, novelID, stateData)
		if err != nil {
			log.Printf("[saveStateProgress] Warning: Failed to save setup state to novels table: %v. Continuing with standard save...", err)
			// Продолжаем выполнение даже при ошибке
		} else {
			log.Printf("[saveStateProgress] Successfully saved setup state to novels table for NovelID: %s", novelID)
		}
	}

	// Сначала пробуем сохранить полное состояние с хешем, исключая user_id
	err = s.novelRepo.SaveNovelState(ctx, novelID, sceneIndex, userID, state.StateHash, stateData)
	if err != nil {
		return fmt.Errorf("failed to save novel state: %w", err)
	}

	// Подготавливаем объект прогресса, сохраняя только динамические элементы
	progress := &domain.UserStoryProgress{
		NovelID:           novelID,
		UserID:            userID,
		SceneIndex:        sceneIndex,
		GlobalFlags:       state.GlobalFlags,
		Relationship:      state.Relationship,
		StoryVariables:    state.StoryVariables,
		PreviousChoices:   state.PreviousChoices,
		StorySummarySoFar: state.StorySummarySoFar,
		FutureDirection:   state.FutureDirection,
		StateHash:         state.StateHash,
	}

	// Затем сохраняем прогресс пользователя
	err = s.novelRepo.SaveUserStoryProgress(ctx, novelID, sceneIndex, userID, progress)
	if err != nil {
		return fmt.Errorf("failed to save user story progress: %w", err)
	}

	return nil
}

// MergeStateWithProgress объединяет статические данные из базового состояния (сетапа)
// с динамическими элементами из прогресса пользователя
func MergeStateWithProgress(baseState *domain.NovelState, progress *domain.UserStoryProgress) *domain.NovelState {
	if baseState == nil {
		log.Println("[MergeStateWithProgress] Error: baseState is nil")
		return nil
	}

	if progress == nil {
		log.Println("[MergeStateWithProgress] Warning: progress is nil, returning only baseState")
		return baseState
	}

	// Создаем копию базового состояния
	result := *baseState

	// Заменяем динамические элементы из прогресса
	result.GlobalFlags = progress.GlobalFlags
	result.Relationship = progress.Relationship
	result.StoryVariables = progress.StoryVariables
	result.PreviousChoices = progress.PreviousChoices
	result.StorySummarySoFar = progress.StorySummarySoFar
	result.FutureDirection = progress.FutureDirection

	// Добавляем информацию о хеше из прогресса
	result.StateHash = progress.StateHash

	return &result
}

// NovelStateToUserProgress преобразует NovelState в UserStoryProgress для сохранения прогресса пользователя
func NovelStateToUserProgress(state *domain.NovelState, novelID uuid.UUID, userID string, sceneIndex int) *domain.UserStoryProgress {
	if state == nil {
		return nil
	}

	progress := &domain.UserStoryProgress{
		NovelID:           novelID,
		UserID:            userID,
		SceneIndex:        sceneIndex,
		GlobalFlags:       state.GlobalFlags,
		Relationship:      state.Relationship,
		StoryVariables:    state.StoryVariables,
		PreviousChoices:   state.PreviousChoices,
		StorySummarySoFar: state.StorySummarySoFar,
		FutureDirection:   state.FutureDirection,
		StateHash:         state.StateHash,
	}

	return progress
}
