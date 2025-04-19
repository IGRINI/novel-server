package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"novel-server/story-generator/internal/service"
)

// APIHandler обрабатывает HTTP запросы к API генератора.
type APIHandler struct {
	aiClient service.AIClient
	// Можно добавить логгер, если нужен кастомный
}

// NewAPIHandler создает новый экземпляр APIHandler.
func NewAPIHandler(aiClient service.AIClient) *APIHandler {
	return &APIHandler{
		aiClient: aiClient,
	}
}

// generateStreamRequest - структура для парсинга JSON из тела запроса.
type generateStreamRequest struct {
	SystemPrompt string `json:"system_prompt"`
	UserPrompt   string `json:"user_prompt"`
	// Добавляем параметры генерации (используем указатели, чтобы отличить 0/0.0 от отсутствия)
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
}

// HandleGenerateStream обрабатывает POST /generate/stream
func (h *APIHandler) HandleGenerateStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	// Парсим JSON тело запроса
	var reqPayload generateStreamRequest
	if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
		http.Error(w, "Ошибка чтения тела запроса: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	log.Printf("API: Получен запрос на /generate/stream (System: %d chars, User: %d chars, Temp: %v, MaxTokens: %v, TopP: %v)",
		len(reqPayload.SystemPrompt),
		len(reqPayload.UserPrompt),
		reqPayload.Temperature, // Логируем полученные параметры
		reqPayload.MaxTokens,
		reqPayload.TopP,
	)

	// Устанавливаем заголовки для стриминга простого текста
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*") // TODO: Ограничить для production!

	// <<< Передаем параметры в AI клиент >>>
	// Создаем структуру с параметрами
	genParams := service.GenerationParams{
		Temperature: reqPayload.Temperature,
		MaxTokens:   reqPayload.MaxTokens,
		TopP:        reqPayload.TopP,
	}

	// Получаем Flusher для отправки данных по частям
	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Println("API: Ошибка - ResponseWriter не поддерживает Flusher")
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	log.Println("API: Начинаем стримить ответ клиенту...")

	// <<< Вызываем GenerateTextStream с callback-функцией >>>
	err := h.aiClient.GenerateTextStream(r.Context(), "api_user", reqPayload.SystemPrompt, reqPayload.UserPrompt, genParams, func(chunk string) error {
		// Отправляем сырой текст клиенту
		_, writeErr := fmt.Fprint(w, chunk) // Пишем просто текст
		if writeErr != nil {
			log.Printf("API: Ошибка записи клиенту (в callback): %v", writeErr)
			return writeErr // Возвращаем ошибку, чтобы прервать стриминг
		}
		flusher.Flush() // Отправляем немедленно
		return nil      // Успешно обработали чанк
	})

	// <<< Обрабатываем ошибку, возвращенную GenerateTextStream >>>
	if err != nil {
		log.Printf("API: Ошибка во время стриминга от AI или при записи клиенту: %v", err)
		// Если заголовки еще не отправлены, можем отправить HTTP ошибку
		// Проверить, были ли уже записаны данные, сложно без доп. состояния.
		// Просто логируем ошибку, т.к. ответ мог уже частично уйти.
		// http.Error(w, "Ошибка стриминга: "+err.Error(), http.StatusInternalServerError) // Это может не сработать
		return
	}

	log.Println("API: Стриминг клиенту успешно завершен.")
}

// escapeSSEData больше не нужна
/*
func escapeSSEData(data string) string {
	data = strings.ReplaceAll(data, "\r", "") // Убираем одиночные CR
	data = strings.ReplaceAll(data, "\n", "\ndata: ")
	return data
}
*/

// HandleGenerate обрабатывает POST /generate (не-стриминговый)
func (h *APIHandler) HandleGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	// Парсим JSON тело запроса (та же структура)
	var reqPayload generateStreamRequest
	if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
		http.Error(w, "Ошибка чтения тела запроса: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	log.Printf("API: Получен запрос на /generate (Non-Streaming) (System: %d chars, User: %d chars, Temp: %v, MaxTokens: %v, TopP: %v)",
		len(reqPayload.SystemPrompt),
		len(reqPayload.UserPrompt),
		reqPayload.Temperature,
		reqPayload.MaxTokens,
		reqPayload.TopP,
	)

	// <<< Создаем структуру параметров для AI клиента >>>
	genParams := service.GenerationParams{
		Temperature: reqPayload.Temperature,
		MaxTokens:   reqPayload.MaxTokens,
		TopP:        reqPayload.TopP,
	}

	// <<< Вызываем не-стриминговый метод AI клиента >>>
	// generatedText, err := h.aiClient.GenerateText(r.Context(), reqPayload.SystemPrompt, reqPayload.UserPrompt)
	// <<< Исправляем вызов: передаем "api_user" как userID и genParams >>>
	generatedText, err := h.aiClient.GenerateText(r.Context(), "api_user", reqPayload.SystemPrompt, reqPayload.UserPrompt, genParams)
	if err != nil {
		log.Printf("API: Ошибка генерации текста от AI: %v", err)
		http.Error(w, "Ошибка генерации текста от AI: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Отправляем результат как простой текст
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write([]byte(generatedText))
	if err != nil {
		log.Printf("API: Ошибка записи ответа клиенту: %v", err)
	}
	log.Println("API: Ответ (non-streaming) успешно отправлен.")
}
