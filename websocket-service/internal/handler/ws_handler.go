package handler

import (
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
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
	manager *ConnectionManager
}

// NewWebSocketHandler создает новый обработчик WebSocket.
func NewWebSocketHandler(manager *ConnectionManager) *WebSocketHandler {
	return &WebSocketHandler{manager: manager}
}

// Handle обрабатывает входящий HTTP запрос для WebSocket.
func (h *WebSocketHandler) Handle(c echo.Context) error {
	userID := c.Get("user_id") // Получаем user_id из контекста, установленного JWT middleware
	if userID == nil || userID.(string) == "" {
		log.Println("Ошибка: user_id не найден в контексте JWT")
		return c.String(http.StatusUnauthorized, "Unauthorized: user_id not found")
	}
	userIDStr := userID.(string)

	conn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		log.Printf("Ошибка обновления до WebSocket для UserID=%s: %v", userIDStr, err)
		// Echo уже отправил ответ при ошибке Upgrade, так что просто возвращаем ошибку
		return err
	}

	log.Printf("WebSocket соединение установлено для UserID=%s", userIDStr)

	client := &Client{
		UserID: userIDStr,
		Conn:   conn,
		send:   make(chan []byte, 256), // Буферизованный канал для отправки
	}

	h.manager.RegisterClient(client)

	// Запускаем горутины для чтения и записи в этом соединении
	go client.writePump(h.manager)
	go client.readPump(h.manager)

	// Возвращаем nil, так как соединение установлено и управляется горутинами
	return nil
}

// readPump откачивает сообщения от WebSocket соединения.
func (c *Client) readPump(manager *ConnectionManager) {
	defer func() {
		manager.UnregisterClient(c.UserID)
		_ = c.Conn.Close() // Закрываем соединение при выходе из readPump
		log.Printf("readPump завершен для UserID=%s", c.UserID)
	}()
	c.Conn.SetReadLimit(maxMessageSize)
	_ = c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error {
		_ = c.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// Цикл чтения сообщений от клиента (в данной реализации он пуст,
	// так как мы только отправляем уведомления)
	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("Ошибка чтения WebSocket для UserID=%s: %v", c.UserID, err)
			} else {
				log.Printf("WebSocket соединение закрыто для UserID=%s (ожидаемое закрытие): %v", c.UserID, err)
			}
			break // Выход из цикла при любой ошибке чтения
		}
		// В этой версии мы не ожидаем сообщений от клиента, но можно добавить обработку здесь
		log.Printf("Получено сообщение от UserID=%s: %s (игнорируется)", c.UserID, message)
	}
}

// writePump откачивает сообщения из канала send в WebSocket соединение.
func (c *Client) writePump(manager *ConnectionManager) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		// Не закрываем соединение здесь, так как readPump может быть еще активен
		// manager.UnregisterClient(c.UserID) // Дерегистрация происходит в readPump
		log.Printf("writePump завершен для UserID=%s", c.UserID)
	}()
	for {
		select {
		case message, ok := <-c.send:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Канал send был закрыт менеджером (клиент дерегистрирован)
				log.Printf("Канал send закрыт для UserID=%s, отправляем CloseMessage", c.UserID)
				_ = c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return // Выход из writePump
			}

			w, err := c.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				log.Printf("Ошибка получения NextWriter для UserID=%s: %v", c.UserID, err)
				return // Выход из writePump
			}
			_, err = w.Write(message)
			if err != nil {
				log.Printf("Ошибка записи сообщения для UserID=%s: %v", c.UserID, err)
				// Не выходим сразу, пытаемся закрыть writer
			}

			// Если были еще сообщения в очереди, добавить их в текущий writer
			n := len(c.send)
			for i := 0; i < n; i++ {
				_, err = w.Write([]byte("\n")) // Разделитель сообщений (если нужно)
				if err != nil {
					log.Printf("Ошибка записи разделителя для UserID=%s: %v", c.UserID, err)
					_ = w.Close() // Закрываем writer при ошибке
					return
				}
				queuedMsg := <-c.send
				_, err = w.Write(queuedMsg)
				if err != nil {
					log.Printf("Ошибка записи сообщения из очереди для UserID=%s: %v", c.UserID, err)
					_ = w.Close() // Закрываем writer при ошибке
					return
				}
			}

			if err := w.Close(); err != nil {
				log.Printf("Ошибка закрытия writer для UserID=%s: %v", c.UserID, err)
				return // Выход из writePump при ошибке закрытия
			}

		case <-ticker.C:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("Ошибка отправки Ping для UserID=%s: %v", c.UserID, err)
				return // Выход из writePump при ошибке пинга
			}
		}
	}
}
