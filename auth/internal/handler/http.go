package handler

import (
	"errors"
	"fmt"
	"net/http"
	"novel-server/auth/internal/domain" // Нужен для Claims при парсинге
	"novel-server/auth/internal/service"
	"novel-server/shared/interfaces" // Импортируем интерфейс репозитория
	"novel-server/shared/models"
	"regexp"  // <<< Добавляем для валидации username
	"strconv" // Добавляем для парсинга ID
	"strings"
	"unicode" // <<< Добавляем для валидации пароля

	"novel-server/auth/internal/config" // <<< Добавляем импорт конфига

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5" // Нужен для парсинга refresh token
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
)

// <<< Определение и регистрация кастомных метрик >>>
var (
	registrationsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "auth_registrations_total",
		Help: "Total number of successful user registrations.",
	})

	// Счетчик успешных обновлений токенов
	refreshesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "auth_refreshes_total",
		Help: "Total number of successful token refreshes.",
	})

	// Счетчик сгенерированных межсервисных токенов
	interServiceTokensGeneratedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "auth_inter_service_tokens_generated_total",
		Help: "Total number of generated inter-service tokens.",
	})

	// Счетчик проверок токенов с метками типа и статуса
	tokenVerificationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_token_verifications_total",
			Help: "Total number of token verification attempts by type and status.",
		},
		[]string{"type", "status"}, // Метки: тип токена и статус проверки
	)
)

// <<< Конец определения >>>

// --- Коды ошибок API ---
const (
	// 4xx Клиентские ошибки
	ErrCodeBadRequest         = 40001
	ErrCodeInvalidCredentials = 40101
	ErrCodeInvalidToken       = 40102
	ErrCodeExpiredToken       = 40103
	ErrCodeRevokedToken       = 40104 // Токен не найден в хранилище (отозван/вышли)
	ErrCodeForbidden          = 40301 // Доступ запрещен (если понадобится)
	ErrCodeNotFound           = 40401 // Общая "не найдено"
	ErrCodeUserNotFound       = 40402
	ErrCodeUserAlreadyExists  = 40901 // Конфликт

	// 5xx Серверные ошибки
	ErrCodeInternalError = 50001
)

// --- Константы для валидации ---
const (
	minUsernameLength = 3
	maxUsernameLength = 30
	minPasswordLength = 8
	maxPasswordLength = 100
)

