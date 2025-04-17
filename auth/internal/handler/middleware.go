package handler

import (
	"novel-server/shared/models"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (h *AuthHandler) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			zap.L().Warn("Authorization header missing")
			tokenVerificationsTotal.WithLabelValues("access", "failure").Inc()
			handleServiceError(c, models.ErrTokenInvalid)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			zap.L().Warn("Invalid Authorization header format", zap.String("header", authHeader))
			tokenVerificationsTotal.WithLabelValues("access", "failure").Inc()
			handleServiceError(c, models.ErrTokenInvalid)
			return
		}

		tokenString := parts[1]
		claims, err := h.authService.VerifyAccessToken(c.Request.Context(), tokenString)
		if err != nil {
			zap.L().Warn("Access token verification failed", zap.Error(err))
			tokenVerificationsTotal.WithLabelValues("access", "failure").Inc()
			handleServiceError(c, err)
			return
		}

		tokenVerificationsTotal.WithLabelValues("access", "success").Inc()
		c.Set("user_id", claims.UserID.String())
		c.Set("access_uuid", claims.ID)
		zap.L().Debug("Access token verified successfully", zap.String("userID", claims.UserID.String()), zap.String("accessUUID", claims.ID))
		c.Next()
	}
}

func (h *AuthHandler) InternalAuthMiddleware() gin.HandlerFunc {
	staticSecret := h.cfg.InterServiceSecret
	if staticSecret == "" {
		zap.L().Warn("InternalAuthMiddleware: INTER_SERVICE_SECRET is not configured on auth-service! Static secret check will fail.")
	}

	return func(c *gin.Context) {
		tokenString := c.GetHeader("X-Internal-Service-Token")
		if tokenString == "" {
			tokenVerificationsTotal.WithLabelValues("inter-service", "failure").Inc()
			c.AbortWithStatusJSON(401, ErrorResponse{
				Code:    ErrCodeInvalidToken,
				Message: "Missing internal service token",
			})
			return
		}

		if staticSecret != "" && tokenString == staticSecret {
			zap.L().Debug("Internal service access granted via static secret")
			c.Set("service_name", "_static_secret_")
			c.Next()
			return
		} else {
			ctx := c.Request.Context()
			serviceName, err := h.authService.VerifyInterServiceToken(ctx, tokenString)
			if err != nil {
				zap.L().Warn("Internal service JWT token verification failed (or it was an invalid static secret)", zap.Error(err))
				tokenVerificationsTotal.WithLabelValues("inter-service", "failure").Inc()
				c.AbortWithStatusJSON(401, ErrorResponse{
					Code:    ErrCodeInvalidToken,
					Message: "Invalid internal service token",
				})
				return
			}
			tokenVerificationsTotal.WithLabelValues("inter-service", "success").Inc()
			c.Set("service_name", serviceName)
			c.Next()
		}
	}
}
