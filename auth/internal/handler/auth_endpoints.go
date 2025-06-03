package handler

import (
	"errors"
	"fmt"
	"net/http"
	"novel-server/shared/models"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

// @Summary Регистрация нового пользователя
// @Description Создает новый аккаунт пользователя
// @Tags auth
// @Accept json
// @Produce json
// @Param request body registerRequest true "Данные для регистрации"
// @Success 201 {object} map[string]interface{} "Успешная регистрация"
// @Failure 400 {object} ErrorResponse "Неверные данные запроса"
// @Failure 409 {object} ErrorResponse "Пользователь уже существует"
// @Router /auth/register [post]
func (h *AuthHandler) register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errResp := models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Invalid request data: " + err.Error()}
		c.AbortWithStatusJSON(http.StatusBadRequest, errResp)
		return
	}

	if len(req.Username) < minUsernameLength || len(req.Username) > maxUsernameLength {
		errResp := models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: fmt.Sprintf("Username length must be between %d and %d characters", minUsernameLength, maxUsernameLength)}
		c.AbortWithStatusJSON(http.StatusBadRequest, errResp)
		return
	}
	if !usernameRegex.MatchString(req.Username) {
		errResp := models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Username can only contain letters, numbers, underscores, and hyphens"}
		c.AbortWithStatusJSON(http.StatusBadRequest, errResp)
		return
	}

	if len(req.Password) < minPasswordLength || len(req.Password) > maxPasswordLength {
		errResp := models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: fmt.Sprintf("Password length must be between %d and %d characters", minPasswordLength, maxPasswordLength)}
		c.AbortWithStatusJSON(http.StatusBadRequest, errResp)
		return
	}
	var (
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
			break
		}
	}
	if !hasLetter || !hasDigit {
		errResp := models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Password must contain at least one letter and one digit"}
		c.AbortWithStatusJSON(http.StatusBadRequest, errResp)
		return
	}

	user, err := h.authService.Register(c.Request.Context(), req.Username, req.Email, req.Password)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	registrationsTotal.Inc()

	c.JSON(http.StatusCreated, gin.H{
		"id":       user.ID.String(),
		"username": user.Username,
		"email":    user.Email,
	})
}

// @Summary Вход в систему
// @Description Аутентификация пользователя и получение токенов
// @Tags auth
// @Accept json
// @Produce json
// @Param request body loginRequest true "Данные для входа"
// @Success 200 {object} TokenDetails "Токены доступа"
// @Failure 400 {object} ErrorResponse "Неверные данные запроса"
// @Failure 401 {object} ErrorResponse "Неверные учетные данные"
// @Router /auth/login [post]
func (h *AuthHandler) login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}

	tokens, err := h.authService.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	c.JSON(http.StatusOK, tokens)
}

