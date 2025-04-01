package novel_handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"novel-server/internal/auth"
	"novel-server/internal/domain"

	"github.com/google/uuid"
)

// CreateDraftResponse структура ответа для создания черновика, включает ID и полный конфиг
type CreateDraftResponse struct {
	DraftID uuid.UUID          `json:"draft_id"`
	Config  domain.NovelConfig `json:"config"` // Используем полную структуру конфига
}

// CreateNovelDraft обрабатывает запрос на создание черновика новеллы
func (h *NovelHandler) CreateNovelDraft(w http.ResponseWriter, r *http.Request) {
	// Проверяем метод запроса
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "Only POST method is allowed")
		return
	}

	// --- Получаем UserID из контекста (добавлено middleware) ---
	log.Printf("CreateNovelDraft: Attempting to get UserID from context with key '%v'", auth.UserIDKey)
	userID, ok := r.Context().Value(auth.UserIDKey).(string)
	if !ok || userID == "" {
		log.Println("CreateNovelDraft: ERROR - UserID not found in context.")
		respondWithError(w, http.StatusInternalServerError, "Internal server error: User ID missing")
		return
	}
	log.Printf("CreateNovelDraft: Handling request for UserID: %s", userID)
	// ---------------------------------------------------------

	// Декодируем запрос
	var request domain.NovelGenerationRequest
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	defer r.Body.Close()

	// Проверяем наличие обязательных полей
	if request.UserPrompt == "" {
		respondWithError(w, http.StatusBadRequest, "user_prompt is required")
		return
	}

	// Генерируем конфигурацию новеллы и сохраняем как черновик
	draftID, config, err := h.novelService.CreateDraft(r.Context(), userID, request)
	if err != nil {
		log.Printf("Error creating novel draft: %v", err)
		respondWithError(w, http.StatusInternalServerError, "Failed to create novel draft")
		return
	}

	// Создаем ответ с DraftID и полной конфигурацией
	draftResponse := CreateDraftResponse{
		DraftID: draftID,
		Config:  *config, // Возвращаем весь полученный конфиг
	}

	// Отправляем ответ
	respondWithJSON(w, http.StatusOK, draftResponse)
}

// ConfirmNovelDraft обрабатывает подтверждение черновика и запуск генерации новеллы
func (h *NovelHandler) ConfirmNovelDraft(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "Only POST method is allowed")
		return
	}

	// Получаем UserID из контекста
	userID, ok := r.Context().Value(auth.UserIDKey).(string)
	if !ok || userID == "" {
		log.Println("ConfirmNovelDraft: ERROR - UserID not found in context.")
		respondWithError(w, http.StatusInternalServerError, "Internal server error: User ID missing")
		return
	}
	log.Printf("ConfirmNovelDraft: Handling request for UserID: %s", userID)

	// Получаем DraftID из URL или тела запроса
	var request struct {
		DraftID uuid.UUID `json:"draft_id"`
	}

	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	defer r.Body.Close()

	if request.DraftID == uuid.Nil {
		respondWithError(w, http.StatusBadRequest, "draft_id is required")
		return
	}

	// Вызываем сервис для подтверждения черновика
	novelID, err := h.novelService.ConfirmDraft(r.Context(), userID, request.DraftID)
	if err != nil {
		log.Printf("Error confirming draft: %v", err)
		respondWithError(w, http.StatusInternalServerError, "Failed to confirm novel draft")
		return
	}

	// Формируем ответ
	response := struct {
		NovelID uuid.UUID `json:"novel_id"`
		Message string    `json:"message"`
	}{
		NovelID: novelID,
		Message: "Novel draft confirmed and novel created successfully",
	}

	respondWithJSON(w, http.StatusOK, response)
}

// RefineNovelDraft обрабатывает уточнение черновика новеллы
func (h *NovelHandler) RefineNovelDraft(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "Only POST method is allowed")
		return
	}

	// Получаем UserID из контекста
	userID, ok := r.Context().Value(auth.UserIDKey).(string)
	if !ok || userID == "" {
		log.Println("RefineNovelDraft: ERROR - UserID not found in context.")
		respondWithError(w, http.StatusInternalServerError, "Internal server error: User ID missing")
		return
	}
	log.Printf("RefineNovelDraft: Handling request for UserID: %s", userID)

	// Получаем DraftID и дополнительный промпт из тела запроса
	var request struct {
		DraftID          uuid.UUID `json:"draft_id"`
		AdditionalPrompt string    `json:"additional_prompt"`
	}

	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request format")
		return
	}
	defer r.Body.Close()

	if request.DraftID == uuid.Nil {
		respondWithError(w, http.StatusBadRequest, "draft_id is required")
		return
	}

	if request.AdditionalPrompt == "" {
		respondWithError(w, http.StatusBadRequest, "additional_prompt is required")
		return
	}

	// Вызываем сервис для уточнения черновика
	updatedConfig, err := h.novelService.RefineDraft(r.Context(), userID, request.DraftID, request.AdditionalPrompt)
	if err != nil {
		log.Printf("Error refining draft: %v", err)
		respondWithError(w, http.StatusInternalServerError, "Failed to refine novel draft")
		return
	}

	// Создаем ответ с обновленным конфигом и draft_id
	response := struct {
		DraftID uuid.UUID          `json:"draft_id"`
		Config  domain.NovelConfig `json:"config"`
	}{
		DraftID: request.DraftID,
		Config:  *updatedConfig,
	}

	respondWithJSON(w, http.StatusOK, response)
}

// Вспомогательные функции respondWithError и respondWithJSON остаются без изменений
