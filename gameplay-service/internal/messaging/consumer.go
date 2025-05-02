package messaging

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"novel-server/gameplay-service/internal/config"
	sharedInterfaces "novel-server/shared/interfaces"

	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

// --- NotificationConsumer ---

const (
	// Максимальное количество одновременно обрабатываемых сообщений
	maxConcurrentHandlers = 10 // TODO: Сделать настраиваемым через config
)

// NotificationConsumer отвечает за получение уведомлений из RabbitMQ.
type NotificationConsumer struct {
	conn        *amqp.Connection
	processor   *NotificationProcessor // Используем процессор
	queueName   string
	stopChannel chan struct{}
	wg          sync.WaitGroup     // <<< Для ожидания завершения обработчиков
	ctx         context.Context    // <<< ДОБАВЛЕНО: Контекст для управления горутинами
	cancelFunc  context.CancelFunc // <<< ДОБАВЛЕНО: Функция отмены контекста
	config      *config.Config     // <<< ВОЗВРАЩЕНО: Для доступа к consumerConcurrency
	logger      *zap.Logger
}

// NewNotificationConsumer создает нового консьюмера уведомлений.
func NewNotificationConsumer(
	conn *amqp.Connection,
	// Зависимости для NotificationProcessor:
	storyConfigRepo sharedInterfaces.StoryConfigRepository,
	publishedRepo sharedInterfaces.PublishedStoryRepository,
	sceneRepo sharedInterfaces.StorySceneRepository,
	gameStateRepo sharedInterfaces.PlayerGameStateRepository,
	playerProgressRepo sharedInterfaces.PlayerProgressRepository,
	imageReferenceRepo sharedInterfaces.ImageReferenceRepository,
	genResultRepo sharedInterfaces.GenerationResultRepository,
	clientUpdatePub ClientUpdatePublisher,
	taskPub TaskPublisher,
	pushPub PushNotificationPublisher,
	characterImageTaskPub CharacterImageTaskPublisher,
	characterImageTaskBatchPub CharacterImageTaskBatchPublisher,
	authClient sharedInterfaces.AuthServiceClient,
	logger *zap.Logger,
	// Параметры самого консьюмера:
	queueName string,
	cfg *config.Config,
) (*NotificationConsumer, error) {
	if conn == nil {
		return nil, errors.New("connection is nil")
	}

	// Создаем NotificationProcessor с переданными зависимостями
	processor := NewNotificationProcessor(
		storyConfigRepo,
		publishedRepo,
		sceneRepo,
		gameStateRepo,
		imageReferenceRepo,
		genResultRepo,
		clientUpdatePub,
		taskPub,
		pushPub,
		characterImageTaskPub,
		characterImageTaskBatchPub,
		authClient,
		logger,
		cfg,
		playerProgressRepo,
	)

	ctx, cancel := context.WithCancel(context.Background())

	consumer := &NotificationConsumer{
		conn:        conn,
		processor:   processor,
		queueName:   queueName,
		stopChannel: make(chan struct{}),
		ctx:         ctx,
		cancelFunc:  cancel,
		config:      cfg,
		logger:      logger.With(zap.String("queue", queueName)),
	}
	return consumer, nil
}

