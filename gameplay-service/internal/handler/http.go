package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"novel-server/gameplay-service/internal/config"
	"novel-server/gameplay-service/internal/service"
	"novel-server/shared/authutils"
	interfaces "novel-server/shared/interfaces"
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
	storyConfigRepo           interfaces.StoryConfigRepository
	publishedStoryRepo        interfaces.PublishedStoryRepository
	config                    *config.Config
}

// NewGameplayHandler создает новый GameplayHandler.
func NewGameplayHandler(s service.GameplayService, logger *zap.Logger, jwtSecret, interServiceSecret string, storyConfigRepo interfaces.StoryConfigRepository, publishedStoryRepo interfaces.PublishedStoryRepository, cfg *config.Config) *GameplayHandler {
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
		storyConfigRepo:           storyConfigRepo,
		publishedStoryRepo:        publishedStoryRepo,
		config:                    cfg,
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
		storiesGroup.DELETE("/:id", h.deleteDraft)
	}

	// --- Маршруты для опубликованных историй (API для пользователей) ---
	publishedGroup := router.Group("/published-stories")
	publishedGroup.Use(authMiddleware)
	{
		publishedGroup.GET("/me", h.listMyPublishedStories)
		publishedGroup.GET("/public", h.listPublicPublishedStories)
		publishedGroup.GET("/:id", h.getPublishedStoryDetails)
		publishedGroup.GET("/:id/scene", h.getPublishedStoryScene)
		publishedGroup.POST("/:id/choice", h.makeChoice)
		publishedGroup.DELETE("/:id/progress", h.deletePlayerProgress)
		publishedGroup.POST("/:id/like", h.likeStory)
		publishedGroup.DELETE("/:id/like", h.unlikeStory)
		publishedGroup.GET("/me/likes", h.listLikedStories)
		publishedGroup.GET("/me/progress", h.listStoriesWithProgress)
		publishedGroup.PATCH("/:id/visibility", h.setStoryVisibility)
		publishedGroup.POST("/:id/retry", h.retryPublishedStoryGeneration)
		publishedGroup.DELETE("/:id", h.deletePublishedStory)
	}

	// --- Маршруты для внутреннего API (межсервисное взаимодействие) ---
	// Middleware для проверки межсервисного токена
	interServiceAuthMiddleware := sharedMiddleware.InterServiceAuthMiddlewareGin(h.interServiceTokenVerifier, h.logger)

	internalGroup := router.Group("/internal")
	internalGroup.Use(interServiceAuthMiddleware)
	{
		// Маршруты для чтения данных админкой (ПОЛЬЗОВАТЕЛЬСКИЕ ДАННЫЕ)
		internalGroup.GET("/users/:user_id/drafts", h.listUserDraftsInternal)   // GET /internal/users/{id}/drafts
		internalGroup.GET("/users/:user_id/stories", h.listUserStoriesInternal) // GET /internal/users/{id}/stories
		// Старые, неверные маршруты для деталей/сцен, закомментированы или удалены
		// internalGroup.GET("/users/:user_id/stories/:story_id", h.getPublishedStoryDetailsInternal) // УДАЛЕНО/ЗАМЕНЕНО
		// internalGroup.GET("/users/:user_id/stories/:story_id/scenes", h.listStoryScenesInternal) // УДАЛЕНО/ЗАМЕНЕНО

		// Маршруты для чтения данных админкой (ПО ID СУЩНОСТЕЙ) - Соответствуют клиенту
		internalGroup.GET("/drafts/:draft_id", h.getDraftDetailsInternal)               // GET /internal/drafts/{id} - НОВЫЙ
		internalGroup.GET("/stories/:story_id", h.getPublishedStoryDetailsInternal)     // GET /internal/stories/{id} - НОВЫЙ
		internalGroup.GET("/stories/:story_id/scenes", h.listStoryScenesInternal)       // GET /internal/stories/{id}/scenes - НОВЫЙ
		internalGroup.GET("/stories/:story_id/players", h.listStoryPlayersInternal)     // GET /internal/stories/{id}/players
		internalGroup.GET("/player-progress/:progress_id", h.getPlayerProgressInternal) // GET /internal/player-progress/{id}

		// Обновление данных
		internalGroup.POST("/drafts/:draft_id", h.updateDraftInternal)                     // POST /internal/drafts/{id}
		internalGroup.POST("/stories/:story_id", h.updateStoryInternal)                    // POST /internal/stories/{id}
		internalGroup.POST("/scenes/:scene_id", h.updateSceneInternal)                     // POST /internal/scenes/{id}
		internalGroup.PUT("/player-progress/:progress_id", h.updatePlayerProgressInternal) // PUT /internal/player-progress/{id} - РАСКОММЕНТИРОВАНО И ДОБАВЛЕН ОБРАБОТЧИК

		// Удаление данных
		internalGroup.DELETE("/scenes/:scene_id", h.deleteSceneInternal) // DELETE /internal/scenes/{id}
	}
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
		statusCode = http.StatusConflict
		apiErr = sharedModels.ErrorResponse{Code: sharedModels.ErrCodeStoryNotReadyYet, Message: err.Error()}
	case errors.Is(err, sharedModels.ErrSceneNeedsGeneration):
		statusCode = http.StatusConflict
		apiErr = sharedModels.ErrorResponse{Code: sharedModels.ErrCodeSceneNeedsGeneration, Message: err.Error()}
	case errors.Is(err, service.ErrInvalidChoiceIndex), errors.Is(err, service.ErrInvalidChoice), errors.Is(err, service.ErrStoryNotReady):
		statusCode = http.StatusBadRequest
		apiErr = sharedModels.ErrorResponse{Code: sharedModels.ErrCodeBadRequest, Message: err.Error()} // Можно уточнить код, если нужно
	case errors.Is(err, service.ErrStoryNotReadyForPublishing):
		statusCode = http.StatusConflict // 409 Conflict - состояние ресурса не позволяет выполнить операцию
		apiErr = sharedModels.ErrorResponse{Code: sharedModels.ErrCodeStoryNotReadyForPublishing, Message: err.Error()}
	case errors.Is(err, service.ErrAdultContentCannotBePublic):
		statusCode = http.StatusBadRequest // 400 Bad Request - сама операция невалидна для такого контента
		apiErr = sharedModels.ErrorResponse{Code: sharedModels.ErrCodeAdultContentCannotBePublic, Message: err.Error()}
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

