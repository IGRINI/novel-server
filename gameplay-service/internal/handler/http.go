package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"novel-server/gameplay-service/internal/service"
	"novel-server/shared/authutils"
	sharedMiddleware "novel-server/shared/middleware"
	sharedModels "novel-server/shared/models"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// StoryConfigSummary представляет сокращенную версию StoryConfig для списков.
type StoryConfigSummary struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
	Status      string    `json:"status"`
}

// StoryConfigDetail представляет детальную информацию о StoryConfig для ответа.
type StoryConfigDetail struct {
	ID        string          `json:"id"`
	CreatedAt time.Time       `json:"createdAt"`
	Status    string          `json:"status"`
	Config    json.RawMessage `json:"config,omitempty"` // Может быть nil, omitempty скроет если так
}

// StoryConfigParsedDetail представляет распарсенные данные из StoryConfig.Config для ответа.
type StoryConfigParsedDetail struct {
	Title             string                    `json:"title"`
	ShortDescription  string                    `json:"shortDescription"`
	Franchise         string                    `json:"franchise"`
	Genre             string                    `json:"genre"`
	Language          string                    `json:"language"`
	IsAdultContent    bool                      `json:"isAdultContent"`
	PlayerName        string                    `json:"playerName"`
	PlayerDescription string                    `json:"playerDescription"`
	WorldContext      string                    `json:"worldContext"`
	StorySummary      string                    `json:"storySummary"`
	CoreStats         map[string]parsedCoreStat `json:"coreStats"`
}

// parsedCoreStat структура для статов в StoryConfigParsedDetail.
type parsedCoreStat struct {
	Description        string                   `json:"description"`
	InitialValue       int                      `json:"initialValue"`
	GameOverConditions parsedGameOverConditions `json:"gameOverConditions"`
}

// parsedGameOverConditions структура для условий Game Over.
type parsedGameOverConditions struct {
	Min bool `json:"min"`
	Max bool `json:"max"`
}

// APIError представляет стандартизированный ответ об ошибке.
type APIError struct {
	Message string `json:"message"`
	// Code    int    `json:"code,omitempty"` // Можно добавить внутренний код ошибки
}

// GameplayHandler обрабатывает HTTP запросы для gameplay сервиса.
type GameplayHandler struct {
	service                   service.GameplayService
	logger                    *zap.Logger
	userTokenVerifier         *authutils.JWTVerifier
	interServiceTokenVerifier *authutils.JWTVerifier
}

// NewGameplayHandler создает новый GameplayHandler.
func NewGameplayHandler(s service.GameplayService, logger *zap.Logger, jwtSecret, interServiceSecret string) *GameplayHandler {
	userVerifier, err := authutils.NewJWTVerifier(jwtSecret, logger)
	if err != nil {
		logger.Fatal("Failed to create User JWT Verifier", zap.Error(err))
	}

	interServiceVerifier, err := authutils.NewJWTVerifier(interServiceSecret, logger)
	if err != nil {
		logger.Fatal("Failed to create Inter-Service JWT Verifier", zap.Error(err))
	}

	return &GameplayHandler{
		service:                   s,
		logger:                    logger.Named("GameplayHandler"),
		userTokenVerifier:         userVerifier,
		interServiceTokenVerifier: interServiceVerifier,
	}
}

// RegisterRoutes регистрирует маршруты для gameplay сервиса в Gin.
func (h *GameplayHandler) RegisterRoutes(router gin.IRouter) {
	// Middleware для проверки токена пользователя (используем Gin middleware)
	authMiddleware := sharedMiddleware.AuthMiddleware(h.userTokenVerifier.VerifyToken, h.logger)

	// --- Маршруты для черновиков историй (API для пользователей) ---
	// Используем router.Group и синтаксис Gin
	storiesGroup := router.Group("/stories")
	storiesGroup.Use(authMiddleware) // Применяем middleware к группе
	{
		storiesGroup.POST("/generate", h.generateInitialStory)
		storiesGroup.GET("", h.listStoryConfigs)
		storiesGroup.GET("/:id", h.getStoryConfig)
		storiesGroup.POST("/:id/revise", h.reviseStoryConfig)
		storiesGroup.POST("/:id/publish", h.publishStoryDraft)
		storiesGroup.POST("/drafts/:draft_id/retry", h.retryDraftGeneration)
	}

	// --- Маршруты для опубликованных историй (API для пользователей) ---
	publishedGroup := router.Group("/published-stories")
	publishedGroup.Use(authMiddleware)
	{
		publishedGroup.GET("/me", h.listMyPublishedStories)
		publishedGroup.GET("/public", h.listPublicPublishedStories)
		publishedGroup.GET("/:id/scene", h.getPublishedStoryScene)
		publishedGroup.POST("/:id/choice", h.makeChoice)
		publishedGroup.DELETE("/:id/progress", h.deletePlayerProgress)
		publishedGroup.POST("/:id/like", h.likeStory)
		publishedGroup.DELETE("/:id/like", h.unlikeStory)
	}

	// Группа для внутренних маршрутов (если понадобится, ее можно будет добавить позже)
}

// --- Вспомогательные функции --- //