// StartConsuming запускает прослушивание очереди уведомлений.
func (c *NotificationConsumer) StartConsuming() error {
	ch, err := c.conn.Channel()
	if err != nil {
		return fmt.Errorf("не удалось открыть канал RabbitMQ: %w", err)
	}
	// Не закрываем канал здесь, так как он может быть нужен для переподключения
	// defer ch.Close()

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

	// Устанавливаем prefetch count для ограничения количества сообщений, получаемых за раз
	// Используем значение из конфига
	consumerConcurrency := c.config.ConsumerConcurrency // <<< Используем сохраненный config
	if consumerConcurrency <= 0 {
		c.logger.Warn("ConsumerConcurrency в конфиге <= 0, используется значение по умолчанию 10")
		consumerConcurrency = 10
	}
	if err := ch.Qos(
		consumerConcurrency, // prefetch count
		0,                   // prefetch size
		false,               // global
	); err != nil {
		return fmt.Errorf("не удалось установить Qos: %w", err)
	}

	msgs, err := ch.Consume(
		q.Name,
		"gameplay-consumer", // consumer tag
		false,               // auto-ack (устанавливаем в false для ручного подтверждения)
		false,               // exclusive
		false,               // no-local
		false,               // no-wait
		nil,                 // args
	)
	if err != nil {
		return fmt.Errorf("не удалось зарегистрировать консьюмера: %w", err)
	}

	log.Printf(" [*] Ожидание уведомлений в очереди '%s'. Для выхода нажмите CTRL+C", q.Name)

	// <<< Запускаем обработчики в горутинах >>>
	// Используем consumerConcurrency из конфига
	sem := make(chan struct{}, consumerConcurrency) // Семафор для ограничения concurrency

	go func() {
		for {
			select {
			case d, ok := <-msgs:
				if !ok {
					log.Println("Канал сообщений RabbitMQ закрыт. Завершение работы консьюмера...")
					// Канал закрыт, возможно, из-за Stop() или проблем с соединением
					// Даем существующим обработчикам шанс завершиться
					// c.wg.Wait() // Не нужно здесь, Wait вызывается в Stop()
					return
				}

				// Получаем "слот" для обработки
				sem <- struct{}{}
				c.wg.Add(1) // Увеличиваем счетчик активных обработчиков

				// Запускаем обработку в отдельной горутине
				go func(delivery amqp.Delivery) {
					defer func() {
						<-sem       // Освобождаем слот
						c.wg.Done() // Уменьшаем счетчик
					}()

					log.Printf("[handler] Получено сообщение: TaskID %s", delivery.MessageId)

					// Используем контекст с таймаутом 30с. TODO про настройку удален.
					handlerCtx, handlerCancel := context.WithTimeout(c.ctx, 30*time.Second) // <<< ИСПОЛЬЗУЕМ СОХРАНЕННЫЙ КОНТЕКСТ
					defer handlerCancel()

					// <<< ИСПРАВЛЕНИЕ: Передаем всю delivery в Process >>>
					if err := c.processor.Process(handlerCtx, delivery); err != nil {
						// Если Process вернул ошибку, логируем и отправляем Nack
						// <<< ИСПОЛЬЗУЕМ zap логгер >>>
						c.logger.Error("Error processing notification",
							zap.String("correlation_id", delivery.CorrelationId),
							zap.Uint64("delivery_tag", delivery.DeliveryTag),
							zap.Error(err),
						)
						// requeue=false используется, т.к. предполагается наличие DLQ для постоянных ошибок.
						// TODO удален.
						_ = delivery.Nack(false, false)
					} else {
						// Успешная обработка, подтверждаем сообщение
						// <<< ИСПОЛЬЗУЕМ zap логгер >>>
						c.logger.Info("Notification processed successfully. Sending Ack.",
							zap.String("correlation_id", delivery.CorrelationId),
							zap.Uint64("delivery_tag", delivery.DeliveryTag),
						)
						_ = delivery.Ack(false)
					}
				}(d)

			case <-c.stopChannel:
				log.Println("Получен сигнал остановки. Завершение работы консьюмера...")
				// Даем существующим обработчикам шанс завершиться
				// c.wg.Wait() // Не нужно здесь, Wait вызывается в Stop()
				return
			case <-c.ctx.Done(): // <<< Используем контекст для остановки
				log.Println("Контекст консьюмера отменен. Завершение цикла обработки сообщений...")
				// Если контекст отменен (например, через Stop()), выходим из цикла
				return
			}
		}
	}()

	// Ожидаем закрытия канала stopChannel (через вызов Stop())
	<-c.stopChannel
	log.Println("Получен сигнал остановки консьюмера.")

	// Отменяем контекст, чтобы завершить горутины обработчиков
	c.cancelFunc()

	// Ожидаем завершения всех активных обработчиков
	log.Println("Ожидание завершения активных обработчиков...")
	c.wg.Wait()
	log.Println("Все обработчики завершены.")

	// Закрываем канал RabbitMQ, если он еще открыт
	if ch != nil && !ch.IsClosed() {
		if err := ch.Close(); err != nil {
			log.Printf("Ошибка при закрытии канала RabbitMQ: %v", err)
		}
	}

	log.Println("Консьюмер уведомлений успешно остановлен.")
	return nil
}

// Stop останавливает консьюмер.
func (c *NotificationConsumer) Stop() {
	log.Println("Запрос на остановку консьюмера...")
	select {
	case <-c.stopChannel:
		// Уже остановлен
		log.Println("Консьюмер уже остановлен.")
		return
	default:
		// Отправляем сигнал на остановку
		close(c.stopChannel)
		log.Println("Сигнал остановки отправлен.")
		// Отмена контекста и ожидание Wait() происходит в StartConsuming
	}
}

// --- Вспомогательные функции --- // Удалены, перемещены в helpers.go
