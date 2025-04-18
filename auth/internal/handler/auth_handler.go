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

func (h *AuthHandler) RegisterRoutes(router *gin.Engine) {
	baseAuthGroup := router.Group("/auth")
	{
		baseAuthGroup.POST("/register", h.register)
		baseAuthGroup.POST("/login", h.login)
		baseAuthGroup.POST("/logout", h.AuthMiddleware(), h.logout)
		baseAuthGroup.POST("/refresh", h.refresh)
		baseAuthGroup.POST("/token/verify", h.verify)
	}

	protectedV1 := router.Group("/api/v1")
	protectedV1.Use(h.AuthMiddleware())
	{
		protectedV1.GET("/me", h.getMe)

		deviceTokenRoutes := protectedV1.Group("/device-tokens")
		{
			deviceTokenRoutes.POST("", h.registerDeviceToken)
			deviceTokenRoutes.DELETE("", h.unregisterDeviceToken)
		}
	}

	internalAuthGroup := router.Group("/internal/auth")
	internalAuthGroup.Use(h.InternalAuthMiddleware())
	{
		internalAuthGroup.POST("/token/generate", h.generateInterServiceToken)
		internalAuthGroup.POST("/token/verify", h.verifyInterServiceToken)
		internalAuthGroup.GET("/users/count", h.getUserCount)
		internalAuthGroup.GET("/users", h.listUsers)
		internalAuthGroup.POST("/users/:user_id/ban", h.banUser)
		internalAuthGroup.DELETE("/users/:user_id/ban", h.unbanUser)
		internalAuthGroup.POST("/token/validate", h.validateToken)
		internalAuthGroup.GET("/users/:user_id", h.getUserDetails)
		internalAuthGroup.PUT("/users/:user_id", h.updateUser)
		internalAuthGroup.PUT("/users/:user_id/password", h.updatePassword)
		internalAuthGroup.POST("/token/refresh/admin", h.refreshAdminToken)
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
