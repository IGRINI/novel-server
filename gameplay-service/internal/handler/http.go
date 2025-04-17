package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http" // Импорт для StoryConfig

	// <<< Добавляем импорт репозитория
	"novel-server/gameplay-service/internal/service"
	"novel-server/shared/authutils"                   // <<< Добавляем импорт общего верификатора
	sharedInterfaces "novel-server/shared/interfaces" // <<< Добавляем импорт для ErrInvalidCursor
	sharedMiddleware "novel-server/shared/middleware"
	sharedModels "novel-server/shared/models"
	"strconv" // Для парсинга limit
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
	interServiceAuthMiddleware := sharedMiddleware.InterServiceAuthMiddleware(h.interServiceTokenVerifier, h.logger)

	// --- Маршруты для черновиков историй (API для пользователей) ---
	storiesGroup := e.Group("/stories", authMiddleware)
	{
		storiesGroup.POST("/generate", h.generateInitialStory)
		storiesGroup.GET("", h.listStoryConfigs)
		storiesGroup.GET("/:id", h.getStoryConfig)
		storiesGroup.POST("/:id/revise", h.reviseStoryConfig)
		storiesGroup.POST("/:id/publish", h.publishStoryDraft)
	}

	// --- Маршруты для опубликованных историй (API для пользователей) ---
	publishedGroup := e.Group("/published-stories", authMiddleware)
	{
		publishedGroup.GET("/me", h.listMyPublishedStories)
		publishedGroup.GET("/public", h.listPublicPublishedStories)
		publishedGroup.GET("/:id/scene", h.getPublishedStoryScene)
		publishedGroup.POST("/:id/choice", h.makeChoice)
		publishedGroup.DELETE("/:id/progress", h.deletePlayerProgress)
	}

	// <<< Новая группа для внутренних маршрутов >>>
	internalGroup := e.Group("/internal", interServiceAuthMiddleware) // <<< Защищаем межсервисным токеном
	{
		internalGroup.GET("/users/:user_id/drafts", h.listUserDraftsInternal)   // <<< Новый роут
		internalGroup.GET("/users/:user_id/stories", h.listUserStoriesInternal) // <<< Новый роут
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
	Data       interface{} `json:"data"` // Срез с данными (истории, черновики)
	NextCursor string      `json:"next_cursor,omitempty"`
}

// --- НОВЫЕ ОБРАБОТЧИКИ (ЗАГЛУШКИ) ---

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
		zap.Uint64("userID", userID),
		zap.Int("limit", limit),
		zap.String("cursor", cursor),
	)

	// Вызываем метод сервиса
	drafts, nextCursor, err := h.service.ListMyDrafts(c.Request().Context(), userID, cursor, limit) // <<< Меняем порядок cursor и limit
	if err != nil {
		// Логируем только если это не стандартная ошибка курсора
		if !errors.Is(err, sharedInterfaces.ErrInvalidCursor) { // <<< Используем sharedInterfaces
			h.logger.Error("Error listing story drafts", zap.Uint64("userID", userID), zap.Error(err))
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
		zap.Uint64("userID", userID),
		zap.Int("count", len(draftSummaries)), // <<< Используем длину среза DTO
		zap.Bool("hasNext", nextCursor != ""),
	)

	return c.JSON(http.StatusOK, resp)
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
		zap.Uint64("userID", userID),
		zap.Int("limit", limit),
		zap.Int("offset", offset),
	)

	stories, err := h.service.ListMyPublishedStories(c.Request().Context(), userID, limit, offset)
	if err != nil {
		h.logger.Error("Error listing my published stories", zap.Uint64("userID", userID), zap.Error(err))
		// Используем общий обработчик, который может вернуть 500 или другие ошибки
		return handleServiceError(c, err)
	}

	// Используем PaginatedResponse без курсора, т.к. сервис использует offset
	resp := PaginatedResponse{
		Data: stories,
		// NextCursor здесь не используется, так как пагинация через offset
	}

	h.logger.Debug("Successfully fetched my published stories",
		zap.Uint64("userID", userID),
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
		zap.Uint64("userID", userID),
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
			h.logger.Error("Error getting story scene (unhandled)", zap.Uint64("userID", userID), zap.String("publishedStoryID", publishedStoryID.String()), zap.Error(err))
		}
		return handleServiceError(c, err) // Используем общий обработчик
	}

	h.logger.Debug("Successfully fetched story scene",
		zap.Uint64("userID", userID),
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
		h.logger.Warn("Invalid request body for makeChoice", zap.Uint64("userID", userID), zap.String("publishedStoryID", publishedStoryID.String()), zap.Error(err))
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid request body: " + err.Error()})
	}

	// Валидация индекса (хотя может быть и в Bind, но для надежности)
	if req.SelectedOptionIndex < 0 || req.SelectedOptionIndex > 1 {
		h.logger.Warn("Invalid selected option index", zap.Int("index", req.SelectedOptionIndex))
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid 'selected_option_index', must be 0 or 1"})
	}

	h.logger.Info("Player making choice", // Используем Info, т.к. это важное действие
		zap.Uint64("userID", userID),
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
			h.logger.Error("Error making choice (unhandled)", zap.Uint64("userID", userID), zap.String("publishedStoryID", publishedStoryID.String()), zap.Int("index", req.SelectedOptionIndex), zap.Error(err))
		}
		return handleServiceError(c, err) // Используем общий обработчик
	}

	h.logger.Info("Player choice processed successfully",
		zap.Uint64("userID", userID),
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
		zap.Uint64("userID", userID),
		zap.String("publishedStoryID", publishedStoryID.String()),
	)

	err = h.service.DeletePlayerProgress(c.Request().Context(), userID, publishedStoryID)
	if err != nil {
		// Логируем только если это не ErrNotFound (ожидаемая ошибка, если прогресса нет)
		if !errors.Is(err, service.ErrPlayerProgressNotFound) && !errors.Is(err, sharedModels.ErrNotFound) {
			h.logger.Error("Error deleting player progress", zap.Uint64("userID", userID), zap.String("publishedStoryID", publishedStoryID.String()), zap.Error(err))
		}
		// Можно вернуть 204 даже при ErrNotFound, т.к. итоговое состояние - прогресса нет.
		// Но если хотим четко сигнализировать, что прогресса и не было, используем handleServiceError
		if errors.Is(err, service.ErrPlayerProgressNotFound) || errors.Is(err, sharedModels.ErrNotFound) {
			// Если прогресса не найдено, можно просто вернуть 204, так как цель достигнута
			h.logger.Info("Player progress not found, deletion skipped (considered success)",
				zap.Uint64("userID", userID),
				zap.String("publishedStoryID", publishedStoryID.String()),
			)
			return c.NoContent(http.StatusNoContent)
		}
		// Для других ошибок используем общий обработчик
		return handleServiceError(c, err)
	}

	h.logger.Info("Player progress deleted successfully",
		zap.Uint64("userID", userID),
		zap.String("publishedStoryID", publishedStoryID.String()),
	)

	return c.NoContent(http.StatusNoContent)
}