// <<< ИЗМЕНЕНО: Обработчик getPublishedStoryDetails (ДЛЯ ПОЛЬЗОВАТЕЛЕЙ) >>>
func (h *GameplayHandler) getPublishedStoryDetails(c *gin.Context) {
	storyIDStr := c.Param("id")
	storyID, err := uuid.Parse(storyIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, APIError{Message: "Invalid story ID format"})
		return
	}

	// Получаем userID, он может быть Nil, если пользователь не аутентифицирован
	userID, _ := getUserIDFromContext(c) // Игнорируем ошибку, если user ID не найден

	h.logger.Info("getPublishedStoryDetails called", zap.String("storyID", storyIDStr), zap.String("userID", userID.String()))

	// Вызываем правильный метод сервиса для получения деталей
	storyDetailsDTO, err := h.service.GetPublishedStoryDetails(c.Request.Context(), storyID, userID) // Используем GetPublishedStoryDetails
	if err != nil {
		h.logger.Error("Error calling GetPublishedStoryDetails service", zap.Error(err)) // Обновляем сообщение об ошибке
		handleServiceError(c, err, h.logger)                                             // Используем стандартный обработчик ошибок
		return
	}

	// Отправляем детальный ответ DTO напрямую
	c.JSON(http.StatusOK, storyDetailsDTO)
}

