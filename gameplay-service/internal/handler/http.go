package handler

import (
	"errors"
	"fmt"
	"net/http" // Импорт для StoryConfig
	"novel-server/gameplay-service/internal/service"
	sharedMiddleware "novel-server/shared/middleware"
	sharedModels "novel-server/shared/models"
	"strconv"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap" // Добавляем импорт zap
)

// APIError представляет стандартизированный ответ об ошибке.
type APIError struct {
	Message string `json:"message"`
	// Code    int    `json:"code,omitempty"` // Можно добавить внутренний код ошибки
}

// GameplayHandler обрабатывает HTTP запросы для gameplay сервиса.
type GameplayHandler struct {
	service service.GameplayService
	logger  *zap.Logger // Добавляем логгер
}

// NewGameplayHandler создает новый GameplayHandler.
func NewGameplayHandler(s service.GameplayService, logger *zap.Logger) *GameplayHandler {
	return &GameplayHandler{
		service: s,
		logger:  logger.Named("GameplayHandler"), // Инициализируем логгер
	}
}

// RegisterRoutes регистрирует маршруты для gameplay сервиса.
func (h *GameplayHandler) RegisterRoutes(e *echo.Echo, jwtSecret string) {
	apiGroup := e.Group("/api")
	apiGroup.Use(sharedMiddleware.JWTAuthMiddleware(jwtSecret))
	{
		// Маршруты для черновиков (StoryConfig)
		storiesGroup := apiGroup.Group("/stories")
		{
			storiesGroup.POST("/generate", h.generateInitialStory)
			storiesGroup.GET("", h.listMyDrafts) // Новый маршрут GET /api/stories
			storiesGroup.GET("/:id", h.getStoryConfig)
			storiesGroup.POST("/:id/revise", h.reviseStoryConfig)
			storiesGroup.POST("/:id/publish", h.publishStoryDraft)
		}

		// Маршруты для опубликованных историй (PublishedStory)
		publishedGroup := apiGroup.Group("/published-stories")
		{
			publishedGroup.GET("/me", h.listMyPublishedStories)            // Новый маршрут GET /api/published-stories/me
			publishedGroup.GET("/public", h.listPublicStories)             // Новый маршрут GET /api/published-stories/public
			publishedGroup.GET("/:id/scene", h.getStoryScene)              // GET /api/published-stories/:id/scene
			publishedGroup.POST("/:id/choice", h.makeChoice)               // POST /api/published-stories/:id/choice
			publishedGroup.DELETE("/:id/progress", h.deletePlayerProgress) // DELETE /api/published-stories/:id/progress
		}
	}
}

// --- Вспомогательные функции --- //

// getUserIDFromContext извлекает userID как uint64 (для старых эндпоинтов).
func getUserIDFromContext(c echo.Context) (uint64, error) {
	userIDStr, ok := c.Get("user_id").(string)
	if !ok || userIDStr == "" {
		return 0, fmt.Errorf("user_id не найден или имеет неверный тип в контексте")
	}
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("неверный формат user_id в контексте: %w", err)
	}
	return userID, nil
}

// getUserUUIDFromContext извлекает userID как uuid.UUID (для новых эндпоинтов).
func getUserUUIDFromContext(c echo.Context) (uuid.UUID, error) {
	userIDStr, ok := c.Get("user_id").(string) // Предполагаем, что middleware кладет UUID как строку
	if !ok || userIDStr == "" {
		return uuid.Nil, fmt.Errorf("user_id (uuid) не найден или имеет неверный тип в контексте")
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return uuid.Nil, fmt.Errorf("неверный формат user_id (uuid) в контексте: %w", err)
	}
	return userID, nil
}

