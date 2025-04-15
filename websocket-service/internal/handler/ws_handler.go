package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"

	"novel-server/websocket-service/internal/service" // Добавляем импорт service
)

const (
	// Время, разрешенное для записи сообщения клиенту.
	writeWait = 10 * time.Second
	// Время, разрешенное для чтения следующего pong сообщения от клиента.
	pongWait = 60 * time.Second
	// Отправлять пинги клиенту с этим периодом. Должно быть меньше pongWait.
	pingPeriod = (pongWait * 9) / 10
	// Максимальный размер сообщения, разрешенный от клиента.
	maxMessageSize = 512
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Проверяем origin запроса (в продакшене здесь должна быть проверка)
	CheckOrigin: func(r *http.Request) bool {
		// TODO: Добавить проверку Origin для безопасности
		return true
	},
}

// WebSocketHandler обрабатывает запросы на установку WebSocket соединения.
type WebSocketHandler struct {
	manager     *ConnectionManager
	authService *service.AuthService // Добавляем зависимость от AuthService
	logger      zerolog.Logger       // Добавляем логгер
}

// NewWebSocketHandler создает новый обработчик WebSocket.
func NewWebSocketHandler(manager *ConnectionManager, authService *service.AuthService, logger zerolog.Logger) *WebSocketHandler {
	return &WebSocketHandler{
		manager:     manager,
		authService: authService,
		logger:      logger.With().Str("component", "WebSocketHandler").Logger(),
	}
}

// ServeWS обрабатывает входящий HTTP запрос для WebSocket.
func (h *WebSocketHandler) ServeWS(w http.ResponseWriter, r *http.Request) {
	// Извлекаем токен из query-параметра 'token'
	tokenString := r.URL.Query().Get("token")
	if tokenString == "" {
		h.logger.Warn().Msg("Missing 'token' query parameter")
		http.Error(w, "Unauthorized: Missing token", http.StatusUnauthorized)
		return
	}

	// Валидируем токен и извлекаем UserID
	claims, err := h.validateToken(tokenString)
	if err != nil {
		h.logger.Warn().Err(err).Str("token", tokenString).Msg("Invalid token")
		http.Error(w, fmt.Sprintf("Unauthorized: %s", err.Error()), http.StatusUnauthorized)
		return
	}

	userID, ok := claims["sub"].(string) // "sub" обычно используется для User ID
	if !ok || userID == "" {
		h.logger.Error().Interface("claims", claims).Msg("UserID ('sub') not found or empty in token claims")
		http.Error(w, "Unauthorized: Invalid token claims", http.StatusUnauthorized)
		return
	}

	// Обновляем соединение до WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error().Err(err).Str("userID", userID).Msg("Failed to upgrade connection")
		// Не пишем ошибку в http.ResponseWriter, так как upgrader уже это сделал
		return
	}

	h.logger.Info().Str("userID", userID).Msg("WebSocket connection established")

	client := &Client{
		UserID: userID,
		Conn:   conn,
		send:   make(chan []byte, 256), // Буферизованный канал для отправки
	}

	h.manager.RegisterClient(client)

	// Запускаем горутины для чтения и записи в этом соединении
	go client.writePump(h.manager, h.logger.With().Str("userID", userID).Logger())
	go client.readPump(h.manager, h.logger.With().Str("userID", userID).Logger())
}

// validateToken проверяет JWT токен и возвращает claims.
func (h *WebSocketHandler) validateToken(tokenString string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		// Проверяем метод подписи
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		// Возвращаем секрет из AuthService
		return h.authService.GetJWTSecret(), nil
	})

	if err != nil {
		return nil, fmt.Errorf("token parse error: %w", err)
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		// Дополнительные проверки (например, время жизни 'exp') выполняются библиотекой
		return claims, nil
	} else {
		return nil, fmt.Errorf("invalid token")
	}
}

// readPump откачивает сообщения от WebSocket соединения.
func (c *Client) readPump(manager *ConnectionManager, logger zerolog.Logger) {
	defer func() {
		manager.UnregisterClient(c.UserID)
		_ = c.Conn.Close() // Закрываем соединение при выходе из readPump
		logger.Info().Msg("readPump finished")
	}()
	c.Conn.SetReadLimit(maxMessageSize)
	_ = c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error {
		logger.Debug().Msg("Pong received")
		_ = c.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logger.Warn().Err(err).Msg("WebSocket read error")
			} else {
				logger.Info().Msg("WebSocket connection closed (expected)")
			}
			break
		}
		logger.Warn().Bytes("message", message).Msg("Received unexpected message from client (ignored)")
	}
}

// writePump откачивает сообщения из канала send в WebSocket соединение.
func (c *Client) writePump(manager *ConnectionManager, logger zerolog.Logger) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		logger.Info().Msg("writePump finished")
	}()
	for {
		select {
		case message, ok := <-c.send:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				logger.Info().Msg("Send channel closed, sending CloseMessage")
				_ = c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				logger.Error().Err(err).Msg("Failed to get next writer")
				return
			}

			logger.Debug().Int("messageSize", len(message)).Msg("Sending message")
			_, err = w.Write(message)
			if err != nil {
				logger.Error().Err(err).Msg("Failed to write message")
				// Пытаемся закрыть writer даже при ошибке записи
			}

			// Отправляем все сообщения из очереди за раз
			n := len(c.send)
			for i := 0; i < n; i++ {
				queuedMsg := <-c.send
				logger.Debug().Int("messageSize", len(queuedMsg)).Int("queueNum", i+1).Msg("Sending queued message")
				_, err = w.Write([]byte("\n")) // Используем newline как разделитель, если клиент поддерживает
				if err != nil {
					logger.Error().Err(err).Msg("Failed to write newline separator")
					_ = w.Close() // Закрываем writer при ошибке
					return
				}
				_, err = w.Write(queuedMsg)
				if err != nil {
					logger.Error().Err(err).Msg("Failed to write queued message")
					_ = w.Close() // Закрываем writer при ошибке
					return
				}
			}

			if err := w.Close(); err != nil {
				logger.Error().Err(err).Msg("Failed to close writer")
				return
			}

		case <-ticker.C:
			logger.Debug().Msg("Sending ping")
			_ = c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				logger.Warn().Err(err).Msg("Failed to send ping")
				return
			}
		}
	}
}
