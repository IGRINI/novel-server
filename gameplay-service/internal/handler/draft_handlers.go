package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	sharedInterfaces "novel-server/shared/interfaces"
	sharedModels "novel-server/shared/models"
	"strconv"

	"novel-server/gameplay-service/internal/service" // Добавляем импорт сервиса

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

// Новый запрос для начальной генерации
type generateInitialStoryRequest struct {
	Prompt string `json:"prompt" binding:"required"`
}

// Новый обработчик для начальной генерации
func (h *GameplayHandler) generateInitialStory(c echo.Context) error {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, APIError{Message: err.Error()})
	}

	var req generateInitialStoryRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid request body: " + err.Error()})
	}

	// Вызываем новый метод сервиса
	config, err := h.service.GenerateInitialStory(c.Request().Context(), userID, req.Prompt)
	if err != nil {
		// GenerateInitialStory может вернуть ошибку И конфиг (если ошибка была при отправке задачи)
		if config != nil {
			// Если ошибка отправки, статус конфига уже Error
			// Возвращаем 500 и сам конфиг (с ID и статусом Error)
			// чтобы клиент знал, что что-то пошло не так, но запись создана.
			// Используем логгер хендлера
			h.logger.Error("Error publishing initial generation task", zap.String("userID", userID.String()), zap.Error(err))
			// Вернуть 500 можно, но не через handleServiceError, так как это специфичный случай
			return c.JSON(http.StatusInternalServerError, config) // Возвращаем конфиг со статусом Error
		}
		// Если ошибка была до отправки (например, проверка конкуренции), обрабатываем стандартно
		// Логируем только если это НЕ стандартная ошибка
		if !errors.Is(err, sharedModels.ErrNotFound) &&
			!errors.Is(err, service.ErrCannotRevise) && // << Используем service.ErrCannotRevise
			!errors.Is(err, sharedModels.ErrUserHasActiveGeneration) &&
			!errors.Is(err, service.ErrInvalidOperation) {
			h.logger.Error("Error generating initial story (unhandled)", zap.String("userID", userID.String()), zap.Error(err))
		}
		return handleServiceError(c, err)
	}

	// Возвращаем 202 Accepted и созданный конфиг (со статусом generating)
	return c.JSON(http.StatusAccepted, config)
}

func (h *GameplayHandler) getStoryConfig(c echo.Context) error {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, APIError{Message: err.Error()})
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid story ID format in getStoryConfig", zap.String("id", idStr), zap.Error(err))
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid story ID format"})
	}

	config, err := h.service.GetStoryConfig(c.Request().Context(), id, userID)
	if err != nil {
		// Логируем только если это НЕ NotFound
		if !errors.Is(err, sharedModels.ErrNotFound) {
			h.logger.Error("Error getting story config", zap.String("userID", userID.String()), zap.String("storyID", id.String()), zap.Error(err))
		}
		return handleServiceError(c, err)
	}

	// Проверяем, есть ли сам конфиг
	if config.Config == nil || len(config.Config) == 0 {
		// Если конфига нет (например, статус generating или error),
		// возвращаем базовую информацию о черновике.
		detail := StoryConfigDetail{
			ID:        config.ID.String(),
			CreatedAt: config.CreatedAt,
			Status:    string(config.Status),
			Config:    nil, // Явно указываем, что конфига нет
		}
		h.logger.Warn("Story config JSON is nil, returning basic details", zap.String("storyID", id.String()), zap.String("status", string(config.Status)))
		return c.JSON(http.StatusOK, detail)
	}

	// Определяем внутреннюю структуру для анмаршалинга JSON из config.Config
	// Используем теги json, соответствующие сжатым ключам из промпта narrator.md
	type internalCoreStat struct {
		D  string `json:"d"`
		Iv int    `json:"iv"`
		Go struct {
			Min bool `json:"min"`
			Max bool `json:"max"`
		} `json:"go"`
	}
	type internalConfig struct {
		T     string                      `json:"t"`
		Sd    string                      `json:"sd"`
		Fr    string                      `json:"fr"`
		Gn    string                      `json:"gn"`
		Ln    string                      `json:"ln"`
		Ac    bool                        `json:"ac"`
		Pn    string                      `json:"pn"`
		PDesc string                      `json:"p_desc"`
		Wc    string                      `json:"wc"`
		Ss    string                      `json:"ss"`
		Cs    map[string]internalCoreStat `json:"cs"`
		// Остальные поля (pg, s_so_far, fd, pp, sc) нам не нужны для DTO, пропускаем
	}

	var parsedInternal internalConfig
	if err := json.Unmarshal(config.Config, &parsedInternal); err != nil {
		h.logger.Error("Failed to unmarshal story config JSON", zap.String("storyID", id.String()), zap.Error(err))
		// Возвращаем базовую информацию + статус error, т.к. не смогли распарсить конфиг
		detail := StoryConfigDetail{
			ID:        config.ID.String(),
			CreatedAt: config.CreatedAt,
			Status:    string(sharedModels.StatusError), // <<< Используем sharedModels
			Config:    nil,
		}
		return c.JSON(http.StatusInternalServerError, detail) // Ошибка сервера, т.к. данные некорректны
	}

	// Преобразуем внутреннюю структуру в публичный DTO
	publicDetail := StoryConfigParsedDetail{
		Title:             parsedInternal.T,
		ShortDescription:  parsedInternal.Sd,
		Franchise:         parsedInternal.Fr,
		Genre:             parsedInternal.Gn,
		Language:          parsedInternal.Ln,
		IsAdultContent:    parsedInternal.Ac,
		PlayerName:        parsedInternal.Pn,
		PlayerDescription: parsedInternal.PDesc,
		WorldContext:      parsedInternal.Wc,
		StorySummary:      parsedInternal.Ss,
		CoreStats:         make(map[string]parsedCoreStat, len(parsedInternal.Cs)),
	}

	// Преобразуем карту статов
	for name, internalStat := range parsedInternal.Cs {
		publicDetail.CoreStats[name] = parsedCoreStat{
			Description:  internalStat.D,
			InitialValue: internalStat.Iv,
			GameOverConditions: parsedGameOverConditions{
				Min: internalStat.Go.Min,
				Max: internalStat.Go.Max,
			},
		}
	}

	return c.JSON(http.StatusOK, publicDetail) // <<< Возвращаем распарсенный DTO
}

