package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	sharedModels "novel-server/shared/models"
)

// MinimalConfigForFirstScene - структура для минимального конфига, отправляемого для PromptTypeNovelFirstSceneCreator
// Содержит только поля, необходимые этому промпту
type MinimalConfigForFirstScene struct {
	Language       string                   `json:"ln,omitempty"`     // Required
	IsAdultContent bool                     `json:"ac,omitempty"`     // Required
	Genre          string                   `json:"gn,omitempty"`     // <<< ДОБАВЛЕНО
	PlayerName     string                   `json:"pn,omitempty"`     // Player Name из конфига
	PlayerGender   string                   `json:"pg,omitempty"`     // <<< ДОБАВЛЕНО
	PlayerDesc     string                   `json:"p_desc,omitempty"` // <<< ДОБАВЛЕНО (основное описание)
	WorldContext   string                   `json:"wc,omitempty"`     // <<< ДОБАВЛЕНО
	StorySummary   string                   `json:"ss,omitempty"`     // <<< ДОБАВЛЕНО
	PlayerPrefs    sharedModels.PlayerPrefs `json:"pp,omitempty"`     // <<< ДОБАВЛЕНО (структура без Style)
}

// ToMinimalConfigForFirstScene преобразует полный конфиг в минимальный
func ToMinimalConfigForFirstScene(configBytes []byte) MinimalConfigForFirstScene {
	var fullConfig sharedModels.Config
	// Игнорируем ошибку, если конфиг некорректный, просто вернем пустые поля
	_ = json.Unmarshal(configBytes, &fullConfig)

	// Создаем минимальную структуру PlayerPrefs
	minimalPrefs := sharedModels.PlayerPrefs{
		Themes:               fullConfig.PlayerPrefs.Themes,
		Tone:                 fullConfig.PlayerPrefs.Tone,
		PlayerDescription:    fullConfig.PlayerPrefs.PlayerDescription,
		WorldLore:            fullConfig.PlayerPrefs.WorldLore,
		DesiredLocations:     fullConfig.PlayerPrefs.DesiredLocations,
		DesiredCharacters:    fullConfig.PlayerPrefs.DesiredCharacters,
		CharacterVisualStyle: fullConfig.PlayerPrefs.CharacterVisualStyle,
		Style:                fullConfig.PlayerPrefs.Style,
	}

	return MinimalConfigForFirstScene{
		Language:       fullConfig.Language,
		IsAdultContent: fullConfig.IsAdultContent,
		Genre:          fullConfig.Genre, // <<< ДОБАВЛЕНО
		PlayerName:     fullConfig.PlayerName,
		PlayerGender:   fullConfig.PlayerGender, // <<< ДОБАВЛЕНО
		PlayerDesc:     fullConfig.PlayerDesc,   // <<< ДОБАВЛЕНО
		WorldContext:   fullConfig.WorldContext, // <<< ДОБАВЛЕНО
		StorySummary:   fullConfig.StorySummary, // <<< ДОБАВЛЕНО
		PlayerPrefs:    minimalPrefs,            // <<< ДОБАВЛЕНО
	}
}

