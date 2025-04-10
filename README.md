# Novel Server API Documentation

## Базовая информация

Все запросы к API должны быть аутентифицированы с помощью токена доступа, передаваемого в заголовке `Authorization: Bearer <token>`.

Все ответы возвращаются в формате JSON.

## Аутентификация

### Регистрация пользователя
- **URL**: `/auth/register`
- **Метод**: `POST`
- **Запрос**:
  ```json
  {
    "username": "string (от 4 до 32 символов)",
    "email": "string (валидный email)",
    "password": "string (от 6 до 100 символов)"
  }
  ```
- **Ответ**:
  ```json
  {
    "message": "пользователь успешно зарегистрирован",
    "success": "true"
  }
  ```
- **Примечания**: 
  - Имя пользователя (username) хранится в нижнем регистре (lowercase), но отображаемое имя (display_name) сохраняет оригинальный регистр
  - Имя пользователя должно быть уникальным (без учета регистра)

### Авторизация пользователя
- **URL**: `/auth/login`
- **Метод**: `POST`
- **Запрос**:
  ```json
  {
    "username": "string",
    "password": "string (от 6 до 100 символов)"
  }
  ```
- **Ответ**:
  ```json
  {
    "token": "string",
    "refresh_token": "string",
    "user_id": "uuid",
    "username": "string",
    "display_name": "string",
    "expires_at": 1691234567
  }
  ```

### Обновление токена
- **URL**: `/auth/refresh`
- **Метод**: `POST`
- **Запрос**:
  ```json
  {
    "refresh_token": "string"
  }
  ```
  или просто передать `refresh_token` в заголовке `Authorization: Bearer <refresh_token>`
- **Ответ**:
  ```json
  {
    "token": "string",
    "refresh_token": "string",
    "user_id": "uuid",
    "username": "string",
    "display_name": "string",
    "expires_at": 1691234567
  }
  ```

### Обновление отображаемого имени (требует аутентификации)
- **URL**: `/api/auth/display-name`
- **Метод**: `PUT`
- **Заголовки**: `Authorization: Bearer <access_token>`
- **Запрос**:
  ```json
  {
    "display_name": "string (до 100 символов)"
  }
  ```
- **Ответ**:
  ```json
  {
    "message": "отображаемое имя успешно обновлено"
  }
  ```

### Выход из системы (отзыв одного токена)
- **URL**: `/auth/logout`
- **Метод**: `POST`
- **Запрос**:
  ```json
  {
    "refresh_token": "string"
  }
  ```
  или просто передать `refresh_token` в заголовке `Authorization: Bearer <refresh_token>`
- **Ответ**:
  ```json
  {
    "message": "выход выполнен успешно"
  }
  ```

### Выход со всех устройств (требует аутентификации)
- **URL**: `/api/auth/logout-all`
- **Метод**: `POST`
- **Заголовки**: `Authorization: Bearer <access_token>`
- **Ответ**:
  ```json
  {
    "message": "выход со всех устройств выполнен успешно"
  }
  ```

## Работа с новеллами

### Получение списка публичных новелл
- **URL**: `/api/novels?limit=10&offset=0`
- **Метод**: `GET`
- **Ответ**: Массив новелл
  ```json
  [
    {
      "id": "uuid",
      "title": "string",
      "description": "string",
      "author_id": "uuid",
      "author_display_name": "string",
      "is_public": true,
      "cover_image": "string",
      "tags": ["string"],
      "like_count": 0,
      "created_at": "timestamp",
      "updated_at": "timestamp"
    }
  ]
  ```

