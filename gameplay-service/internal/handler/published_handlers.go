package handler

import (
	"errors"
	"net/http"
	"novel-server/gameplay-service/internal/service" // Добавляем импорт сервиса
	sharedModels "novel-server/shared/models"
	"strconv"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

// MakeChoiceRequest определяет тело запроса для выбора игрока.
type MakeChoiceRequest struct {
	// Индекс выбранной опции (0 или 1) в текущем блоке выбора.
	SelectedOptionIndex int `json:"selected_option_index" validate:"min=0,max=1"`
}

// listMyPublishedStories получает список опубликованных историй текущего пользователя.
func (h *GameplayHandler) listMyPublishedStories(c echo.Context) error {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, APIError{Message: err.Error()})
	}

	// Получаем параметры пагинации (limit, offset)
	limitStr := c.QueryParam("limit")
	offsetStr := c.QueryParam("offset")

	limit := 10 // Значение по умолчанию
	if limitStr != "" {
		parsedLimit, err := strconv.Atoi(limitStr)
		if err != nil || parsedLimit <= 0 {
			h.logger.Warn("Invalid limit parameter in listMyPublishedStories", zap.String("limit", limitStr), zap.Error(err))
			return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid 'limit' parameter"})
		}
		if parsedLimit > 100 { // Ограничение сверху
			parsedLimit = 100
		}
		limit = parsedLimit
	}

	offset := 0 // Значение по умолчанию
	if offsetStr != "" {
		parsedOffset, err := strconv.Atoi(offsetStr)
		if err != nil || parsedOffset < 0 {
			h.logger.Warn("Invalid offset parameter in listMyPublishedStories", zap.String("offset", offsetStr), zap.Error(err))
			return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid 'offset' parameter"})
		}
		offset = parsedOffset
	}

	h.logger.Debug("Fetching my published stories",
		zap.Stringer("userID", userID),
		zap.Int("limit", limit),
		zap.Int("offset", offset),
	)

	stories, err := h.service.ListMyPublishedStories(c.Request().Context(), userID, limit, offset)
	if err != nil {
		h.logger.Error("Error listing my published stories", zap.Stringer("userID", userID), zap.Error(err))
		// Используем общий обработчик, который может вернуть 500 или другие ошибки
		return handleServiceError(c, err)
	}

	// Используем PaginatedResponse без курсора, т.к. сервис использует offset
	resp := PaginatedResponse{
		Data: stories,
		// NextCursor здесь не используется, так как пагинация через offset
	}

	h.logger.Debug("Successfully fetched my published stories",
		zap.Stringer("userID", userID),
		zap.Int("count", len(stories)),
	)

	return c.JSON(http.StatusOK, resp)
}

// listPublicPublishedStories получает список публичных опубликованных историй.
func (h *GameplayHandler) listPublicPublishedStories(c echo.Context) error {
	// Для публичных историй userID не нужен
	// Получаем параметры пагинации (limit, offset)
	limitStr := c.QueryParam("limit")
	offsetStr := c.QueryParam("offset")

	limit := 20 // Значение по умолчанию (может отличаться от "моих")
	if limitStr != "" {
		parsedLimit, err := strconv.Atoi(limitStr)
		if err != nil || parsedLimit <= 0 {
			h.logger.Warn("Invalid limit parameter in listPublicPublishedStories", zap.String("limit", limitStr), zap.Error(err))
			return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid 'limit' parameter"})
		}
		if parsedLimit > 100 { // Ограничение сверху
			parsedLimit = 100
		}
		limit = parsedLimit
	}

	offset := 0 // Значение по умолчанию
	if offsetStr != "" {
		parsedOffset, err := strconv.Atoi(offsetStr)
		if err != nil || parsedOffset < 0 {
			h.logger.Warn("Invalid offset parameter in listPublicPublishedStories", zap.String("offset", offsetStr), zap.Error(err))
			return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid 'offset' parameter"})
		}
		offset = parsedOffset
	}

	h.logger.Debug("Fetching public published stories",
		zap.Int("limit", limit),
		zap.Int("offset", offset),
	)

	stories, err := h.service.ListPublicStories(c.Request().Context(), limit, offset)
	if err != nil {
		h.logger.Error("Error listing public published stories", zap.Error(err))
		// Используем общий обработчик
		return handleServiceError(c, err)
	}

	// Используем PaginatedResponse без курсора
	resp := PaginatedResponse{
		Data: stories,
	}

	h.logger.Debug("Successfully fetched public published stories",
		zap.Int("count", len(stories)),
	)

	return c.JSON(http.StatusOK, resp)
}