func handleServiceError(c echo.Context, err error) error {
	var statusCode int
	var apiErr APIError

	switch {
	case errors.Is(err, sharedModels.ErrNotFound):
		statusCode = http.StatusNotFound
		apiErr = APIError{Message: "Resource not found or access denied"}
	case errors.Is(err, sharedModels.ErrCannotRevise): // Используем sharedModels
		statusCode = http.StatusConflict // 409 Conflict
		apiErr = APIError{Message: err.Error()}
	case errors.Is(err, sharedModels.ErrUserHasActiveGeneration): // Используем sharedModels
		statusCode = http.StatusConflict // 409 Conflict (или 429 Too Many Requests?)
		apiErr = APIError{Message: err.Error()}
	case errors.Is(err, service.ErrInvalidOperation):
		statusCode = http.StatusBadRequest // 400 Bad Request для недопустимой операции
		apiErr = APIError{Message: err.Error()}
	case errors.Is(err, sharedModels.ErrStoryNotReadyYet): // Используем sharedModels
		statusCode = http.StatusNotFound // Сцена еще не готова
		apiErr = APIError{Message: err.Error()}
	case errors.Is(err, sharedModels.ErrSceneNeedsGeneration): // Используем sharedModels
		statusCode = http.StatusNotFound // Сцену нужно генерировать
		apiErr = APIError{Message: err.Error()}
	case errors.Is(err, service.ErrInvalidChoiceIndex):
		statusCode = http.StatusBadRequest
		apiErr = APIError{Message: err.Error()}
	default:
		statusCode = http.StatusInternalServerError
		apiErr = APIError{Message: "Internal server error"}
	}
	return c.JSON(statusCode, apiErr)
}

// --- Обработчики HTTP --- //

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
			h.logger.Error("Error publishing initial generation task", zap.Uint64("userID", userID), zap.Error(err))
			// Вернуть 500 можно, но не через handleServiceError, так как это специфичный случай
			return c.JSON(http.StatusInternalServerError, config) // Возвращаем конфиг со статусом Error
		}
		// Если ошибка была до отправки (например, проверка конкуренции), обрабатываем стандартно
		// Логируем только если это НЕ стандартная ошибка
		if !errors.Is(err, sharedModels.ErrNotFound) &&
			!errors.Is(err, service.ErrCannotRevise) &&
			!errors.Is(err, service.ErrUserHasActiveGeneration) &&
			!errors.Is(err, service.ErrInvalidOperation) {
			h.logger.Error("Error generating initial story (unhandled)", zap.Uint64("userID", userID), zap.Error(err))
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
			h.logger.Error("Error getting story config", zap.Uint64("userID", userID), zap.String("storyID", id.String()), zap.Error(err))
		}
		return handleServiceError(c, err)
	}

	return c.JSON(http.StatusOK, config)
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
			!errors.Is(err, service.ErrCannotRevise) &&
			!errors.Is(err, service.ErrUserHasActiveGeneration) &&
			!errors.Is(err, service.ErrInvalidOperation) {
			h.logger.Error("Error revising draft (unhandled)", zap.Uint64("userID", userID), zap.String("storyID", id.String()), zap.Error(err))
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

// MakeChoiceRequest определяет тело запроса для выбора игрока.
type MakeChoiceRequest struct {
	// Индекс выбранной опции (0 или 1) в текущем блоке выбора.
	SelectedOptionIndex int `json:"selected_option_index" validate:"min=0,max=1"`
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

	h.logger.Info("Received request to publish draft", zap.Uint64("userID", userID), zap.String("draftID", draftID.String()))

	// Вызываем метод сервиса для публикации
	publishedID, err := h.service.PublishDraft(c.Request().Context(), draftID, userID)
	if err != nil {
		// Логируем только если это НЕ стандартная ошибка
		if !errors.Is(err, sharedModels.ErrNotFound) &&
			!errors.Is(err, service.ErrInvalidOperation) {
			h.logger.Error("Error publishing draft (unhandled)", zap.Uint64("userID", userID), zap.String("draftID", draftID.String()), zap.Error(err))
		}
		// Ошибка уже залогирована внутри PublishDraft при неудачной попытке
		return handleServiceError(c, err) // Используем общий обработчик ошибок
	}

	h.logger.Info("Draft published successfully", zap.Uint64("userID", userID), zap.String("draftID", draftID.String()), zap.String("publishedID", publishedID.String()))

	// Возвращаем 202 Accepted и ID опубликованной истории
	// Используем экспортированный тип
	resp := PublishStoryResponse{PublishedStoryID: publishedID.String()}
	return c.JSON(http.StatusAccepted, resp)
}

// Структура ответа для пагинированных списков
type PaginatedResponse struct {
	Data       interface{} `json:"data"`                  // Срез с данными (истории, черновики)
	NextCursor string      `json:"next_cursor,omitempty"` // Следующий курсор (только для курсорной пагинации)
	// Можно добавить TotalCount для offset пагинации, если нужно
}

// listMyDrafts обрабатывает запрос на получение списка черновиков пользователя.
func (h *GameplayHandler) listMyDrafts(c echo.Context) error {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, APIError{Message: err.Error()})
	}

	// Читаем параметры пагинации
	limitStr := c.QueryParam("limit")
	cursor := c.QueryParam("cursor")

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 || limit > 100 { // Валидация limit
		limit = 20 // Значение по умолчанию
	}

	h.logger.Debug("Request to list user drafts", zap.Uint64("userID", userID), zap.Int("limit", limit), zap.String("cursor", cursor))

	drafts, nextCursor, err := h.service.ListMyDrafts(c.Request().Context(), userID, limit, cursor)
	if err != nil {
		// Логируем только непредвиденные ошибки
		if !errors.Is(err, service.ErrInvalidCursor) {
			h.logger.Error("Error listing user drafts", zap.Uint64("userID", userID), zap.Int("limit", limit), zap.String("cursor", cursor), zap.Error(err))
		}
		if errors.Is(err, service.ErrInvalidCursor) {
			return echo.NewHTTPError(http.StatusBadRequest, "Невалидный формат курсора")
		}
		return handleServiceError(c, err) // Используем общий обработчик
	}

	// Формируем ответ
	resp := PaginatedResponse{
		Data:       drafts,
		NextCursor: nextCursor,
	}
	return c.JSON(http.StatusOK, resp)
}

