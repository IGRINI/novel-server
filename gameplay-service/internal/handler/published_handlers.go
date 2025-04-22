package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"novel-server/gameplay-service/internal/service" // Добавляем импорт сервиса
	sharedModels "novel-server/shared/models"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// --- Структуры для ответов и запросов --- //

// <<< НОВЫЕ СТРУКТУРЫ DTO ДЛЯ ОТВЕТА СЦЕНЫ КЛИЕНТУ >>>

// GameSceneResponse - структура ответа для сцены, отправляемая клиенту
type GameSceneResponse struct {
	ID               string           `json:"id"`               // ID сцены
	PublishedStoryID string           `json:"publishedStoryId"` // ID истории
	Content          GameSceneContent `json:"content"`          // Содержимое сцены для клиента
}

// GameSceneContent - Содержимое сцены, отправляемое клиенту
type GameSceneContent struct {
	Type    string            `json:"type"`         // Тип контента ("choices", "game_over", "continuation")
	Choices []GameChoiceBlock `json:"ch,omitempty"` // Блоки выбора (только для type="choices" или "continuation")
	// Поля для концовок (копируем из SceneContent, т.к. они нужны клиенту)
	EndingText             string         `json:"et,omitempty"`  // Текст стандартной концовки
	NewPlayerDescription   string         `json:"npd,omitempty"` // Описание нового персонажа (для продолжения)
	CoreStatsReset         map[string]int `json:"csr,omitempty"` // Новые статы (для продолжения)
	EndingTextPreviousChar string         `json:"etp,omitempty"` // Текст концовки пред. персонажа (для продолжения)
}

// GameChoiceBlock - Блок выбора для клиента
type GameChoiceBlock struct {
	Shuffleable int               `json:"sh"`   // Можно ли перемешивать
	Char        string            `json:"char"` // <<< ДОБАВЛЕНО ПОЛЕ ДЛЯ ПАРСИНГА
	Description string            `json:"desc"` // Описание ситуации
	Options     []GameSceneOption `json:"opts"` // Опции выбора
}

// GameSceneOption - Опция выбора для клиента
type GameSceneOption struct {
	Text         string           `json:"txt"`  // Текст опции
	Consequences GameConsequences `json:"cons"` // Последствия (только нужные клиенту)
}

// GameConsequences - Последствия выбора для клиента
type GameConsequences struct {
	CoreStatsChange map[string]int `json:"cs_chg,omitempty"`   // Изменения статов (показываем игроку)
	ResponseText    string         `json:"resp_txt,omitempty"` // Текст-реакция на выбор (если есть)
}

// <<< КОНЕЦ НОВЫХ СТРУКТУР DTO >>>

// <<< ДОБАВЛЕНО: Определение запроса для makeChoice
type MakeChoicesRequest struct {
	SelectedOptionIndices []int `json:"selected_option_indices" binding:"required,dive,min=0,max=1"`
}

type publishedCoreStatDetail struct {
	Description        string                        `json:"description"`
	InitialValue       int                           `json:"initial_value"`
	GameOverConditions []sharedModels.StatDefinition `json:"game_over_conditions"` // <<< Заменено StatRule на StatDefinition
}

type PublishedStoryDetail struct {
	ID                string                             `json:"id"`
	Title             string                             `json:"title"`
	ShortDescription  string                             `json:"short_description"`
	AuthorID          string                             `json:"author_id"`
	AuthorName        string                             `json:"author_name"`
	PublishedAt       time.Time                          `json:"published_at"`
	Genre             string                             `json:"genre"`
	Language          string                             `json:"language"`
	IsAdultContent    bool                               `json:"is_adult_content"`
	PlayerName        string                             `json:"player_name"`
	PlayerDescription string                             `json:"player_description"`
	WorldContext      string                             `json:"world_context"`
	StorySummary      string                             `json:"story_summary"`
	CoreStats         map[string]publishedCoreStatDetail `json:"core_stats"`
	LastPlayedAt      *time.Time                         `json:"last_played_at,omitempty"` // Время последнего взаимодействия игрока с историей
	IsAuthor          bool                               `json:"is_author"`                // Является ли текущий пользователь автором
}

