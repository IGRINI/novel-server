package messaging

import (
	"encoding/json"
	"fmt"
	"log" // Импортируем общую структуру
	"novel-server/websocket-service/internal/handler"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Consumer отвечает за получение сообщений из RabbitMQ и их обработку.
type Consumer struct {
	conn        *amqp.Connection
	manager     *handler.ConnectionManager
	queueName   string
	stopChannel chan struct{} // Канал для остановки консьюмера
}

// NewConsumer создает нового консьюмера RabbitMQ.
func NewConsumer(conn *amqp.Connection, manager *handler.ConnectionManager, queueName string) (*Consumer, error) {
	return &Consumer{
		conn:        conn,
		manager:     manager,
		queueName:   queueName,
		stopChannel: make(chan struct{}),
	}, nil
}

// StartConsuming начинает прослушивание очереди уведомлений.
// Эта функция блокирующая, поэтому ее следует запускать в отдельной горутине.
func (c *Consumer) StartConsuming() error {
	ch, err := c.conn.Channel()
	if err != nil {
		return fmt.Errorf("не удалось открыть канал RabbitMQ: %w", err)
	}
	defer ch.Close()

	// Объявляем очередь (на случай, если она еще не создана story-generator)
	// Важно: параметры должны совпадать с теми, что в story-generator (особенно durable=true)
	q, err := ch.QueueDeclare(
		c.queueName, // Используем имя из структуры Consumer, которое было передано из конфига
		true,        // durable
		false,       // delete when unused
		false,       // exclusive
		false,       // no-wait
		nil,         // arguments
	)
	if err != nil {
		return fmt.Errorf("не удалось объявить очередь '%s': %w", c.queueName, err)
	}
	log.Printf("Очередь уведомлений '%s' успешно объявлена/найдена", q.Name)

	// Устанавливаем QoS (prefetch count = 1), чтобы обрабатывать по одному сообщению за раз
	err = ch.Qos(1, 0, false)
	if err != nil {
		return fmt.Errorf("не удалось установить QoS: %w", err)
	}
	log.Println("QoS (prefetch count=1) установлен для консьюмера")

	msgs, err := ch.Consume(
		q.Name,
		"websocket-service-consumer", // consumer tag
		false,                        // auto-ack (false, т.к. будем подтверждать вручную)
		false,                        // exclusive
		false,                        // no-local (не поддерживается RabbitMQ)
		false,                        // no-wait
		nil,                          // args
	)
	if err != nil {
		return fmt.Errorf("не удалось зарегистрировать консьюмера: %w", err)
	}

	log.Printf("Консьюмер запущен, ожидание уведомлений из очереди '%s'...", q.Name)

	for {
		select {
		case d, ok := <-msgs:
			if !ok {
				log.Println("Канал сообщений RabbitMQ закрыт")
				return nil // Нормальное завершение, если канал закрыт
			}

			log.Printf("Получено уведомление (DeliveryTag: %d): %s", d.DeliveryTag, d.Body)

			// Получаем UserID из сообщения. Структура сообщения должна быть известна.
			// Предполагаем, что сообщение содержит поле "user_id" или аналогичное.
			// !!! Важно: Необходимо определить общую структуру для ClientStoryUpdate !!!
			var clientUpdate map[string]interface{} // Используем map пока нет структуры
			err = json.Unmarshal(d.Body, &clientUpdate)
			if err != nil {
				log.Printf("Ошибка десериализации тела сообщения для извлечения UserID: %v. Nack.", err)
				_ = d.Nack(false, false)
				continue
			}

			userIDRaw, ok := clientUpdate["user_id"]
			if !ok {
				log.Printf("Поле 'user_id' не найдено в сообщении из очереди %s. Nack.", c.queueName)
				_ = d.Nack(false, false)
				continue
			}
			userIDStr, ok := userIDRaw.(string) // UserID должен быть строкой
			if !ok {
				// Попытка преобразовать из float64 (если JSON парсер преобразовал число)
				if userIDFloat, okFloat := userIDRaw.(float64); okFloat {
					userIDStr = fmt.Sprintf("%.0f", userIDFloat)
					ok = true
				} else {
					log.Printf("Поле 'user_id' в сообщении из очереди %s имеет неверный тип (%T). Nack.", c.queueName, userIDRaw)
					_ = d.Nack(false, false)
					continue
				}
			}

			// Пытаемся отправить уведомление пользователю через ConnectionManager
			sent := c.manager.SendToUser(userIDStr, d.Body) // Отправляем исходный JSON

			if sent {
				log.Printf("Уведомление успешно отправлено UserID=%s из очереди %s", userIDStr, c.queueName)
				_ = d.Ack(false) // Подтверждаем сообщение
			} else {
				// Пользователь не найден (оффлайн) или произошла ошибка отправки
				log.Printf("Не удалось отправить уведомление UserID=%s из очереди %s (оффлайн или ошибка). Nack.", userIDStr, c.queueName)
				_ = d.Nack(false, false)
			}

		case <-c.stopChannel:
			log.Println("Получен сигнал остановки консьюмера RabbitMQ")
			// Можно добавить логику отмены консьюмера через ch.Cancel(), если необходимо
			return nil
		}
	}
}

// Stop останавливает консьюмер.
func (c *Consumer) Stop() {
	log.Println("Остановка консьюмера RabbitMQ...")
	close(c.stopChannel)
	// Дальнейшая логика остановки (например, ожидание завершения) может быть добавлена здесь
}