// @Summary Выход из системы
// @Description Отзыв токенов пользователя
// @Tags auth
// @Accept json
// @Produce json
// @Param request body logoutRequest true "Refresh токен для отзыва"
// @Success 200 {object} map[string]interface{} "Успешный выход"
// @Failure 400 {object} ErrorResponse "Неверные данные запроса"
// @Failure 401 {object} ErrorResponse "Неверный токен"
// @Security BearerAuth
// @Router /auth/logout [post]
func (h *AuthHandler) logout(c *gin.Context) {
	accessUUIDRaw, exists := c.Get("access_uuid")
	if !exists {
		zap.L().Error("Access UUID missing in context during logout")
		handleServiceError(c, errors.New("internal server error: context missing access uuid"))
		return
	}
	accessUUID, ok := accessUUIDRaw.(string)
	if !ok || accessUUID == "" {
		zap.L().Error("Invalid or empty Access UUID in context during logout", zap.Any("uuid_raw", accessUUIDRaw))
		handleServiceError(c, errors.New("internal server error: invalid access uuid in context"))
		return
	}

	var req logoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Missing or invalid refresh_token in request body: " + err.Error()})
		return
	}

	token, _, err := new(jwt.Parser).ParseUnverified(req.RefreshToken, &models.Claims{})
	if err != nil {
		handleServiceError(c, models.ErrTokenMalformed)
		return
	}

	claims, ok := token.Claims.(*models.Claims)
	if !ok {
		zap.L().Error("Invalid claims type in refresh token during logout")
		handleServiceError(c, models.ErrTokenMalformed)
		return
	}
	refreshUUID := claims.ID
	if refreshUUID == "" {
		zap.L().Error("Refresh UUID ('jti' claim) missing in refresh token during logout")
		handleServiceError(c, models.ErrTokenMalformed)
		return
	}

	err = h.authService.Logout(c.Request.Context(), accessUUID, refreshUUID)
	if err != nil {
		zap.L().Error("Failed to perform logout in service",
			zap.Error(err),
			zap.String("accessUUID", accessUUID),
			zap.String("refreshUUID", refreshUUID),
		)
		type internalError interface {
			IsInternal() bool
		}
		if ierr, ok := err.(internalError); !ok || !ierr.IsInternal() {
			handleServiceError(c, err)
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "Successfully logged out"})
}

// @Summary Обновление токенов
// @Description Получение новых токенов по refresh токену
// @Tags auth
// @Accept json
// @Produce json
// @Param request body refreshRequest true "Refresh токен"
// @Success 200 {object} TokenDetails "Новые токены"
// @Failure 400 {object} ErrorResponse "Неверные данные запроса"
// @Failure 401 {object} ErrorResponse "Неверный или истекший токен"
// @Router /auth/refresh [post]
func (h *AuthHandler) refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}

	tokens, err := h.authService.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		if errors.Is(err, models.ErrTokenNotFound) || errors.Is(err, models.ErrTokenExpired) || errors.Is(err, models.ErrTokenInvalid) || errors.Is(err, models.ErrTokenMalformed) {
			tokenVerificationsTotal.WithLabelValues("refresh", "failure").Inc()
		}
		handleServiceError(c, err)
		return
	}

	refreshesTotal.Inc()
	tokenVerificationsTotal.WithLabelValues("refresh", "success").Inc()

	c.JSON(http.StatusOK, tokens)
}

// @Summary Проверка токена
// @Description Проверка валидности access токена
// @Tags auth
// @Accept json
// @Produce json
// @Param request body tokenVerifyRequest true "Токен для проверки"
// @Success 200 {object} map[string]interface{} "Результат проверки"
// @Failure 400 {object} ErrorResponse "Неверные данные запроса"
// @Failure 401 {object} ErrorResponse "Неверный токен"
// @Router /auth/token/verify [post]
func (h *AuthHandler) verify(c *gin.Context) {
	var req tokenVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}

	claims, err := h.authService.VerifyAccessToken(c.Request.Context(), req.Token)
	if err != nil {
		tokenVerificationsTotal.WithLabelValues("access", "failure").Inc()
		handleServiceError(c, err)
		return
	}
	tokenVerificationsTotal.WithLabelValues("access", "success").Inc()

	c.JSON(http.StatusOK, gin.H{"user_id": claims.UserID.String(), "valid": true})
}

func (h *AuthHandler) generateInterServiceToken(c *gin.Context) {
	var req generateInterServiceTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}

	token, err := h.authService.GenerateInterServiceToken(c.Request.Context(), req.ServiceName)
	if err != nil {
		interServiceTokensGeneratedTotal.Inc()
		handleServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"inter_service_token": token})
}

func (h *AuthHandler) verifyInterServiceToken(c *gin.Context) {
	var req tokenVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, models.ErrorResponse{Code: models.ErrCodeBadRequest, Message: "Invalid request body: " + err.Error()})
		return
	}

	serviceName, err := h.authService.VerifyInterServiceToken(c.Request.Context(), req.Token)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"service_name": serviceName, "valid": true})
}
