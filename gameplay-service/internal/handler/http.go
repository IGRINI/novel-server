package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http" // Импорт для StoryConfig

	// <<< Добавляем импорт репозитория
	"novel-server/gameplay-service/internal/service"
	"novel-server/shared/authutils" // <<< Добавляем импорт общего верификатора

	// <<< Добавляем импорт для ErrInvalidCursor
	sharedMiddleware "novel-server/shared/middleware"
	sharedModels "novel-server/shared/models" // Для парсинга limit
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap" // Добавляем импорт zap
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
	userTokenVerifier         *authutils.JWTVerifier // Верификатор для токенов пользователей
	interServiceTokenVerifier *authutils.JWTVerifier // <<< Верификатор для межсервисных токенов
}

// NewGameplayHandler создает новый GameplayHandler.
// <<< Добавляем interServiceSecret в аргументы >>>
func NewGameplayHandler(s service.GameplayService, logger *zap.Logger, jwtSecret, interServiceSecret string) *GameplayHandler {
	// Создаем верификатор токенов пользователей
	userVerifier, err := authutils.NewJWTVerifier(jwtSecret, logger)
	if err != nil {
		logger.Fatal("Failed to create User JWT Verifier", zap.Error(err))
	}

	// <<< Создаем верификатор межсервисных токенов >>>
	interServiceVerifier, err := authutils.NewJWTVerifier(interServiceSecret, logger)
	if err != nil {
		logger.Fatal("Failed to create Inter-Service JWT Verifier", zap.Error(err))
	}

	return &GameplayHandler{
		service:                   s,
		logger:                    logger.Named("GameplayHandler"),
		userTokenVerifier:         userVerifier,         // <<< Сохраняем верификатор пользователя
		interServiceTokenVerifier: interServiceVerifier, // <<< Сохраняем межсервисный верификатор
	}
}

// RegisterRoutes регистрирует маршруты для gameplay сервиса.
func (h *GameplayHandler) RegisterRoutes(e *echo.Echo) {
	// Middleware для проверки токена пользователя
	authMiddleware := echo.WrapMiddleware(sharedMiddleware.AuthMiddleware(h.userTokenVerifier.VerifyToken, h.logger))

	// Middleware для проверки межсервисного токена
	// Используем созданный нами InterServiceAuthMiddleware
	// Передаем сам верификатор, а не конкретный метод
	// interServiceAuthMiddleware := sharedMiddleware.InterServiceAuthMiddleware(h.interServiceTokenVerifier, h.logger)

	// --- Маршруты для черновиков историй (API для пользователей) ---
	storiesGroup := e.Group("/stories", authMiddleware)
	{
		storiesGroup.POST("/generate", h.generateInitialStory)
		storiesGroup.GET("", h.listStoryConfigs)
		storiesGroup.GET("/:id", h.getStoryConfig)
		storiesGroup.POST("/:id/revise", h.reviseStoryConfig)
		storiesGroup.POST("/:id/publish", h.publishStoryDraft)
		storiesGroup.POST("/drafts/:draft_id/retry", h.retryDraftGeneration)
	}

	// --- Маршруты для опубликованных историй (API для пользователей) ---
	publishedGroup := e.Group("/published-stories", authMiddleware)
	{
		publishedGroup.GET("/me", h.listMyPublishedStories)
		publishedGroup.GET("/public", h.listPublicPublishedStories)
		publishedGroup.GET("/:id/scene", h.getPublishedStoryScene)
		publishedGroup.POST("/:id/choice", h.makeChoice)
		publishedGroup.DELETE("/:id/progress", h.deletePlayerProgress)
		// Добавляем роуты для лайков
		publishedGroup.POST("/:id/like", h.likeStory)
		publishedGroup.DELETE("/:id/like", h.unlikeStory)
	}

	// <<< Новая группа для внутренних маршрутов >>>
	// Группа internalGroup удалена, т.к. обработчики перенесены
}

// --- Вспомогательные функции --- //

// getUserIDFromContext извлекает userID как uuid.UUID.
func getUserIDFromContext(c echo.Context) (uuid.UUID, error) {
	userIDVal := c.Request().Context().Value(sharedModels.UserContextKey)
	if userIDVal == nil {
		return uuid.Nil, fmt.Errorf("user_id не найден в контексте")
	}
	// <<< Ожидаем uuid.UUID из контекста >>>
	userID, ok := userIDVal.(uuid.UUID)
	if !ok {
		// <<< Логика парсинга строки больше не нужна >>>
		// userIDStr, ok := userIDVal.(string)
		// if !ok {
		return uuid.Nil, fmt.Errorf("неверный тип user_id в контексте: ожидался uuid.UUID, получено %T", userIDVal)
		// }
		//
		// // Парсим строку как UUID
		// userID, err := uuid.Parse(userIDStr)
		// if err != nil {
		// 	// Логгируем ошибку парсинга
		// 	// h.logger.Error("Не удалось распарсить user_id из контекста как UUID", zap.String("value", userIDStr), zap.Error(err)) // Logger недоступен здесь
		// 	return uuid.Nil, fmt.Errorf("невалидный формат user_id в контексте: %w", err)
		// }
	}

	// Проверяем, не является ли UUID нулевым (дополнительная проверка)
	if userID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("невалидный (нулевой) user_id UUID в контексте")
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

// Структура ответа для пагинированных списков
type PaginatedResponse struct {
	Data       interface{} `json:"data"` // Срез с данными (истории, черновики)
	NextCursor string      `json:"next_cursor,omitempty"`
}

// --- Обработчики HTTP --- //

// Все обработчики перенесены в *_handlers.go файлы

// --- Новые обработчики для внутренних API --- //

// Все обработчики перенесены в *_handlers.go файлы

// <<< КОНЕЦ ДОБАВЛЕННОГО ОБРАБОТЧИКА >>>

// <<< Конец файла >>>
