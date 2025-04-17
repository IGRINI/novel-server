package handler

import (
	"errors"
	"net/http"
	"novel-server/shared/models"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func handleServiceError(c *gin.Context, err error) {
	var statusCode int
	var errResp ErrorResponse

	switch {
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
	case errors.Is(err, models.ErrTokenInvalid), errors.Is(err, models.ErrTokenMalformed):
		statusCode = http.StatusUnauthorized
		errResp = ErrorResponse{Code: ErrCodeInvalidToken, Message: "Provided token is invalid or malformed"}
	case errors.Is(err, models.ErrTokenExpired):
		statusCode = http.StatusUnauthorized
		errResp = ErrorResponse{Code: ErrCodeExpiredToken, Message: "Provided token has expired"}
	case errors.Is(err, models.ErrTokenNotFound):
		statusCode = http.StatusUnauthorized
		errResp = ErrorResponse{Code: ErrCodeRevokedToken, Message: "Provided token is invalid (possibly revoked or expired)"}
	default:
		zap.L().Error("Unhandled internal error", zap.Error(err))
		statusCode = http.StatusInternalServerError
		errResp = ErrorResponse{Code: ErrCodeInternalError, Message: "An unexpected internal error occurred"}
	}

	c.AbortWithStatusJSON(statusCode, errResp)
}
