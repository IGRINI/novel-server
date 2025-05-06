package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	interfaces "novel-server/shared/interfaces"
	sharedMessaging "novel-server/shared/messaging"
	"novel-server/shared/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

const (
	dlqName        = "story_generation_tasks_dlq" // Должно совпадать с именем в story-generator
	consumerTag    = "gameplay-dlq-consumer"
	reconnectDelay = 5 * time.Second
	maxRetries     = 5
)

// DLQConsumer слушает очередь Dead Letter Queue и обрабатывает ошибки генерации.
type DLQConsumer struct {
	conn         *amqp.Connection
	logger       *zap.Logger
	storyRepo    interfaces.PublishedStoryRepository
	db           *pgxpool.Pool
	shutdownChan chan struct{}
}

// NewDLQConsumer создает новый экземпляр DLQConsumer.
func NewDLQConsumer(conn *amqp.Connection, storyRepo interfaces.PublishedStoryRepository, db *pgxpool.Pool, logger *zap.Logger) *DLQConsumer {
	return &DLQConsumer{
		conn:         conn,
		logger:       logger.Named("DLQConsumer"),
		storyRepo:    storyRepo,
		db:           db,
		shutdownChan: make(chan struct{}),
	}
}

// StartConsuming запускает цикл прослушивания DLQ.
func (c *DLQConsumer) StartConsuming() {
	go func() {
		for {
			select {
			case <-c.shutdownChan:
				c.logger.Info("Остановка DLQ consumer...")
				return
			default:
				err := c.consumeMessages()
				if err != nil {
					c.logger.Error("Ошибка в цикле DLQ consumer, повторное подключение через", zap.Duration("delay", reconnectDelay), zap.Error(err))
					time.Sleep(reconnectDelay)
				}
			}
		}
	}()
	c.logger.Info("DLQ Consumer запущен")
}

// Stop останавливает DLQ consumer.
func (c *DLQConsumer) Stop() {
	close(c.shutdownChan)
	// Возможно, потребуется дождаться завершения обработки текущего сообщения
}

func (c *DLQConsumer) consumeMessages() error {
	ch, err := c.conn.Channel()
	if err != nil {
		return fmt.Errorf("не удалось открыть канал: %w", err)
	}
	defer ch.Close()

	// Убедимся, что DLQ существует (на всякий случай)
	_, err = ch.QueueDeclarePassive(dlqName, true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("очередь DLQ '%s' не найдена или не может быть объявлена пассивно: %w", dlqName, err)
	}

	msgs, err := ch.Consume(
		dlqName,     // queue
		consumerTag, // consumer
		false,       // auto-ack (false, т.к. подтверждаем вручную)
		false,       // exclusive
		false,       // no-local
		false,       // no-wait
		nil,         // args
	)
	if err != nil {
		return fmt.Errorf("не удалось зарегистрировать DLQ consumer: %w", err)
	}

	c.logger.Info("Ожидание сообщений в DLQ", zap.String("queue", dlqName))

	for {
		select {
		case <-c.shutdownChan: // Проверяем сигнал остановки
			return nil
		case d, ok := <-msgs:
			if !ok {
				c.logger.Warn("Канал сообщений DLQ закрыт")
				return fmt.Errorf("канал сообщений DLQ закрыт")
			}
			c.handleMessage(d)
		}
	}
}

func (c *DLQConsumer) handleMessage(d amqp.Delivery) {
	log := c.logger.With(zap.String("deliveryTag", fmt.Sprint(d.DeliveryTag)))
	log.Info("Получено сообщение из DLQ")

	var payload sharedMessaging.GenerationTaskPayload
	err := json.Unmarshal(d.Body, &payload)
	if err != nil {
		log.Error("Ошибка десериализации JSON из сообщения DLQ", zap.Error(err))
		// Что делать в этом случае? Вероятно, отбросить сообщение (ack), так как оно некорректно.
		if ackErr := d.Ack(false); ackErr != nil {
			log.Error("Ошибка подтверждения (ack) некорректного сообщения DLQ", zap.Error(ackErr))
		}
		return
	}

	log = log.With(zap.String("storyID", payload.PublishedStoryID), zap.String("taskID", payload.TaskID))
	log.Info("Обработка сообщения об ошибке генерации", zap.String("promptType", string(payload.PromptType)))

	storyID, err := uuid.Parse(payload.PublishedStoryID)
	if err != nil {
		log.Error("Некорректный PublishedStoryID в сообщении DLQ", zap.String("rawID", payload.PublishedStoryID), zap.Error(err))
		// Отбросить сообщение, т.к. ID невалидный
		if ackErr := d.Ack(false); ackErr != nil {
			log.Error("Ошибка подтверждения (ack) сообщения DLQ с невалидным ID", zap.Error(ackErr))
		}
		return
	}

	// Формируем сообщение об ошибке
	errorReason := "Generation task failed. See story-generator logs for details."
	if xDeath, ok := d.Headers["x-death"].([]interface{}); ok && len(xDeath) > 0 {
		if deathInfo, ok := xDeath[0].(amqp.Table); ok {
			if reason, ok := deathInfo["reason"].(string); ok {
				errorReason = fmt.Sprintf("Generation task failed (reason: %s). See story-generator logs for details.", reason)
			}
		}
	}

	// Обновляем статус истории на Error
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second) // Таймаут на операцию с БД
	defer cancel()

	err = c.storyRepo.UpdateStatusDetails(ctx, c.db, storyID, models.StatusError, nil, nil, nil, &errorReason)
	if err != nil {
		log.Error("Ошибка обновления статуса истории на Error из DLQ", zap.Error(err))
		// Не подтверждаем сообщение (nack с requeue=true?), чтобы попробовать снова?
		// Или лучше ack, чтобы не зациклиться, если ошибка в БД?
		// Пока выбираем Nack с requeue=false, чтобы не блокировать другие сообщения, но логируем серьезность.
		if nackErr := d.Nack(false, false); nackErr != nil { // false = multiple, false = requeue
			log.Error("Критическая ошибка: Не удалось nack сообщение DLQ после ошибки обновления статуса", zap.Error(nackErr))
		}
		return
	}

	log.Info("Статус истории успешно обновлен на Error")

	// Подтверждаем успешную обработку сообщения из DLQ
	if err := d.Ack(false); err != nil {
		log.Error("Ошибка подтверждения (ack) сообщения DLQ после успешной обработки", zap.Error(err))
	}
}
