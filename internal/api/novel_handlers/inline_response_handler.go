package novel_handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"novel-server/internal/auth"
	"novel-server/internal/domain"

	"github.com/google/uuid"
)

// HandleInlineResponse обрабатывает запрос на обработку inline_response и сохраняет изменения в состоянии
func (h *NovelHandler) HandleInlineResponse(w http.ResponseWriter, r *http.Request) {
	// Проверяем метод запроса
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "Only POST method is allowed")
		return
	}

	// Получаем user_id из контекста
	userID, ok := r.Context().Value(auth.UserIDKey).(string)
	if !ok || userID == "" {
		log.Printf("[API] HandleInlineResponse - Error: userID not found in context or empty")
		respondWithError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}

	// Декодируем запрос
	var request domain.InlineResponseRequest
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	defer r.Body.Close()

	// Проверяем наличие обязательных полей
	if request.NovelID == uuid.Nil {
		respondWithError(w, http.StatusBadRequest, "novel_id is required")
		return
	}

	if request.ChoiceID == "" {
		respondWithError(w, http.StatusBadRequest, "choice_id is required")
		return
	}

	if request.ChoiceText == "" {
		respondWithError(w, http.StatusBadRequest, "choice_text is required")
		return
	}

	// Получаем текущее состояние из репозитория через сервис
	result, err := h.novelContentService.HandleInlineResponse(r.Context(), userID, request)
	if err != nil {
		log.Printf("[API] HandleInlineResponse - Error processing inline response: %v", err)
		respondWithError(w, http.StatusInternalServerError, "Failed to process inline response")
		return
	}

	// Возвращаем результат
	respondWithJSON(w, http.StatusOK, result)
}
