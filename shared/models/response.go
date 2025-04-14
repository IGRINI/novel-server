package models

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse - стандартная структура для ответа об ошибке в формате JSON.
type ErrorResponse struct {
	Error string `json:"error"`
}

// SendJSONError отправляет стандартизированный ответ об ошибке в формате JSON.
// Устанавливает Content-Type в application/json и пишет тело ответа с указанным сообщением и кодом состояния.
func SendJSONError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(ErrorResponse{Error: message})
}

// SendJSONResponse отправляет успешный ответ в формате JSON.
// Устанавливает Content-Type в application/json, код состояния 200 OK (или другой указанный)
// и кодирует переданные данные в JSON.
func SendJSONResponse(w http.ResponseWriter, data interface{}, statusCode int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	} // Если data == nil, просто возвращаем статус без тела
}
