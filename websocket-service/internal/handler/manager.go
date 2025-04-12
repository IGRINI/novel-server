package handler

import (
	"log"
	"sync"

	"github.com/gorilla/websocket"
)

// Client представляет собой одно WebSocket соединение с идентификатором пользователя.
type Client struct {
	UserID string
	Conn   *websocket.Conn
	send   chan []byte // Канал для отправки сообщений этому клиенту
}

// ConnectionManager управляет активными WebSocket соединениями.
type ConnectionManager struct {
	clients    map[string]*Client // Карта userID -> Client
	register   chan *Client       // Канал для регистрации нового клиента
	unregister chan string        // Канал для удаления клиента (по userID)
	broadcast  chan []byte        // Канал для отправки сообщения всем клиентам (если нужно)
	mu         sync.RWMutex       // Мьютекс для защиты доступа к clients
}

// NewConnectionManager создает и запускает новый менеджер соединений.
func NewConnectionManager() *ConnectionManager {
	m := &ConnectionManager{
		clients:    make(map[string]*Client),
		register:   make(chan *Client),
		unregister: make(chan string),
		broadcast:  make(chan []byte),
	}
	go m.run() // Запускаем цикл управления в отдельной горутине
	return m
}

// run запускает основной цикл менеджера для обработки регистрации/дерегистрации.
func (m *ConnectionManager) run() {
	log.Println("ConnectionManager запущен")
	for {
		select {
		case client := <-m.register:
			log.Printf("Регистрация клиента: UserID=%s", client.UserID)
			m.mu.Lock()
			// Если клиент с таким UserID уже есть, закрываем старое соединение
			if oldClient, ok := m.clients[client.UserID]; ok {
				log.Printf("Закрытие старого соединения для UserID=%s", client.UserID)
				close(oldClient.send)
				_ = oldClient.Conn.Close() // Игнорируем ошибку закрытия
			}
			m.clients[client.UserID] = client
			m.mu.Unlock()

		case userID := <-m.unregister:
			m.mu.Lock()
			if client, ok := m.clients[userID]; ok {
				log.Printf("Дерегистрация клиента: UserID=%s", userID)
				delete(m.clients, userID)
				close(client.send)
				// Соединение закрывается в readPump/writePump клиента
			}
			m.mu.Unlock()

			// Обработка broadcast (если нужна)
			// case message := <-m.broadcast:
			// 	m.mu.RLock()
			// 	for _, client := range m.clients {
			// 		select {
			// 		case client.send <- message:
			// 		default:
			// 			log.Printf("Очередь отправки для UserID=%s переполнена", client.UserID)
			// 			// Рассмотреть закрытие соединения или другие действия
			// 		}
			// 	}
			// 	m.mu.RUnlock()
		}
	}
}

// RegisterClient регистрирует нового клиента.
func (m *ConnectionManager) RegisterClient(client *Client) {
	m.register <- client
}

// UnregisterClient удаляет клиента.
func (m *ConnectionManager) UnregisterClient(userID string) {
	m.unregister <- userID
}

// SendToUser отправляет сообщение конкретному пользователю.
// Возвращает true, если пользователь онлайн и сообщение отправлено в канал, иначе false.
func (m *ConnectionManager) SendToUser(userID string, message []byte) bool {
	m.mu.RLock()
	client, ok := m.clients[userID]
	m.mu.RUnlock()

	if ok {
		select {
		case client.send <- message:
			log.Printf("Сообщение поставлено в очередь для UserID=%s", userID)
			return true
		default:
			// Канал переполнен или закрыт (клиент отключается)
			log.Printf("Не удалось отправить сообщение UserID=%s: очередь переполнена или клиент отключается", userID)
			// Можно инициировать дерегистрацию отсюда, если нужно
			// go m.UnregisterClient(userID)
			return false
		}
	} else {
		log.Printf("Пользователь UserID=%s не найден (оффлайн)", userID)
		return false
	}
}
