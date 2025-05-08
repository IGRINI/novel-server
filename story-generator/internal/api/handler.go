package api

import (
	"encoding/json"
	"log"
	"net/http"
	"novel-server/shared/models"
	"novel-server/story-generator/internal/config"
	"novel-server/story-generator/internal/service"
	"slices"
)

// APIHandler обрабатывает HTTP запросы к API генератора.
type APIHandler struct {
	aiClient service.AIClient
	config   *config.Config
}

// NewAPIHandler создает новый экземпляр APIHandler.
func NewAPIHandler(aiClient service.AIClient, cfg *config.Config) *APIHandler {
	if cfg == nil {
		log.Fatal("APIHandler: Конфигурация не должна быть nil")
	}
	return &APIHandler{
		aiClient: aiClient,
		config:   cfg,
	}
}

// GetPort возвращает порт HTTP сервера из конфигурации.
func (h *APIHandler) GetPort() string {
	return h.config.HTTPServerPort
}

// RegisterRoutes регистрирует все HTTP пути для API.
func (h *APIHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/generate", h.corsMiddleware(h.HandleGenerate))

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status": "ok"}`))
	})
}

// corsMiddleware обрабатывает CORS preflight (OPTIONS) и устанавливает заголовки для основных запросов.
func (h *APIHandler) corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := false

		if h.config == nil || len(h.config.AllowedOrigins) == 0 {
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next(w, r)
			return
		}

		isWildcardAllowed := slices.Contains(h.config.AllowedOrigins, "*")
		isOriginAllowed := slices.Contains(h.config.AllowedOrigins, origin)

		if isWildcardAllowed {
			allowed = true
			origin = "*"
		} else if origin != "" && isOriginAllowed {
			allowed = true
		}

		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}

		if r.Method == http.MethodOptions {
			if allowed {
				w.WriteHeader(http.StatusNoContent)
			} else {
				http.Error(w, "CORS Rejected", http.StatusForbidden)
			}
			return
		}

		next(w, r)
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

func (h *APIHandler) HandleGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	var reqPayload generateStreamRequest
	if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
		http.Error(w, "Ошибка чтения тела запроса: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	log.Printf("API: Получен запрос на /generate (Non-Streaming) (System: %d chars, User: %d chars, Temp: %v, MaxTokens: %v, TopP: %v)",
		len(reqPayload.SystemPrompt), len(reqPayload.UserPrompt), reqPayload.Temperature, reqPayload.MaxTokens, reqPayload.TopP)

	genParams := service.GenerationParams{
		Temperature: reqPayload.Temperature,
		MaxTokens:   reqPayload.MaxTokens,
		TopP:        reqPayload.TopP,
	}

	generatedText, usageInfo, err := h.aiClient.GenerateText(r.Context(), "api_user", reqPayload.SystemPrompt, reqPayload.UserPrompt, models.PromptType(""), genParams)
	if err != nil {
		log.Printf("API: Ошибка генерации текста от AI: %v", err)
		http.Error(w, "Ошибка генерации текста от AI: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("API: Генерация (Non-Streaming) завершена. Usage: PromptTokens=%d, CompletionTokens=%d, TotalTokens=%d, Cost=%.6f USD",
		usageInfo.PromptTokens,
		usageInfo.CompletionTokens,
		usageInfo.TotalTokens,
		usageInfo.EstimatedCostUSD)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write([]byte(generatedText))
	if err != nil {
		log.Printf("API: Ошибка записи ответа клиенту: %v", err)
	}
}
