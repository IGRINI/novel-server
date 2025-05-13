package worker

import (
	// Для рендеринга шаблона
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	sharedInterfaces "novel-server/shared/interfaces" // <<< Используем этот импорт
	"novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/utils" // <<< CORRECTED import path for utils

	// Импортируем конфиг для AIMaxAttempts
	// Добавляем модель
	// "novel-server/story-generator/internal/repository" // <<< Удаляем старый импорт
	"novel-server/story-generator/internal/service" // Для шаблонизации промтов
	// <<< ДОБАВЛЕНО: для strings.Replace >>>
	"time"

	// "novel-server/story-generator/internal/model" // <<< Удаляем импорт model
	"strings"

	"github.com/google/uuid"
)

// Ключ для системного промпта
const (
	systemPromptKey = "system_prompt" // Ключ, по которому ищем системный промпт в базе
)

// --- Структуры для валидации JSON ответа AI ---

// NarratorPPValidation используется для проверки внутренней структуры "pp" в ответе Narrator
type NarratorPPValidation struct {
	TH *[]string `json:"th"` // tags for story - required, non-empty
	ST *string   `json:"st"` // visual style - required, non-empty
	WL *string   `json:"wl"` // world lore - required, non-empty
	// dt, dl, dc are optional and not strictly validated here
}

// NarratorValidation используется для проверки базовой структуры ответа Narrator
type NarratorValidation struct {
	Title             *string               `json:"t"`  // required, non-empty
	Description       *string               `json:"sd"` // required, non-empty
	Genre             *string               `json:"gn"` // required, non-empty
	ProtagonistName   *string               `json:"pn"` // required, non-empty
	ProtagonistDesc   *string               `json:"pd"` // required, non-empty
	WorldContext      *string               `json:"wc"` // required, non-empty
	StorySummary      *string               `json:"ss"` // required, non-empty
	CoreStats         json.RawMessage       `json:"cs"` // required, must contain exactly 4 stats
	PlayerPreferences *NarratorPPValidation `json:"pp"` // required
	// fr (Franchise) is optional
}

// CoreStatItemValidation используется для проверки каждого объекта стата в Narrator.cs
type CoreStatItemValidation struct {
	Name        *string `json:"n"`  // required, non-empty
	Description *string `json:"d"`  // required, non-empty
	GameOver    *string `json:"go"` // required, "min", "max", or "both"
}

// ResultStringValidation используется для промптов, возвращающих {"result": "string"}
type ResultStringValidation struct {
	Result *string `json:"result"` // required, non-empty
}

// ContentModerationValidation используется для проверки ответа ContentModeration
type ContentModerationValidation struct {
	AC *int `json:"ac"` // required, must be 0 or 1
}

// CharacterGenerationItemValidation используется для проверки каждого объекта NPC
type CharacterGenerationItemValidation struct {
	ID              *string         `json:"id"`                      // required, non-empty
	Name            *string         `json:"name"`                    // required, non-empty
	Role            *string         `json:"role"`                    // required, non-empty
	Traits          *string         `json:"traits"`                  // required, non-empty
	Relationship    json.RawMessage `json:"relationship"`            // required, must contain "protaghonist" key
	Memories        *string         `json:"memories"`                // required, non-empty
	PlotHook        *string         `json:"plotHook"`                // required, non-empty
	ImagePromptDesc *string         `json:"image_prompt_descriptor"` // required, non-empty
	ImageRefName    *string         `json:"image_reference_name"`    // required, non-empty
}

// JsonGenerationChoiceOptionConsequenceValidation используется для проверки "cons" в JsonGeneration
type JsonGenerationChoiceOptionConsequenceValidation struct {
	CS json.RawMessage `json:"cs"` // required, map[string]int
	RT *string         `json:"rt"` // required, can be empty string
}

// JsonGenerationChoiceOptionValidation используется для проверки каждой опции в JsonGeneration
type JsonGenerationChoiceOptionValidation struct {
	Txt  *string                                          `json:"txt"`  // required, non-empty
	Cons *JsonGenerationChoiceOptionConsequenceValidation `json:"cons"` // required
}

// JsonGenerationChoiceValidation используется для проверки каждого блока выбора в JsonGeneration
type JsonGenerationChoiceValidation struct {
	SceneName *string                                `json:"scene"` // required, non-empty
	Name      *string                                `json:"name"`  // required, non-empty
	Desc      *string                                `json:"desc"`  // required, non-empty (markdown ok)
	Opts      []JsonGenerationChoiceOptionValidation `json:"opts"`  // required, must have exactly 2 options
}

// JsonGenerationValidation используется для проверки ответа JsonGeneration
type JsonGenerationValidation struct {
	Location *string                          `json:"location"` // required, non-empty
	Choices  []JsonGenerationChoiceValidation `json:"ch"`       // required, non-empty array
}

// ScenePlannerCharacterSuggestionValidation ...
type ScenePlannerCharacterSuggestionValidation struct {
	Role   *string `json:"role"`   // required, non-empty
	Reason *string `json:"reason"` // required, non-empty
}

// ScenePlannerCardSuggestionValidation ...
type ScenePlannerCardSuggestionValidation struct {
	ImagePromptDesc *string `json:"image_prompt_descriptor"` // required, non-empty
	ImageRefName    *string `json:"image_reference_name"`    // required, non-empty
	Title           *string `json:"title"`                   // required, non-empty
	Reason          *string `json:"reason"`                  // required, non-empty
}

// ScenePlannerCharacterUpdateValidation ...
type ScenePlannerCharacterUpdateValidation struct {
	ID *string `json:"id"` // required, non-empty
	// memory_update, relationship_update are optional
}

// ScenePlannerCharacterToRemoveValidation ...
type ScenePlannerCharacterToRemoveValidation struct {
	ID     *string `json:"id"`     // required, non-empty
	Reason *string `json:"reason"` // required, non-empty
}

// ScenePlannerValidation используется для проверки ответа ScenePlanner
type ScenePlannerValidation struct {
	NeedNewCharacter        *bool                                       `json:"need_new_character"`        // required
	NewCharacterSuggestions []ScenePlannerCharacterSuggestionValidation `json:"new_character_suggestions"` // optional array
	NewCardSuggestions      []ScenePlannerCardSuggestionValidation      `json:"new_card_suggestions"`      // optional array
	CharacterUpdates        []ScenePlannerCharacterUpdateValidation     `json:"character_updates"`         // optional array
	CharactersToRemove      []ScenePlannerCharacterToRemoveValidation   `json:"characters_to_remove"`      // optional array
	SceneFocus              *string                                     `json:"scene_focus"`               // required, non-empty
}

