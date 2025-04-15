# Используем официальный образ golang-migrate
FROM migrate/migrate:v4.17.1

# Копируем миграции в контейнер
COPY ./shared/database/migrations /migrations

# Копируем скрипт entrypoint
COPY ./deploy/docker/migrate/entrypoint.sh /entrypoint.sh

# Даем права на исполнение (на всякий случай, если chmod не сработал)
RUN chmod +x /entrypoint.sh

# Указываем entrypoint
ENTRYPOINT ["/entrypoint.sh"]

# CMD больше не нужен, т.к. запуск происходит в entrypoint
# CMD ["-path", "/migrations", "up"] 