// getPublishedStoryScene получает текущую игровую сцену для опубликованной истории.
func (h *GameplayHandler) getPublishedStoryScene(c echo.Context) error {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, APIError{Message: err.Error()})
	}

	idStr := c.Param("id")
	publishedStoryID, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid published story ID format in getPublishedStoryScene", zap.String("id", idStr), zap.Error(err))
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid published story ID format"})
	}

	h.logger.Debug("Fetching story scene",
		zap.Stringer("userID", userID),
		zap.String("publishedStoryID", publishedStoryID.String()),
	)

	scene, err := h.service.GetStoryScene(c.Request().Context(), userID, publishedStoryID)
	if err != nil {
		// Логируем только если это НЕ стандартная ошибка (NotFound, NeedsGeneration, NotReadyYet)
		if !errors.Is(err, sharedModels.ErrNotFound) &&
			!errors.Is(err, sharedModels.ErrStoryNotReadyYet) &&
			!errors.Is(err, sharedModels.ErrSceneNeedsGeneration) &&
			!errors.Is(err, service.ErrStoryNotFound) && // Добавим проверку на ошибку сервиса
			!errors.Is(err, service.ErrSceneNotFound) { // Добавим проверку на ошибку сервиса
			h.logger.Error("Error getting story scene (unhandled)", zap.Stringer("userID", userID), zap.String("publishedStoryID", publishedStoryID.String()), zap.Error(err))
		}
		return handleServiceError(c, err) // Используем общий обработчик
	}

	h.logger.Debug("Successfully fetched story scene",
		zap.Stringer("userID", userID),
		zap.String("publishedStoryID", publishedStoryID.String()),
		zap.String("sceneID", scene.ID.String()),
	)

	return c.JSON(http.StatusOK, scene)
}

// makeChoice обрабатывает выбор игрока в опубликованной истории.
func (h *GameplayHandler) makeChoice(c echo.Context) error {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, APIError{Message: err.Error()})
	}

	idStr := c.Param("id")
	publishedStoryID, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid published story ID format in makeChoice", zap.String("id", idStr), zap.Error(err))
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid published story ID format"})
	}

	var req MakeChoiceRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Warn("Invalid request body for makeChoice", zap.Stringer("userID", userID), zap.String("publishedStoryID", publishedStoryID.String()), zap.Error(err))
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid request body: " + err.Error()})
	}

	// Валидация индекса (хотя может быть и в Bind, но для надежности)
	if req.SelectedOptionIndex < 0 || req.SelectedOptionIndex > 1 {
		h.logger.Warn("Invalid selected option index", zap.Int("index", req.SelectedOptionIndex))
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid 'selected_option_index', must be 0 or 1"})
	}

	h.logger.Info("Player making choice", // Используем Info, т.к. это важное действие
		zap.Stringer("userID", userID),
		zap.String("publishedStoryID", publishedStoryID.String()),
		zap.Int("selectedOptionIndex", req.SelectedOptionIndex),
	)

	err = h.service.MakeChoice(c.Request().Context(), userID, publishedStoryID, req.SelectedOptionIndex)
	if err != nil {
		// Логируем только если это НЕ стандартные ожидаемые ошибки
		if !errors.Is(err, service.ErrInvalidChoiceIndex) &&
			!errors.Is(err, service.ErrStoryNotFound) &&
			!errors.Is(err, service.ErrSceneNotFound) &&
			!errors.Is(err, service.ErrPlayerProgressNotFound) && // Добавим ошибку прогресса
			!errors.Is(err, service.ErrStoryNotReady) &&
			!errors.Is(err, service.ErrInvalidChoice) &&
			!errors.Is(err, service.ErrNoChoicesAvailable) && // Добавим ошибку отсутствия выбора
			!errors.Is(err, sharedModels.ErrSceneNeedsGeneration) { // Если сцена требует генерации
			h.logger.Error("Error making choice (unhandled)", zap.Stringer("userID", userID), zap.String("publishedStoryID", publishedStoryID.String()), zap.Int("index", req.SelectedOptionIndex), zap.Error(err))
		}
		return handleServiceError(c, err) // Используем общий обработчик
	}

	h.logger.Info("Player choice processed successfully",
		zap.Stringer("userID", userID),
		zap.String("publishedStoryID", publishedStoryID.String()),
		zap.Int("selectedOptionIndex", req.SelectedOptionIndex),
	)

	// После успешного выбора, новая сцена будет доступна через getPublishedStoryScene
	// Возвращаем 204 No Content, т.к. результат нужно запрашивать отдельно
	return c.NoContent(http.StatusNoContent)
}