// SetupValidation используется для проверки структуры ответа NovelSetup
// Используем существующую sharedModels.NovelSetupContent, т.к. она точно описывает ожидаемый результат
// type SetupValidation sharedModels.NovelSetupContent

// SceneValidation используется для проверки структуры ответа NovelCreator и NovelFirstSceneCreator
type SceneValidation struct {
	StorySummarySoFar *string           `json:"sssf"` // Указатель для проверки наличия
	FutureDirection   *string           `json:"fd"`   // Указатель для проверки наличия
	Choices           []json.RawMessage `json:"ch"`   // Проверяем наличие и что это массив
	// vis и svd опциональны, не проверяем строго здесь
}

// ContinueValidation используется для проверки структуры ответа NovelContinueCreator
type ContinueValidation struct {
	StorySummarySoFar *string           `json:"sssf"`
	FutureDirection   *string           `json:"fd"`
	NewPlayerDesc     *string           `json:"npd"`
	CoreStatsReset    json.RawMessage   `json:"csr"` // Проверяем наличие
	EndingTextPrev    *string           `json:"etp"`
	Choices           []json.RawMessage `json:"ch"`
}

// GameOverValidation используется для проверки структуры ответа NovelGameOverCreator
type GameOverValidation struct {
	EndingText *string           `json:"et"` // Может быть nil, если используются Choices
	Choices    []json.RawMessage `json:"ch"` // Может быть nil, если используется EndingText
}

// --- Конец структур для валидации ---

// TaskHandler обрабатывает задачи генерации
type TaskHandler struct {
	// <<< УДАЛЕНО: cfg *config.Config >>>
	maxAttempts    int           // <<< ДОБАВЛЕНО
	baseRetryDelay time.Duration // <<< ДОБАВЛЕНО
	aiTimeout      time.Duration // <<< ДОБАВЛЕНО
	aiClient       service.AIClient
	resultRepo     sharedInterfaces.GenerationResultRepository
	sceneRepo      sharedInterfaces.StorySceneRepository // <<< ДОБАВЛЕНО: Репозиторий для сцен >>>
	notifier       service.Notifier
	prompts        *service.PromptProvider
	db             sharedInterfaces.DBTX // <<< ДОБАВЛЕНО: Пул соединений БД >>>
	// configProvider *service.ConfigProvider // <<< УДАЛЕНО
}

// NewTaskHandler создает новый экземпляр обработчика задач
func NewTaskHandler(
	// <<< ПРИНИМАЕМ ПАРАМЕТРЫ ВМЕСТО КОНФИГА >>>
	maxAttempts int,
	baseRetryDelay time.Duration,
	aiTimeout time.Duration,
	aiClient service.AIClient,
	resultRepo sharedInterfaces.GenerationResultRepository,
	sceneRepo sharedInterfaces.StorySceneRepository, // <<< ДОБАВЛЕНО: Принимаем репозиторий сцен >>>
	notifier service.Notifier,
	promptProvider *service.PromptProvider,
	dbPool sharedInterfaces.DBTX, // <<< ДОБАВЛЕНО: Принимаем пул соединений >>>
	// configProvider *service.ConfigProvider, // <<< УДАЛЕНО
) *TaskHandler {
	return &TaskHandler{
		// <<< СОХРАНЯЕМ ПАРАМЕТРЫ >>>
		maxAttempts:    maxAttempts,
		baseRetryDelay: baseRetryDelay,
		aiTimeout:      aiTimeout,
		aiClient:       aiClient,
		resultRepo:     resultRepo,
		sceneRepo:      sceneRepo, // <<< ДОБАВЛЕНО: Сохраняем репозиторий сцен >>>
		notifier:       notifier,
		prompts:        promptProvider,
		db:             dbPool, // <<< ДОБАВЛЕНО: Сохраняем пул соединений >>>
		// configProvider: configProvider, // <<< УДАЛЕНО
	}
}