### Получение новеллы по ID
- **URL**: `/api/novels/{id}`
- **Метод**: `GET`
- **Ответ**: Детали новеллы
  ```json
  {
    "id": "uuid",
    "title": "string",
    "description": "string",
    "author_id": "uuid",
    "author_display_name": "string",
    "is_public": true,
    "cover_image": "string",
    "tags": ["string"],
    "like_count": 0,
    "is_liked_by_user": false,
    "created_at": "timestamp",
    "updated_at": "timestamp",
    "config": {
      "title": "string",
      "short_description": "string",
      "franchise": "string",
      "genre": "string",
      "language": "string",
      "is_adult_content": boolean,
      "player_name": "string",
      "player_gender": "string",
      "player_description": "string",
      "world_context": "string",
      "story_config": {
        "length": "string",
        "character_count": number,
        "scene_event_target": number
      }
    },
    "setup": {
      "core_stats_definition": [{
        "name": "string",
        "description": "string",
        "min": number,
        "max": number,
        "start_value": number,
        "visible": boolean
      }],
      "characters": [{
        "name": "string",
        "description": "string",
        "role": "string"
      }]
    }
  }
  ```

### Получение списка новелл пользователя
- **URL**: `/api/my-novels`
- **Метод**: `GET`
- **Ответ**: Массив новелл пользователя
  ```json
  [
    {
      "id": "uuid",
      "title": "string",
      "description": "string",
      "author_id": "uuid",
      "is_public": boolean,
      "cover_image": "string",
      "tags": ["string"],
      "created_at": "timestamp",
      "updated_at": "timestamp"
    }
  ]
  ```

### Публикация новеллы
- **URL**: `/api/novels/{id}/publish`
- **Метод**: `POST`
- **Ответ**:
  ```json
  {
    "message": "новелла успешно опубликована"
  }
  ```

### Обработка события Game Over
- **URL**: `/api/novels/{id}/gameover`
- **Метод**: `POST`
- **Запрос**:
  ```json
  {
    "reason": {
      "stat_name": "string",
      "condition": "string"
    },
    "user_choices": ["string"]
  }
  ```
- **Ответ**:
  ```json
  {
    "ending_text": "string",
    "is_game_over": true,
    "can_continue": boolean,
    "new_character": {
      "name": "string",
      "description": "string"
    },
    "new_core_stats": [{
      "name": "string",
      "description": "string",
      "min": number,
      "max": number,
      "start_value": number,
      "visible": boolean
    }],
    "initial_choices": ["string"]
  }
  ```

## Генерация новелл

### Генерация драфта новеллы
- **URL**: `/api/generate/draft`
- **Метод**: `POST`
- **Запрос**:
  ```json
  {
    "title": "string",
    "description": "string",
    "genre": "string",
    "character_name": "string",
    "character_description": "string",
    "world_context": "string",
    "additional_notes": "string"
  }
  ```
- **Ответ**:
  ```json
  {
    "task_id": "uuid",
    "message": "генерация драфта запущена"
  }
  ```

### Модификация драфта новеллы
- **URL**: `/api/generate/draft/{id}/modify`
- **Метод**: `POST`
- **Запрос**:
  ```json
  {
    "modification_prompt": "string"
  }
  ```
- **Ответ**:
  ```json
  {
    "task_id": "uuid",
    "message": "модификация драфта запущена"
  }
  ```

### Получение списка драфтов пользователя
- **URL**: `/api/generate/drafts`
- **Метод**: `GET`
- **Ответ**: Массив драфтов
  ```json
  [
    {
      "id": "uuid",
      "title": "string",
      "description": "string",
      "author_id": "uuid",
      "created_at": "timestamp",
      "updated_at": "timestamp"
    }
  ]
  ```

### Получение деталей драфта
- **URL**: `/api/generate/drafts/{id}`
- **Метод**: `GET`
- **Ответ**: Детали драфта
  ```json
  {
    "id": "uuid",
    "title": "string",
    "description": "string",
    "author_id": "uuid",
    "content": "string",
    "created_at": "timestamp",
    "updated_at": "timestamp"
  }
  ```

### Создание новеллы из драфта
- **URL**: `/api/generate/setup`
- **Метод**: `POST`
- **Запрос**:
  ```json
  {
    "draft_id": "uuid"
  }
  ```
- **Ответ**:
  ```json
  {
    "task_id": "uuid",
    "message": "настройка новеллы запущена"
  }
  ```