// <<< НОВЫЙ ОБРАБОТЧИК: setStoryVisibility >>>
// SetStoryVisibilityRequest - структура тела запроса для изменения видимости
type SetStoryVisibilityRequest struct {
	IsPublic bool `json:"is_public"` // Используем binding:"required"?
	// Поле IsPublic должно быть булевым, но binding может не работать с bool напрямую?
	// Проще проверить значение после биндинга.
}

// --- Обработчики --- //

// listMyPublishedStories получает список опубликованных историй текущего пользователя.
func (h *GameplayHandler) listMyPublishedStories(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return
	}

	limitStr := c.Query("limit")
	cursor := c.Query("cursor")

	limit := 10
	if limitStr != "" {
		parsedLimit, err := strconv.Atoi(limitStr)
		if err != nil || parsedLimit <= 0 {
			h.logger.Warn("Invalid limit parameter received", zap.String("limit", limitStr), zap.Error(err))
			handleServiceError(c, fmt.Errorf("%w: invalid 'limit' parameter", sharedModels.ErrBadRequest), h.logger)
			return
		}
		if parsedLimit > 100 {
			parsedLimit = 100
		}
		limit = parsedLimit
	}

	log := h.logger.With(
		zap.String("userID", userID.String()),
		zap.Int("limit", limit),
		zap.String("cursor", cursor),
	)
	log.Debug("Fetching my published stories")

	// Вызываем метод сервиса, который возвращает []*PublishedStoryDetailWithProgressDTO
	storiesDTO, nextCursor, err := h.service.ListMyPublishedStories(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		log.Error("Error listing my published stories", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	// <<< ДОБАВЛЕНО: Конвертация из DTO сервиса в sharedModels >>>
	storySummaries := make([]sharedModels.PublishedStorySummaryWithProgress, len(storiesDTO))
	for i, dto := range storiesDTO {
		if dto == nil {
			log.Warn("Received nil PublishedStoryDetailWithProgressDTO in listMyPublishedStories")
			continue
		}
		title := dto.Title
		description := dto.ShortDescription

		storySummaries[i] = sharedModels.PublishedStorySummaryWithProgress{
			PublishedStorySummary: sharedModels.PublishedStorySummary{
				ID:               dto.ID,
				Title:            title,
				ShortDescription: description,
				AuthorID:         dto.AuthorID,
				AuthorName:       dto.AuthorName,
				PublishedAt:      dto.PublishedAt,
				IsAdultContent:   dto.IsAdultContent,
				LikesCount:       int64(dto.LikesCount),
				IsLiked:          dto.IsLiked,
			},
			HasPlayerProgress: dto.HasPlayerProgress,
		}
	}

	resp := PaginatedResponse{
		Data:       storySummaries,
		NextCursor: nextCursor,
	}

	log.Debug("Successfully fetched my published stories",
		zap.Int("count", len(storySummaries)),
		zap.Bool("hasNext", nextCursor != ""),
	)
	c.JSON(http.StatusOK, resp)
}

// listPublicPublishedStories получает список публичных опубликованных историй.
func (h *GameplayHandler) listPublicPublishedStories(c *gin.Context) {
	userID, _ := getUserIDFromContext(c) // Опционально для проверки лайков и прогресса

	limitStr := c.Query("limit")
	cursor := c.Query("cursor")

	limit := 20
	if limitStr != "" {
		parsedLimit, err := strconv.Atoi(limitStr)
		if err != nil || parsedLimit <= 0 {
			h.logger.Warn("Invalid limit parameter received", zap.String("limit", limitStr), zap.Error(err))
			handleServiceError(c, fmt.Errorf("%w: invalid 'limit' parameter", sharedModels.ErrBadRequest), h.logger)
			return
		}
		if parsedLimit > 100 {
			parsedLimit = 100
		}
		limit = parsedLimit
	}

	log := h.logger.With(
		zap.Int("limit", limit),
		zap.String("cursor", cursor),
	)
	if userID != uuid.Nil {
		log = log.With(zap.String("userID", userID.String()))
	}
	log.Debug("Fetching public published stories")

	// <<< ИЗМЕНЕНО: Вызываем обновленный метод сервиса, который возвращает []sharedModels.PublishedStorySummaryWithProgress >>>
	stories, nextCursor, err := h.service.ListPublishedStoriesPublic(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		log.Error("Error listing public published stories", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	resp := PaginatedResponse{
		Data:       stories,
		NextCursor: nextCursor,
	}

	log.Debug("Successfully fetched public published stories",
		zap.Int("count", len(stories)),
		zap.Bool("hasNext", nextCursor != ""),
	)
	c.JSON(http.StatusOK, resp)
}

// getPublishedStoryScene получает текущую игровую сцену для опубликованной истории
// и возвращает ее в структурированном виде для клиента.
func (h *GameplayHandler) getPublishedStoryScene(c *gin.Context) {
	userID, err := getUserIDFromContext(c) // Используем getUserIDFromContext из http.go
	if err != nil {
		// Ошибка уже обработана и ответ отправлен в getUserIDFromContext
		return
	}

	idStr := c.Param("id")
	storyID, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in getPublishedStoryScene", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	log := h.logger.With(zap.String("storyID", storyID.String()), zap.String("userID", userID.String()))
	log.Info("getPublishedStoryScene called")

	scene, err := h.service.GetStoryScene(c.Request.Context(), userID, storyID)
	if err != nil {
		log.Error("Error calling GetStoryScene service", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	// Парсим json.RawMessage из scene.Content
	var rawContent map[string]json.RawMessage // Используем map для гибкого парсинга
	if scene.Content == nil || len(scene.Content) == 0 || string(scene.Content) == "null" {
		log.Error("Scene content is empty or null", zap.String("sceneID", scene.ID.String()))
		handleServiceError(c, fmt.Errorf("internal error: scene content is missing"), h.logger)
		return
	}
	if err := json.Unmarshal(scene.Content, &rawContent); err != nil {
		log.Error("Failed to unmarshal scene content", zap.String("sceneID", scene.ID.String()), zap.Error(err))
		handleServiceError(c, fmt.Errorf("internal error: failed to parse scene content"), h.logger)
		return
	}

	// Определяем тип сцены
	var sceneType string
	if typeJSON, ok := rawContent["type"]; ok {
		if err := json.Unmarshal(typeJSON, &sceneType); err != nil {
			log.Error("Failed to unmarshal scene type", zap.String("sceneID", scene.ID.String()), zap.Error(err))
			sceneType = "unknown" // Устанавливаем неизвестный тип при ошибке
		}
	} else {
		log.Warn("Scene content missing 'type' field", zap.String("sceneID", scene.ID.String()))
		sceneType = "unknown"
	}

	// Формируем DTO ответа на основе типа сцены
	responseDTO := GameSceneResponseDTO{
		ID:               scene.ID,
		PublishedStoryID: scene.PublishedStoryID,
		Type:             sceneType,
	}

	switch sceneType {
	case "choices", "continuation":
		// Парсим блок 'ch'
		if chJSON, ok := rawContent["ch"]; ok {
			// Временная структура для парсинга блока 'ch'
			type rawChoiceBlock struct {
				Shuffleable int    `json:"sh"`
				Char        string `json:"char"` // <<< ДОБАВЛЕНО ПОЛЕ ДЛЯ ПАРСИНГА
				Description string `json:"desc"`
				Options     []struct {
					Text         string          `json:"txt"`
					Consequences json.RawMessage `json:"cons"`
				} `json:"opts"`
			}
			var rawChoices []rawChoiceBlock
			if err := json.Unmarshal(chJSON, &rawChoices); err != nil {
				log.Error("Failed to unmarshal choices block ('ch')", zap.String("sceneID", scene.ID.String()), zap.Error(err))
			} else {
				responseDTO.Choices = make([]ChoiceBlockDTO, len(rawChoices))
				for i, rawChoice := range rawChoices {
					choiceDTO := ChoiceBlockDTO{
						Shuffleable:   rawChoice.Shuffleable == 1,
						CharacterName: rawChoice.Char, // <<< КОПИРУЕМ ИМЯ ПЕРСОНАЖА
						Description:   rawChoice.Description,
						Options:       make([]ChoiceOptionDTO, len(rawChoice.Options)),
					}
					for j, rawOpt := range rawChoice.Options {
						optionDTO := ChoiceOptionDTO{
							Text: rawOpt.Text,
						}
						// Парсим последствия ('cons') для извлечения 'resp_txt' и 'cs_chg'
						if rawOpt.Consequences != nil && len(rawOpt.Consequences) > 2 { // Проверяем, что не пустое {}
							var consMap map[string]json.RawMessage // Используем RawMessage для гибкости парсинга cs_chg
							if err := json.Unmarshal(rawOpt.Consequences, &consMap); err == nil {
								preview := ConsequencePreviewDTO{}
								hasData := false

								// Извлекаем resp_txt
								if respTxtJSON, ok := consMap["resp_txt"]; ok {
									var respTxtStr string
									if errUnmarshal := json.Unmarshal(respTxtJSON, &respTxtStr); errUnmarshal == nil && respTxtStr != "" {
										preview.ResponseText = &respTxtStr
										hasData = true
									}
								}

								// Извлекаем cs_chg
								if csChgJSON, ok := consMap["cs_chg"]; ok {
									var statChanges map[string]int
									if errUnmarshal := json.Unmarshal(csChgJSON, &statChanges); errUnmarshal == nil && len(statChanges) > 0 {
										preview.StatChanges = statChanges
										hasData = true
									} else if errUnmarshal != nil {
										log.Warn("Failed to unmarshal cs_chg content", zap.String("sceneID", scene.ID.String()), zap.Error(errUnmarshal))
									}
								}

								// Присваиваем preview только если есть какие-то данные
								if hasData {
									optionDTO.Consequences = &preview
								}
							} else {
								log.Warn("Failed to unmarshal consequences map", zap.String("sceneID", scene.ID.String()), zap.Error(err))
							}
						}
						choiceDTO.Options[j] = optionDTO
					}
					responseDTO.Choices[i] = choiceDTO
				}
			}
		}
		if sceneType == "continuation" {
			continuationData := ContinuationDTO{}
			parseError := false
			if npdJSON, ok := rawContent["npd"]; ok {
				if err := json.Unmarshal(npdJSON, &continuationData.NewPlayerDescription); err != nil {
					parseError = true
				}
			} else {
				parseError = true
			}
			if etpJSON, ok := rawContent["etp"]; ok {
				if err := json.Unmarshal(etpJSON, &continuationData.EndingTextPrevious); err != nil {
					parseError = true
				}
			} else {
				parseError = true
			}
			if csrJSON, ok := rawContent["csr"]; ok {
				if err := json.Unmarshal(csrJSON, &continuationData.CoreStatsReset); err != nil {
					parseError = true
				}
			} else {
				parseError = true
			}

			if !parseError {
				responseDTO.Continuation = &continuationData
			} else {
				log.Error("Failed to parse required fields for 'continuation' type scene", zap.String("sceneID", scene.ID.String()))
				// Отправляем ошибку, т.к. данные неполные для этого типа
				handleServiceError(c, fmt.Errorf("internal error: failed to parse continuation scene data"), h.logger)
				return
			}
		}

	case "game_over":
		if etJSON, ok := rawContent["et"]; ok {
			var endingText string
			if err := json.Unmarshal(etJSON, &endingText); err == nil {
				responseDTO.EndingText = &endingText
			} else {
				log.Error("Failed to unmarshal game_over 'et' field", zap.String("sceneID", scene.ID.String()), zap.Error(err))
				// Отправляем ошибку, т.к. данные неполные для этого типа
				handleServiceError(c, fmt.Errorf("internal error: failed to parse game over scene data"), h.logger)
				return
			}
		}
	default:
		log.Error("Unknown or missing scene type in content", zap.String("sceneID", scene.ID.String()), zap.String("type", sceneType))
		handleServiceError(c, fmt.Errorf("internal error: unknown scene type '%s'", sceneType), h.logger)
		return
	}

	log.Info("Successfully fetched and formatted story scene", zap.String("sceneID", scene.ID.String()))
	c.JSON(http.StatusOK, responseDTO)
}

// makeChoice обрабатывает выбор игрока в опубликованной истории.
func (h *GameplayHandler) makeChoice(c *gin.Context) {
	userID, err := getUserIDFromContext(c) // <<< Меняем 'ok' на 'err'
	if err != nil {                        // <<< Проверяем 'err != nil'
		// Ошибка уже обработана в getUserIDFromContext
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in makeChoice", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	var req MakeChoicesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid request body for makeChoice", zap.Stringer("userID", userID), zap.String("storyID", id.String()), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid request body: %v", sharedModels.ErrBadRequest, err), h.logger)
		return
	}

	log := h.logger.With(
		zap.Stringer("userID", userID),
		zap.String("storyID", id.String()),
		zap.Any("selectedOptionIndices", req.SelectedOptionIndices),
	)
	log.Info("Player making choices (batch)")

	err = h.service.MakeChoice(c.Request.Context(), userID, id, req.SelectedOptionIndices)
	if err != nil {
		// Логируем только если это НЕ стандартные ожидаемые ошибки
		if !errors.Is(err, sharedModels.ErrNotFound) &&
			!errors.Is(err, sharedModels.ErrBadRequest) && // BadRequest может быть при невалидном выборе
			!errors.Is(err, service.ErrStoryNotFound) &&
			!errors.Is(err, service.ErrSceneNotFound) &&
			!errors.Is(err, service.ErrInvalidChoiceIndex) && // Добавим ошибку неверного индекса
			!errors.Is(err, service.ErrNoChoicesAvailable) && // Добавим ошибку отсутствия выбора
			!errors.Is(err, sharedModels.ErrSceneNeedsGeneration) { // Если сцена требует генерации
			log.Error("Error making choice (unhandled)", zap.Error(err))
		}
		handleServiceError(c, err, h.logger) // Используем общий обработчик
		return
	}

	log.Info("Player choice processed successfully")

	// После успешного выбора, новая сцена будет доступна через getPublishedStoryScene
	// Возвращаем 204 No Content, т.к. результат нужно запрашивать отдельно
	c.Status(http.StatusNoContent)
}

// deletePlayerProgress удаляет прогресс игрока для опубликованной истории.
func (h *GameplayHandler) deletePlayerProgress(c *gin.Context) {
	userID, err := getUserIDFromContext(c) // <<< Меняем 'ok' на 'err'
	if err != nil {                        // <<< Проверяем 'err != nil'
		// Ошибка уже обработана в getUserIDFromContext
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in deletePlayerProgress", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	log := h.logger.With(zap.String("storyID", id.String()))
	if userID != uuid.Nil {
		log = log.With(zap.String("userID", userID.String()))
	}
	log.Info("Deleting player progress")

	err = h.service.DeletePlayerProgress(c.Request.Context(), userID, id)
	if err != nil {
		// Логируем только если это не ErrNotFound (ожидаемая ошибка, если прогресса нет)
		if !errors.Is(err, service.ErrPlayerProgressNotFound) && !errors.Is(err, sharedModels.ErrNotFound) {
			log.Error("Error deleting player progress", zap.Error(err))
		}
		// Можно вернуть 204 даже при ErrNotFound, т.к. итоговое состояние - прогресса нет.
		// Но если хотим четко сигнализировать, что прогресса и не было, используем handleServiceError
		if errors.Is(err, service.ErrPlayerProgressNotFound) || errors.Is(err, sharedModels.ErrNotFound) {
			// Если прогресса не найдено, можно просто вернуть 204, так как цель достигнута
			log.Info("Player progress not found, deletion skipped (considered success)")
			c.Status(http.StatusNoContent)
			return
		}
		// Для других ошибок используем общий обработчик
		handleServiceError(c, err, h.logger)
		return
	}

	log.Info("Player progress deleted successfully")
	c.Status(http.StatusNoContent)
}

// likeStory обрабатывает запрос на постановку лайка опубликованной истории.
func (h *GameplayHandler) likeStory(c *gin.Context) {
	userID, err := getUserIDFromContext(c) // <<< Меняем 'ok' на 'err'
	if err != nil {                        // <<< Проверяем 'err != nil'
		// Ошибка уже обработана в getUserIDFromContext
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in likeStory", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	log := h.logger.With(
		zap.String("storyID", id.String()),
		zap.String("userID", userID.String()),
	)
	log.Info("Liking story")

	err = h.service.LikeStory(c.Request.Context(), userID, id)
	if err != nil {
		if !errors.Is(err, sharedModels.ErrNotFound) && !errors.Is(err, service.ErrStoryNotFound) {
			log.Error("Error liking story", zap.Error(err))
		}
		handleServiceError(c, err, h.logger)
		return
	}

	log.Info("Story liked successfully")
	c.Status(http.StatusNoContent)
}

// unlikeStory обрабатывает запрос на снятие лайка с опубликованной истории.
func (h *GameplayHandler) unlikeStory(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in unlikeStory", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	log := h.logger.With(
		zap.String("storyID", id.String()),
		zap.String("userID", userID.String()),
	)
	log.Info("Unliking story")

	err = h.service.UnlikeStory(c.Request.Context(), userID, id)
	if err != nil {
		if !errors.Is(err, sharedModels.ErrNotFound) && !errors.Is(err, service.ErrStoryNotFound) {
			// Ошибка 'лайк не найден' также игнорируется при логировании ошибки
			if !errors.Is(err, service.ErrNotLikedYet) {
				log.Error("Error unliking story", zap.Error(err))
			}
		}
		// Возвращаем 204 даже если лайка не было (цель достигнута - лайка нет)
		if errors.Is(err, sharedModels.ErrNotFound) || errors.Is(err, service.ErrStoryNotFound) || errors.Is(err, service.ErrNotLikedYet) {
			log.Info("Story or like not found, unliking skipped (considered success)")
			c.Status(http.StatusNoContent)
			return
		}
		handleServiceError(c, err, h.logger)
		return
	}

	log.Info("Story unliked successfully")
	c.Status(http.StatusNoContent)
}

// listLikedStories получает список историй, которые лайкнул пользователь.
func (h *GameplayHandler) listLikedStories(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return
	}

	limitStr := c.Query("limit")
	cursor := c.Query("cursor")

	limit := 10
	if limitStr != "" {
		parsedLimit, err := strconv.Atoi(limitStr)
		if err != nil || parsedLimit <= 0 {
			h.logger.Warn("Invalid limit parameter received", zap.String("limit", limitStr), zap.Error(err))
			handleServiceError(c, fmt.Errorf("%w: invalid 'limit' parameter", sharedModels.ErrBadRequest), h.logger)
			return
		}
		if parsedLimit > 100 {
			parsedLimit = 100
		}
		limit = parsedLimit
	}

	log := h.logger.With(
		zap.String("userID", userID.String()),
		zap.Int("limit", limit),
		zap.String("cursor", cursor),
	)
	log.Debug("Fetching liked stories")

	// <<< ИЗМЕНЕНО: Вызываем обновленный сервис, получаем DTO - теперь это []*service.LikedStoryDetailDTO >>>
	// Важно: ListLikedStories *не* возвращает []sharedModels.PublishedStorySummaryWithProgress
	// Он возвращает []*service.LikedStoryDetailDTO, который нужно преобразовать в sharedModels.PublishedStorySummaryWithProgress
	likedStoriesDetails, nextCursor, err := h.service.ListLikedStories(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		log.Error("Error listing liked stories", zap.Error(err))
		handleServiceError(c, err, h.logger)
		return
	}

	// <<< ОСТАВЛЯЕМ ПРЕОБРАЗОВАНИЕ, т.к. сервис возвращает другой тип DTO >>>
	storySummaries := make([]sharedModels.PublishedStorySummaryWithProgress, len(likedStoriesDetails))
	for i, detail := range likedStoriesDetails {
		if detail == nil { // Safety check
			log.Warn("Received nil LikedStoryDetailDTO in listLikedStories")
			continue
		}
		title := ""
		if detail.Title != nil {
			title = *detail.Title
		}
		description := ""
		if detail.Description != nil {
			description = *detail.Description
		}
		storySummaries[i] = sharedModels.PublishedStorySummaryWithProgress{
			PublishedStorySummary: sharedModels.PublishedStorySummary{
				ID:               detail.ID,
				Title:            title,
				ShortDescription: description, // Map description to ShortDescription
				AuthorID:         detail.UserID,
				AuthorName:       detail.AuthorName, // <-- Исправлено: Используем поле из DTO
				PublishedAt:      detail.CreatedAt,
				IsAdultContent:   detail.IsAdultContent, // Assuming this field exists in LikedStoryDetailDTO
				LikesCount:       detail.LikesCount,     // Assuming this field exists
				IsLiked:          true,                  // Always true for liked stories endpoint
			},
			HasPlayerProgress: detail.HasPlayerProgress, // Assuming this field exists
		}
	}

	resp := PaginatedResponse{
		Data:       storySummaries, // Используем преобразованные данные
		NextCursor: nextCursor,
	}

	log.Debug("Successfully fetched liked stories",
		zap.Int("count", len(storySummaries)),
		zap.Bool("hasNext", nextCursor != ""),
	)
	c.JSON(http.StatusOK, resp)
}

// setStoryVisibility обрабатывает запрос на изменение видимости опубликованной истории.
func (h *GameplayHandler) setStoryVisibility(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return // Ошибка уже обработана
	}

	idStr := c.Param("id")
	storyID, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in setStoryVisibility", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	var req SetStoryVisibilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid request body for setStoryVisibility", zap.String("storyID", storyID.String()), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid request body: %v", sharedModels.ErrBadRequest, err), h.logger)
		return
	}

	log := h.logger.With(
		zap.String("storyID", storyID.String()),
		zap.Stringer("userID", userID),
		zap.Bool("isPublic", req.IsPublic),
	)
	log.Info("Setting story visibility")

	err = h.service.SetStoryVisibility(c.Request.Context(), storyID, userID, req.IsPublic)
	if err != nil {
		// Логируем только неожидаемые ошибки
		if !errors.Is(err, sharedModels.ErrNotFound) &&
			!errors.Is(err, sharedModels.ErrForbidden) &&
			!errors.Is(err, service.ErrStoryNotReadyForPublishing) &&
			!errors.Is(err, service.ErrInternal) { // Проверяем и на ErrInternal
			log.Error("Error setting story visibility (unhandled)", zap.Error(err))
		}
		handleServiceError(c, err, h.logger) // Передаем ошибку для стандартизированной обработки
		return
	}

	log.Info("Story visibility set successfully")
	c.Status(http.StatusNoContent) // Успех
}

// <<< ДОБАВЛЕНО: Обработчик для повторной генерации опубликованной истории >>>
func (h *GameplayHandler) retryPublishedStoryGeneration(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return // Ошибка уже обработана
	}

	idStr := c.Param("id")
	storyID, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in retryPublishedStoryGeneration", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	log := h.logger.With(zap.String("userID", userID.String()), zap.String("storyID", storyID.String()))
	log.Info("Handling retry published story generation request")

	// <<< ИЗМЕНЕНО: Проверка лимита активных генераций через publishedStoryRepo >>>
	activeCount, err := h.publishedStoryRepo.CountActiveGenerationsForUser(c.Request.Context(), userID)
	if err != nil {
		log.Error("Error counting active generations before retry", zap.Error(err))
		handleServiceError(c, fmt.Errorf("error checking generation status: %w", err), h.logger)
		return
	}
	// <<< ИЗМЕНЕНО: Используем GenerationLimitPerUser из config >>>
	generationLimit := h.config.GenerationLimitPerUser // Используем лимит из конфига
	if activeCount >= generationLimit {
		log.Warn("User reached active generation limit, retry rejected", zap.Int("limit", generationLimit), zap.Int("count", activeCount))
		handleServiceError(c, sharedModels.ErrUserHasActiveGeneration, h.logger)
		return
	}

	// Вызываем метод сервиса
	err = h.service.RetryStoryGeneration(c.Request.Context(), storyID, userID)
	if err != nil {
		log.Error("Error retrying published story generation", zap.Error(err))
		// Используем handleServiceError для стандартизации
		handleServiceError(c, err, h.logger)
		return
	}

	log.Info("Published story retry request accepted")
	c.Status(http.StatusAccepted)
}

// <<<<< НАЧАЛО ОБРАБОТЧИКА УДАЛЕНИЯ ОПУБЛИКОВАННОЙ ИСТОРИИ >>>>>
func (h *GameplayHandler) deletePublishedStory(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return // Abort already called
	}

	idStr := c.Param("id")
	storyID, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid published story ID format in deletePublishedStory", zap.String("id", idStr), zap.Error(err))
		handleServiceError(c, fmt.Errorf("%w: invalid story ID format", sharedModels.ErrBadRequest), h.logger)
		return
	}

	// Вызываем метод сервиса (GameplayService)
	err = h.service.DeletePublishedStory(c.Request.Context(), storyID, userID)
	if err != nil {
		h.logger.Error("Error deleting published story", zap.String("userID", userID.String()), zap.String("storyID", storyID.String()), zap.Error(err))
		handleServiceError(c, err, h.logger) // Обрабатываем стандартные ошибки, включая ErrNotFound и ErrForbidden
		return
	}

	// При успехе возвращаем 204 No Content
	c.Status(http.StatusNoContent)
}

