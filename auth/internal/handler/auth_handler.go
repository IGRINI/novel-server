package handler

import (
	"novel-server/auth/internal/config"
	"novel-server/auth/internal/service"
	"novel-server/shared/interfaces"

	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	authService service.AuthService
	userRepo    interfaces.UserRepository
	cfg         *config.Config
}

func NewAuthHandler(authService service.AuthService, userRepo interfaces.UserRepository, cfg *config.Config) *AuthHandler {
	return &AuthHandler{
		authService: authService,
		userRepo:    userRepo,
		cfg:         cfg,
	}
}

func (h *AuthHandler) RegisterRoutes(router *gin.Engine) {
	authGroup := router.Group("/auth")
	{
		authGroup.POST("/register", h.register)
		authGroup.POST("/login", h.login)
		authGroup.POST("/logout", h.AuthMiddleware(), h.logout)
		authGroup.POST("/refresh", h.refresh)
		authGroup.POST("/token/verify", h.verify)
	}

	protected := router.Group("/api")
	protected.Use(h.AuthMiddleware())
	{
		protected.GET("/me", h.getMe)
	}

	interServiceGroup := router.Group("/internal/auth")
	interServiceGroup.Use(h.InternalAuthMiddleware())
	{
		interServiceGroup.POST("/token/generate", h.generateInterServiceToken)
		interServiceGroup.POST("/token/verify", h.verifyInterServiceToken)
		interServiceGroup.GET("/users/count", h.getUserCount)
		interServiceGroup.GET("/users", h.listUsers)
		interServiceGroup.POST("/users/:user_id/ban", h.banUser)
		interServiceGroup.DELETE("/users/:user_id/ban", h.unbanUser)
		interServiceGroup.POST("/token/validate", h.validateToken)
		interServiceGroup.GET("/users/:user_id", h.getUserDetails)
		interServiceGroup.PUT("/users/:user_id", h.updateUser)
		interServiceGroup.PUT("/users/:user_id/password", h.updatePassword)
		interServiceGroup.POST("/token/refresh/admin", h.refreshAdminToken)
	}
}