// Handle обрабатывает одну задачу генерации
func (h *TaskHandler) Handle(payload messaging.GenerationTaskPayload) (err error) {
	MetricsIncrementTasksReceived() // Увеличиваем счетчик полученных задач
	taskStartTime := time.Now()     // Замеряем время начала обработки задачи
	log.Printf("[TaskID: %s] Обработка задачи: UserID=%s, PromptType=%s, Language=%s",
		payload.TaskID, payload.UserID, payload.PromptType, payload.Language)

	// Переменная для статуса задачи (для финальной метрики длительности)
	taskStatus := "success" // По умолчанию успех

	// Объявляем переменные для результатов и ошибок ЗАРАНЕЕ
	var aiResponse string
	var processingErr error
	var completedAt time.Time
	var finalUsageInfo service.UsageInfo
	var language string   // <<< Перенесено сюда
	var promptKey string  // <<< Перенесено сюда
	var promptText string // <<< Перенесено сюда

	// Defer для записи общей длительности задачи и отправки метрик
	defer func() {
		duration := time.Since(taskStartTime)
		MetricsRecordTaskProcessingDuration(duration) // Записываем общую длительность
		if err != nil {
			// Если была ошибка на любом этапе, записываем метрику ошибки
			// Тип ошибки уже должен быть записан ранее (prompt_preparation, user_input_empty, ai_error, save_error)
			taskStatus = "failed" // Меняем статус для лога
		}
		// Принудительно отправляем все собранные метрики для этой задачи
		if pushErr := PushMetricsNow(); pushErr != nil {
			log.Printf("[TaskID: %s][WARN] Не удалось принудительно отправить метрики в конце задачи: %v", payload.TaskID, pushErr)
		}
		log.Printf("[TaskID: %s] Завершение обработки задачи. Статус: %s. Общее время: %v.", payload.TaskID, taskStatus, duration)
	}()

	// --- Этап 2: Получение промпта задачи ---
	language = payload.Language // <<< Используем язык из payload >>>
	if language == "" {         // <<< Добавляем fallback и здесь >>>
		language = "en"
		log.Printf("[TaskID: %s][WARN] Язык в payload пуст, используется запасной язык '%s' для промпта задачи.", payload.TaskID, language)
	}
	promptKey = string(payload.PromptType) // <<< Используем PromptType как ключ
	// ВСЕГДА ЗАПРАШИВАЕМ ПРОМПТ НА АНГЛИЙСКОМ
	basePromptText, err := h.prompts.GetPrompt(context.Background(), promptKey, "en")
	if err != nil {
		log.Printf("[TaskID: %s] Ошибка получения базового (en) промпта из кэша: %v. key='%s', lang='en'", payload.TaskID, err, promptKey)
		MetricsIncrementTaskFailed("prompt_cache_miss")
		processingErr = fmt.Errorf("failed to get base prompt 'en/%s': %w", promptKey, err)
		// Ошибка получения промпта - дальше не идем, сразу сохраняем/уведомляем
	} else {
		// <<< Промпт получен, продолжаем обработку >>>

		// Определяем инструкцию для языка
		langInstruction := getLanguageInstruction(language)
		promptText = strings.Replace(basePromptText, "{{LANGUAGE_DEFINITION}}", langInstruction, 1)
		if promptText == basePromptText && strings.Contains(basePromptText, "{{LANGUAGE_DEFINITION}}") {
			log.Printf("[TaskID: %s][WARN] Placeholder {{LANGUAGE_DEFINITION}} не был заменен в промпте '%s' для языка '%s'. Убедитесь, что плейсхолдер присутствует в 'en' версии промпта.", payload.TaskID, promptKey, language)
		} else if promptText != basePromptText {
			log.Printf("[TaskID: %s] Placeholder {{LANGUAGE_DEFINITION}} успешно заменен на '%s' для языка '%s'.", payload.TaskID, langInstruction, language)
		}

		// <<< Заменяем плейсхолдер {{USER_INPUT}} --- ЭТА ЛОГИКА УДАЛЕНА >>>
		// userInputForAI = strings.Replace(promptText, "{{USER_INPUT}}", payload.UserInput, 1)
		// if userInputForAI == promptText && strings.Contains(promptText, "{{USER_INPUT}}") {
		// 	// Если плейсхолдер был, но замена не произошла (например, UserInput пуст)
		// 	log.Printf("[TaskID: %s][WARN] Placeholder {{USER_INPUT}} не был заменен в промпте типа '%s'. Возможно, UserInput пуст.", payload.TaskID, payload.PromptType)
		// 	// Решаем, что делать дальше: либо ошибка, либо использовать промпт как есть
		// 	// Пока оставим как есть, AI получит промпт с незамененным плейсхолдером или без него.
		// }
		// log.Printf("[TaskID: %s] Final UserInput for AI (length: %d).", payload.TaskID, len(userInputForAI))
		log.Printf("[TaskID: %s] Prompt text (to be used as system prompt, length: %d). User input (to be used as user input, length: %d)", payload.TaskID, len(promptText), len(payload.UserInput))

		// --- Этап 3: Вызов AI API с ретраями (только если промпт загружен) ---
		if payload.UserInput == "" && payload.PromptType != "" { // TODO: Уточнить, всегда ли нужен UserInput? Рассмотреть случаи, когда UserInput может быть пустым, но это валидно.
			// Если UserInput пуст, но тип промпта предполагает его наличие (например, все кроме начального Narrator),
			// это может быть ошибкой или просто означать, что пользователь ничего не ввел.
			// Для некоторых PromptType пустой UserInput может быть валидным.
			// Пока оставим как есть, но стоит пересмотреть эту логику.
			// Если UserInput не обязателен для данного PromptType, то ошибку генерировать не нужно.
			// Возможно, стоит передавать promptText как userInput, если payload.UserInput пуст, а systemPrompt оставить пустым или базовым?
			// Текущая логика: если payload.UserInput пуст, он и передается пустым.
			// Если это проблема, AI вернет ошибку, или это будет обработано на уровне логики AI.
			// Оставим лог для информации, если payload.UserInput пуст.
			log.Printf("[TaskID: %s] UserInput is empty for PromptType '%s'. Proceeding with empty user input to AI.", payload.TaskID, payload.PromptType)
			// processingErr = fmt.Errorf("ошибка: userInput пуст для PromptType '%s'", payload.PromptType)
			// log.Printf("[TaskID: %s] %v", payload.TaskID, processingErr)
			// MetricsIncrementTaskFailed("user_input_empty")
			// err = fmt.Errorf("ошибка валидации: %w", processingErr)
			// // <<< УДАЛЕНО: return err >>>
		}
		// Старая проверка на пустой UserInput закомментирована, так как UserInput может быть опциональным.
		// AI клиент сам разберется с пустым UserInput, если это проблема для конкретной модели/задачи.
		// else {
		baseDelay := h.baseRetryDelay

		for attempt := 1; attempt <= h.maxAttempts; attempt++ {
			aiCallStartTime := time.Now()
			log.Printf("[TaskID: %s] Вызов AI API (Попытка %d/%d)...", payload.TaskID, attempt, h.maxAttempts)
			ctx, cancel := context.WithTimeout(context.Background(), h.aiTimeout)

			var attemptUsageInfo service.UsageInfo
			var attemptErr error
			// ИЗМЕНЕН ВЫЗОВ: promptText передается как systemPrompt, payload.UserInput как userInput
			aiResponse, attemptUsageInfo, attemptErr = h.aiClient.GenerateText(ctx,
				payload.UserID,
				promptText,        // <<< ИСПОЛЬЗУЕМ promptText КАК systemPrompt
				payload.UserInput, // <<< ИСПОЛЬЗУЕМ payload.UserInput КАК userInput
				service.GenerationParams{})
			cancel()

			aiCallDuration := time.Since(aiCallStartTime)
			aiStatusLabel := "success"

			if attemptErr == nil {
				log.Printf("[TaskID: %s] AI API успешно ответил (Попытка %d). Время ответа: %v", payload.TaskID, attempt, aiCallDuration)
				log.Printf("[TaskID: %s] Raw AI Response (length: %d): %s", payload.TaskID, len(aiResponse), aiResponse)
				MetricsRecordAIRequest("unknown", aiStatusLabel, aiCallDuration)
				if attemptUsageInfo.TotalTokens > 0 || attemptUsageInfo.EstimatedCostUSD > 0 {
					MetricsRecordAITokens("unknown", float64(attemptUsageInfo.PromptTokens), float64(attemptUsageInfo.CompletionTokens))
					MetricsAddAICost("unknown", attemptUsageInfo.EstimatedCostUSD)
					log.Printf("[TaskID: %s][Attempt %d Metrics] Tokens: P=%d, C=%d. Cost: %.6f USD",
						payload.TaskID, attempt, attemptUsageInfo.PromptTokens, attemptUsageInfo.CompletionTokens, attemptUsageInfo.EstimatedCostUSD)
				}
				finalUsageInfo = attemptUsageInfo
				processingErr = nil

				// <<< MODIFIED: Add explicit BEFORE/AFTER logging for ExtractJsonContent >>>
				originalAIResponse := aiResponse // Keep original

				// --- Log BEFORE extraction ---
				log.Printf("[TaskID: %s] BEFORE ExtractJsonContent. Raw AI Response (length: %d): %s", payload.TaskID, len(originalAIResponse), originalAIResponse)

				extractedJSON := utils.ExtractJsonContent(originalAIResponse) // Always pass original here

				// --- Log AFTER extraction ---
				log.Printf("[TaskID: %s] AFTER ExtractJsonContent. Extracted JSON (length: %d): %s", payload.TaskID, len(extractedJSON), extractedJSON)

				if extractedJSON == "" {
					log.Printf("[TaskID: %s][WARN] ExtractJsonContent returned empty string, using original AI response.", payload.TaskID)
					// Keep original aiResponse (which is originalAIResponse)
					aiResponse = originalAIResponse
				} else {
					if extractedJSON != originalAIResponse {
						log.Printf("[TaskID: %s] ExtractJsonContent modified the AI response.", payload.TaskID)
					} else {
						log.Printf("[TaskID: %s] ExtractJsonContent did not modify the AI response.", payload.TaskID)
					}
					aiResponse = extractedJSON // Use the potentially cleaned JSON moving forward
				}
				// <<< END MODIFIED >>>

				// <<< ДОБАВЛЕНО: Валидация JSON структуры >>>
				validationStartTime := time.Now()
				validateErr := validateAIResponseJSON(payload.PromptType, []byte(aiResponse))
				if validateErr != nil {
					log.Printf("[TaskID: %s] ОШИБКА ВАЛИДАЦИИ JSON (PromptType: %s): %v. JSON: %s",
						payload.TaskID, payload.PromptType, validateErr, aiResponse)
					MetricsIncrementTaskFailed("json_validation_error")
					processingErr = fmt.Errorf("ошибка валидации JSON ответа AI: %w", validateErr)
					// Прерываем успешный путь и переходим к сохранению ошибки
					log.Printf("[TaskID: %s] JSON Validation took: %v", payload.TaskID, time.Since(validationStartTime))
					break // Выходим из цикла ретраев, так как ответ получен, но он невалиден
				} else {
					log.Printf("[TaskID: %s] JSON структура успешно валидирована для PromptType: %s. Validation took: %v",
						payload.TaskID, payload.PromptType, time.Since(validationStartTime))
				}
				// <<< КОНЕЦ ВАЛИДАЦИИ >>>

				// Log the response *after* potential extraction - REMOVED as redundant now
				// log.Printf("[TaskID: %s] Final AI Response used (length: %d): %s", payload.TaskID, len(aiResponse), aiResponse)
				break
			}

			aiStatusLabel = "error"
			processingErr = attemptErr
			log.Printf("[TaskID: %s] Ошибка вызова AI API (Попытка %d/%d, время: %v): %v", payload.TaskID, attempt, h.maxAttempts, aiCallDuration, processingErr)
			MetricsRecordAIRequest("unknown", aiStatusLabel, aiCallDuration)

			if attempt == h.maxAttempts {
				log.Printf("[TaskID: %s] Достигнуто максимальное количество попыток (%d) вызова AI.", payload.TaskID, h.maxAttempts)
				MetricsIncrementTaskFailed("ai_error")
				// Не устанавливаем err здесь, processingErr уже содержит ошибку
				break // Выходим из цикла ретраев после последней неудачной попытки
			}

			delay := float64(baseDelay) * math.Pow(2, float64(attempt-1))
			jitter := delay * 0.1
			delay += jitter * (rand.Float64()*2 - 1)
			waitDuration := time.Duration(delay)
			if waitDuration < baseDelay {
				waitDuration = baseDelay
			}
			log.Printf("[TaskID: %s] Ожидание %v перед следующей попыткой...", payload.TaskID, waitDuration)
			time.Sleep(waitDuration)
		}
	} // <<< Конец блока else (обработка после успешного получения промпта задачи) >>>

	// --- Этап N: Сохранение результата и отправка уведомления --- //
	completedAt = time.Now() // Обновляем время завершения перед сохранением
	processingDuration := completedAt.Sub(taskStartTime)
	saveErr := h.saveResultAndNotify(
		context.Background(),
		payload,    // Передаем весь payload
		aiResponse, // Результат AI (может быть пустой при ошибке)
		processingDuration,
		processingErr, // Ошибка обработки (может быть nil)
		finalUsageInfo,
	)

	if saveErr != nil {
		log.Printf("[TaskID: %s] Критическая ошибка при сохранении результата или отправке уведомления: %v", payload.TaskID, saveErr)
		if processingErr == nil { // Если до этого не было ошибки AI/промпта
			// Это новая ошибка, связанная с сохранением/уведомлением
			err = fmt.Errorf("ошибка сохранения/уведомления: %w", saveErr)
			MetricsIncrementTaskFailed("save_notify_error")
		} else {
			// Если уже была ошибка AI/промпта, она приоритетнее
			err = processingErr
		}
		return err // Возвращаем ошибку (либо AI/промпта, либо save/notify)
	}

	// Если сохранение/уведомление прошло успешно, возвращаем ошибку обработки (если она была)
	if processingErr != nil {
		return processingErr
	}

	// Если ошибок не было ни при обработке, ни при сохранении/уведомлении
	return nil
}

