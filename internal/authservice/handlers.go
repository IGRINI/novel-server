package authservice

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

type RegisterRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type TokenResponse struct {
	AccessToken  string `json:"token"` // Оставляем имя "token" для обратной совместимости
	RefreshToken string `json:"refresh_token"`
	UserID       string `json:"user_id"`
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	ExpiresAt    int64  `json:"expires_at"`
}

type UpdateDisplayNameRequest struct {
	DisplayName string `json:"display_name"`
}

type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type ValidateTokenRequest struct {
	Token string `json:"token"`
}

type ValidateTokenResponse struct {
	Valid     bool   `json:"valid"`
	UserID    string `json:"user_id,omitempty"`
	Username  string `json:"username,omitempty"`
	Email     string `json:"email,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
}

type ServiceTokenRequest struct {
	ServiceID   string `json:"service_id"`
	ServiceName string `json:"service_name"`
	APIKey      string `json:"api_key"` // Дополнительный уровень безопасности
}

type ServiceTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

func respondWithError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(ErrorResponse{Error: message})
}

func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(payload)
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "неверный формат запроса")
		return
	}

	// Валидация входных данных
	if req.Username == "" {
		respondWithError(w, http.StatusBadRequest, "имя пользователя не может быть пустым")
		return
	}

	// Проверяем длину username
	if len(req.Username) < MinUsernameLength || len(req.Username) > MaxUsernameLength {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("имя пользователя должно быть от %d до %d символов", MinUsernameLength, MaxUsernameLength))
		return
	}

	if req.Password == "" {
		respondWithError(w, http.StatusBadRequest, "пароль не может быть пустым")
		return
	}

	// Проверяем длину пароля
	if len(req.Password) < MinPasswordLength || len(req.Password) > MaxPasswordLength {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("пароль должен быть от %d до %d символов", MinPasswordLength, MaxPasswordLength))
		return
	}

	if !emailRegex.MatchString(req.Email) {
		respondWithError(w, http.StatusBadRequest, "неверный формат email")
		return
	}

	if err := h.service.Register(r.Context(), req.Username, req.Email, req.Password); err != nil {
		switch {
		case err == ErrUserAlreadyExists:
			respondWithError(w, http.StatusConflict, err.Error())
		case err == ErrInvalidEmail:
			respondWithError(w, http.StatusBadRequest, err.Error())
		case err == ErrInvalidUsernameLength:
			respondWithError(w, http.StatusBadRequest, err.Error())
		case err == ErrInvalidPasswordLength:
			respondWithError(w, http.StatusBadRequest, err.Error())
		default:
			fmt.Printf("Ошибка при регистрации: %v\n", err)
			respondWithError(w, http.StatusInternalServerError, "внутренняя ошибка сервера")
		}
		return
	}

	respondWithJSON(w, http.StatusCreated, map[string]string{
		"message": "пользователь успешно зарегистрирован",
		"success": "true",
	})
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "неверный формат запроса")
		return
	}

	// Валидация входных данных
	if req.Username == "" {
		respondWithError(w, http.StatusBadRequest, "имя пользователя не может быть пустым")
		return
	}

	if req.Password == "" {
		respondWithError(w, http.StatusBadRequest, "пароль не может быть пустым")
		return
	}

	// Получаем токены и информацию о пользователе
	tokenDetails, err := h.service.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		switch {
		case err == ErrUserNotFound || err == ErrInvalidPassword:
			respondWithError(w, http.StatusUnauthorized, "неверное имя пользователя или пароль")
		case err == ErrInvalidPasswordLength:
			respondWithError(w, http.StatusBadRequest, err.Error())
		default:
			fmt.Printf("Ошибка при входе: %v\n", err)
			respondWithError(w, http.StatusInternalServerError, "внутренняя ошибка сервера")
		}
		return
	}

	// Получаем пользователя для DisplayName
	user, err := h.service.repo.GetUserByID(r.Context(), tokenDetails.UserID)
	if err != nil {
		fmt.Printf("Ошибка при получении данных пользователя: %v\n", err)
		respondWithError(w, http.StatusInternalServerError, "внутренняя ошибка сервера")
		return
	}

	// Формируем ответ
	response := TokenResponse{
		AccessToken:  tokenDetails.AccessToken,
		RefreshToken: tokenDetails.RefreshToken,
		UserID:       tokenDetails.UserID,
		Username:     tokenDetails.Username,
		DisplayName:  user.DisplayName,
		ExpiresAt:    tokenDetails.ExpiresAt.Unix(),
	}

	respondWithJSON(w, http.StatusOK, response)
}

// RefreshToken обновляет токены по refresh token
func (h *Handler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req RefreshTokenRequest

	// Пытаемся получить refresh token из запроса
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Если не получается получить из тела, пробуем из заголовка
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
			req.RefreshToken = strings.TrimPrefix(authHeader, "Bearer ")
		} else {
			respondWithError(w, http.StatusBadRequest, "неверный формат запроса: отсутствует refresh_token")
			return
		}
	}

	if req.RefreshToken == "" {
		respondWithError(w, http.StatusBadRequest, "refresh_token не может быть пустым")
		return
	}

	// Обновляем токены
	tokenDetails, err := h.service.RefreshTokens(r.Context(), req.RefreshToken)
	if err != nil {
		switch err {
		case ErrInvalidToken:
			respondWithError(w, http.StatusUnauthorized, "недействительный refresh token")
		case ErrExpiredToken:
			respondWithError(w, http.StatusUnauthorized, "истекший refresh token")
		case ErrRevokedToken:
			respondWithError(w, http.StatusUnauthorized, "отозванный refresh token")
		default:
			fmt.Printf("Ошибка при обновлении токенов: %v\n", err)
			respondWithError(w, http.StatusInternalServerError, "внутренняя ошибка сервера")
		}
		return
	}

	// Получаем пользователя для DisplayName
	user, err := h.service.repo.GetUserByID(r.Context(), tokenDetails.UserID)
	if err != nil {
		fmt.Printf("Ошибка при получении данных пользователя: %v\n", err)
		respondWithError(w, http.StatusInternalServerError, "внутренняя ошибка сервера")
		return
	}

	// Формируем ответ
	response := TokenResponse{
		AccessToken:  tokenDetails.AccessToken,
		RefreshToken: tokenDetails.RefreshToken,
		UserID:       tokenDetails.UserID,
		Username:     tokenDetails.Username,
		DisplayName:  user.DisplayName,
		ExpiresAt:    tokenDetails.ExpiresAt.Unix(),
	}

	respondWithJSON(w, http.StatusOK, response)
}

// ValidateToken проверяет действительность токена доступа
func (h *Handler) ValidateToken(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		respondWithError(w, http.StatusUnauthorized, "отсутствует или неверный формат токена доступа")
		return
	}

	accessToken := strings.TrimPrefix(authHeader, "Bearer ")

	// Используем метод сервиса для валидации токена
	valid, claims, err := h.service.ValidateAccessToken(accessToken)
	if err != nil {
		fmt.Printf("Ошибка при валидации токена: %v\n", err)
		respondWithError(w, http.StatusUnauthorized, "ошибка при проверке токена")
		return
	}

	if !valid {
		respondWithError(w, http.StatusUnauthorized, "недействительный токен доступа")
		return
	}

	// Получаем пользователя для проверки существования
	_, err = h.service.repo.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "пользователь не найден")
		return
	}

	// Возвращаем успешный результат
	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"valid":   true,
		"user_id": claims.UserID,
	})
}

// UpdateDisplayName обновляет отображаемое имя пользователя
func (h *Handler) UpdateDisplayName(w http.ResponseWriter, r *http.Request) {
	var req UpdateDisplayNameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "неверный формат запроса")
		return
	}

	// Получаем ID пользователя из контекста
	userID, ok := GetUserIDFromContext(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "пользователь не авторизован")
		return
	}

	// Обновляем displayName
	if err := h.service.UpdateDisplayName(r.Context(), userID, req.DisplayName); err != nil {
		if err == ErrInvalidDisplayName {
			respondWithError(w, http.StatusBadRequest, err.Error())
		} else {
			fmt.Printf("Ошибка при обновлении display_name: %v\n", err)
			respondWithError(w, http.StatusInternalServerError, "внутренняя ошибка сервера")
		}
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success":      true,
		"display_name": req.DisplayName,
	})
}

// Logout отзывает текущий токен обновления
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	var req RefreshTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "неверный формат запроса")
		return
	}

	if req.RefreshToken == "" {
		respondWithError(w, http.StatusBadRequest, "refresh_token не может быть пустым")
		return
	}

	// Отзываем токен
	if err := h.service.RevokeToken(r.Context(), req.RefreshToken); err != nil {
		fmt.Printf("Ошибка при отзыве токена: %v\n", err)
		respondWithError(w, http.StatusInternalServerError, "внутренняя ошибка сервера")
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "токен успешно отозван",
	})
}

// LogoutAll отзывает все токены пользователя
func (h *Handler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	// Получаем ID пользователя из контекста
	userID, ok := GetUserIDFromContext(r.Context())
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "пользователь не авторизован")
		return
	}

	// Отзываем все токены пользователя
	if err := h.service.RevokeAllUserTokens(r.Context(), userID); err != nil {
		fmt.Printf("Ошибка при отзыве всех токенов: %v\n", err)
		respondWithError(w, http.StatusInternalServerError, "внутренняя ошибка сервера")
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "все токены успешно отозваны",
	})
}

// InternalValidateToken проверяет действительность токена доступа для межсервисного взаимодействия
func (h *Handler) InternalValidateToken(w http.ResponseWriter, r *http.Request) {
	var req ValidateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "неверный формат запроса")
		return
	}

	if req.Token == "" {
		respondWithError(w, http.StatusBadRequest, "токен не может быть пустым")
		return
	}

	// Проверяем авторизацию сервиса
	serviceAuthHeader := r.Header.Get("Service-Authorization")
	if serviceAuthHeader == "" || !strings.HasPrefix(serviceAuthHeader, "Bearer ") {
		respondWithError(w, http.StatusUnauthorized, "отсутствует или неверный формат токена сервиса")
		return
	}

	serviceToken := strings.TrimPrefix(serviceAuthHeader, "Bearer ")
	valid, _, err := h.service.ValidateServiceToken(serviceToken)
	if err != nil || !valid {
		respondWithError(w, http.StatusUnauthorized, "недействительный токен сервиса")
		return
	}

	// Теперь валидируем пользовательский токен
	valid, claims, err := h.service.ValidateAccessToken(req.Token)

	response := ValidateTokenResponse{
		Valid: valid,
	}

	if valid && err == nil {
		response.UserID = claims.UserID
		response.Username = claims.Username
		response.Email = claims.Email
		response.ExpiresAt = claims.ExpiresAt.Time.Unix()
	}

	respondWithJSON(w, http.StatusOK, response)
}

// CreateServiceToken создает токен для межсервисной авторизации
func (h *Handler) CreateServiceToken(w http.ResponseWriter, r *http.Request) {
	var req ServiceTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "неверный формат запроса")
		return
	}

	if req.ServiceID == "" {
		respondWithError(w, http.StatusBadRequest, "service_id не может быть пустым")
		return
	}

	// Здесь можно добавить проверку API ключа, если это необходимо

	// Создаем токен для сервиса
	token, err := h.service.CreateServiceToken(req.ServiceID)
	if err != nil {
		if err == ErrServiceNotAuthorized {
			respondWithError(w, http.StatusUnauthorized, "сервис не авторизован")
		} else {
			fmt.Printf("Ошибка при создании токена сервиса: %v\n", err)
			respondWithError(w, http.StatusInternalServerError, "внутренняя ошибка сервера")
		}
		return
	}

	// Формируем ответ
	response := ServiceTokenResponse{
		Token:     token,
		ExpiresAt: time.Now().Add(ServiceTokenTTL).Unix(),
	}

	respondWithJSON(w, http.StatusOK, response)
}
