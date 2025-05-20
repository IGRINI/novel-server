package novel_handlers

import (
	"encoding/json"
	"net/http"
	"novel-server/internal/auth"
	"novel-server/internal/domain"
	"novel-server/internal/logger"

	"github.com/google/uuid"
)

// GenerateNovelContent обрабатывает запрос на генерацию контента новеллы
func (h *NovelHandler) GenerateNovelContent(w http.ResponseWriter, r *http.Request) {
	// Проверяем метод запроса
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "Only POST method is allowed")
		return
	}

	// --- Получаем UserID из контекста (добавлено middleware) ---
	userID, ok := r.Context().Value(auth.UserIDKey).(string)
	if !ok || userID == "" {
		logger.Logger.Error("GenerateNovelContent: userID not found in context")
		respondWithError(w, http.StatusInternalServerError, "Internal server error: User ID missing")
		return
	}
	logger.Logger.Info("GenerateNovelContent: handling request", "userID", userID)
	// ---------------------------------------------------------

	// Декодируем упрощенный запрос от клиента
	var simplifiedRequest domain.SimplifiedNovelContentRequest
	err := json.NewDecoder(r.Body).Decode(&simplifiedRequest)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	defer r.Body.Close()

	// Проверяем наличие обязательного поля novelID
	if simplifiedRequest.NovelID == uuid.Nil {
		respondWithError(w, http.StatusBadRequest, "novel_id is required")
		return
	}

	// Преобразуем упрощенный запрос в полный для сервиса
	fullRequest := domain.NovelContentRequest{
		NovelID:               simplifiedRequest.NovelID,
		UserID:                userID, // UserID берем из JWT токена
		UserChoice:            simplifiedRequest.UserChoice,
		RestartFromSceneIndex: simplifiedRequest.RestartFromSceneIndex,
	}

	// Генерируем контент новеллы
	fullResponse, err := h.novelContentService.GenerateNovelContent(r.Context(), fullRequest)
	if err != nil {
		logger.Logger.Error("Error generating novel content", "err", err)
		respondWithError(w, http.StatusInternalServerError, "Failed to generate novel content")
		return
	}

	// Преобразуем полный ответ в упрощенный для клиента
	simplifiedResponse := createSimplifiedResponse(fullResponse)

	// Отправляем упрощенный ответ клиенту
	respondWithJSON(w, http.StatusOK, simplifiedResponse)
}
