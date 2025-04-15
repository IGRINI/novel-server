# Развертывание Novel Server на сервере

Этот документ описывает шаги по развертыванию приложения Novel Server на сервере с использованием Docker Swarm и Docker Secrets для безопасного управления конфигурацией.

## 1. Предварительные требования

*   **Сервер:** Подготовленный сервер с установленной ОС Linux (например, Ubuntu, Debian, CentOS).
*   **Docker:** Установленный Docker Engine. [Инструкция по установке Docker](https://docs.docker.com/engine/install/).
*   **Docker Compose:** Установленный Docker Compose (обычно идет вместе с Docker Desktop, но на сервере может потребоваться отдельная установка). [Инструкция по установке Docker Compose](https://docs.docker.com/compose/install/).
*   **Docker Swarm:** Инициализированный режим Swarm на сервере. Если еще не сделано, выполните:
    ```bash
    docker swarm init
    ```
*   **Firewall:** Настроенный брандмауэр сервера. Как минимум, должны быть открыты порты, которые использует Traefik для приема внешнего трафика (обычно 80 для HTTP и 443 для HTTPS).
*   **Git:** Установленный Git для клонирования репозитория.

## 2. Подготовка

1.  **Клонируйте репозиторий** на сервер:
    ```bash
    git clone <URL_вашего_репозитория> novel-server
    cd novel-server
    ```
2.  **Соберите или скачайте Docker-образы:**
    *   **Вариант А (Сборка из исходников):** Если ваши Dockerfile настроены для сборки всех сервисов, выполните:
        ```bash
        # Эта команда только соберет образы, не запуская контейнеры
        docker-compose build
        ```
        Убедитесь, что в `docker-compose.yml` для каждого сервиса указано `build: .` или путь к Dockerfile.
    *   **Вариант Б (Использование Registry - Рекомендуется для Prod):** Если вы храните собранные образы в Docker Hub или приватном registry:
        *   Убедитесь, что сервер аутентифицирован в registry (`docker login ...`), если он приватный.
        *   Убедитесь, что в `docker-compose.yml` для каждого сервиса указан `image: <ваш_registry>/<имя_образа>:<тег>`.
        *   Скачивать образы не обязательно вручную, `docker stack deploy` сделает это сам при необходимости.

## 3. Управление секретами (Docker Secrets)

Никогда не храните пароли, API-ключи и другие секреты напрямую в `docker-compose.yml`, образах или системе контроля версий. Используйте Docker Secrets.

1.  **Сгенерируйте сильные, уникальные секреты.** Используйте менеджер паролей или `openssl rand` для генерации случайных строк (рекомендуется 32-64 байта / ~44-88 символов Base64):
    ```bash
    # Пример генерации секрета (выполните для каждого секрета)
    openssl rand -base64 32
    ```
2.  **Создайте Docker Secrets на сервере.** Для каждого секрета выполните:
    ```bash
    # 1. Создайте временный файл с секретом (ТОЛЬКО значение, без перевода строки)
    echo -n 'СЮДА_ВСТАВЬТЕ_ВАШ_СЕКРЕТ' > secret_value.txt

    # 2. Создайте Docker Secret из файла
    #    Используйте осмысленные имена для секретов (например, db_password, jwt_secret)
    docker secret create <имя_секрета_в_docker> secret_value.txt

    # 3. Удалите временный файл
    rm secret_value.txt
    ```
    **Необходимые секреты для создания:**
    *   `db_password` (Пароль для PostgreSQL)
    *   `jwt_secret` (Секрет для подписи JWT токенов)
    *   `password_pepper` (Перец для хеширования паролей)
    *   `inter_service_secret` (Секрет для межсервисных токенов)
    *   `rabbitmq_password` (Пароль для RabbitMQ, если вы сменили дефолтного 'guest')
    *   `ai_api_key` (API ключ для нейросети)
    *   *Добавьте другие, если они есть.*

3.  **Проверьте созданные секреты:** `docker secret ls`

## 4. Конфигурация `docker-compose.yml` для Swarm

Адаптируйте ваш `docker-compose.yml` для развертывания в Swarm:

1.  **Версия:** Убедитесь, что `version: '3.1'` или выше.
2.  **Секция `deploy`:** Для сервисов, которые должны масштабироваться или иметь ограничения, добавьте секцию `deploy`:
    ```yaml
    services:
      auth:
        # ... image, build ...
        deploy:
          replicas: 1 # Количество экземпляров сервиса
          resources:
            limits:
              cpus: '0.50'
              memory: 256M
            reservations:
              cpus: '0.25'
              memory: 128M
          restart_policy:
            condition: on-failure
    ```
3.  **Образы (`image`):** Убедитесь, что указаны правильные имена образов (локально собранные или из registry).
4.  **Секция `secrets` (для сервисов):** Для каждого сервиса, которому нужен доступ к секрету, добавьте секцию `secrets`:
    ```yaml
    services:
      auth:
        # ... image, deploy ...
        secrets:
          - source: jwt_secret # Имя секрета, созданного через docker secret create
            target: /run/secrets/jwt_secret # Путь внутри контейнера
          - source: password_pepper
            target: /run/secrets/password_pepper
          - source: inter_service_secret
            target: /run/secrets/inter_service_secret
          - source: db_password
            target: /run/secrets/db_password
          # ... другие секреты для этого сервиса
      postgres:
        # ... image ...
        environment:
          POSTGRES_USER: postgres # Можно оставить или тоже сделать секретом
          POSTGRES_DB: novel_db
          # Пароль читается из файла секрета
          POSTGRES_PASSWORD_FILE: /run/secrets/db_password
        secrets:
          - db_password
      # ... другие сервисы (gameplay, story-generator и т.д.) с их секретами
    ```
5.  **Удаление секретов из `environment`:** **Обязательно удалите** все переменные окружения, которые теперь передаются через Docker Secrets (например, `DB_PASSWORD`, `JWT_SECRET`, `PASSWORD_PEPPER`, `AI_API_KEY` и т.д.) из секций `environment` в `docker-compose.yml`.
6.  **Секция `secrets` (верхний уровень):** Объявите все используемые секреты на верхнем уровне файла, указав, что они созданы внешне:
    ```yaml
    secrets:
      db_password:
        external: true
      jwt_secret:
        external: true
      password_pepper:
        external: true
      inter_service_secret:
        external: true
      rabbitmq_password:
        external: true # Если создавали
      ai_api_key:
        external: true # Если создавали
      # ... другие секреты ...
    ```
7.  **Volumes и Networks:** Убедитесь, что тома (volumes) и сети (networks) настроены правильно для сохранения данных и взаимодействия сервисов.

## 5. Развертывание стека

1.  **Запустите стек:**
    ```bash
    # Замените novel_stack на желаемое имя вашего стека
    docker stack deploy -c docker-compose.yml novel_stack
    ```
2.  **Проверьте статус развертывания:**
    ```bash
    docker stack services novel_stack
    # Подождите, пока REPLICAS станет 1/1 (или сколько вы указали)
    ```
3.  **Просмотрите логи сервисов (если нужно):**
    ```bash
    docker service logs novel_stack_auth # Имя сервиса формируется как <имя_стека>_<имя_сервиса_в_compose>
    docker service logs novel_stack_postgres
    # и т.д.
    ```
4.  **Проверьте доступность приложения** через Traefik (порт 80 или 443).

## 6. Обновление стека

Если вы внесли изменения в код, собрали и запушили новые образы, или изменили `docker-compose.yml`:

1.  **Повторно выполните команду развертывания:**
    ```bash
    docker stack deploy -c docker-compose.yml novel_stack
    ```
    Docker Swarm сравнит текущее состояние с новым описанием и применит изменения (например, обновит образы контейнеров с использованием rolling update по умолчанию).

## 7. Удаление стека

1.  **Остановите и удалите все сервисы стека:**
    ```bash
    docker stack rm novel_stack
    ```
2.  **Удалите секреты (ОСТОРОЖНО):** Эта команда удалит **все** секреты, если они больше не используются ни одним сервисом. Будьте уверены, что они вам больше не нужны.
    ```bash
    # Посмотреть ID секретов
    docker secret ls -q
    # Удалить конкретные секреты
    docker secret rm db_password jwt_secret password_pepper ...
    # Или удалить все сразу (опасно, если есть другие стеки)
    # docker secret rm $(docker secret ls -q)
    ```
3.  **Удалите тома (Volumes) (ОСТОРОЖНО):** Удаление томов приведет к потере данных (например, базы данных PostgreSQL). Выполняйте, только если уверены.
    ```bash
    # Посмотреть тома
    docker volume ls
    # Удалить конкретный том (имя обычно <имя_стека>_<имя_тома_в_compose>)
    docker volume rm novel_stack_postgres_data
    ```

## 8. Дополнительные замечания

*   **HTTPS/TLS для Traefik:** Настоятельно рекомендуется настроить Traefik для работы с HTTPS и автоматического получения сертификатов Let's Encrypt. Это потребует дополнительной конфигурации в `docker-compose.yml` и наличия доменного имени, указывающего на IP сервера. См. [документацию Traefik](https://doc.traefik.io/traefik/https/overview/).
*   **Резервное копирование:** Настройте регулярное резервное копирование данных из томов Docker, особенно для базы данных PostgreSQL.
*   **Мониторинг:** Рассмотрите возможность добавления в стек сервисов мониторинга, таких как Prometheus и Grafana, для отслеживания состояния и производительности приложения.
*   **Логирование:** Настройте централизованное логирование (например, стек EFK/Loki) для удобного анализа логов всех сервисов. 