// preparePrompt - ЭТА ФУНКЦИЯ БОЛЬШЕ НЕ НУЖНА, ТАК КАК ПРОМПТ БЕРЕТСЯ ИЗ ПРОВАЙДЕРА
/*
func (h *TaskHandler) preparePrompt(taskID string, promptType sharedModels.PromptType) (string, error) {
	promptFilename := model.GetPromptFilename(promptType)
	promptFilePath := filepath.Join(h.cfg.PromptsDir, promptFilename)

	log.Printf("[TaskID: %s] Загрузка инструкций из файла промпта: %s", taskID, promptFilePath)
	promptBytes, err := os.ReadFile(promptFilePath)
	if err != nil {
		return "", fmt.Errorf("ошибка чтения файла промпта %s: %w", promptFilePath, err)
	}
	return string(promptBytes), nil
}
*/

// saveResultAndNotify сохраняет результат генерации и отправляет уведомление
func (h *TaskHandler) saveResultAndNotify(
	ctx context.Context,
	payload messaging.GenerationTaskPayload,
	generatedText string,
	duration time.Duration,
	execErr error,
	usage service.UsageInfo,
) error {
	result := &sharedModels.GenerationResult{
		ID:               payload.TaskID,
		UserID:           payload.UserID,
		PromptType:       payload.PromptType,
		GeneratedText:    generatedText, // Сохраняем полный текст ответа AI в generation_results для отладки
		ProcessingTimeMs: duration.Milliseconds(),
		CreatedAt:        time.Now().UTC().Add(-duration),
		CompletedAt:      time.Now().UTC(),
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		EstimatedCostUSD: usage.EstimatedCostUSD,
	}
	if execErr != nil {
		result.Error = execErr.Error()
	}

	// Всегда сохраняем результат в generation_results (для аудита и статистики)
	saveDbErr := h.resultRepo.Save(ctx, result)
	if saveDbErr != nil {
		log.Printf("[TaskID: %s] КРИТИЧЕСКАЯ ОШИБКА: Не удалось сохранить результат генерации в generation_results: %v; Result: %+v", payload.TaskID, saveDbErr, result)
		// Не прерываем выполнение, попробуем отправить уведомление об ошибке
		if execErr == nil { // Если исходной ошибки не было, то эта ошибка становится основной
			execErr = fmt.Errorf("ошибка сохранения результата генерации: %w", saveDbErr)
		}
	}

	log.Printf("[TaskID: %s] Результат генерации сохранен в generation_results.", payload.TaskID)

	// --- СОХРАНЕНИЕ СЦЕНЫ (только при успехе и для нужных типов) --- START
	notifyStatus := messaging.NotificationStatusSuccess
	errorDetails := ""
	if execErr != nil {
		notifyStatus = messaging.NotificationStatusError
		errorDetails = execErr.Error()
	} else {
		// Ошибки не было, проверяем тип промпта
		isScenePrompt := payload.PromptType == sharedModels.PromptTypeJsonGeneration // <<< UPDATED

		if isScenePrompt {
			log.Printf("[TaskID: %s] Попытка сохранения сгенерированной сцены (PromptType: %s)...", payload.TaskID, payload.PromptType)
			// Парсим PublishedStoryID
			storyUUID, storyParseErr := uuid.Parse(payload.PublishedStoryID)
			if storyParseErr != nil {
				log.Printf("[TaskID: %s] КРИТИЧЕСКАЯ ОШИБКА: Не удалось распарсить PublishedStoryID '%s' для сохранения сцены: %v", payload.TaskID, payload.PublishedStoryID, storyParseErr)
				execErr = fmt.Errorf("invalid PublishedStoryID '%s' for scene saving: %w", payload.PublishedStoryID, storyParseErr)
				notifyStatus = messaging.NotificationStatusError
				errorDetails = execErr.Error()
			} else {
				// Создаем объект сцены
				scene := &sharedModels.StoryScene{
					ID:               uuid.New(),
					PublishedStoryID: storyUUID,
					StateHash:        payload.StateHash,
					Content:          json.RawMessage(generatedText),
					CreatedAt:        time.Now().UTC(),
				}

				// Используем Upsert, т.к. задача может быть перезапущена
				// --- ИСПРАВЛЕНО: Принимаем два значения от Upsert ---
				_, upsertErr := h.sceneRepo.Upsert(ctx, h.db, scene)
				if upsertErr != nil {
					log.Printf("[TaskID: %s] КРИТИЧЕСКАЯ ОШИБКА: Не удалось сохранить/обновить сцену (StoryID: %s, StateHash: %s): %v", payload.TaskID, payload.PublishedStoryID, payload.StateHash, upsertErr)
					execErr = fmt.Errorf("ошибка сохранения сцены: %w", upsertErr)
					notifyStatus = messaging.NotificationStatusError
					errorDetails = execErr.Error()
				} else {
					log.Printf("[TaskID: %s] Сцена успешно сохранена/обновлена (StoryID: %s, StateHash: %s).", payload.TaskID, payload.PublishedStoryID, payload.StateHash)
				}
			}
		}
	}
	// --- СОХРАНЕНИЕ СЦЕНЫ --- END

	// Отправка уведомления (статус и ошибка могли измениться при сохранении сцены)
	notifyErr := h.notify(ctx, payload, notifyStatus, errorDetails)
	if notifyErr != nil {
		if execErr == nil && saveDbErr == nil { // Если до этого не было ошибок
			return fmt.Errorf("ошибка отправки уведомления: %w", notifyErr) // Основная ошибка - отправка уведомления
		} else {
			// Если были ошибки до этого (AI, save result, save scene), они важнее.
			// Просто логируем, что уведомление тоже не ушло.
			log.Printf("[TaskID: %s] Ошибка отправки уведомления '%s' после предыдущей ошибки '%s': %v",
				payload.TaskID, notifyStatus, errorDetails, notifyErr)
			// Возвращаем исходную ошибку (execErr приоритетнее saveDbErr)
			if execErr != nil {
				return execErr
			}
			return saveDbErr // execErr был nil, но была ошибка сохранения результата
		}
	}

	if execErr == nil && saveDbErr == nil && notifyStatus == messaging.NotificationStatusSuccess {
		// Инкрементируем успех только если ВСЕ прошло хорошо (включая сохранение сцены)
		MetricsIncrementTaskSucceeded()
		log.Printf("[TaskID: %s] Успешное завершение и уведомление отправлено.", payload.TaskID)
	}

	// Возвращаем ошибку, если она была на каком-либо этапе (AI, save result, save scene)
	if execErr != nil {
		return execErr
	}
	if saveDbErr != nil {
		return saveDbErr // Ошибка сохранения результата (менее приоритетная)
	}

	return nil // Все прошло успешно
}

