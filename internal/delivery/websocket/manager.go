package websocket

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// WebSocketManager управляет WebSocket-соединениями
type WebSocketManager struct {
	clients    map[uuid.UUID]*Client
	register   chan *Client
	unregister chan *Client
	broadcast  chan Message
	mu         sync.RWMutex
}

// Client представляет WebSocket-клиента
type Client struct {
	ID      uuid.UUID
	UserID  string
	Conn    *websocket.Conn
	Manager *WebSocketManager
	Send    chan []byte
	Topics  map[string]bool
}

// Message представляет сообщение для отправки через WebSocket
type Message struct {
	Type    string      `json:"type"`
	Topic   string      `json:"topic"`
	Payload interface{} `json:"payload"`
	Target  string      `json:"target,omitempty"` // ID пользователя или "broadcast"
}

// Настройки для WebSocket-соединения
var (
	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true // В продакшене следует настроить проверку на разрешенные источники
		},
	}
)

// NewWebSocketManager создает новый экземпляр WebSocketManager
func NewWebSocketManager() *WebSocketManager {
	return &WebSocketManager{
		clients:    make(map[uuid.UUID]*Client),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan Message),
	}
}

// Start запускает WebSocketManager в отдельной горутине
func (m *WebSocketManager) Start() {
	go m.run()
}

// run обрабатывает все операции WebSocketManager
func (m *WebSocketManager) run() {
	for {
		select {
		case client := <-m.register:
			m.mu.Lock()
			m.clients[client.ID] = client
			m.mu.Unlock()
			log.Printf("WebSocket: клиент %s подключен", client.ID)

		case client := <-m.unregister:
			m.mu.Lock()
			if _, ok := m.clients[client.ID]; ok {
				close(client.Send)
				delete(m.clients, client.ID)
				log.Printf("WebSocket: клиент %s отключен", client.ID)
			}
			m.mu.Unlock()

		case message := <-m.broadcast:
			// Преобразуем сообщение в JSON
			data, err := json.Marshal(message)
			if err != nil {
				log.Printf("WebSocket: ошибка маршалинга сообщения: %v", err)
				continue
			}

			// В зависимости от цели, отправляем сообщение конкретному пользователю или всем
			m.mu.RLock()
			if message.Target != "" && message.Target != "broadcast" {
				// Отправка конкретному пользователю
				for _, client := range m.clients {
					if client.UserID == message.Target && client.IsSubscribed(message.Topic) {
						select {
						case client.Send <- data:
						default:
							close(client.Send)
							delete(m.clients, client.ID)
						}
					}
				}
			} else {
				// Широковещательная рассылка всем подписанным на тему
				for _, client := range m.clients {
					if client.IsSubscribed(message.Topic) {
						select {
						case client.Send <- data:
						default:
							close(client.Send)
							delete(m.clients, client.ID)
						}
					}
				}
			}
			m.mu.RUnlock()
		}
	}
}

// Handler обрабатывает новые WebSocket-соединения
func (m *WebSocketManager) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Получаем ID пользователя из запроса
		userID := r.URL.Query().Get("user_id")
		if userID == "" {
			http.Error(w, "Отсутствует user_id", http.StatusBadRequest)
			return
		}

		// Апгрейд HTTP-соединения до WebSocket
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("WebSocket: ошибка апгрейда соединения: %v", err)
			return
		}

		// Создаем нового клиента
		clientID := uuid.New()
		client := &Client{
			ID:      clientID,
			UserID:  userID,
			Conn:    conn,
			Manager: m,
			Send:    make(chan []byte, 256),
			Topics:  make(map[string]bool),
		}

		// Подписываем клиента на канал уведомлений о задачах
		client.Topics["tasks"] = true

		// Регистрируем клиента
		m.register <- client

		// Запускаем горутины для чтения и записи
		go client.readPump()
		go client.writePump()
	})
}

// SendToUser отправляет сообщение конкретному пользователю
func (m *WebSocketManager) SendToUser(userID, messageType, topic string, payload interface{}) {
	m.broadcast <- Message{
		Type:    messageType,
		Topic:   topic,
		Payload: payload,
		Target:  userID,
	}
}

// Broadcast отправляет сообщение всем подключенным клиентам, подписанным на указанную тему
func (m *WebSocketManager) Broadcast(messageType, topic string, payload interface{}) {
	m.broadcast <- Message{
		Type:    messageType,
		Topic:   topic,
		Payload: payload,
		Target:  "broadcast",
	}
}

// readPump обрабатывает входящие сообщения от клиента
func (c *Client) readPump() {
	defer func() {
		c.Manager.unregister <- c
		c.Conn.Close()
	}()

	c.Conn.SetReadLimit(512) // Ограничиваем размер входящих сообщений
	c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket: ошибка чтения: %v", err)
			}
			break
		}

		// Обрабатываем команды от клиента (подписка/отписка от тем)
		var cmd struct {
			Action string `json:"action"`
			Topic  string `json:"topic"`
		}

		if err := json.Unmarshal(message, &cmd); err != nil {
			log.Printf("WebSocket: ошибка разбора команды: %v", err)
			continue
		}

		switch cmd.Action {
		case "subscribe":
			c.Subscribe(cmd.Topic)
		case "unsubscribe":
			c.Unsubscribe(cmd.Topic)
		}
	}
}

// writePump отправляет сообщения клиенту
func (c *Client) writePump() {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				// Канал закрыт, отправляем сообщение о закрытии
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Добавляем в очередь сообщения, которые ожидают отправки
			n := len(c.Send)
			for i := 0; i < n; i++ {
				w.Write([]byte{'\n'})
				w.Write(<-c.Send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// Subscribe подписывает клиента на тему
func (c *Client) Subscribe(topic string) {
	c.Topics[topic] = true
}

// Unsubscribe отписывает клиента от темы
func (c *Client) Unsubscribe(topic string) {
	delete(c.Topics, topic)
}

// IsSubscribed проверяет, подписан ли клиент на тему
func (c *Client) IsSubscribed(topic string) bool {
	return c.Topics[topic]
}