### Генерация контента новеллы
- **URL**: `/api/generate/content`
- **Метод**: `POST`
- **Запрос**:
  ```json
  {
    "novel_id": "uuid",
    "client_state": {
      "user_choice": "string",
      "core_stats": {
        "stat_name": number
      },
      "story_variables": {
        "var_name": "value"
      },
      "history": [
        {
          "type": "narrative|choice|stats_change",
          "content": "string"
        }
      ]
    }
  }
  ```
- **Ответ**:
  ```json
  {
    "task_id": "uuid",
    "message": "генерация контента запущена"
  }
  ```

## Статус задач

### Получение статуса задачи
- **URL**: `/api/tasks/{id}`
- **Метод**: `GET`
- **Ответ**: Статус задачи
  ```json
  {
    "id": "uuid",
    "status": "pending|running|completed|failed|cancelled",
    "progress": number,
    "message": "string",
    "result": {
      // Зависит от типа задачи
    },
    "created_at": "timestamp",
    "updated_at": "timestamp"
  }
  ```

## WebSocket API

Для получения уведомлений о прогрессе выполнения задач в реальном времени, нужно подключиться к WebSocket:

- **URL**: `/ws?user_id={user_id}`
- После подключения клиент автоматически подписывается на канал `tasks`
- Сервер отправляет уведомления в формате:
  ```json
  {
    "type": "task_update",
    "topic": "tasks",
    "payload": {
      "task_id": "uuid",
      "status": "pending|running|completed|failed|cancelled",
      "progress": number,
      "message": "string",
      "updated_at": "timestamp",
      "result": {
        // Если status=completed
      }
    }
  }
  ```

## Примеры использования

### Создание и настройка новеллы

1. Генерируем драфт новеллы:
   ```javascript
   const response = await fetch('/api/generate/draft', {
     method: 'POST',
     headers: {
       'Content-Type': 'application/json',
       'Authorization': `Bearer ${token}`
     },
     body: JSON.stringify({
       title: "Приключения в космосе",
       description: "Космическая одиссея",
       genre: "sci-fi adventure",
       character_name: "Капитан Алекс",
       character_description: "Опытный космонавт",
       world_context: "Далекое будущее, где люди колонизировали другие планеты",
       additional_notes: "Много экшена и загадок"
     })
   });
   const data = await response.json();
   const taskId = data.task_id;
   ```

2. Отслеживаем выполнение задачи через WebSocket или периодические запросы:
   ```javascript
   const taskStatus = await fetch(`/api/tasks/${taskId}`, {
     headers: {
       'Authorization': `Bearer ${token}`
     }
   });
   const taskData = await taskStatus.json();
   if (taskData.status === 'completed') {
     const draftId = taskData.result.id;
     // Переходим к настройке новеллы
   }
   ```

3. Настраиваем новеллу из драфта:
   ```javascript
   const setupResponse = await fetch('/api/generate/setup', {
     method: 'POST',
     headers: {
       'Content-Type': 'application/json',
       'Authorization': `Bearer ${token}`
     },
     body: JSON.stringify({
       draft_id: draftId
     })
   });
   const setupData = await setupResponse.json();
   const setupTaskId = setupData.task_id;
   ```

4. После завершения настройки получаем ID новеллы:
   ```javascript
   const novelId = setupTaskResult.result.novel_id;
   ```

### Игровой процесс

1. Начинаем игровой процесс (первый запрос на генерацию контента):
   ```javascript
   const contentResponse = await fetch('/api/generate/content', {
     method: 'POST',
     headers: {
       'Content-Type': 'application/json',
       'Authorization': `Bearer ${token}`
     },
     body: JSON.stringify({
       novel_id: novelId,
       client_state: {
         // Пустой state для первого запроса
       }
     })
   });
   const contentData = await contentResponse.json();
   const contentTaskId = contentData.task_id;
   ```

2. Получаем результат генерации контента:
   ```javascript
   const contentTask = await waitForTaskCompletion(contentTaskId);
   const gameplayData = contentTask.result;
   
   // Отображаем нарратив, выборы и статистику игроку
   displayContent(gameplayData);
   ```