// notify отправляет уведомление
func (h *TaskHandler) notify(ctx context.Context, payload messaging.GenerationTaskPayload, status messaging.NotificationStatus, errorDetails string) error {
	notification := messaging.NotificationPayload{
		TaskID:           payload.TaskID,
		UserID:           payload.UserID,
		PromptType:       payload.PromptType,
		Status:           status,
		ErrorDetails:     errorDetails,
		StoryConfigID:    payload.StoryConfigID,
		PublishedStoryID: payload.PublishedStoryID,
		StateHash:        payload.StateHash,
		// GameStateID устанавливается в зависимости от типа сцены и наличия его в payload
	}

	// --- Логика установки GameStateID в уведомлении --- START
	// Определяем, относится ли задача к генерации сцены
	isSceneGenerationTask := payload.PromptType == sharedModels.PromptTypeJsonGeneration // <<< UPDATED

	// GameStateID передается в уведомлении, ЕСЛИ:
	// 1. Это НЕ генерация сцены ВОВСЕ.
	// ИЛИ
	// 2. Это генерация сцены, но это НЕ начальная сцена (StateHash != initial) ИЛИ GameStateID БЫЛ в исходном payload.
	if !isSceneGenerationTask || (payload.StateHash != sharedModels.InitialStateHash || payload.GameStateID != "") {
		notification.GameStateID = payload.GameStateID
		log.Printf("[TaskID: %s] GameStateID '%s' будет передан в уведомлении (isScene: %t, isInitialHash: %t, payloadHasGid: %t).",
			payload.TaskID, payload.GameStateID, isSceneGenerationTask, payload.StateHash == sharedModels.InitialStateHash, payload.GameStateID != "")
	} else {
		// Это генерация НАЧАЛЬНОЙ сцены, и GameStateID в payload был пустой.
		// Не передаем GameStateID в уведомлении.
		notification.GameStateID = ""
		log.Printf("[TaskID: %s] Генерация начальной сцены без исходного GameStateID, GameStateID в уведомлении будет пустой.", payload.TaskID)
	}
	// --- Логика установки GameStateID в уведомлении --- END

	if err := h.notifier.Notify(ctx, notification); err != nil {
		log.Printf("[TaskID: %s] Не удалось отправить уведомление (Status: %s, Error: '%s', GID: '%s'): %v",
			payload.TaskID, status, errorDetails, notification.GameStateID, err)
		return err
	}

	if status == messaging.NotificationStatusSuccess {
		log.Printf("[TaskID: %s] Уведомление об успехе отправлено (GID: '%s').", payload.TaskID, notification.GameStateID)
	} else {
		log.Printf("[TaskID: %s] Уведомление об ошибке отправлено (Error: '%s', GID: '%s').", payload.TaskID, errorDetails, notification.GameStateID)
	}
	return nil
}