func (p *NotificationProcessor) handleNovelSetupNotification(ctx context.Context, notification sharedMessaging.NotificationPayload, publishedStoryID uuid.UUID) error {
	taskID := notification.TaskID
	dbCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	p.logger.Info("Processing NovelSetup", zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()))

	publishedStory, err := p.publishedRepo.GetByID(dbCtx, publishedStoryID)
	if err != nil {
		p.logger.Error("Error getting PublishedStory for Setup update", zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()), zap.Error(err))
		return fmt.Errorf("error getting PublishedStory %s: %w", publishedStoryID, err)
	}

	if notification.Status == sharedMessaging.NotificationStatusSuccess {
		if publishedStory.Status != sharedModels.StatusSetupGenerating {
			p.logger.Warn("PublishedStory not in SetupGenerating status, Setup Success update cancelled.",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.String("current_status", string(publishedStory.Status)),
			)
			return nil
		}
		p.logger.Info("Setup Success notification", zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()))
		rawGeneratedText := notification.GeneratedText
		jsonToParse := extractJsonContent(rawGeneratedText) // Helper from consumer.go
		if jsonToParse == "" {
			p.logger.Error("SETUP PARSING ERROR: Could not extract JSON from Setup text",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.String("raw_text_snippet", stringShort(rawGeneratedText, 100)), // Helper from consumer.go
			)
			errDetails := "Failed to extract JSON block from Setup text"
			if updateErr := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, sharedModels.StatusError, nil, nil, nil, &errDetails); updateErr != nil {
				p.logger.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after Setup JSON extraction error" /* zap fields */, zap.Error(updateErr))
				return fmt.Errorf("error updating Error status for PublishedStory %s: %w", publishedStoryID, updateErr)
			}
			return nil
		}

		setupBytes := []byte(jsonToParse)
		var temp map[string]interface{}
		if err := json.Unmarshal(setupBytes, &temp); err != nil {
			p.logger.Error("SETUP PARSING ERROR: Invalid JSON received for Setup",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(err),
				zap.String("json_to_parse", jsonToParse),
			)
			errDetails := fmt.Sprintf("Invalid JSON received for Setup: %v", err)
			if updateErr := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, sharedModels.StatusError, nil, nil, nil, &errDetails); updateErr != nil {
				p.logger.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after Setup parsing error",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.Error(updateErr),
				)
				return fmt.Errorf("error updating Error status for PublishedStory %s: %w", publishedStoryID, updateErr)
			}
			return nil
		}

		if err := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, sharedModels.StatusFirstScenePending, setupBytes, nil, nil, nil); err != nil {
			p.logger.Error("CRITICAL ERROR: Failed to update status and Setup for PublishedStory",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(err),
			)
			return fmt.Errorf("error updating status/Setup for PublishedStory %s: %w", publishedStoryID, err)
		}
		p.logger.Info("PublishedStory status updated -> FirstScenePending and Setup saved", zap.String("task_id", taskID), zap.String("published_story_id", publishedStoryID.String()))

		// Объявляем simplifiedSetup здесь, чтобы она была доступна ниже
		var simplifiedSetup map[string]interface{}

		var setupContent sharedModels.NovelSetupContent
		var fullConfig sharedModels.Config
		var characterVisualStyle string
		var storyStyle string // <<< Переменная для pp.st

		if err := json.Unmarshal(setupBytes, &setupContent); err != nil {
			p.logger.Warn("Failed to unmarshal setup JSON for image task trigger",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(err),
			)
			// Не можем продолжать без setupContent
		} else {
			// Распарсим конфиг, чтобы получить cvs и st
			if errCfg := json.Unmarshal(publishedStory.Config, &fullConfig); errCfg != nil {
				p.logger.Warn("Failed to unmarshal config JSON to get CharacterVisualStyle",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.Error(errCfg),
				)
				// Продолжаем без cvs, если не удалось распарсить
			} else {
				characterVisualStyle = fullConfig.PlayerPrefs.CharacterVisualStyle
				storyStyle = fullConfig.PlayerPrefs.Style // <<< Извлекаем Style (st)

				// Форматируем строки стилей для добавления
				if characterVisualStyle != "" {
					characterVisualStyle = ", " + characterVisualStyle
				}
				if storyStyle != "" {
					storyStyle = ", " + storyStyle // <<< Форматируем Style
				}
			}

			p.logger.Info("Triggering image generation check",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
			)
			imageTasks := make([]sharedMessaging.CharacterImageTaskPayload, 0, len(setupContent.Characters))

			for _, charData := range setupContent.Characters {
				if charData.ImageRef == "" || charData.Prompt == "" { // Пропускаем, если нет референса или основного промпта
					continue
				}
				imageRef := charData.ImageRef
				basePrompt := charData.Prompt // Исходный промпт персонажа
				characterIDForTask := uuid.New()

				logFieldsImg := []zap.Field{
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
					zap.String("image_ref", imageRef),
					zap.String("character_task_id", characterIDForTask.String()),
				}
				p.logger.Info("Checking image for character", logFieldsImg...)
				_, errCheck := p.imageReferenceRepo.GetImageURLByReference(dbCtx, imageRef)
				if errCheck == nil {
					p.logger.Info("Found existing URL for ImageRef. Generation not required.", logFieldsImg...)
				} else if errors.Is(errCheck, interfaces.ErrNotFound) {
					p.logger.Info("URL for ImageRef not found. Adding image generation task to batch.", logFieldsImg...)

					// <<< ИЗМЕНЕНИЕ: Формируем полный промпт >>>
					fullCharacterPrompt := basePrompt + characterVisualStyle // Добавляем стиль из конфига

					imageTask := sharedMessaging.CharacterImageTaskPayload{
						TaskID:         characterIDForTask.String(),
						CharacterID:    characterIDForTask,
						Prompt:         fullCharacterPrompt, // <<< Используем полный промпт
						NegativePrompt: charData.NegPrompt,  // Используем поле из setup
						ImageReference: imageRef,
						Ratio:          "2:3", // <<< ДОБАВЛЕНО: Соотношение для персонажа
					}
					imageTasks = append(imageTasks, imageTask)

				} else {
					p.logger.Error("Error checking ImageRef in DB. Skipping generation.", append(logFieldsImg, zap.Error(errCheck))...)
				}
			}

			// Отправляем батч персонажей...
			if len(imageTasks) > 0 {
				batchPayload := sharedMessaging.CharacterImageTaskBatchPayload{
					BatchID: uuid.New().String(),
					Tasks:   imageTasks,
				}
				p.logger.Info("Publishing character image task batch",
					zap.String("batch_id", batchPayload.BatchID),
					zap.Int("task_count", len(batchPayload.Tasks)),
					zap.String("published_story_id", publishedStoryID.String()),
				)
				if errPub := p.characterImageTaskBatchPub.PublishCharacterImageTaskBatch(ctx, batchPayload); errPub != nil {
					p.logger.Error("Failed to publish character image task batch", zap.Error(errPub),
						zap.String("batch_id", batchPayload.BatchID),
						zap.String("published_story_id", publishedStoryID.String()),
					)
					// TODO: Подумать о стратегии отката или повторной попытки для всего батча?
					// Пока просто логируем ошибку.
				} else {
					p.logger.Info("Character image task batch published successfully",
						zap.String("batch_id", batchPayload.BatchID),
						zap.String("published_story_id", publishedStoryID.String()),
					)
				}
			}

			// <<< НАЧАЛО: Логика генерации превью истории >>>
			if setupContent.StoryPreviewImagePrompt != "" {
				p.logger.Info("Generating story preview image",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
				)
				previewImageRef := fmt.Sprintf("history_preview_%s", publishedStoryID.String())

				// <<< ИЗМЕНЕНИЕ: Формируем полный промпт для превью с обоими стилями >>>
				basePreviewPrompt := setupContent.StoryPreviewImagePrompt
				// Добавляем оба стиля, если они есть
				fullPreviewPromptWithStyles := basePreviewPrompt + storyStyle + characterVisualStyle

				previewTask := sharedMessaging.CharacterImageTaskPayload{
					TaskID:         uuid.New().String(),
					CharacterID:    publishedStoryID,
					Prompt:         fullPreviewPromptWithStyles, // <<< Используем промпт с обоими стилями
					NegativePrompt: "",
					ImageReference: previewImageRef,
					Ratio:          "3:2", // <<< ДОБАВЛЕНО: Соотношение для превью
				}

				// Оборачиваем в батч из одной задачи
				previewBatchPayload := sharedMessaging.CharacterImageTaskBatchPayload{
					BatchID: uuid.New().String(),
					Tasks:   []sharedMessaging.CharacterImageTaskPayload{previewTask},
				}

				// Отправляем задачу
				if errPub := p.characterImageTaskBatchPub.PublishCharacterImageTaskBatch(ctx, previewBatchPayload); errPub != nil {
					p.logger.Error("Failed to publish story preview image task", zap.Error(errPub),
						zap.String("preview_batch_id", previewBatchPayload.BatchID),
						zap.String("published_story_id", publishedStoryID.String()),
					)
					// Не критично для основного потока, просто логируем
				} else {
					p.logger.Info("Story preview image task published successfully",
						zap.String("preview_batch_id", previewBatchPayload.BatchID),
						zap.String("published_story_id", publishedStoryID.String()),
					)
					// <<< Опционально: Обновить поле PreviewImageReference в PublishedStory >>>
					// previewRef := previewImageRef // Создаем копию
					// if updateRefErr := p.publishedRepo.UpdatePreviewImageReference(dbCtx, publishedStoryID, &previewRef); updateRefErr != nil {
					// 	p.logger.Error("Failed to update preview image reference in DB", zap.Error(updateRefErr), zap.String("published_story_id", publishedStoryID.String()))
					// }
				}
			} else {
				p.logger.Warn("StoryPreviewImagePrompt (spi) is empty in setup, skipping preview generation.",
					zap.String("task_id", taskID),
					zap.String("published_story_id", publishedStoryID.String()),
				)
			}
			// <<< КОНЕЦ: Логика генерации превью истории >>>

			// --- НАЧАЛО: Формирование УПРОЩЕННОГО stp для FirstScene ---
			simplifiedChars := make([]map[string]string, 0, len(setupContent.Characters))
			for _, char := range setupContent.Characters {
				simplifiedChar := map[string]string{
					"n": char.Name,
					"d": char.Description,
				}
				if char.Personality != "" {
					simplifiedChar["p"] = char.Personality
				}
				simplifiedChars = append(simplifiedChars, simplifiedChar)
			}
			simplifiedSetup = map[string]interface{}{"chars": simplifiedChars}
			// --- КОНЕЦ: Формирование УПРОЩЕННОГО stp ---
		}

		// --- НАЧАЛО: Формирование UserInput для FirstScene ---
		combinedInputMap := make(map[string]interface{})

		// 1. Добавляем минимальный конфиг под ключом "cfg"
		minimalConfig := ToMinimalConfigForFirstScene(publishedStory.Config)
		combinedInputMap["cfg"] = minimalConfig

		// 2. Добавляем упрощенный сетап под ключом "stp"
		combinedInputMap["stp"] = simplifiedSetup

		// 3. Добавляем пустые плейсхолдеры для состояния
		combinedInputMap["sv"] = make(map[string]interface{})
		combinedInputMap["gf"] = []string{}
		combinedInputMap["uc"] = []sharedModels.UserChoiceInfo{}
		combinedInputMap["pss"] = ""
		combinedInputMap["pfd"] = ""
		combinedInputMap["pvis"] = ""

		// --- КОНЕЦ: Формирование UserInput для FirstScene ---

		combinedInputBytes, errMarshal := json.Marshal(combinedInputMap)
		if errMarshal != nil {
			p.logger.Error("ERROR: Failed to marshal JSON for FirstScene task",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(errMarshal),
			)
			// Важно: Не возвращаем ошибку, чтобы не блокировать обработку других уведомлений,
			// но и не отправляем задачу, так как payload некорректен.
			// Статус истории останется FirstScenePending, потребует ручного вмешательства или Retry.
			return nil
		}
		combinedInputJSON := string(combinedInputBytes)
		nextTaskPayload := sharedMessaging.GenerationTaskPayload{
			TaskID:           uuid.New().String(),
			UserID:           publishedStory.UserID.String(),
			PromptType:       sharedMessaging.PromptTypeNovelFirstSceneCreator,
			PublishedStoryID: publishedStoryID.String(),
			UserInput:        combinedInputJSON,
			StateHash:        sharedModels.InitialStateHash,
		}

		if errPub := p.taskPub.PublishGenerationTask(ctx, nextTaskPayload); errPub != nil {
			p.logger.Error("CRITICAL ERROR: Failed to send first scene generation task",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(errPub),
			)
			// Статус истории останется FirstScenePending
		} else {
			p.logger.Info("First scene generation task sent successfully",
				zap.String("task_id", taskID),
				zap.String("next_task_id", nextTaskPayload.TaskID),
				zap.String("published_story_id", publishedStoryID.String()),
			)
		}
	} else {
		p.logger.Warn("Setup Error notification received",
			zap.String("task_id", taskID),
			zap.String("published_story_id", publishedStoryID.String()),
			zap.String("error_details", notification.ErrorDetails),
		)
		if err := p.publishedRepo.UpdateStatusDetails(dbCtx, publishedStoryID, sharedModels.StatusError, nil, nil, nil, &notification.ErrorDetails); err != nil {
			p.logger.Error("CRITICAL ERROR: Failed to update PublishedStory status to Error after Setup generation error",
				zap.String("task_id", taskID),
				zap.String("published_story_id", publishedStoryID.String()),
				zap.Error(err),
			)
			return fmt.Errorf("error updating Error status for PublishedStory %s: %w", publishedStoryID, err)
		}
	}
	return nil
}