// Обновляем reviseStoryRequest и reviseStoryConfig
type reviseStoryRequest struct {
	RevisionPrompt string `json:"revision_prompt" binding:"required"`
}

func (h *GameplayHandler) reviseStoryConfig(c echo.Context) error {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, APIError{Message: err.Error()})
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid story ID format"})
	}

	var req reviseStoryRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid request body: " + err.Error()})
	}

	// Вызываем обновленный метод сервиса
	err = h.service.ReviseDraft(c.Request().Context(), id, userID, req.RevisionPrompt)
	if err != nil {
		// Логируем только если это НЕ стандартная ошибка
		if !errors.Is(err, sharedModels.ErrNotFound) &&
			!errors.Is(err, service.ErrCannotRevise) && // << Используем service.ErrCannotRevise
			!errors.Is(err, sharedModels.ErrUserHasActiveGeneration) &&
			!errors.Is(err, service.ErrInvalidOperation) {
			h.logger.Error("Error revising draft (unhandled)", zap.String("userID", userID.String()), zap.String("storyID", id.String()), zap.Error(err))
		}
		return handleServiceError(c, err)
	}

	// Возвращаем 202 Accepted (без тела, т.к. результат придет по WebSocket)
	return c.NoContent(http.StatusAccepted)
}

// PublishStoryResponse определяет тело ответа при публикации истории.
type PublishStoryResponse struct {
	PublishedStoryID string `json:"published_story_id"`
}

// publishStoryDraft обрабатывает запрос на публикацию черновика.
func (h *GameplayHandler) publishStoryDraft(c echo.Context) error {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, APIError{Message: err.Error()})
	}

	idStr := c.Param("id")
	draftID, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid draft ID format received", zap.String("id", idStr), zap.Error(err))
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid story ID format"})
	}

	h.logger.Info("Received request to publish draft", zap.String("userID", userID.String()), zap.String("draftID", draftID.String()))

	// Вызываем метод сервиса для публикации
	publishedID, err := h.service.PublishDraft(c.Request().Context(), draftID, userID)
	if err != nil {
		// Логируем только если это НЕ стандартная ошибка
		if !errors.Is(err, sharedModels.ErrNotFound) &&
			!errors.Is(err, service.ErrInvalidOperation) {
			h.logger.Error("Error publishing draft (unhandled)", zap.String("userID", userID.String()), zap.String("draftID", draftID.String()), zap.Error(err))
		}
		// Ошибка уже залогирована внутри PublishDraft при неудачной попытке
		return handleServiceError(c, err) // Используем общий обработчик ошибок
	}

	h.logger.Info("Draft published successfully", zap.String("userID", userID.String()), zap.String("draftID", draftID.String()), zap.String("publishedID", publishedID.String()))

	// Возвращаем 202 Accepted и ID опубликованной истории
	// Используем экспортированный тип
	resp := PublishStoryResponse{PublishedStoryID: publishedID.String()}
	return c.JSON(http.StatusAccepted, resp)
}