// getUserIDFromContext извлекает userID как uuid.UUID из *gin.Context.
func getUserIDFromContext(c *gin.Context) (uuid.UUID, error) {
	// Используем ключ из shared/middleware
	userIDVal, exists := c.Get(sharedMiddleware.GinUserContextKey)
	if !exists {
		return uuid.Nil, fmt.Errorf("user_id не найден в контексте Gin")
	}

	userID, ok := userIDVal.(uuid.UUID)
	if !ok {
		return uuid.Nil, fmt.Errorf("неверный тип user_id в контексте Gin: ожидался uuid.UUID, получено %T", userIDVal)
	}

	if userID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("невалидный (нулевой) user_id UUID в контексте Gin")
	}

	return userID, nil
}

// handleServiceError обрабатывает ошибки сервисного слоя и отправляет ответ через *gin.Context.
func handleServiceError(c *gin.Context, err error, logger *zap.Logger) {
	var statusCode int
	// Используем sharedModels.ErrorResponse
	var apiErr sharedModels.ErrorResponse

	switch {
	// Используем ошибки и коды из sharedModels
	case errors.Is(err, sharedModels.ErrUnauthorized):
		statusCode = http.StatusUnauthorized
		apiErr = sharedModels.ErrorResponse{Code: sharedModels.ErrCodeUnauthorized, Message: "Unauthorized"}
	case errors.Is(err, sharedModels.ErrForbidden):
		statusCode = http.StatusForbidden
		apiErr = sharedModels.ErrorResponse{Code: sharedModels.ErrCodeForbidden, Message: "Forbidden"}
	case errors.Is(err, sharedModels.ErrUserBanned): // Пример, если понадобится
		statusCode = http.StatusForbidden
		apiErr = sharedModels.ErrorResponse{Code: sharedModels.ErrCodeUserBanned, Message: "User is banned"}
	case errors.Is(err, sharedModels.ErrNotFound):
		statusCode = http.StatusNotFound
		// Уточняем сообщение в зависимости от типа NotFound, если возможно, иначе общее
		msg := "Resource not found" // Общее сообщение
		if errors.Is(err, service.ErrStoryNotFound) || errors.Is(err, service.ErrSceneNotFound) {
			msg = err.Error() // Используем сообщение из ошибки сервиса
		}
		apiErr = sharedModels.ErrorResponse{Code: sharedModels.ErrCodeNotFound, Message: msg}
	case errors.Is(err, sharedModels.ErrCannotRevise):
		statusCode = http.StatusConflict
		apiErr = sharedModels.ErrorResponse{Code: sharedModels.ErrCodeCannotRevise, Message: err.Error()}
	case errors.Is(err, sharedModels.ErrUserHasActiveGeneration):
		statusCode = http.StatusConflict
		apiErr = sharedModels.ErrorResponse{Code: sharedModels.ErrCodeUserHasActiveGeneration, Message: err.Error()}
	case errors.Is(err, sharedModels.ErrGenerationInProgress):
		statusCode = http.StatusConflict // Или 422 Unprocessable Entity?
		apiErr = sharedModels.ErrorResponse{Code: sharedModels.ErrCodeGenerationInProgress, Message: err.Error()}
	case errors.Is(err, service.ErrInvalidOperation):
		statusCode = http.StatusBadRequest
		apiErr = sharedModels.ErrorResponse{Code: sharedModels.ErrCodeBadRequest, Message: err.Error()}
	case errors.Is(err, sharedModels.ErrStoryNotReadyYet):
		statusCode = http.StatusNotFound // Или 409 Conflict?
		apiErr = sharedModels.ErrorResponse{Code: sharedModels.ErrCodeStoryNotReadyYet, Message: err.Error()}
	case errors.Is(err, sharedModels.ErrSceneNeedsGeneration):
		statusCode = http.StatusNotFound // Или 409 Conflict?
		apiErr = sharedModels.ErrorResponse{Code: sharedModels.ErrCodeSceneNeedsGeneration, Message: err.Error()}
	case errors.Is(err, service.ErrInvalidChoiceIndex), errors.Is(err, service.ErrInvalidChoice), errors.Is(err, service.ErrStoryNotReady):
		statusCode = http.StatusBadRequest
		apiErr = sharedModels.ErrorResponse{Code: sharedModels.ErrCodeBadRequest, Message: err.Error()} // Можно уточнить код, если нужно
	case errors.Is(err, sharedModels.ErrBadRequest), errors.Is(err, sharedModels.ErrInvalidInput):
		statusCode = http.StatusBadRequest
		apiErr = sharedModels.ErrorResponse{Code: sharedModels.ErrCodeBadRequest, Message: err.Error()}
	default: // Все остальное - внутренняя ошибка
		logger.Error("Unhandled internal error", zap.Error(err)) // Используем переданный логгер
		statusCode = http.StatusInternalServerError
		apiErr = sharedModels.ErrorResponse{Code: sharedModels.ErrCodeInternal, Message: "Internal server error"}
	}
	// Используем c.AbortWithStatusJSON для отправки ошибки и прерывания
	c.AbortWithStatusJSON(statusCode, apiErr)
}

// Структура ответа для пагинированных списков
type PaginatedResponse struct {
	Data       interface{} `json:"data"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

// --- Обработчики HTTP --- //
// Обработчики (h.generateInitialStory, h.listStoryConfigs и т.д.)
// теперь должны принимать *gin.Context вместо echo.Context.
// Их реализацию нужно будет обновить в соответствующих *_handlers.go файлах.