// float64Ptr возвращает указатель на float64
func float64Ptr(f float64) *float64 {
	return &f
}

// <<< ДОБАВЛЕНО: Функция валидации JSON >>>
// validateAIResponseJSON проверяет, соответствует ли JSON ответа AI ожидаемой структуре для данного PromptType.
func validateAIResponseJSON(promptType sharedModels.PromptType, jsonData []byte) error {
	if len(jsonData) == 0 {
		return errors.New("JSON data is empty")
	}

	switch promptType {
	case sharedModels.PromptTypeNarrator:
		var v NarratorValidation
		if err := json.Unmarshal(jsonData, &v); err != nil {
			return fmt.Errorf("failed to unmarshal into NarratorValidation: %w", err)
		}
		if v.Title == nil || *v.Title == "" {
			return errors.New("NarratorValidation: missing or empty required field 't' (title)")
		}
		if v.Description == nil || *v.Description == "" {
			return errors.New("NarratorValidation: missing or empty required field 'sd' (short_description)")
		}
		if v.Genre == nil || *v.Genre == "" {
			return errors.New("NarratorValidation: missing or empty required field 'gn' (genre)")
		}
		if v.ProtagonistName == nil || *v.ProtagonistName == "" {
			return errors.New("NarratorValidation: missing or empty required field 'pn' (protagonist_name)")
		}
		if v.ProtagonistDesc == nil || *v.ProtagonistDesc == "" {
			return errors.New("NarratorValidation: missing or empty required field 'pd' (protagonist_description)")
		}
		if v.WorldContext == nil || *v.WorldContext == "" {
			return errors.New("NarratorValidation: missing or empty required field 'wc' (world_context)")
		}
		if v.StorySummary == nil || *v.StorySummary == "" {
			return errors.New("NarratorValidation: missing or empty required field 'ss' (story_summary)")
		}
		if len(v.CoreStats) == 0 || string(v.CoreStats) == "null" {
			return errors.New("NarratorValidation: missing or empty required field 'cs' (core_stats)")
		}

		var csMap map[string]CoreStatItemValidation
		if err := json.Unmarshal(v.CoreStats, &csMap); err != nil {
			return fmt.Errorf("NarratorValidation: 'cs' is not a valid JSON object of stats: %w. JSON: %s", err, string(v.CoreStats))
		}

		if len(csMap) != 4 {
			return fmt.Errorf("NarratorValidation: 'cs' must contain exactly 4 stats, got %d", len(csMap))
		}

		expectedKeys := []string{"0", "1", "2", "3"}
		for _, key := range expectedKeys {
			statItem, ok := csMap[key]
			if !ok {
				return fmt.Errorf("NarratorValidation: 'cs' is missing stat with index '%s'", key)
			}
			if statItem.Name == nil || *statItem.Name == "" {
				return fmt.Errorf("NarratorValidation: stat '%s' missing or empty 'n' (name)", key)
			}
			if statItem.Description == nil || *statItem.Description == "" {
				return fmt.Errorf("NarratorValidation: stat '%s' missing or empty 'd' (description)", key)
			}
			if statItem.GameOver == nil || *statItem.GameOver == "" {
				return fmt.Errorf("NarratorValidation: stat '%s' missing or empty 'go' (game_over_condition)", key)
			}
			goVal := *statItem.GameOver
			if goVal != "min" && goVal != "max" && goVal != "both" {
				return fmt.Errorf("NarratorValidation: stat '%s' has invalid 'go' value '%s'. Must be 'min', 'max', or 'both'", key, goVal)
			}
		}

		if v.PlayerPreferences == nil {
			return errors.New("NarratorValidation: missing required field 'pp' (player_preferences)")
		}
		if v.PlayerPreferences.TH == nil || len(*v.PlayerPreferences.TH) == 0 {
			return errors.New("NarratorValidation: 'pp.th' (tags) is missing or empty")
		}
		if v.PlayerPreferences.ST == nil || *v.PlayerPreferences.ST == "" {
			return errors.New("NarratorValidation: 'pp.st' (visual_style) is missing or empty")
		}
		if v.PlayerPreferences.WL == nil || *v.PlayerPreferences.WL == "" {
			return errors.New("NarratorValidation: 'pp.wl' (world_lore) is missing or empty")
		}

	case sharedModels.PromptTypeProtagonistGoal,
		sharedModels.PromptTypeStoryContinuation,
		sharedModels.PromptTypeNovelGameOverCreator:
		var v ResultStringValidation
		if err := json.Unmarshal(jsonData, &v); err != nil {
			return fmt.Errorf("failed to unmarshal into ResultStringValidation for %s: %w", promptType, err)
		}
		if v.Result == nil || *v.Result == "" {
			return fmt.Errorf("ResultStringValidation for %s: missing or empty required field 'result'", promptType)
		}

	case sharedModels.PromptTypeStorySetup:
		var v struct {
			Result *string `json:"result"`
			PR     *string `json:"pr"`
		}
		if err := json.Unmarshal(jsonData, &v); err != nil {
			return fmt.Errorf("StorySetupValidation: failed to unmarshal into validation struct: %w", err)
		}
		if v.Result == nil || *v.Result == "" {
			return errors.New("StorySetupValidation: missing or empty required field 'result'")
		}
		if v.PR == nil || *v.PR == "" {
			return errors.New("StorySetupValidation: missing or empty required field 'pr'")
		}

	case sharedModels.PromptTypeContentModeration:
		var v ContentModerationValidation
		if err := json.Unmarshal(jsonData, &v); err != nil {
			return fmt.Errorf("failed to unmarshal into ContentModerationValidation: %w", err)
		}
		if v.AC == nil {
			return errors.New("ContentModerationValidation: missing required field 'ac'")
		}
		if *v.AC != 0 && *v.AC != 1 {
			return fmt.Errorf("ContentModerationValidation: field 'ac' must be 0 or 1, got %d", *v.AC)
		}

	case sharedModels.PromptTypeCharacterGeneration:
		var v []CharacterGenerationItemValidation
		if err := json.Unmarshal(jsonData, &v); err != nil {
			return fmt.Errorf("failed to unmarshal into []CharacterGenerationItemValidation: %w. JSON: %s", err, string(jsonData))
		}
		if v == nil { // Explicitly check for nil slice if unmarshal of empty array results in nil
			return errors.New("CharacterGenerationValidation: response is nil, expected a JSON array")
		}
		if len(v) < 2 || len(v) > 4 {
			return fmt.Errorf("CharacterGenerationValidation: must generate 2 to 4 characters, got %d", len(v))
		}
		for i, item := range v {
			if item.ID == nil || *item.ID == "" {
				return fmt.Errorf("CharacterGenerationValidation item %d: missing or empty 'id'", i)
			}
			if item.Name == nil || *item.Name == "" {
				return fmt.Errorf("CharacterGenerationValidation item %d: missing or empty 'name'", i)
			}
			if item.Role == nil || *item.Role == "" {
				return fmt.Errorf("CharacterGenerationValidation item %d: missing or empty 'role'", i)
			}
			if item.Traits == nil || *item.Traits == "" {
				return fmt.Errorf("CharacterGenerationValidation item %d: missing or empty 'traits'", i)
			}
			if len(item.Relationship) == 0 || string(item.Relationship) == "null" {
				return fmt.Errorf("CharacterGenerationValidation item %d: missing or empty 'relationship'", i)
			}
			var relMap map[string]interface{}
			if err := json.Unmarshal(item.Relationship, &relMap); err != nil {
				return fmt.Errorf("CharacterGenerationValidation item %d: 'relationship' is not a valid JSON object: %w", i, err)
			}
			if _, ok := relMap["protaghonist"]; !ok { // Note: prompt uses "protaghonist"
				return fmt.Errorf("CharacterGenerationValidation item %d: 'relationship' must contain 'protaghonist' key", i)
			}
			if item.Memories == nil || *item.Memories == "" {
				return fmt.Errorf("CharacterGenerationValidation item %d: missing or empty 'memories'", i)
			}
			if item.PlotHook == nil || *item.PlotHook == "" {
				return fmt.Errorf("CharacterGenerationValidation item %d: missing or empty 'plotHook'", i)
			}
			if item.ImagePromptDesc == nil || *item.ImagePromptDesc == "" {
				return fmt.Errorf("CharacterGenerationValidation item %d: missing or empty 'image_prompt_descriptor'", i)
			}
			if item.ImageRefName == nil || *item.ImageRefName == "" {
				return fmt.Errorf("CharacterGenerationValidation item %d: missing or empty 'image_reference_name'", i)
			}
		}

	case sharedModels.PromptTypeJsonGeneration:
		var v JsonGenerationValidation
		if err := json.Unmarshal(jsonData, &v); err != nil {
			return fmt.Errorf("failed to unmarshal into JsonGenerationValidation: %w", err)
		}
		if v.Location == nil || *v.Location == "" {
			return errors.New("JsonGenerationValidation: missing or empty 'location'")
		}
		if v.Choices == nil || len(v.Choices) == 0 {
			// TODO: Validate against {{CHOICE_COUNT}} if available
			return errors.New("JsonGenerationValidation: 'ch' (choices) is missing or empty")
		}
		for i, choice := range v.Choices {
			if choice.SceneName == nil || *choice.SceneName == "" {
				return fmt.Errorf("JsonGenerationValidation choice %d: missing or empty 'scene'", i)
			}
			if choice.Name == nil || *choice.Name == "" {
				return fmt.Errorf("JsonGenerationValidation choice %d: missing or empty 'name'", i)
			}
			if choice.Desc == nil || *choice.Desc == "" {
				// Markdown is OK, so empty is fine if allowed by prompt, but prompt implies situation text.
				return fmt.Errorf("JsonGenerationValidation choice %d: missing or empty 'desc'", i)
			}
			if choice.Opts == nil || len(choice.Opts) != 2 {
				return fmt.Errorf("JsonGenerationValidation choice %d: 'opts' must contain exactly 2 options, got %d", i, len(choice.Opts))
			}
			for j, opt := range choice.Opts {
				if opt.Txt == nil || *opt.Txt == "" {
					return fmt.Errorf("JsonGenerationValidation choice %d, option %d: missing or empty 'txt'", i, j)
				}
				if opt.Cons == nil {
					return fmt.Errorf("JsonGenerationValidation choice %d, option %d: missing 'cons'", i, j)
				}
				if len(opt.Cons.CS) == 0 || string(opt.Cons.CS) == "null" { // "cs" itself can be an empty object {}
					return fmt.Errorf("JsonGenerationValidation choice %d, option %d: missing 'cons.cs'", i, j)
				}
				// Validate cs is a map
				var csMap map[string]interface{}
				if err := json.Unmarshal(opt.Cons.CS, &csMap); err != nil {
					return fmt.Errorf("JsonGenerationValidation choice %d, option %d: 'cons.cs' is not a valid JSON object: %w", i, j, err)
				}
				if opt.Cons.RT == nil { // rt can be an empty string ""
					return fmt.Errorf("JsonGenerationValidation choice %d, option %d: missing 'cons.rt'", i, j)
				}
			}
		}

	case sharedModels.PromptTypeScenePlanner:
		var v ScenePlannerValidation
		if err := json.Unmarshal(jsonData, &v); err != nil {
			return fmt.Errorf("failed to unmarshal into ScenePlannerValidation: %w", err)
		}
		if v.NeedNewCharacter == nil {
			return errors.New("ScenePlannerValidation: missing 'need_new_character'")
		}
		if v.SceneFocus == nil || *v.SceneFocus == "" {
			return errors.New("ScenePlannerValidation: missing or empty 'scene_focus'")
		}
		// Validate NewCharacterSuggestions if present
		if v.NewCharacterSuggestions != nil {
			for i, sug := range v.NewCharacterSuggestions {
				if sug.Role == nil || *sug.Role == "" {
					return fmt.Errorf("ScenePlannerValidation NewCharacterSuggestion %d: missing or empty 'role'", i)
				}
				if sug.Reason == nil || *sug.Reason == "" {
					return fmt.Errorf("ScenePlannerValidation NewCharacterSuggestion %d: missing or empty 'reason'", i)
				}
			}
		}
		// Validate NewCardSuggestions if present
		if v.NewCardSuggestions != nil {
			for i, sug := range v.NewCardSuggestions {
				if sug.ImagePromptDesc == nil || *sug.ImagePromptDesc == "" {
					return fmt.Errorf("ScenePlannerValidation NewCardSuggestion %d: missing or empty 'image_prompt_descriptor'", i)
				}
				if sug.ImageRefName == nil || *sug.ImageRefName == "" {
					return fmt.Errorf("ScenePlannerValidation NewCardSuggestion %d: missing or empty 'image_reference_name'", i)
				}
				if sug.Title == nil || *sug.Title == "" {
					return fmt.Errorf("ScenePlannerValidation NewCardSuggestion %d: missing or empty 'title'", i)
				}
				if sug.Reason == nil || *sug.Reason == "" {
					return fmt.Errorf("ScenePlannerValidation NewCardSuggestion %d: missing or empty 'reason'", i)
				}
			}
		}
		// Validate CharacterUpdates if present
		if v.CharacterUpdates != nil {
			for i, upd := range v.CharacterUpdates {
				if upd.ID == nil || *upd.ID == "" {
					return fmt.Errorf("ScenePlannerValidation CharacterUpdate %d: missing or empty 'id'", i)
				}
			}
		}
		// Validate CharactersToRemove if present
		if v.CharactersToRemove != nil {
			for i, rem := range v.CharactersToRemove {
				if rem.ID == nil || *rem.ID == "" {
					return fmt.Errorf("ScenePlannerValidation CharacterToRemove %d: missing or empty 'id'", i)
				}
				if rem.Reason == nil || *rem.Reason == "" {
					return fmt.Errorf("ScenePlannerValidation CharacterToRemove %d: missing or empty 'reason'", i)
				}
			}
		}

	default:
		log.Printf("[validateAIResponseJSON] No specific validation defined for PromptType: %s. Passing through.", promptType)
	}

	return nil // Валидация прошла успешно
}

