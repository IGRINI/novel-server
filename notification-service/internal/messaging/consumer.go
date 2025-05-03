package messaging

import (
	"context"
	"encoding/json"
	"fmt"

	// "novel-server/notification-service/internal/service" // Убираем импорт service
	sharedModels "novel-server/shared/models" // <<< Добавляем импорт
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

// NotificationSender определяет интерфейс для отправки уведомлений.
// Перенесен сюда из пакета service для избежания цикла импорта.
type NotificationSender interface {
	SendNotification(ctx context.Context, payload sharedModels.PushNotificationPayload) error
}

type Consumer struct {
	conn        *amqp.Connection
	logger      *zap.Logger
	queueName   string
	concurrency int
	processor   *Processor // Обработчик сообщений
	stopChannel chan struct{}
	cancelFunc  context.CancelFunc
	wg          sync.WaitGroup
}

func NewConsumer(conn *amqp.Connection, logger *zap.Logger, queueName string, concurrency int, processor *Processor) (*Consumer, error) {
	c := &Consumer{
		conn:        conn,
		logger:      logger.Named("consumer"),
		queueName:   queueName,
		concurrency: concurrency,
		processor:   processor,
		stopChannel: make(chan struct{}),
	}
	return c, nil
}

func (c *Consumer) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancelFunc = cancel

	ch, err := c.conn.Channel()
	if err != nil {
		return fmt.Errorf("не удалось открыть канал RabbitMQ: %w", err)
	}
	defer ch.Close()

	q, err := ch.QueueDeclare(
		c.queueName,
		true,  // durable
		false, // delete when unused
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		return fmt.Errorf("не удалось объявить очередь '%s': %w", c.queueName, err)
	}
	c.logger.Info("Очередь успешно объявлена/найдена", zap.String("queue", q.Name))

	err = ch.Qos(c.concurrency, 0, false) // Ограничиваем количество сообщений в обработке
	if err != nil {
		return fmt.Errorf("не удалось установить QoS: %w", err)
	}

	msgs, err := ch.Consume(
		q.Name,
		"notification-consumer", // consumer tag
		false,                   // auto-ack = false
		false,                   // exclusive
		false,                   // no-local
		false,                   // no-wait
		nil,                     // args
	)
	if err != nil {
		return fmt.Errorf("не удалось зарегистрировать консьюмера: %w", err)
	}

	c.logger.Info("Консьюмер запущен, ожидание сообщений...", zap.Int("concurrency", c.concurrency))

	c.wg.Add(c.concurrency)
	for i := 0; i < c.concurrency; i++ {
		go func(workerID int) {
			defer c.wg.Done()
			logger := c.logger.With(zap.Int("worker_id", workerID))
			logger.Info("Воркер запущен")
			for {
				select {
				case <-ctx.Done():
					logger.Info("Воркер останавливается из-за отмены контекста")
					return
				case <-c.stopChannel:
					logger.Info("Воркер останавливается из-за сигнала stopChannel")
					return
				case d, ok := <-msgs:
					if !ok {
						logger.Info("Канал сообщений закрыт, воркер завершает работу")
						return
					}
					logger.Debug("Получено сообщение", zap.Uint64("delivery_tag", d.DeliveryTag))
					c.processor.ProcessMessage(ctx, d)
				}
			}
		}(i)
	}

	c.logger.Info("Все воркеры консьюмера запущены")
	<-c.stopChannel // Блокируемся до вызова Stop()
	c.logger.Info("Получен сигнал остановки, отменяем контекст воркеров...")
	c.cancelFunc() // Отменяем контекст для всех воркеров

	c.logger.Info("Ожидание завершения всех воркеров...")
	c.wg.Wait() // Ожидаем завершения всех горутин
	c.logger.Info("Все воркеры консьюмера остановлены")
	return nil
}

func (c *Consumer) Stop() {
	c.logger.Info("Инициирована остановка консьюмера...")
	close(c.stopChannel)
}

// Processor обрабатывает входящие сообщения
// (Вынесен для лучшей организации и тестируемости)
type Processor struct {
	logger *zap.Logger
	sender NotificationSender // Используем интерфейс, определенный в этом пакете
}

// Используем интерфейс NotificationSender из этого пакета
func NewProcessor(logger *zap.Logger, sender NotificationSender) *Processor {
	return &Processor{
		logger: logger.Named("processor"),
		sender: sender,
	}
}

func (p *Processor) ProcessMessage(ctx context.Context, d amqp.Delivery) {
	p.logger.Debug("Обработка сообщения", zap.Uint64("delivery_tag", d.DeliveryTag))

	var payload sharedModels.PushNotificationPayload
	if err := json.Unmarshal(d.Body, &payload); err != nil {
		p.logger.Error("Ошибка десериализации JSON",
			zap.Error(err),
			zap.ByteString("body", d.Body),
			zap.Uint64("delivery_tag", d.DeliveryTag))
		// Отклоняем сообщение без повторной постановки в очередь (nack, requeue=false)
		if ackErr := d.Nack(false, false); ackErr != nil {
			p.logger.Error("Ошибка Nack сообщения после ошибки JSON", zap.Error(ackErr), zap.Uint64("delivery_tag", d.DeliveryTag))
		}
		return
	}

	p.logger.Info("Сообщение успешно десериализовано",
		zap.String("user_id", payload.UserID.String()),
		zap.String("title", payload.Notification.Title),
		zap.Uint64("delivery_tag", d.DeliveryTag))

	// Создаем контекст с таймаутом для обработки
	processCtx, cancel := context.WithTimeout(ctx, 30*time.Second) // Таймаут на всю обработку, включая получение токенов и отправку
	defer cancel()

	err := p.sender.SendNotification(processCtx, payload)
	if err != nil {
		p.logger.Error("Ошибка обработки уведомления",
			zap.Error(err),
			zap.String("user_id", payload.UserID.String()),
			zap.Uint64("delivery_tag", d.DeliveryTag))
		// Отклоняем с повторной постановкой в очередь (nack, requeue=true)
		// или без (requeue=false), в зависимости от типа ошибки
		// TODO: Добавить логику определения, можно ли повторить попытку
		requeue := false // Пока не повторяем
		if ackErr := d.Nack(false, requeue); ackErr != nil {
			p.logger.Error("Ошибка Nack сообщения после ошибки обработки", zap.Error(ackErr), zap.Uint64("delivery_tag", d.DeliveryTag))
		}
		return
	}

	// Подтверждаем успешную обработку (ack)
	if ackErr := d.Ack(false); ackErr != nil {
		p.logger.Error("Ошибка Ack сообщения после успешной обработки", zap.Error(ackErr), zap.Uint64("delivery_tag", d.DeliveryTag))
	}
	p.logger.Info("Сообщение успешно обработано и подтверждено (Ack)", zap.Uint64("delivery_tag", d.DeliveryTag))
}