// <<< ДОБАВЛЕНО: Обработчик для получения деталей прогресса игрока админкой >>>
func (h *GameplayHandler) getPlayerProgressInternal(c *gin.Context) {
	progressIDStr := c.Param("progress_id")
	progressID, err := uuid.Parse(progressIDStr)
	if err != nil {
		h.logger.Error("Invalid progress ID format", zap.String("progressID", progressIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, APIError{Message: "Invalid progress ID format"})
		return
	}

	h.logger.Info("getPlayerProgressInternal called", zap.String("progressID", progressIDStr))

	progress, err := h.service.GetPlayerProgressInternal(c.Request.Context(), progressID)
	if err != nil {
		// Логгирование уже происходит внутри handleServiceError или в сервисе
		handleServiceError(c, err, h.logger) // Используем общий обработчик ошибок
		return
	}

	c.JSON(http.StatusOK, progress) // Отправляем найденный прогресс
}

// <<< ДОБАВЛЕНО: Обработчик для получения деталей черновика админкой >>>
func (h *GameplayHandler) getDraftDetailsInternal(c *gin.Context) {
	draftIDStr := c.Param("draft_id")
	draftID, err := uuid.Parse(draftIDStr)
	if err != nil {
		h.logger.Error("Invalid draft ID format for internal request", zap.String("draftID", draftIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, APIError{Message: "Invalid draft ID format"})
		return
	}

	h.logger.Info("getDraftDetailsInternal called", zap.String("draftID", draftIDStr))

	draft, err := h.service.GetDraftDetailsInternal(c.Request.Context(), draftID)
	if err != nil {
		handleServiceError(c, err, h.logger)
		return
	}

	c.JSON(http.StatusOK, draft)
}

// <<< ДОБАВЛЕНО: Обработчик для получения деталей опубликованной истории админкой >>>
func (h *GameplayHandler) getPublishedStoryDetailsInternal(c *gin.Context) {
	storyIDStr := c.Param("story_id")
	storyID, err := uuid.Parse(storyIDStr)
	if err != nil {
		h.logger.Error("Invalid story ID format for internal request", zap.String("storyID", storyIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, APIError{Message: "Invalid story ID format"})
		return
	}

	h.logger.Info("getPublishedStoryDetailsInternal called", zap.String("storyID", storyIDStr))

	// Используем метод сервиса, который не требует userID
	story, err := h.service.GetPublishedStoryDetailsInternal(c.Request.Context(), storyID)
	if err != nil {
		handleServiceError(c, err, h.logger)
		return
	}

	c.JSON(http.StatusOK, story)
}

// <<< ДОБАВЛЕНО: Обработчик для получения списка сцен истории админкой >>>
func (h *GameplayHandler) listStoryScenesInternal(c *gin.Context) {
	storyIDStr := c.Param("story_id")
	storyID, err := uuid.Parse(storyIDStr)
	if err != nil {
		h.logger.Error("Invalid story ID format for internal request", zap.String("storyID", storyIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, APIError{Message: "Invalid story ID format"})
		return
	}

	h.logger.Info("listStoryScenesInternal called", zap.String("storyID", storyIDStr))

	scenes, err := h.service.ListStoryScenesInternal(c.Request.Context(), storyID)
	if err != nil {
		handleServiceError(c, err, h.logger)
		return
	}

	// Убедимся, что возвращается пустой срез, а не nil, если сцен нет
	if scenes == nil {
		scenes = make([]sharedModels.StoryScene, 0)
	}

	c.JSON(http.StatusOK, scenes)
}

// <<< ДОБАВЛЕНО: Обработчик для обновления прогресса игрока админкой >>>
func (h *GameplayHandler) updatePlayerProgressInternal(c *gin.Context) {
	progressIDStr := c.Param("progress_id")
	progressID, err := uuid.Parse(progressIDStr)
	if err != nil {
		h.logger.Error("Invalid progress ID format for update", zap.String("progressID", progressIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, APIError{Message: "Invalid progress ID format"})
		return
	}

	var progressData map[string]interface{}
	if err := c.ShouldBindJSON(&progressData); err != nil {
		h.logger.Error("Failed to bind player progress update JSON", zap.String("progressID", progressIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, APIError{Message: "Invalid request body format"})
		return
	}

	h.logger.Info("updatePlayerProgressInternal called", zap.String("progressID", progressIDStr))

	err = h.service.UpdatePlayerProgressInternal(c.Request.Context(), progressID, progressData)
	if err != nil {
		handleServiceError(c, err, h.logger)
		return
	}

	c.Status(http.StatusNoContent) // Или http.StatusOK, если сервис что-то возвращает
}

// --- Реализации существующих обработчиков (listUserDraftsInternal, listUserStoriesInternal, etc.) ---
// Убедитесь, что эти обработчики существуют и правильно вызывают методы сервиса.
// Если они были частью удаленных/измененных маршрутов, их нужно проверить/адаптировать.

// Пример (если нужно будет добавить/адаптировать):
// func (h *GameplayHandler) listUserDraftsInternal(c *gin.Context) { ... }
// func (h *GameplayHandler) listUserStoriesInternal(c *gin.Context) { ... }
// func (h *GameplayHandler) listStoryPlayersInternal(c *gin.Context) { ... }
// func (h *GameplayHandler) updateDraftInternal(c *gin.Context) { ... }
// func (h *GameplayHandler) updateStoryInternal(c *gin.Context) { ... }
// func (h *GameplayHandler) updateSceneInternal(c *gin.Context) { ... }
// func (h *GameplayHandler) deleteSceneInternal(c *gin.Context) { ... }

// Оставляем существующие реализации для этих методов, если они корректны.