// listMyPublishedStories обрабатывает запрос на получение списка опубликованных историй пользователя.
func (h *GameplayHandler) listMyPublishedStories(c echo.Context) error {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, APIError{Message: err.Error()})
	}

	// Читаем параметры пагинации offset/limit
	limitStr := c.QueryParam("limit")
	offsetStr := c.QueryParam("offset")

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 || limit > 100 {
		limit = 20 // Default
	}
	offset, err := strconv.Atoi(offsetStr)
	if err != nil || offset < 0 {
		offset = 0 // Default
	}

	h.logger.Debug("Request to list user published stories", zap.Uint64("userID", userID), zap.Int("limit", limit), zap.Int("offset", offset))

	stories, err := h.service.ListMyPublishedStories(c.Request().Context(), userID, limit, offset)
	if err != nil {
		// Ошибки валидации limit/offset обрабатываются в сервисе (возвращается дефолт)
		// Логируем ошибки БД
		h.logger.Error("Error listing user published stories", zap.Uint64("userID", userID), zap.Int("limit", limit), zap.Int("offset", offset), zap.Error(err))
		return handleServiceError(c, err)
	}

	// Формируем ответ (без next_cursor для offset пагинации)
	resp := PaginatedResponse{
		Data: stories,
	}
	return c.JSON(http.StatusOK, resp)
}

// listPublicStories обрабатывает запрос на получение списка публичных историй.
func (h *GameplayHandler) listPublicStories(c echo.Context) error {
	// Аутентификация для публичных историй не обязательна?
	// Если да, убрать apiGroup.Use(sharedMiddleware.JWTAuthMiddleware(jwtSecret)) для этого маршрута
	// Пока оставляем под аутентификацией

	limitStr := c.QueryParam("limit")
	offsetStr := c.QueryParam("offset")

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 || limit > 100 {
		limit = 20
	}
	offset, err := strconv.Atoi(offsetStr)
	if err != nil || offset < 0 {
		offset = 0
	}

	h.logger.Debug("Request to list public stories", zap.Int("limit", limit), zap.Int("offset", offset))

	stories, err := h.service.ListPublicStories(c.Request().Context(), limit, offset)
	if err != nil {
		h.logger.Error("Error listing public stories", zap.Int("limit", limit), zap.Int("offset", offset), zap.Error(err))
		return handleServiceError(c, err)
	}

	resp := PaginatedResponse{
		Data: stories,
	}
	return c.JSON(http.StatusOK, resp)
}

