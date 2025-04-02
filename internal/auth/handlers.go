package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
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

type LoginResponse struct {
	Token string `json:"token"`
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

	if req.Password == "" {
		respondWithError(w, http.StatusBadRequest, "пароль не может быть пустым")
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

	token, err := h.service.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		switch {
		case err == ErrUserNotFound || err == ErrInvalidPassword:
			respondWithError(w, http.StatusUnauthorized, "неверное имя пользователя или пароль")
		default:
			fmt.Printf("Ошибка при входе: %v\n", err)
			respondWithError(w, http.StatusInternalServerError, "внутренняя ошибка сервера")
		}
		return
	}

	respondWithJSON(w, http.StatusOK, LoginResponse{Token: token})
}