3. Продолжаем игру с выбором пользователя:
   ```javascript
   const userChoice = "Исследовать пещеру";
   const nextContentResponse = await fetch('/api/generate/content', {
     method: 'POST',
     headers: {
       'Content-Type': 'application/json',
       'Authorization': `Bearer ${token}`
     },
     body: JSON.stringify({
       novel_id: novelId,
       client_state: {
         user_choice: userChoice,
         core_stats: gameplayData.core_stats,
         story_variables: gameplayData.story_variables,
         history: gameplayData.history.concat([{
           type: "choice",
           content: userChoice
         }])
       }
     })
   });
   ```

### Обработка Game Over

```javascript
const gameOverResponse = await fetch(`/api/novels/${novelId}/gameover`, {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${token}`
  },
  body: JSON.stringify({
    reason: {
      stat_name: "health",
      condition: "low"
    },
    user_choices: ["Исследовать пещеру", "Сражаться с монстром"]
  })
});
const gameOverData = await gameOverResponse.json();

// Отображаем концовку
displayEndingText(gameOverData.ending_text);

// Если можно продолжить, предлагаем новую игру
if (gameOverData.can_continue) {
  startNewGameWithNewCharacter(
    gameOverData.new_character,
    gameOverData.new_core_stats,
    gameOverData.initial_choices
  );
}
```

## Безопасность

### Хеширование паролей

В приложении реализовано безопасное хранение паролей с использованием:

1. **Соли из переменных окружения**: Для каждого пароля добавляется уникальная строка соли, определенная в переменной окружения `PASSWORD_SALT`.
2. **Алгоритма bcrypt**: После добавления соли пароли хешируются с использованием bcrypt с настройками сложности по умолчанию.

Эта комбинация защищает пароли пользователей даже в случае компрометации базы данных, так как без соли из переменных окружения злоумышленник не сможет эффективно атаковать хеши паролей.

### Механизм токенов доступа

Система использует двухуровневую аутентификацию с помощью токенов:

1. **Access Token (JWT)**: 
   - Короткий срок жизни (по умолчанию 1 час)
   - Используется для аутентификации в API
   - Содержит информацию о пользователе в зашифрованном виде

2. **Refresh Token**:
   - Длинный срок жизни (по умолчанию 7 дней)
   - Хранится в базе данных
   - Используется только для получения нового access token
   - Одноразовый (отзывается при использовании)
   - Имеет возможность отзыва отдельного токена или всех токенов пользователя

Этот подход повышает безопасность системы следующим образом:
- Даже при компрометации access token, злоумышленник имеет доступ только на короткое время
- Refresh token можно отозвать в любой момент, принудительно завершив сеанс пользователя
- Компрометация одного устройства не влияет на другие сеансы пользователя
- Можно отслеживать активные сеансы и управлять ими

> ⚠️ **Важно**: При развертывании приложения в продакшене обязательно установите уникальные значения для переменных `JWT_SECRET` и `PASSWORD_SALT`. Эти значения должны быть достаточно длинными (минимум 32 символа) и случайными строками. 

## Лайки и взаимодействие с новеллами

### Лайк новеллы
- **URL**: `/api/novels/{id}/like`
- **Метод**: `POST`
- **Заголовки**: `Authorization: Bearer <access_token>`
- **Ответ**:
  ```json
  {
    "message": "лайк успешно добавлен"
  }
  ```

### Отмена лайка новеллы
- **URL**: `/api/novels/{id}/unlike`
- **Метод**: `POST`
- **Заголовки**: `Authorization: Bearer <access_token>`
- **Ответ**:
  ```json
  {
    "message": "лайк успешно удален"
  }
  ```

### Получение списка лайкнутых новелл
- **URL**: `/api/novels/liked?limit=10&offset=0`
- **Метод**: `GET`
- **Заголовки**: `Authorization: Bearer <access_token>`
- **Ответ**: Массив новелл
  ```json
  [
    {
      "id": "uuid",
      "title": "string",
      "description": "string",
      "author_id": "uuid",
      "author_display_name": "string",
      "is_public": true,
      "cover_image": "string",
      "tags": ["string"],
      "like_count": 0,
      "is_liked_by_user": true,
      "created_at": "timestamp",
      "updated_at": "timestamp"
    }
  ]
  ```