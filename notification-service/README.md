# Notification Service

Сервис отвечает за отправку push-уведомлений на Android (FCM) и iOS (APNS).

Он слушает очередь RabbitMQ, в которую другие сервисы (например, `gameplay-service`) помещают запросы на отправку уведомлений.

## Запуск

```bash
# TODO: Добавить инструкции по запуску
```

## Конфигурация

Сервис конфигурируется через переменные окружения или файл `config.yml`.

- `RABBITMQ_URI`: URI для подключения к RabbitMQ.
- `PUSH_QUEUE_NAME`: Имя очереди для получения запросов на push-уведомления.
- `FCM_SERVER_KEY`: Ключ сервера Firebase Cloud Messaging.
- `APNS_KEY_ID`: Apple Push Notification Service Key ID.
- `APNS_TEAM_ID`: Apple Developer Team ID.
- `APNS_KEY_PATH`: Путь к файлу ключа APNS (.p8).
- `APNS_TOPIC`: Bundle ID приложения для APNS.
- `TOKEN_SERVICE_URL`: URL сервиса, предоставляющего токены устройств по UserID (если есть).
- `LOG_LEVEL`: Уровень логирования (например, debug, info, warn, error). 