// getStoryScene обрабатывает запрос на получение текущей сцены для опубликованной истории.
func (h *GameplayHandler) getStoryScene(c echo.Context) error {
	userID, err := getUserUUIDFromContext(c) // <<< Используем новую функцию
	if err != nil {
		h.logger.Error("Failed to get user UUID from context", zap.Error(err))
		return echo.NewHTTPError(http.StatusUnauthorized, err.Error()) // Возвращаем ошибку парсинга
	}

	storyIDStr := c.Param("id")
	publishedStoryID, err := uuid.Parse(storyIDStr)
	if err != nil {
		h.logger.Warn("Invalid published story ID format received", zap.String("id", storyIDStr), zap.Error(err))
		return echo.NewHTTPError(http.StatusBadRequest, "Неверный формат ID опубликованной истории")
	}

	ctx := c.Request().Context()
	h.logger.Info("Request to get story scene", zap.String("userID", userID.String()), zap.String("publishedStoryID", publishedStoryID.String())) // <<< Логируем UUID как строку

	scene, err := h.service.GetStoryScene(ctx, userID, publishedStoryID) // <<< Передаем UUID
	if err != nil {
		logFields := []zap.Field{
			zap.String("userID", userID.String()), // <<< Логируем UUID как строку
			zap.String("publishedStoryID", publishedStoryID.String()),
			zap.Error(err),
		}
		// Используем handleServiceError для стандартизации ответа
		if !errors.Is(err, sharedModels.ErrNotFound) &&
			!errors.Is(err, sharedModels.ErrStoryNotReadyYet) &&
			!errors.Is(err, sharedModels.ErrSceneNeedsGeneration) {
			h.logger.Error("Failed to get story scene from service (unhandled)", logFields...)
		}
		return handleServiceError(c, err)
	}

	// Сцена найдена, возвращаем ее
	return c.JSON(http.StatusOK, scene)
}