// deletePlayerProgress удаляет прогресс игрока для опубликованной истории.
func (h *GameplayHandler) deletePlayerProgress(c echo.Context) error {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, APIError{Message: err.Error()})
	}

	idStr := c.Param("id")
	publishedStoryID, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid published story ID format in deletePlayerProgress", zap.String("id", idStr), zap.Error(err))
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid published story ID format"})
	}

	h.logger.Info("Deleting player progress",
		zap.Stringer("userID", userID),
		zap.String("publishedStoryID", publishedStoryID.String()),
	)

	err = h.service.DeletePlayerProgress(c.Request().Context(), userID, publishedStoryID)
	if err != nil {
		// Логируем только если это не ErrNotFound (ожидаемая ошибка, если прогресса нет)
		if !errors.Is(err, service.ErrPlayerProgressNotFound) && !errors.Is(err, sharedModels.ErrNotFound) {
			h.logger.Error("Error deleting player progress", zap.Stringer("userID", userID), zap.String("publishedStoryID", publishedStoryID.String()), zap.Error(err))
		}
		// Можно вернуть 204 даже при ErrNotFound, т.к. итоговое состояние - прогресса нет.
		// Но если хотим четко сигнализировать, что прогресса и не было, используем handleServiceError
		if errors.Is(err, service.ErrPlayerProgressNotFound) || errors.Is(err, sharedModels.ErrNotFound) {
			// Если прогресса не найдено, можно просто вернуть 204, так как цель достигнута
			h.logger.Info("Player progress not found, deletion skipped (considered success)",
				zap.Stringer("userID", userID),
				zap.String("publishedStoryID", publishedStoryID.String()),
			)
			return c.NoContent(http.StatusNoContent)
		}
		// Для других ошибок используем общий обработчик
		return handleServiceError(c, err)
	}

	h.logger.Info("Player progress deleted successfully",
		zap.Stringer("userID", userID),
		zap.String("publishedStoryID", publishedStoryID.String()),
	)

	return c.NoContent(http.StatusNoContent)
}

// likeStory обрабатывает запрос на постановку лайка опубликованной истории.
func (h *GameplayHandler) likeStory(c echo.Context) error {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, APIError{Message: err.Error()})
	}

	idStr := c.Param("id")
	publishedStoryID, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid published story ID format in likeStory", zap.String("id", idStr), zap.Error(err))
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid published story ID format"})
	}

	h.logger.Info("User liking story",
		zap.Stringer("userID", userID),
		zap.String("publishedStoryID", publishedStoryID.String()),
	)

	// TODO: Заменить заглушку на вызов реального сервиса
	// err = h.service.LikeStory(c.Request().Context(), userID, publishedStoryID)
	err = nil // Заглушка

	if err != nil {
		// TODO: Добавить обработку ожидаемых ошибок (ErrAlreadyLiked, ErrStoryNotFound)
		// if !errors.Is(err, service.ErrAlreadyLiked) && !errors.Is(err, service.ErrStoryNotFound) {
		h.logger.Error("Error liking story (unhandled)", zap.Stringer("userID", userID), zap.String("publishedStoryID", publishedStoryID.String()), zap.Error(err))
		// }
		return handleServiceError(c, err) // Используем общий обработчик
	}

	h.logger.Info("Story liked successfully",
		zap.Stringer("userID", userID),
		zap.String("publishedStoryID", publishedStoryID.String()),
	)

	return c.NoContent(http.StatusNoContent)
}

// unlikeStory обрабатывает запрос на снятие лайка с опубликованной истории.
func (h *GameplayHandler) unlikeStory(c echo.Context) error {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, APIError{Message: err.Error()})
	}

	idStr := c.Param("id")
	publishedStoryID, err := uuid.Parse(idStr)
	if err != nil {
		h.logger.Warn("Invalid published story ID format in unlikeStory", zap.String("id", idStr), zap.Error(err))
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid published story ID format"})
	}

	h.logger.Info("User unliking story",
		zap.Stringer("userID", userID),
		zap.String("publishedStoryID", publishedStoryID.String()),
	)

	// TODO: Заменить заглушку на вызов реального сервиса
	// err = h.service.UnlikeStory(c.Request().Context(), userID, publishedStoryID)
	err = nil // Заглушка

	if err != nil {
		// TODO: Добавить обработку ожидаемых ошибок (ErrNotLikedYet, ErrStoryNotFound)
		// if !errors.Is(err, service.ErrNotLikedYet) && !errors.Is(err, service.ErrStoryNotFound) {
		h.logger.Error("Error unliking story (unhandled)", zap.Stringer("userID", userID), zap.String("publishedStoryID", publishedStoryID.String()), zap.Error(err))
		// }
		return handleServiceError(c, err) // Используем общий обработчик
	}

	h.logger.Info("Story unliked successfully",
		zap.Stringer("userID", userID),
		zap.String("publishedStoryID", publishedStoryID.String()),
	)

	return c.NoContent(http.StatusNoContent)
}
