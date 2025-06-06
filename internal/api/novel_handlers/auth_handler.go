package novel_handlers

import (
	"encoding/json"
	"net/http"
	"novel-server/internal/auth"
	"novel-server/internal/logger"
)

// Authenticate генерирует JWT токен для пользователя
func (h *NovelHandler) Authenticate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "Only POST method is allowed")
		return
	}

	var req struct {
		UserID string `json:"user_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request format. Expected {'user_id': 'string'}")
		return
	}
	defer r.Body.Close()

	if req.UserID == "" {
		respondWithError(w, http.StatusBadRequest, "user_id is required")
		return
	}

	// Здесь в будущем может быть проверка пароля или другие методы аутентификации
	logger.Logger.Info("Generating token", "userID", req.UserID)

	tokenString, err := auth.GenerateToken(req.UserID)
	if err != nil {
		logger.Logger.Error("Error generating token", "userID", req.UserID, "err", err)
		respondWithError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]string{"token": tokenString})
}