// --- Новые обработчики для внутренних API --- //

// listUserDraftsInternal возвращает список черновиков для указанного пользователя (для админки).
func (h *GameplayHandler) listUserDraftsInternal(c echo.Context) error {
	log := h.logger.With(zap.String("handler", "listUserDraftsInternal"))

	userIDStr := c.Param("user_id")
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		log.Warn("Invalid user ID format", zap.String("user_id", userIDStr), zap.Error(err))
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid user ID format"})
	}

	limitStr := c.QueryParam("limit")
	cursor := c.QueryParam("cursor")
	limit := 20 // Значение по умолчанию
	if limitStr != "" {
		if l, parseErr := strconv.Atoi(limitStr); parseErr == nil && l > 0 {
			limit = l
		} else {
			log.Warn("Invalid limit parameter received, using default", zap.String("limit", limitStr), zap.Error(parseErr))
		}
	}

	log = log.With(zap.Uint64("userID", userID), zap.Int("limit", limit), zap.String("cursor", cursor))
	log.Info("Internal request for user drafts")

	drafts, nextCursor, err := h.service.ListUserDrafts(c.Request().Context(), userID, cursor, limit)
	if err != nil {
		if errors.Is(err, sharedInterfaces.ErrInvalidCursor) {
			log.Warn("Invalid cursor provided", zap.Error(err))
			return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid cursor format"})
		}
		log.Error("Failed to list user drafts internally", zap.Error(err))
		// Не используем handleServiceError, так как это внутренний API, возвращаем 500
		return c.JSON(http.StatusInternalServerError, APIError{Message: "Failed to retrieve drafts"})
	}

	// Используем ту же DTO PaginatedResponse, что и для публичного API
	response := PaginatedResponse{
		Data:       drafts, // Сервис уже возвращает []*StoryConfigSummary
		NextCursor: nextCursor,
	}
	return c.JSON(http.StatusOK, response)
}

// listUserStoriesInternal возвращает список опубликованных историй пользователя (для админки).
func (h *GameplayHandler) listUserStoriesInternal(c echo.Context) error {
	log := h.logger.With(zap.String("handler", "listUserStoriesInternal"))

	userIDStr := c.Param("user_id")
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		log.Warn("Invalid user ID format", zap.String("user_id", userIDStr), zap.Error(err))
		return c.JSON(http.StatusBadRequest, APIError{Message: "Invalid user ID format"})
	}

	limitStr := c.QueryParam("limit")
	offsetStr := c.QueryParam("offset")

	limit := 20 // Значение по умолчанию
	if limitStr != "" {
		if l, parseErr := strconv.Atoi(limitStr); parseErr == nil && l > 0 {
			limit = l
		} else {
			log.Warn("Invalid limit parameter received, using default", zap.String("limit", limitStr), zap.Error(parseErr))
		}
	}
	offset := 0
	if offsetStr != "" {
		if o, parseErr := strconv.Atoi(offsetStr); parseErr == nil && o >= 0 {
			offset = o
		} else {
			log.Warn("Invalid offset parameter received, using default", zap.String("offset", offsetStr), zap.Error(parseErr))
		}
	}

	log = log.With(zap.Uint64("userID", userID), zap.Int("limit", limit), zap.Int("offset", offset))
	log.Info("Internal request for user published stories")

	stories, err := h.service.ListUserPublishedStories(c.Request().Context(), userID, limit, offset)
	if err != nil {
		log.Error("Failed to list user published stories internally", zap.Error(err))
		return c.JSON(http.StatusInternalServerError, APIError{Message: "Failed to retrieve published stories"})
	}

	// Для offset пагинации нам нужно определить 'hasMore'
	hasMore := len(stories) == limit

	// Отдаем только данные, без курсора. Клиент должен сам определить пагинацию.
	// Можно было бы создать отдельную DTO для этого ответа.
	type InternalListResponse struct {
		Data    interface{} `json:"data"`
		HasMore bool        `json:"has_more"`
	}

	response := InternalListResponse{
		Data:    stories, // Сервис возвращает []*PublishedStory
		HasMore: hasMore,
	}

	return c.JSON(http.StatusOK, response)
}