// listStoryConfigs получает список черновиков пользователя с пагинацией.
func (h *GameplayHandler) listStoryConfigs(c echo.Context) error {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, APIError{Message: err.Error()})
	}

	// Получаем параметры пагинации из query
	limitStr := c.QueryParam("limit")
	cursor := c.QueryParam("cursor") // Курсор может быть пустым

	// Устанавливаем лимит по умолчанию
	limit := 10 // Можете изменить значение по умолчанию
	if limitStr != "" {
		parsedLimit, err := strconv.Atoi(limitStr)
		if err != nil || parsedLimit <= 0 {
			h.logger.Warn("Invalid limit parameter received", zap.String("limit", limitStr), zap.Error(err))
			return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid 'limit' parameter"})
		}
		// Ограничим максимальный лимит, чтобы избежать чрезмерной нагрузки
		if parsedLimit > 100 {
			parsedLimit = 100
		}
		limit = parsedLimit
	}

	h.logger.Debug("Fetching story drafts",
		zap.String("userID", userID.String()),
		zap.Int("limit", limit),
		zap.String("cursor", cursor),
	)

	// Вызываем метод сервиса
	drafts, nextCursor, err := h.service.ListMyDrafts(c.Request().Context(), userID, cursor, limit) // <<< Меняем порядок cursor и limit
	if err != nil {
		// Логируем только если это не стандартная ошибка курсора
		if !errors.Is(err, sharedInterfaces.ErrInvalidCursor) { // <<< Используем sharedInterfaces
			h.logger.Error("Error listing story drafts", zap.String("userID", userID.String()), zap.Error(err))
		}
		// Обрабатываем ошибку через стандартный хелпер
		return handleServiceError(c, err)
	}

	// Преобразуем полные модели в DTO для ответа
	draftSummaries := make([]StoryConfigSummary, len(drafts))
	for i, draft := range drafts {
		draftSummaries[i] = StoryConfigSummary{
			ID:          draft.ID.String(),
			Title:       draft.Title,
			Description: draft.Description,
			CreatedAt:   draft.CreatedAt,
			Status:      string(draft.Status),
		}
	}

	// Формируем ответ
	resp := PaginatedResponse{
		Data:       draftSummaries, // <<< Возвращаем DTO
		NextCursor: nextCursor,
	}

	h.logger.Debug("Successfully fetched story drafts",
		zap.String("userID", userID.String()),
		zap.Int("count", len(draftSummaries)), // <<< Используем длину среза DTO
		zap.Bool("hasNext", nextCursor != ""),
	)

	return c.JSON(http.StatusOK, resp)
}

// <<< ДОБАВЛЕНО: Обработчик для повторной генерации >>>
func (h *GameplayHandler) retryDraftGeneration(c echo.Context) error {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, APIError{Message: err.Error()})
	}

	draftIDStr := c.Param("draft_id")
	draftID, err := uuid.Parse(draftIDStr)
	if err != nil {
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid draft ID format: " + err.Error()})
	}

	log := h.logger.With(zap.String("userID", userID.String()), zap.String("draftID", draftID.String()))
	log.Info("Handling retry draft generation request")

	err = h.service.RetryDraftGeneration(c.Request().Context(), draftID, userID)
	if err != nil {
		log.Error("Error retrying draft generation", zap.Error(err))
		// Handle specific errors returned by the service
		if errors.Is(err, sharedModels.ErrNotFound) {
			return c.JSON(http.StatusNotFound, APIError{Message: "Draft not found or access denied"})
		} else if errors.Is(err, service.ErrCannotRetry) { // << Используем service.ErrCannotRetry
			return c.JSON(http.StatusConflict, APIError{Message: err.Error()})
		} else if errors.Is(err, sharedModels.ErrUserHasActiveGeneration) {
			return c.JSON(http.StatusConflict, APIError{Message: err.Error()})
		}
		// Default to internal server error
		return c.JSON(http.StatusInternalServerError, APIError{Message: "Internal server error while retrying generation"})
	}

	// Return 202 Accepted on success
	log.Info("Draft retry request accepted")
	return c.NoContent(http.StatusAccepted)
}