// Регулярное выражение для проверки допустимых символов в имени пользователя
// (латинские буквы, цифры, подчеркивание, дефис)
var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// AuthHandler handles HTTP requests related to authentication.
type AuthHandler struct {
	authService service.AuthService
	userRepo    interfaces.UserRepository // Добавляем репозиторий
	cfg         *config.Config            // <<< Добавляем поле для конфига
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(authService service.AuthService, userRepo interfaces.UserRepository, cfg *config.Config) *AuthHandler { // <<< Добавляем cfg
	return &AuthHandler{
		authService: authService,
		userRepo:    userRepo, // Инициализируем поле
		cfg:         cfg,      // <<< Сохраняем конфиг
	}
}

// RegisterRoutes registers the authentication routes.
func (h *AuthHandler) RegisterRoutes(router *gin.Engine) {
	authGroup := router.Group("/auth")
	{
		authGroup.POST("/register", h.register)
		authGroup.POST("/login", h.login)
		authGroup.POST("/logout", h.logout) // Requires auth middleware via /api group
		authGroup.POST("/refresh", h.refresh)
		authGroup.POST("/token/verify", h.verify) // Endpoint for other services to verify token
	}

	// Защищенная группа /api
	protected := router.Group("/api")
	protected.Use(h.AuthMiddleware()) // Применяем middleware аутентификации
	{
		protected.GET("/me", h.getMe) // Эндпоинт для получения данных текущего пользователя
		// Сюда можно добавить другие защищенные маршруты
	}

	// Группа для межсервисного взаимодействия
	interServiceGroup := router.Group("/internal/auth")
	interServiceGroup.Use(h.InternalAuthMiddleware()) // Добавляем middleware
	{
		// Потенциально нужна своя защита для генерации
		interServiceGroup.POST("/token/generate", h.generateInterServiceToken)
		// Верификация должна быть доступна для внутренних сервисов
		interServiceGroup.POST("/token/verify", h.verifyInterServiceToken)

		// Новые маршруты для админки
		interServiceGroup.GET("/users/count", h.getUserCount)
		interServiceGroup.GET("/users", h.listUsers)
		interServiceGroup.POST("/users/:user_id/ban", h.banUser)
		interServiceGroup.DELETE("/users/:user_id/ban", h.unbanUser)
		interServiceGroup.POST("/token/validate", h.validateToken)
		// <<< Маршрут для получения деталей пользователя >>>
		interServiceGroup.GET("/users/:user_id", h.getUserDetails)
		// <<< Маршрут для обновления пользователя >>>
		interServiceGroup.PUT("/users/:user_id", h.updateUser)
		// <<< Маршрут для обновления пароля >>>
		interServiceGroup.PUT("/users/:user_id/password", h.updatePassword)
		// <<< Маршрут для обновления токена администратора >>>
		interServiceGroup.POST("/token/refresh/admin", h.refreshAdminToken)
	}
}

// --- Request/Response Structs ---

type registerRequest struct {
	Username string `json:"username" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// Добавляем структуру для logout
type logoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type tokenVerifyRequest struct {
	Token string `json:"token" binding:"required"`
}

type generateInterServiceTokenRequest struct {
	ServiceName string `json:"service_name" binding:"required"`
}

// DTO для ответа /api/me и /internal/auth/users
type meResponse struct {
	ID       uint64   `json:"id"`
	Username string   `json:"username"`
	Email    string   `json:"email"`
	Roles    []string `json:"roles,omitempty"`
	IsBanned bool     `json:"isBanned"`
}

// --- Структура для запроса на обновление ---
type updateUserRequest struct {
	// Используем указатели, чтобы различать неуказанные поля и поля с нулевыми значениями
	Email    *string  `json:"email,omitempty"`
	Roles    []string `json:"roles,omitempty"` // Если roles не передано, оно будет nil
	IsBanned *bool    `json:"is_banned,omitempty"`
}

// --- Структура для запроса на обновление пароля ---
type updatePasswordRequest struct {
	NewPassword string `json:"new_password" binding:"required"`
}

// --- Error Handling Helper ---

// ErrorResponse represents a standardized error response format.
type ErrorResponse struct {
	Code    int    `json:"code"`    // Application-specific error code (numeric)
	Message string `json:"message"` // User-friendly error message
}

// handleServiceError maps service errors to HTTP responses.
func handleServiceError(c *gin.Context, err error) {
	var statusCode int
	var errResp ErrorResponse

	switch {
	// --- User/Auth Specific Errors ---
	case errors.Is(err, models.ErrInvalidCredentials):
		statusCode = http.StatusUnauthorized
		errResp = ErrorResponse{Code: ErrCodeInvalidCredentials, Message: "Invalid credentials or input format"}
	case errors.Is(err, models.ErrUserAlreadyExists):
		statusCode = http.StatusConflict
		errResp = ErrorResponse{Code: ErrCodeUserAlreadyExists, Message: "Username is already taken"}
	case errors.Is(err, models.ErrEmailAlreadyExists):
		statusCode = http.StatusConflict
		errResp = ErrorResponse{Code: ErrCodeUserAlreadyExists, Message: "Email is already taken"}
	case errors.Is(err, models.ErrUserNotFound):
		statusCode = http.StatusNotFound
		errResp = ErrorResponse{Code: ErrCodeUserNotFound, Message: "User not found"}

	// --- Token Specific Errors ---
	case errors.Is(err, models.ErrTokenInvalid), errors.Is(err, models.ErrTokenMalformed):
		statusCode = http.StatusUnauthorized
		errResp = ErrorResponse{Code: ErrCodeInvalidToken, Message: "Provided token is invalid or malformed"}
	case errors.Is(err, models.ErrTokenExpired):
		statusCode = http.StatusUnauthorized // 401 Unauthorized - токен протух
		errResp = ErrorResponse{Code: ErrCodeExpiredToken, Message: "Provided token has expired"}
	case errors.Is(err, models.ErrTokenNotFound): // Ошибка хранилища
		statusCode = http.StatusUnauthorized
		errResp = ErrorResponse{Code: ErrCodeRevokedToken, Message: "Provided token is invalid (possibly revoked or expired)"}

	// --- Generic/Internal Errors ---
	default:
		zap.L().Error("Unhandled internal error", zap.Error(err))
		statusCode = http.StatusInternalServerError
		errResp = ErrorResponse{Code: ErrCodeInternalError, Message: "An unexpected internal error occurred"}
	}

	c.AbortWithStatusJSON(statusCode, errResp)
}

// --- Обновленные Handler Methods ---

func (h *AuthHandler) register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errResp := ErrorResponse{Code: ErrCodeBadRequest, Message: "Invalid request data: " + err.Error()}
		c.AbortWithStatusJSON(http.StatusBadRequest, errResp)
		return
	}

	// --- ДОБАВЛЯЕМ ВАЛИДАЦИЮ ---
	// Валидация имени пользователя
	if len(req.Username) < minUsernameLength || len(req.Username) > maxUsernameLength {
		errResp := ErrorResponse{Code: ErrCodeBadRequest, Message: fmt.Sprintf("Username length must be between %d and %d characters", minUsernameLength, maxUsernameLength)}
		c.AbortWithStatusJSON(http.StatusBadRequest, errResp)
		return
	}
	if !usernameRegex.MatchString(req.Username) {
		errResp := ErrorResponse{Code: ErrCodeBadRequest, Message: "Username can only contain letters, numbers, underscores, and hyphens"}
		c.AbortWithStatusJSON(http.StatusBadRequest, errResp)
		return
	}

	// Валидация пароля
	if len(req.Password) < minPasswordLength || len(req.Password) > maxPasswordLength {
		errResp := ErrorResponse{Code: ErrCodeBadRequest, Message: fmt.Sprintf("Password length must be between %d and %d characters", minPasswordLength, maxPasswordLength)}
		c.AbortWithStatusJSON(http.StatusBadRequest, errResp)
		return
	}
	var ( // Проверяем сложность пароля
		hasLetter bool
		hasDigit  bool
	)
	for _, char := range req.Password {
		if unicode.IsLetter(char) {
			hasLetter = true
		}
		if unicode.IsDigit(char) {
			hasDigit = true
		}
		if hasLetter && hasDigit {
			break // Достаточно, выходим из цикла
		}
	}
	if !hasLetter || !hasDigit {
		errResp := ErrorResponse{Code: ErrCodeBadRequest, Message: "Password must contain at least one letter and one digit"}
		c.AbortWithStatusJSON(http.StatusBadRequest, errResp)
		return
	}
	// --- КОНЕЦ ВАЛИДАЦИИ ---

	// Если валидация прошла, вызываем сервис
	user, err := h.authService.Register(c.Request.Context(), req.Username, req.Email, req.Password)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	registrationsTotal.Inc()

	c.JSON(http.StatusCreated, gin.H{"message": "user registered successfully", "user_id": user.ID, "username": user.Username, "email": user.Email})
}

func (h *AuthHandler) login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, ErrorResponse{Code: ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}

	tokens, err := h.authService.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	c.JSON(http.StatusOK, tokens)
}

func (h *AuthHandler) logout(c *gin.Context) {
	// 1. Получаем accessUUID из контекста (установлен middleware)
	accessUUID := c.GetString("access_uuid")
	if accessUUID == "" {
		// Это не должно произойти, если middleware отработала правильно
		zap.L().Error("Access UUID missing in context during logout")
		handleServiceError(c, errors.New("internal server error: context missing access uuid"))
		return
	}

	// 2. Получаем refresh_token из тела запроса
	var req logoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, ErrorResponse{Code: ErrCodeBadRequest, Message: "Missing or invalid refresh_token in request body: " + err.Error()})
		return
	}

	// 3. Парсим Refresh Token, чтобы извлечь refreshUUID ('jti' клейм)
	// Нам не нужно полностью валидировать подпись/время жизни здесь,
	// т.к. сервис сам проверит его наличие в Redis.
	// Нам нужен только 'jti'.
	token, _, err := new(jwt.Parser).ParseUnverified(req.RefreshToken, &domain.Claims{})
	if err != nil {
		// Не смогли распарсить токен вообще
		handleServiceError(c, models.ErrTokenMalformed)
		return
	}

	claims, ok := token.Claims.(*domain.Claims)
	if !ok {
		// Структура клеймов не та, что ожидалась
		zap.L().Error("Invalid claims type in refresh token during logout")
		handleServiceError(c, models.ErrTokenMalformed)
		return
	}
	refreshUUID := claims.ID // Получаем 'jti'
	if refreshUUID == "" {
		// В токене нет 'jti'
		zap.L().Error("Refresh UUID ('jti' claim) missing in refresh token during logout")
		handleServiceError(c, models.ErrTokenMalformed)
		return
	}

	// 4. Вызываем сервис с обоими UUID
	err = h.authService.Logout(c.Request.Context(), accessUUID, refreshUUID)
	if err != nil {
		zap.L().Error("Failed to perform logout in service",
			zap.Error(err),
			zap.String("accessUUID", accessUUID),
			zap.String("refreshUUID", refreshUUID),
		)
		// Определим гипотетический тип ошибки для внутренних проблем сервиса
		type internalError interface {
			IsInternal() bool // Предполагаем, что внутренние ошибки реализуют этот метод
		}
		if ierr, ok := err.(internalError); ok && ierr.IsInternal() {
			handleServiceError(c, err) // Обрабатываем только реальные внутренние ошибки сервиса/репозитория
			return
		}
		// В остальных случаях (например, токены уже удалены) считаем logout успешным для клиента
	}

	// Успешный logout
	c.JSON(http.StatusOK, gin.H{"message": "Successfully logged out"})
}

func (h *AuthHandler) refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, ErrorResponse{Code: ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}

	tokens, err := h.authService.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		// Можно добавить проверку типа ошибки, чтобы инкрементировать только при ошибках токена
		if errors.Is(err, models.ErrTokenNotFound) || errors.Is(err, models.ErrTokenExpired) || errors.Is(err, models.ErrTokenInvalid) || errors.Is(err, models.ErrTokenMalformed) {
			tokenVerificationsTotal.WithLabelValues("refresh", "failure").Inc()
		}
		handleServiceError(c, err)
		return
	}

	// <<< Инкремент счетчика успешных обновлений >>>
	refreshesTotal.Inc()
	tokenVerificationsTotal.WithLabelValues("refresh", "success").Inc()
	// <<< Конец инкремента >>>

	c.JSON(http.StatusOK, tokens)
}

// verify - эндпоинт для других сервисов
func (h *AuthHandler) verify(c *gin.Context) {
	var req tokenVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, ErrorResponse{Code: ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}

	claims, err := h.authService.VerifyAccessToken(c.Request.Context(), req.Token)
	if err != nil {
		// <<< Инкремент счетчика ошибок верификации >>>
		tokenVerificationsTotal.WithLabelValues("access", "failure").Inc()
		handleServiceError(c, err)
		return
	}

	// <<< Инкремент счетчика успешной верификации >>>
	tokenVerificationsTotal.WithLabelValues("access", "success").Inc()

	c.JSON(http.StatusOK, gin.H{"user_id": claims.UserID, "valid": true})
}

func (h *AuthHandler) generateInterServiceToken(c *gin.Context) {
	var req generateInterServiceTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, ErrorResponse{Code: ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}

	token, err := h.authService.GenerateInterServiceToken(c.Request.Context(), req.ServiceName)
	if err != nil {
		// <<< Инкремент счетчика генерации межсервисных токенов >>>
		interServiceTokensGeneratedTotal.Inc()
		// <<< Конец инкремента >>>
		handleServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"inter_service_token": token})
}

func (h *AuthHandler) verifyInterServiceToken(c *gin.Context) {
	var req tokenVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, ErrorResponse{Code: ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}

	serviceName, err := h.authService.VerifyInterServiceToken(c.Request.Context(), req.Token)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"service_name": serviceName, "valid": true})
}

// getMe обрабатывает запрос GET /api/me
func (h *AuthHandler) getMe(c *gin.Context) {
	userIDStr := c.GetString("user_id") // Middleware должен установить это
	if userIDStr == "" {
		zap.L().Error("User ID missing in context for /me endpoint")
		// Используем код внутренней ошибки
		c.AbortWithStatusJSON(http.StatusInternalServerError, ErrorResponse{
			Code:    ErrCodeInternalError,
			Message: "Internal server error: User context missing",
		})
		return
	}

	// Конвертируем ID в uint64
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		zap.L().Error("Invalid User ID format in context for /me endpoint", zap.String("userIDStr", userIDStr), zap.Error(err))
		// Используем код внутренней ошибки
		c.AbortWithStatusJSON(http.StatusInternalServerError, ErrorResponse{
			Code:    ErrCodeInternalError,
			Message: "Internal server error: Invalid user ID format",
		})
		return
	}

	zap.L().Debug("Handling /me request", zap.Uint64("userID", userID))

	// Получаем пользователя из репозитория
	user, err := h.userRepo.GetUserByID(c.Request.Context(), userID) // Используем инжектированный репозиторий
	if err != nil {
		if errors.Is(err, models.ErrUserNotFound) {
			zap.L().Warn("User not found for ID from token during /me request", zap.Uint64("userID", userID))
			// Возвращаем 404, т.к. пользователь, на которого ссылается токен, не найден в БД
			c.AbortWithStatusJSON(http.StatusNotFound, ErrorResponse{
				Code:    ErrCodeUserNotFound,
				Message: "User associated with token not found",
			})
			return
		}
		// Другая ошибка репозитория (уже залогирована репо?)
		zap.L().Error("Error fetching user details for /me from repository", zap.Uint64("userID", userID), zap.Error(err))
		// Передаем оригинальную ошибку в обработчик
		handleServiceError(c, err) // handleServiceError по умолчанию вернет 500
		return
	}

	zap.L().Info("User details retrieved successfully for /me", zap.Uint64("userID", user.ID), zap.String("username", user.Username))

	// Формируем ответ DTO
	resp := meResponse{
		ID:       user.ID,
		Username: user.Username,
		Email:    user.Email,
		Roles:    user.Roles,
		IsBanned: user.IsBanned,
	}

	// Отправляем успешный ответ
	c.JSON(http.StatusOK, resp)
}

// AuthMiddleware - middleware для проверки JWT access токена
func (h *AuthHandler) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			zap.L().Warn("Authorization header missing")
			// <<< Инкремент счетчика ошибок верификации >>>
			tokenVerificationsTotal.WithLabelValues("access", "failure").Inc()
			handleServiceError(c, models.ErrTokenInvalid)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			zap.L().Warn("Invalid Authorization header format", zap.String("header", authHeader))
			// <<< Инкремент счетчика ошибок верификации >>>
			tokenVerificationsTotal.WithLabelValues("access", "failure").Inc()
			handleServiceError(c, models.ErrTokenInvalid)
			return
		}

		tokenString := parts[1]
		claims, err := h.authService.VerifyAccessToken(c.Request.Context(), tokenString)
		if err != nil {
			zap.L().Warn("Access token verification failed", zap.Error(err))
			// <<< Инкремент счетчика ошибок верификации >>>
			tokenVerificationsTotal.WithLabelValues("access", "failure").Inc()
			handleServiceError(c, err) // Передаем ошибку из VerifyAccessToken
			return
		}

		// <<< Инкремент счетчика успешной верификации >>>
		tokenVerificationsTotal.WithLabelValues("access", "success").Inc()

		// Сохраняем ID пользователя и UUID токена в контексте для дальнейшего использования
		c.Set("user_id", strconv.FormatUint(claims.UserID, 10))
		c.Set("access_uuid", claims.ID) // 'jti' claim
		// c.Set("token_type", "access") // Можно добавить тип токена

		zap.L().Debug("Access token verified successfully", zap.Uint64("userID", claims.UserID), zap.String("accessUUID", claims.ID))

		c.Next() // Передаем управление следующему обработчику
	}
}

// --- Новые обработчики для внутреннего API ---

// getUserCount возвращает общее количество пользователей
func (h *AuthHandler) getUserCount(c *gin.Context) {
	// Здесь не нужна информация о текущем пользователе,
	// middleware InternalAuthMiddleware уже проверил доступ.

	count, err := h.userRepo.GetUserCount(c.Request.Context()) // Предполагаем, что такой метод есть
	if err != nil {
		zap.L().Error("Failed to get user count from repository", zap.Error(err))
		handleServiceError(c, err) // Возвращаем внутреннюю ошибку
		return
	}

	c.JSON(http.StatusOK, gin.H{"count": count})
}

// listUsers возвращает список пользователей
func (h *AuthHandler) listUsers(c *gin.Context) {
	// TODO: Добавить пагинацию (offset, limit)
	users, err := h.userRepo.ListUsers(c.Request.Context())
	if err != nil {
		zap.L().Error("Failed to list users from repository", zap.Error(err))
		handleServiceError(c, err)
		return
	}

	// Преобразуем в DTO, чтобы не светить хэши паролей и т.д.
	userDTOs := make([]meResponse, 0, len(users))
	for _, u := range users {
		userDTOs = append(userDTOs, meResponse{
			ID:       u.ID,
			Username: u.Username,
			Email:    u.Email,
			Roles:    u.Roles,
			IsBanned: u.IsBanned,
		})
	}

	c.JSON(http.StatusOK, userDTOs)
}

// banUser handles the request to ban a user.
func (h *AuthHandler) banUser(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		zap.L().Warn("Invalid user ID format for ban request", zap.String("userID", userIDStr), zap.Error(err))
		c.AbortWithStatusJSON(http.StatusBadRequest, ErrorResponse{Code: ErrCodeBadRequest, Message: "Invalid user ID format"})
		return
	}

	err = h.authService.BanUser(c.Request.Context(), userID)
	if err != nil {
		// Обрабатываем специфичные ошибки, например, UserNotFound
		handleServiceError(c, err)
		return
	}

	c.Status(http.StatusNoContent) // 204 No Content при успехе
}

// unbanUser handles the request to unban a user.
func (h *AuthHandler) unbanUser(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		zap.L().Warn("Invalid user ID format for unban request", zap.String("userID", userIDStr), zap.Error(err))
		c.AbortWithStatusJSON(http.StatusBadRequest, ErrorResponse{Code: ErrCodeBadRequest, Message: "Invalid user ID format"})
		return
	}

	err = h.authService.UnbanUser(c.Request.Context(), userID)
	if err != nil {
		// Обрабатываем специфичные ошибки, например, UserNotFound
		handleServiceError(c, err)
		return
	}

	c.Status(http.StatusNoContent) // 204 No Content при успехе
}

// validateToken handles the request to validate a token and user status.
type validateTokenRequest struct {
	Token string `json:"token" binding:"required"`
}

func (h *AuthHandler) validateToken(c *gin.Context) {
	var req validateTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, ErrorResponse{Code: ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}

	claims, err := h.authService.ValidateAndGetClaims(c.Request.Context(), req.Token)
	if err != nil {
		// Возвращаем ошибку, которую вернул сервис (401 с разными кодами)
		handleServiceError(c, err)
		return
	}

	// Возвращаем claims при успехе (ID, роли и т.д.)
	c.JSON(http.StatusOK, claims)
}

// getUserDetails возвращает детальную информацию о пользователе по ID.
func (h *AuthHandler) getUserDetails(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, ErrorResponse{Code: ErrCodeBadRequest, Message: "Invalid user ID format"})
		return
	}

	// Получаем пользователя из репозитория
	// Метод GetUserByID уже существует и возвращает нужные поля (включая roles, is_banned)
	user, err := h.userRepo.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		// Обрабатываем ошибку (UserNotFound или другую)
		handleServiceError(c, err)
		return
	}

	// Преобразуем в DTO (используем тот же meResponse, т.к. он содержит нужные поля)
	userDTO := meResponse{
		ID:       user.ID,
		Username: user.Username,
		Email:    user.Email,
		Roles:    user.Roles,
		IsBanned: user.IsBanned,
	}

	c.JSON(http.StatusOK, userDTO)
}

// --- Middleware ---

// InternalAuthMiddleware - middleware для проверки межсервисного токена
func (h *AuthHandler) InternalAuthMiddleware() gin.HandlerFunc {
	// --- Читаем секрет из конфига для проверки ---
	staticSecret := h.cfg.InterServiceSecret
	if staticSecret == "" {
		zap.L().Warn("InternalAuthMiddleware: INTER_SERVICE_SECRET is not configured on auth-service! Static secret check will fail.")
	}

	return func(c *gin.Context) {
		tokenString := c.GetHeader("X-Internal-Service-Token")
		if tokenString == "" {
			// <<< Инкремент счетчика ошибок верификации >>>
			tokenVerificationsTotal.WithLabelValues("inter-service", "failure").Inc()
			// ... (ошибка: токен отсутствует)
			// --- Добавляем стандартную обработку ошибки ---
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Code:    ErrCodeInvalidToken,
				Message: "Missing internal service token",
			})
			return
		}

		// --- Проверка: Статичный секрет ИЛИ JWT ---
		if staticSecret != "" && tokenString == staticSecret {
			// Это статичный секрет (вероятно, запрос на генерацию JWT)
			zap.L().Debug("Internal service access granted via static secret")
			// <<< НЕ инкрементируем здесь, т.к. это не верификация JWT >>>
			c.Set("service_name", "_static_secret_")
			c.Next()
			return
		} else {
			// Пытаемся проверить как JWT токен
			ctx := c.Request.Context()
			serviceName, err := h.authService.VerifyInterServiceToken(ctx, tokenString)
			if err != nil {
				zap.L().Warn("Internal service JWT token verification failed (or it was an invalid static secret)", zap.Error(err))
				// <<< Инкремент счетчика ошибок верификации >>>
				tokenVerificationsTotal.WithLabelValues("inter-service", "failure").Inc()
				c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
					Code:    ErrCodeInvalidToken,
					Message: "Invalid internal service token",
				})
				return
			}
			// JWT валиден
			// <<< Инкремент счетчика успешной верификации >>>
			tokenVerificationsTotal.WithLabelValues("inter-service", "success").Inc()
			// TODO: Добавить проверку, что serviceName имеет право доступа к этим ресурсам
			c.Set("service_name", serviceName)
			c.Next()
		}
		// --- Конец проверки ---
	}
}

// --- Новый обработчик для обновления пользователя ---

// updateUser handles the PUT request to update user details.
func (h *AuthHandler) updateUser(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, ErrorResponse{Code: ErrCodeBadRequest, Message: "Invalid user ID format"})
		return
	}

	var req updateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, ErrorResponse{Code: ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}

	// Вызываем сервис для обновления
	err = h.authService.UpdateUser(c.Request.Context(), userID, req.Email, req.Roles, req.IsBanned)
	if err != nil {
		// Обрабатываем ошибку из сервиса (может быть UserNotFound, EmailAlreadyExists, InvalidInput и т.д.)
		handleServiceError(c, err)
		return
	}

	// Успех
	c.Status(http.StatusNoContent) // Возвращаем 204 No Content
}

// --- Новый обработчик для обновления пароля ---

// updatePassword handles the PUT request to update a user's password.
func (h *AuthHandler) updatePassword(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := strconv.ParseUint(userIDStr, 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, ErrorResponse{Code: ErrCodeBadRequest, Message: "Invalid user ID format"})
		return
	}

	var req updatePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, ErrorResponse{Code: ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}

	// Вызываем сервис для обновления пароля
	err = h.authService.UpdatePassword(c.Request.Context(), userID, req.NewPassword)
	if err != nil {
		// Обрабатываем ошибку из сервиса (может быть UserNotFound)
		handleServiceError(c, err)
		return
	}

	// Успех
	c.Status(http.StatusNoContent) // Возвращаем 204 No Content
}

// --- Новый обработчик для обновления токена администратора ---

// refreshAdminTokenResponse - структура для ответа
type refreshAdminTokenResponse struct {
	Tokens models.TokenDetails `json:"tokens"`
	Claims models.Claims       `json:"claims"`
}

func (h *AuthHandler) refreshAdminToken(c *gin.Context) {
	var req refreshRequest // Используем ту же структуру запроса, что и для обычного рефреша
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, ErrorResponse{Code: ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}

	// Вызываем НОВЫЙ метод сервиса
	newTokens, newClaims, err := h.authService.RefreshAdminToken(c.Request.Context(), req.RefreshToken)
	if err != nil {
		// Можно добавить инкремент счетчика ошибок верификации (тип: admin-refresh?)
		handleServiceError(c, err)
		return
	}

	// Инкремент счетчика успешных обновлений (можно сделать отдельный для админов?)
	refreshesTotal.Inc() // Пока используем общий счетчик

	// Возвращаем структуру с токенами и клеймами
	resp := refreshAdminTokenResponse{
		Tokens: *newTokens,
		Claims: *newClaims,
	}
	c.JSON(http.StatusOK, resp)
}
