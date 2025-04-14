package handler

import (
	"errors"
	"fmt"
	"net/http" // Импорт для StoryConfig
	"novel-server/gameplay-service/internal/service"
	sharedMiddleware "novel-server/shared/middleware"
	sharedModels "novel-server/shared/models"
	"novel-server/shared/authutils" // <<< Добавляем импорт общего верификатора

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
	service       service.GameplayService
	logger        *zap.Logger // Добавляем логгер
	tokenVerifier *authutils.JWTVerifier // <<< Добавляем верификатор
}

// NewGameplayHandler создает новый GameplayHandler.
func NewGameplayHandler(s service.GameplayService, logger *zap.Logger, jwtSecret string) *GameplayHandler {
	// Создаем верификатор токенов
	verifier, err := authutils.NewJWTVerifier(jwtSecret, logger)
	if err != nil {
		// Критическая ошибка, если секрет пуст или что-то не так
		logger.Fatal("Failed to create JWT Verifier", zap.Error(err))
	}
	return &GameplayHandler{
		service:       s,
		logger:        logger.Named("GameplayHandler"), // Инициализируем логгер
		tokenVerifier: verifier, // <<< Сохраняем верификатор
	}
}

// RegisterRoutes регистрирует маршруты для gameplay сервиса.
func (h *GameplayHandler) RegisterRoutes(e *echo.Echo) {
	apiGroup := e.Group("/api")
	// Используем метод VerifyToken из нашего верификатора
	apiGroup.Use(echo.WrapMiddleware(sharedMiddleware.AuthMiddleware(h.tokenVerifier.VerifyToken, h.logger)))
	{
		// Маршруты для черновиков (StoryConfig)
		_ = apiGroup.Group("/stories") // Используем _, так как storiesGroup не используется
		// h.registerStoryRoutes(storiesGroup) // TODO: Реализовать или раскомментировать

		// Маршруты для опубликованных историй (PublishedStory)
		_ = apiGroup.Group("/published-stories") // Используем _, так как publishedGroup не используется
		// h.registerPublishedStoryRoutes(publishedGroup) // TODO: Реализовать или раскомментировать
		// h.registerPublicStoriesRoutes(publishedGroup) // TODO: Реализовать или раскомментировать
	}
}

// --- Вспомогательные функции --- //

// getUserIDFromContext извлекает userID как uint64 (для старых эндпоинтов).
func getUserIDFromContext(c echo.Context) (uint64, error) {
	userIDVal := c.Request().Context().Value(sharedModels.UserContextKey)
	if userIDVal == nil {
		return 0, fmt.Errorf("user_id не найден в контексте")
	}
	userID, ok := userIDVal.(uint64)
	if !ok {
		return 0, fmt.Errorf("неверный тип user_id в контексте: %T", userIDVal)
	}
	if userID == 0 {
		// Можно оставить или убрать эту проверку в зависимости от логики
		return 0, fmt.Errorf("невалидный user_id (0) в контексте")
	}
	return userID, nil
}

func handleServiceError(c echo.Context, err error) error {
	var statusCode int
	var apiErr APIError

	switch {
	case errors.Is(err, sharedModels.ErrUnauthorized):
		statusCode = http.StatusUnauthorized
		apiErr = APIError{Message: "Unauthorized"}
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
	case errors.Is(err, service.ErrStoryNotFound) || errors.Is(err, service.ErrSceneNotFound):
		statusCode = http.StatusNotFound
		apiErr = APIError{Message: err.Error()}
	case errors.Is(err, service.ErrInvalidChoice) || errors.Is(err, service.ErrStoryNotReady):
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
	NextCursor string      `json:"next_cursor,omitempty"`
}