// <<<<< КОНЕЦ ОБРАБОТЧИКА УДАЛЕНИЯ ОПУБЛИКОВАННОЙ ИСТОРИИ >>>>>

// listStoriesWithProgress возвращает список опубликованных историй, в которых у пользователя есть прогресс.
// GET /published-stories/me/progress?limit=10&cursor=...
func (h *GameplayHandler) listStoriesWithProgress(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		h.logger.Warn("Failed to get user ID from context", zap.Error(err))
		c.JSON(http.StatusUnauthorized, APIError{Message: "Unauthorized: " + err.Error()})
		return
	}

	// Парсим параметры пагинации
	limitStr := c.DefaultQuery("limit", "10")
	cursor := c.Query("cursor")

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 || limit > 100 {
		h.logger.Warn("Invalid limit parameter for stories with progress", zap.String("limit", limitStr))
		c.JSON(http.StatusBadRequest, APIError{Message: "Invalid limit parameter. Must be between 1 and 100."})
		return
	}

	// Вызываем метод сервиса для получения историй с прогрессом
	stories, nextCursor, serviceErr := h.service.GetStoriesWithProgress(c.Request.Context(), userID, limit, cursor)
	if serviceErr != nil {
		h.logger.Error("Failed to get stories with progress", zap.String("userID", userID.String()), zap.Error(serviceErr))
		// TODO: Различать типы ошибок (Not Found, Internal)?
		c.JSON(http.StatusInternalServerError, APIError{Message: "Failed to retrieve stories with progress"})
		return
	}

	// Формируем ответ
	response := PaginatedResponse{
		Data:       stories,
		NextCursor: nextCursor,
	}

	c.JSON(http.StatusOK, response)
}

// --- Вспомогательные функции для опубликованных историй --- //
