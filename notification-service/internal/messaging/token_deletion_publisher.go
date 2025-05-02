package messaging

import (
	"context"
	"fmt"

	// Импортируем интерфейс из shared
	interfaces "novel-server/shared/interfaces"
	"time"

	"github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

// Убедимся, что наша реализация соответствует интерфейсу из shared
var _ interfaces.TokenDeletionPublisher = (*rabbitTokenDeletionPublisher)(nil)

// rabbitTokenDeletionPublisher отправляет сообщения об удалении токена в RabbitMQ.
type rabbitTokenDeletionPublisher struct {
	conn      *amqp091.Connection
	logger    *zap.Logger
	queueName string
}

// NewRabbitTokenDeletionPublisher создает новый publisher.
func NewRabbitTokenDeletionPublisher(
	conn *amqp091.Connection,
	logger *zap.Logger,
) (interfaces.TokenDeletionPublisher, error) {
	if conn == nil {
		return nil, fmt.Errorf("RabbitMQ connection is nil")
	}

	// TODO: Вынести имя очереди в константы shared/messaging?
	queueName := "auth_token_deletions"

	publisher := &rabbitTokenDeletionPublisher{
		conn:      conn,
		logger:    logger.Named("TokenDeletionPublisher").With(zap.String("queue", queueName)),
		queueName: queueName,
	}

	// Проверим, что можем создать канал и объявить очередь при инициализации
	if err := publisher.verifyQueue(); err != nil {
		return nil, fmt.Errorf("failed to verify queue %s on init: %w", queueName, err)
	}

	publisher.logger.Info("TokenDeletionPublisher инициализирован")
	return publisher, nil
}

// verifyQueue проверяет доступность очереди при старте.
func (p *rabbitTokenDeletionPublisher) verifyQueue() error {
	ch, err := p.conn.Channel()
	if err != nil {
		return fmt.Errorf("failed to open channel: %w", err)
	}
	defer ch.Close()

	_, err = ch.QueueDeclare(
		p.queueName,
		true,  // durable (должно совпадать с consumer'ом)
		false, // delete when unused
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		return fmt.Errorf("failed to declare queue '%s': %w", p.queueName, err)
	}
	p.logger.Debug("Проверка объявления очереди прошла успешно", zap.String("queue", p.queueName))
	return nil
}

// PublishTokenDeletion публикует токен в очередь для удаления.
func (p *rabbitTokenDeletionPublisher) PublishTokenDeletion(ctx context.Context, token string) error {
	log := p.logger.With(zap.String("tokenPrefix", getTokenPrefix(token)))

	// Открываем новый канал для публикации
	ch, err := p.conn.Channel()
	if err != nil {
		log.Error("Не удалось открыть канал для публикации", zap.Error(err))
		return fmt.Errorf("failed to open channel: %w", err)
	}
	defer ch.Close()

	// Очередь должна быть уже объявлена при инициализации или consumer'ом,
	// но на всякий случай объявляем здесь пассивно (проверяем существование)
	/* // Можно убрать, т.к. есть verifyQueue при старте
	_, err = ch.QueueDeclarePassive(
	    p.queueName,
	    true, // durable
	    false, // delete when unused
	    false, // exclusive
	    false, // no-wait
	    nil, // arguments
	)
	if err != nil {
	    log.Error("Очередь для удаления токенов не найдена или параметры не совпадают", zap.Error(err))
	    return fmt.Errorf("queue %s not available: %w", p.queueName, err)
	}
	*/

	log.Debug("Публикация токена на удаление...")

	// Публикуем сообщение прямо в очередь (используем имя очереди как routing key)
	err = ch.PublishWithContext(ctx,
		"",          // exchange (default)
		p.queueName, // routing key (имя очереди)
		false,       // mandatory
		false,       // immediate
		amqp091.Publishing{
			ContentType:  "text/plain",
			DeliveryMode: amqp091.Persistent, // Помечаем сообщение как постоянное
			Timestamp:    time.Now(),
			Body:         []byte(token),
		},
	)

	if err != nil {
		log.Error("Ошибка публикации сообщения на удаление токена", zap.Error(err))
		return fmt.Errorf("failed to publish token deletion message: %w", err)
	}

	log.Info("Сообщение на удаление токена успешно опубликовано")
	return nil
}

// getTokenPrefix возвращает начало токена для логирования.
func getTokenPrefix(token string) string {
	prefixLen := 10
	if len(token) < prefixLen {
		return token
	}
	return token[:prefixLen] + "..."
}
