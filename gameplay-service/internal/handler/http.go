package handler

import (
	"errors"
	"fmt"
	"net/http"
	"novel-server/gameplay-service/internal/service"  // Типы сообщений
	sharedMiddleware "novel-server/shared/middleware" // Middleware аутентификации
	sharedModels "novel-server/shared/models"         // Общие ошибки
	"strconv"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// APIError представляет стандартизированный ответ об ошибке.
type APIError struct {
	Message string `json:"message"`
	// Code    int    `json:"code,omitempty"` // Можно добавить внутренний код ошибки
}

// GameplayHandler обрабатывает HTTP запросы для gameplay сервиса.
type GameplayHandler struct {
	service service.GameplayService
}

// NewGameplayHandler создает новый GameplayHandler.
func NewGameplayHandler(s service.GameplayService) *GameplayHandler {
	return &GameplayHandler{service: s}
}

// RegisterRoutes регистрирует маршруты для gameplay сервиса.
func (h *GameplayHandler) RegisterRoutes(e *echo.Echo, jwtSecret string) {
	apiGroup := e.Group("/api")
	apiGroup.Use(sharedMiddleware.JWTAuthMiddleware(jwtSecret))
	{
		storiesGroup := apiGroup.Group("/stories")
		{
			storiesGroup.POST("/generate", h.generateInitialStory) // Новый эндпоинт для начальной генерации
			storiesGroup.GET("/:id", h.getStoryConfig)
			storiesGroup.POST("/:id/revise", h.reviseStoryConfig) // Обновляем обработчик ревизии
		}
	}
}

// --- Вспомогательные функции --- //

func getUserIDFromContext(c echo.Context) (uint64, error) {
	userIDStr := c.Get("user_id").(string)
	if userIDStr == "" {
		return 0, fmt.Errorf("user_id не найден в контексте")
	}
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("неверный формат user_id в контексте: %w", err)
	}
	return userID, nil
}

func handleServiceError(c echo.Context, err error) error {
	var statusCode int
	var apiErr APIError

	switch {
	case errors.Is(err, sharedModels.ErrNotFound):
		statusCode = http.StatusNotFound
		apiErr = APIError{Message: "Story configuration not found or access denied"}
	case errors.Is(err, service.ErrCannotRevise):
		statusCode = http.StatusConflict // 409 Conflict
		apiErr = APIError{Message: err.Error()}
	case errors.Is(err, service.ErrUserHasActiveGeneration):
		statusCode = http.StatusConflict // 409 Conflict (или 429 Too Many Requests?)
		apiErr = APIError{Message: err.Error()}
	default:
		// Логируем неизвестную ошибку
		c.Logger().Errorf("Unhandled internal error: %v", err)
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
			c.Logger().Errorf("Error publishing initial generation task for UserID %d: %v", userID, err)
			return c.JSON(http.StatusInternalServerError, config)
		}
		// Если ошибка была до отправки (например, проверка конкуренции), обрабатываем стандартно
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
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid story ID format"})
	}

	config, err := h.service.GetStoryConfig(c.Request().Context(), id, userID)
	if err != nil {
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
		return handleServiceError(c, err)
	}

	// Возвращаем 202 Accepted (без тела, т.к. результат придет по WebSocket)
	return c.NoContent(http.StatusAccepted)
}
