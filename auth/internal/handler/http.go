package handler

import (
	"auth/internal/domain" // Нужен для Claims при парсинге
	"auth/internal/service"
	"errors"
	"net/http"
	"shared/models"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5" // Нужен для парсинга refresh token
	"go.uber.org/zap"
)

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

// AuthHandler handles HTTP requests related to authentication.
type AuthHandler struct {
	authService service.AuthService
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(authService service.AuthService) *AuthHandler {
	return &AuthHandler{authService: authService}
}

// RegisterRoutes registers the authentication routes.
func (h *AuthHandler) RegisterRoutes(router *gin.Engine) {
	authGroup := router.Group("/auth")
	{
		authGroup.POST("/register", h.register)
		authGroup.POST("/login", h.login)
		authGroup.POST("/logout", h.logout) // Requires auth middleware
		authGroup.POST("/refresh", h.refresh)
		authGroup.POST("/token/verify", h.verify) // Endpoint for other services to verify token
	}

	// Example of a protected route group
	protected := router.Group("/api")
	protected.Use(h.AuthMiddleware()) // Apply auth middleware
	{
		// Add protected routes here
		protected.GET("/me", h.getMe)
	}

	// Inter-service endpoints (can be protected differently if needed)
	interServiceGroup := router.Group("/internal/auth")
	{
		interServiceGroup.POST("/token/generate", h.generateInterServiceToken) // Potentially restricted access
		interServiceGroup.POST("/token/verify", h.verifyInterServiceToken)     // Open for internal services
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
	case errors.Is(err, models.ErrInvalidToken), errors.Is(err, models.ErrTokenMalformed):
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

	user, err := h.authService.Register(c.Request.Context(), req.Username, req.Email, req.Password)
	if err != nil {
		handleServiceError(c, err)
		return
	}

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
		handleServiceError(c, err)
		return
	}

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
		handleServiceError(c, err)
		return
	}

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

// --- Middleware ---

// AuthMiddleware verifies the access token.
func (h *AuthHandler) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		const BearerSchema = "Bearer "
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			handleServiceError(c, models.ErrInvalidToken)
			return
		}

		tokenString := ""
		if len(authHeader) > len(BearerSchema) && strings.HasPrefix(authHeader, BearerSchema) {
			tokenString = authHeader[len(BearerSchema):]
		}

		if tokenString == "" {
			handleServiceError(c, models.ErrInvalidToken)
			return
		}

		claims, err := h.authService.VerifyAccessToken(c.Request.Context(), tokenString)
		if err != nil {
			handleServiceError(c, err)
			return
		}

		// Добавляем ID пользователя и Access UUID в контекст
		c.Set("user_id", claims.UserID)
		if accessUUID := claims.ID; accessUUID != "" { // claims.ID соответствует 'jti'
			c.Set("access_uuid", accessUUID)
		} else {
			// UUID обязателен для корректной работы, особенно logout
			zap.L().Error("Access UUID ('jti' claim) missing in token", zap.Uint64("userID", claims.UserID))
			// Это внутренняя ошибка конфигурации токена/сервиса
			handleServiceError(c, errors.New("internal server error: invalid token claims configuration"))
			return // Прерываем выполнение
		}

		c.Next()
	}
}

// getMe is an example protected endpoint.
func (h *AuthHandler) getMe(c *gin.Context) {
	userIDAny, exists := c.Get("user_id")
	if !exists {
		zap.L().Error("UserID not found in context after AuthMiddleware")
		handleServiceError(c, errors.New("internal server error: user context missing"))
		return
	}

	userID, ok := userIDAny.(uint64)
	if !ok {
		zap.L().Error("UserID in context is not uint64", zap.Any("value", userIDAny))
		handleServiceError(c, errors.New("internal server error: user context invalid type"))
		return
	}

	// TODO: Получить полную информацию о пользователе из UserRepository
	// user, err := h.userRepo.GetUserByID(c.Request.Context(), userID) // Нужен userRepo в хендлере!
	// if err != nil {
	//     handleServiceError(c, err) // Обработает UserNotFound и другие ошибки БД
	//     return
	// }

	c.JSON(http.StatusOK, gin.H{"user_id": userID})
}
