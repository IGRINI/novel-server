package novel_handlers

import (
	"encoding/json"
	"net/http"
	"novel-server/internal/logger"
	"novel-server/internal/service"
)

// NovelHandler представляет обработчик запросов для генерации новеллы
type NovelHandler struct {
	novelService        *service.NovelService
	novelContentService *service.NovelContentService
}

// NewNovelHandler создает новый экземпляр обработчика
func NewNovelHandler(novelService *service.NovelService, novelContentService *service.NovelContentService) *NovelHandler {
	return &NovelHandler{
		novelService:        novelService,
		novelContentService: novelContentService,
	}
}

// RegisterRoutes регистрирует маршруты обработчика
func (h *NovelHandler) RegisterRoutes(mux *http.ServeMux, basePath string) {
	mux.HandleFunc(basePath+"/auth/token", h.Authenticate)

	// Новые маршруты для работы с черновиками
	mux.HandleFunc(basePath+"/create-draft", AuthMiddleware(h.CreateNovelDraft))
	mux.HandleFunc(basePath+"/confirm-draft", AuthMiddleware(h.ConfirmNovelDraft))
	mux.HandleFunc(basePath+"/refine-draft", AuthMiddleware(h.RefineNovelDraft))

	// Для обратной совместимости используем тот же обработчик CreateNovelDraft
	// TODO: удалить после перехода всех клиентов на новый API
	mux.HandleFunc(basePath+"/generate-novel", AuthMiddleware(h.CreateNovelDraft))

	// Остальные существующие маршруты
	mux.HandleFunc(basePath+"/generate-novel-content", AuthMiddleware(h.GenerateNovelContent))
	mux.HandleFunc(basePath+"/novel-action", AuthMiddleware(h.HandleNovelAction))
	mux.HandleFunc(basePath+"/inline-response", AuthMiddleware(h.HandleInlineResponse))
	mux.HandleFunc(basePath+"/novels", AuthMiddleware(h.ListNovels))
	mux.HandleFunc(basePath+"/novel-details", h.GetNovelDetails)
}

// respondWithError отправляет ошибку в формате JSON
func respondWithError(w http.ResponseWriter, code int, message string) {
	respondWithJSON(w, code, map[string]string{"error": message})
}

// respondWithJSON отправляет ответ в формате JSON
func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	response, err := json.Marshal(payload)
	if err != nil {
		logger.Logger.Error("Error marshaling JSON", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(response)
}
