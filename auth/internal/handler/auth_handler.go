package handler

import (
	"errors"
	"net/http"
	"novel-server/auth/internal/config"
	"novel-server/auth/internal/domain/dto"
	"novel-server/auth/internal/service"
	sharedInterfaces "novel-server/shared/interfaces"
	"novel-server/shared/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type AuthHandler struct {
	authService        service.AuthService
	userRepo           sharedInterfaces.UserRepository
	deviceTokenService sharedInterfaces.DeviceTokenService
	cfg                *config.Config
}

func NewAuthHandler(
	authService service.AuthService,
	userRepo sharedInterfaces.UserRepository,
	deviceTokenService sharedInterfaces.DeviceTokenService,
	cfg *config.Config,
) *AuthHandler {
	return &AuthHandler{
		authService:        authService,
		userRepo:           userRepo,
		deviceTokenService: deviceTokenService,
		cfg:                cfg,
	}
}

func (h *AuthHandler) RegisterRoutes(router *gin.Engine, rateLimiter gin.HandlerFunc) {
	baseAuthGroup := router.Group("/auth")
	{
		baseAuthGroup.POST("/register", rateLimiter, h.register)
		baseAuthGroup.POST("/login", rateLimiter, h.login)
		baseAuthGroup.POST("/refresh", h.refresh)
		baseAuthGroup.POST("/token/verify", h.verify)
	}

	// NEW Group for PROTECTED /auth routes
	protectedAuthGroup := router.Group("/auth")
	protectedAuthGroup.Use(h.AuthMiddleware()) // Apply auth middleware here
	{
		zap.L().Info("Registering POST /auth/logout route")
		protectedAuthGroup.POST("/logout", h.logout) // Will be /auth/logout
	}

	// Group for other PROTECTED routes WITHOUT /auth prefix (e.g., /me, /device-tokens)
	protectedRootGroup := router.Group("/")    // Group from root (renamed from protectedGroup to protectedRootGroup for clarity)
	protectedRootGroup.Use(h.AuthMiddleware()) // Apply auth middleware
	{
		zap.L().Info("Registering protected routes at root") // Updated log message
		zap.L().Info("Registering GET /me route")            // Лог перед /me
		protectedRootGroup.GET("/me", h.getMe)               // /me

		deviceTokenRoutes := protectedRootGroup.Group("/device-tokens") // /device-tokens
		{
			// Middleware уже применено родительской группой
			deviceTokenRoutes.POST("", h.registerDeviceToken)     // /device-tokens
			deviceTokenRoutes.DELETE("", h.unregisterDeviceToken) // /device-tokens
		}
	}

	// <<< Переносим validateToken ИЗ защищенной группы internalAuthGroup >>>
	// Этот эндпоинт используется другими сервисами для валидации ПОЛЬЗОВАТЕЛЬСКОГО токена,
	// он сам не должен требовать ВАЛИДНОГО межсервисного токена.
	// Middleware для межсервисного токена может быть добавлено индивидуально,
	// если нужно логировать вызывающий сервис, но не блокировать запрос.
	internalAuthGroupStandalone := router.Group("/internal/auth")
	internalAuthGroupStandalone.POST("/token/validate", h.validateToken)

	internalAuthGroup := router.Group("/internal/auth")
	internalAuthGroup.Use(h.InternalAuthMiddleware()) // Middleware for service-to-service auth
	{
		// Routes ONLY requiring service-to-service auth (NO admin role check)
		internalAuthGroup.POST("/token/generate", h.generateInterServiceToken)
		internalAuthGroup.POST("/token/verify", h.verifyInterServiceToken)
		internalAuthGroup.POST("/users/batch-info", h.handleBatchGetUsersInfo)
		internalAuthGroup.GET("/users/:user_id/device-tokens", h.getDeviceTokensForUser)

		// Routes requiring BOTH service-to-service auth AND admin role
		adminRequiredGroup := internalAuthGroup.Group("")
		adminRequiredGroup.Use(h.RequireAdminRoleMiddleware()) // <<< Apply admin role check
		{
			adminRequiredGroup.GET("/users/count", h.getUserCount)
			adminRequiredGroup.GET("/users", h.listUsers)
			adminRequiredGroup.POST("/users/:user_id/ban", h.banUser)
			adminRequiredGroup.DELETE("/users/:user_id/ban", h.unbanUser)
			adminRequiredGroup.GET("/users/:user_id", h.getUserDetails)
			adminRequiredGroup.PUT("/users/:user_id", h.updateUser)
			adminRequiredGroup.PUT("/users/:user_id/password", h.updatePassword)
			adminRequiredGroup.POST("/token/refresh/admin", h.refreshAdminToken)
		}
	}
}

func (h *AuthHandler) registerDeviceToken(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		return
	}

	var input dto.RegisterDeviceTokenInput
	if err := c.ShouldBindJSON(&input); err != nil {
		zap.L().Warn("Failed to bind JSON for device token registration", zap.Error(err))
		c.AbortWithStatusJSON(http.StatusBadRequest, models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}

	err = h.deviceTokenService.RegisterDeviceToken(c.Request.Context(), userID, input)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{Message: "Device token registered successfully"})
}

func (h *AuthHandler) unregisterDeviceToken(c *gin.Context) {
	if _, err := getUserIDFromContext(c); err != nil {
		return
	}

	var input dto.UnregisterDeviceTokenInput
	if err := c.ShouldBindJSON(&input); err != nil {
		zap.L().Warn("Failed to bind JSON for device token unregistration", zap.Error(err))
		c.AbortWithStatusJSON(http.StatusBadRequest, models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}

	err := h.deviceTokenService.UnregisterDeviceToken(c.Request.Context(), input)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	c.JSON(http.StatusOK, SuccessResponse{Message: "Device token unregistered successfully"})
}

const UserIDKey = "user_id"

func getUserIDFromContext(c *gin.Context) (uuid.UUID, error) {
	userIDRaw, exists := c.Get(UserIDKey)
	if !exists {
		zap.L().Error("UserID not found in context, AuthMiddleware should have set it")
		c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{Code: models.ErrCodeUnauthorized, Message: "Unauthorized: Missing user context"})
		return uuid.Nil, errors.New("missing user ID in context")
	}

	userID, ok := userIDRaw.(uuid.UUID)
	if !ok {
		zap.L().Error("Invalid UserID format in context", zap.Any("value", userIDRaw))
		c.AbortWithStatusJSON(http.StatusInternalServerError, models.ErrorResponse{Code: models.ErrCodeInternal, Message: "Internal Server Error: Invalid user context"})
		return uuid.Nil, errors.New("invalid user ID format in context")
	}

	return userID, nil
}

type SuccessResponse struct {
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func (h *AuthHandler) getDeviceTokensForUser(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		zap.L().Warn("Invalid user ID (UUID) format for get device tokens request", zap.String("userID", userIDStr), zap.Error(err))
		c.AbortWithStatusJSON(http.StatusBadRequest, models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Invalid user ID format"})
		return
	}

	zap.L().Debug("Request received for getting device tokens", zap.String("userID", userIDStr))

	tokens, err := h.deviceTokenService.GetDeviceTokensForUser(c.Request.Context(), userID)
	if err != nil {
		// Ошибку логирует сам сервис
		handleServiceError(c, err) // Используем общий обработчик ошибок сервиса
		return
	}

	// Возвращаем токены (даже если список пустой) со статусом 200 OK
	c.JSON(http.StatusOK, tokens)
}
