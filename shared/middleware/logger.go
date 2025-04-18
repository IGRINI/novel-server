package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// GinZapLogger returns a gin.HandlerFunc (middleware) that logs requests using zap.
// It is adapted from the logger used in auth-service.
func GinZapLogger(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		// Process request
		c.Next()

		// Log request details after handler processing
		latency := time.Since(start)
		clientIP := c.ClientIP()
		method := c.Request.Method
		statusCode := c.Writer.Status()
		// Get Gin's internal error messages if any
		errorMessage := c.Errors.ByType(gin.ErrorTypePrivate).String()

		fields := []zapcore.Field{
			zap.Int("status", statusCode),
			zap.String("method", method),
			zap.String("path", path),
			zap.String("query", query),
			zap.String("ip", clientIP),
			zap.Duration("latency", latency),
			zap.String("user-agent", c.Request.UserAgent()),
		}
		if errorMessage != "" {
			fields = append(fields, zap.String("error", errorMessage))
		}

		// Differentiate log level based on status code
		if statusCode >= http.StatusInternalServerError {
			logger.Error("Request handled", fields...)
		} else if statusCode >= http.StatusBadRequest {
			logger.Warn("Request handled", fields...)
		} else {
			logger.Info("Request handled", fields...)
		}
	}
}
