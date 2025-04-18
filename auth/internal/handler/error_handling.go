package handler

import (
	"errors"
	"net/http"
	"novel-server/shared/models"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func handleServiceError(c *gin.Context, err error) {
	var statusCode int
	var errResp models.ErrorResponse

	switch {
	case errors.Is(err, models.ErrInvalidCredentials):
		statusCode = http.StatusUnauthorized
		errResp = models.ErrorResponse{Code: models.ErrCodeWrongCredentials, Message: "Invalid username or password"}
	case errors.Is(err, models.ErrUserAlreadyExists):
		statusCode = http.StatusConflict
		errResp = models.ErrorResponse{Code: models.ErrCodeDuplicateUser, Message: "Username already exists"}
	case errors.Is(err, models.ErrEmailAlreadyExists):
		statusCode = http.StatusConflict
		errResp = models.ErrorResponse{Code: models.ErrCodeDuplicateEmail, Message: "Email already exists"}
	case errors.Is(err, models.ErrUserNotFound):
		statusCode = http.StatusNotFound
		errResp = models.ErrorResponse{Code: models.ErrCodeUserNotFound, Message: "User not found"}
	case errors.Is(err, models.ErrTokenInvalid), errors.Is(err, models.ErrTokenMalformed):
		statusCode = http.StatusUnauthorized
		errResp = models.ErrorResponse{Code: models.ErrCodeTokenInvalid, Message: "Token is invalid or malformed"}
	case errors.Is(err, models.ErrTokenExpired):
		statusCode = http.StatusUnauthorized
		errResp = models.ErrorResponse{Code: models.ErrCodeTokenExpired, Message: "Token has expired"}
	case errors.Is(err, models.ErrTokenNotFound):
		statusCode = http.StatusUnauthorized
		errResp = models.ErrorResponse{Code: models.ErrCodeTokenInvalid, Message: "Provided token is invalid (possibly revoked or expired)"}
	case errors.Is(err, models.ErrUserBanned):
		statusCode = http.StatusForbidden
		errResp = models.ErrorResponse{Code: models.ErrCodeUserBanned, Message: "User is banned"}
	case strings.Contains(err.Error(), "validation error"):
		statusCode = http.StatusBadRequest
		errResp = models.ErrorResponse{Code: models.ErrCodeValidation, Message: err.Error()}
	case strings.Contains(err.Error(), "invalid input data"):
		statusCode = http.StatusBadRequest
		errResp = models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: err.Error()}
	default:
		zap.L().Error("Unhandled internal error in handleServiceError", zap.Error(err))
		statusCode = http.StatusInternalServerError
		errResp = models.ErrorResponse{Code: models.ErrCodeInternal, Message: "An unexpected internal error occurred"}
	}

	c.AbortWithStatusJSON(statusCode, errResp)
}