// makeChoice обрабатывает выбор игрока в конкретной истории.
// POST /api/published-stories/:id/choice
func (h *GameplayHandler) makeChoice(c echo.Context) error {
	userID, err := getUserUUIDFromContext(c) // <<< Используем новую функцию
	if err != nil {
		h.logger.Error("Failed to get user UUID from context", zap.Error(err))
		return echo.NewHTTPError(http.StatusUnauthorized, err.Error()) // Возвращаем ошибку парсинга
	}

	storyIDStr := c.Param("id")
	publishedStoryID, err := uuid.Parse(storyIDStr)
	if err != nil {
		h.logger.Warn("Неверный формат publishedStoryID в запросе", zap.String("id", storyIDStr), zap.Error(err))
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid published story ID format")
	}

	var req MakeChoiceRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Warn("Ошибка привязки тела запроса makeChoice", zap.Error(err))
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request body: "+err.Error())
	}

	// Валидация запроса
	if err := c.Validate(req); err != nil {
		h.logger.Warn("Ошибка валидации запроса makeChoice", zap.Error(err))
		// Преобразуем ошибки валидации в читаемый формат
		validationErrors := err.(validator.ValidationErrors)
		errorMsg := "Validation failed: " + validationErrors.Error() // Можно сделать более детально
		return echo.NewHTTPError(http.StatusBadRequest, errorMsg)
	}

	h.logger.Info("Получен запрос makeChoice",
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", storyIDStr),
		zap.Int("selectedOptionIndex", req.SelectedOptionIndex)) // Используем новое поле

	err = h.service.MakeChoice(c.Request().Context(), userID, publishedStoryID, req.SelectedOptionIndex) // Передаем одно значение
	if err != nil {
		h.logger.Error("Ошибка при обработке выбора в сервисе", zap.Error(err))
		// TODO: Определить, какие ошибки сервиса маппить в какие HTTP статусы
		if errors.Is(err, service.ErrStoryNotFound) || errors.Is(err, service.ErrSceneNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		if errors.Is(err, service.ErrInvalidChoice) || errors.Is(err, service.ErrStoryNotReady) {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to process choice")
	}

	h.logger.Info("Выбор успешно обработан",
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", storyIDStr))

	// TODO: Что возвращать клиенту? Пока просто 200 OK
	return c.NoContent(http.StatusOK)
}

// deletePlayerProgress обрабатывает запрос на удаление прогресса игрока.
func (h *GameplayHandler) deletePlayerProgress(c echo.Context) error {
	h.logger.Info("deletePlayerProgress handler called")

	// 1. Получаем UserID из контекста (установлен middleware)
	userIDCtx := c.Get("userID")
	if userIDCtx == nil {
		h.logger.Warn("UserID not found in context")
		return echo.NewHTTPError(http.StatusUnauthorized, "UserID не найден в контексте")
	}
	userID, ok := userIDCtx.(uuid.UUID)
	if !ok {
		h.logger.Error("Invalid UserID type in context", zap.Any("userID", userIDCtx))
		return echo.NewHTTPError(http.StatusInternalServerError, "Невалидный UserID в контексте")
	}

	// 2. Получаем ID опубликованной истории из пути
	publishedStoryIDStr := c.Param("id")
	publishedStoryID, err := uuid.Parse(publishedStoryIDStr)
	if err != nil {
		h.logger.Warn("Invalid published story ID format", zap.String("id", publishedStoryIDStr), zap.Error(err))
		return echo.NewHTTPError(http.StatusBadRequest, "Неверный формат ID опубликованной истории")
	}

	ctx := c.Request().Context()
	h.logger.Info("Attempting to delete player progress",
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()))

	// 3. Вызываем сервис для удаления прогресса
	if err := h.service.DeletePlayerProgress(ctx, userID, publishedStoryID); err != nil {
		h.logger.Error("Service error deleting player progress", zap.Error(err),
			zap.String("userID", userID.String()),
			zap.String("publishedStoryID", publishedStoryID.String()))

		if errors.Is(err, sharedModels.ErrNotFound) {
			// Если история не найдена, возвращаем 404
			return echo.NewHTTPError(http.StatusNotFound, "Опубликованная история не найдена")
		}
		// Другие ошибки считаем внутренними ошибками сервера
		return echo.NewHTTPError(http.StatusInternalServerError, "Ошибка при удалении прогресса игрока")
	}

	h.logger.Info("Player progress deleted successfully",
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()))

	return c.NoContent(http.StatusNoContent) // Успешное удаление
}

// --- Регистрация роутов ---

func (h *GameplayHandler) registerStoryRoutes(g *echo.Group) {
	g.POST("/generate", h.generateInitialStory)
	g.GET("/:id", h.getStoryConfig)
	g.POST("/:id/revise", h.reviseStoryConfig)
	g.POST("/:id/publish", h.publishStoryDraft)
	g.GET("", h.listMyDrafts) // GET /api/stories
}

func (h *GameplayHandler) registerPublishedStoryRoutes(g *echo.Group) {
	g.GET("/me", h.listMyPublishedStories) // GET /api/published-stories/me
	g.GET("/:id/scene", h.getStoryScene)   // GET /api/published-stories/:id/scene
	// !!! ДОБАВЛЯЕМ РОУТ ДЛЯ ВЫБОРА !!!
	g.POST("/:id/choice", h.makeChoice)               // POST /api/published-stories/:id/choice
	g.DELETE("/:id/progress", h.deletePlayerProgress) // DELETE /api/published-stories/:id/progress
}

func (h *GameplayHandler) registerPublicStoriesRoutes(g *echo.Group) {
	g.GET("", h.listPublicStories) // GET /api/published-stories/public
}