// getLanguageInstruction возвращает строку с инструкцией для AI на основе кода языка.
func getLanguageInstruction(langCode string) string {
	switch langCode {
	case "en":
		return "RESPOND ONLY IN ENGLISH. ANSWER ONLY IN ENGLISH."
	case "fr":
		return "RESPOND ONLY IN FRENCH. RÉPONDS UNIQUEMENT EN FRANÇAIS."
	case "de":
		return "RESPOND ONLY IN GERMAN. ANTWORTE NUR AUF DEUTSCH."
	case "es":
		return "RESPOND ONLY IN SPANISH. RESPONDE SOLO EN ESPAÑOL."
	case "it":
		return "RESPOND ONLY IN ITALIAN. RISPONDI SOLO IN ITALIANO."
	case "pt":
		return "RESPOND ONLY IN PORTUGUESE. RESPONDA SOMENTE EM PORTUGUÊS."
	case "ru":
		return "RESPOND ONLY IN RUSSIAN. ОТВЕЧАЙ ТОЛЬКО НА РУССКОМ."
	case "zh":
		return "RESPOND ONLY IN CHINESE. 只用中文回答."
	case "ja":
		return "RESPOND ONLY IN JAPANESE. 日本語でのみ回答してください。"
	default:
		log.Printf("[WARN] Неизвестный код языка '%s' для getLanguageInstruction, используется английский по умолчанию.", langCode)
		return "RESPOND ONLY IN ENGLISH. ANSWER ONLY IN ENGLISH." // По умолчанию английский
	}
}
