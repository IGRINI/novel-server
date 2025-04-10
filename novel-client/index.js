const axios = require('axios');
const fs = require('fs');
const chalk = require('chalk');
const readline = require('readline');
const WebSocket = require('ws');
const config = require('./config');

console.log('Клиент интерактивных новелл');
console.log('Конфигурация:', config);

// Переменные для хранения JWT токена и WebSocket соединения
let jwtToken = null;
let ws = null;
let userId = null; // Будем хранить ID пользователя

// Хранилище для ожидающих задач
const pendingTasks = new Map();

// Создаем интерфейс для чтения ввода пользователя
const rl = readline.createInterface({
  input: process.stdin,
  output: process.stdout
});

// Функция для получения ввода от пользователя
function askQuestion(question) {
  return new Promise((resolve) => {
    rl.question(question, (answer) => {
      resolve(answer);
    });
  });
}

// Добавляем глобальную обработку необработанных исключений
process.on('uncaughtException', (error) => {
  console.error(chalk.red(`Необработанное исключение: ${error.message}`));
  console.error(error.stack);
  process.exit(1);
});

// Функция логирования
function log(message, type = 'info') {
  const timestamp = new Date().toISOString();
  
  switch (type) {
    case 'info':
      console.log(chalk.blue(`[${timestamp}] INFO: ${message}`));
      break;
    case 'success':
      console.log(chalk.green(`[${timestamp}] SUCCESS: ${message}`));
      break;
    case 'error':
      console.error(chalk.red(`[${timestamp}] ERROR: ${message}`));
      break;
    case 'warning':
      console.warn(chalk.yellow(`[${timestamp}] WARNING: ${message}`));
      break;
    default:
      console.log(`[${timestamp}] ${message}`);
  }
}

// Функция для установки WebSocket соединения
function connectWebSocket() {
  if (ws) {
    ws.close(); // Закрываем предыдущее соединение, если оно есть
  }

  if (!userId) {
      log('Не удалось получить ID пользователя для WebSocket.', 'error');
      return;
  }

  const wsUrl = `${config.wsBaseUrl}${config.api.websocket}?user_id=${userId}`; // Используем userId
  log(`Подключение к WebSocket: ${wsUrl}`, 'info');

  ws = new WebSocket(wsUrl, {
      headers: {
          'Authorization': `Bearer ${jwtToken}`
      }
  });

  ws.on('open', () => {
    log('WebSocket соединение установлено.', 'success');
  });

  ws.on('message', (data) => {
    try {
      const message = JSON.parse(data);
      log(`Получено WebSocket сообщение: ${JSON.stringify(message, null, 2)}`, 'info');

      // Обработка обновлений задач
      if (message.type === 'task_update' && message.topic === 'tasks' && message.payload) {
        const taskId = message.payload.task_id;
        const status = message.payload.status;
        const taskPayload = message.payload; // Переименуем для ясности

        if (pendingTasks.has(taskId)) {
          const { resolve, reject, timeoutId } = pendingTasks.get(taskId);
          clearTimeout(timeoutId); // Отменяем таймаут

          if (status === 'completed') {
            log(`Задача ${taskId} завершена (через WebSocket).`, 'success');
            if (taskPayload.result) {
                resolve(taskPayload.result);
            } else {
                log(`Задача ${taskId} завершена, но поле result отсутствует в payload.`, 'error');
                reject(new Error(`Задача ${taskId} завершена без результата.`));
            }
            pendingTasks.delete(taskId);
          } else if (status === 'failed' || status === 'cancelled') {
            log(`Задача ${taskId} не удалась или отменена (через WebSocket): ${taskPayload.message}`, 'error');
            reject(new Error(taskPayload.message || `Задача ${taskId} не удалась`));
            pendingTasks.delete(taskId);
          } else {
            log(`Обновлен статус задачи ${taskId}: ${status}, прогресс: ${taskPayload.progress || 0}%`, 'info');
          }
        } else {
          log(`Получено обновление для задачи ${taskId}, но она не найдена в ожидающих.`, 'warning');
        }
      }
        // TODO: Добавить обработку других типов сообщений, например, 'notification'

    } catch (error) {
      log(`Ошибка обработки WebSocket сообщения: ${error}`, 'error');
    }
  });

  ws.on('close', (code, reason) => {
    log(`WebSocket соединение закрыто. Код: ${code}, Причина: ${reason}`, 'warning');
    ws = null;
    // Можно добавить логику переподключения здесь
  });

  ws.on('error', (error) => {
    log(`WebSocket ошибка: ${error.message}`, 'error');
    ws = null;
  });
}

// Функция для регистрации пользователя
async function register(username, email, password) {
  const url = `${config.baseUrl}${config.api.auth.register}`;
  
  log(`Регистрация пользователя ${username}...`, 'info');
  
  try {
    const response = await axios.post(url, {
      username: username,
      email: email,
      password: password
    }, {
      headers: {
        'Content-Type': 'application/json'
      }
    });
    
    if (response.data && response.data.success) {
      log('Пользователь успешно зарегистрирован!', 'success');
      return response.data;
    } else {
      log('Ошибка: Неверный формат ответа от сервера', 'error');
      return null;
    }
  } catch (error) {
    if (error.response && error.response.data && error.response.data.error) {
      log(`Ошибка при регистрации: ${error.response.data.error}`, 'error');
    } else {
      log(`Ошибка при регистрации: ${error.message}`, 'error');
    }
    return null;
  }
}

// Функция для авторизации пользователя
async function login(username, password) {
  const url = `${config.baseUrl}${config.api.auth.login}`;
  log(`Вход пользователя ${username}...`, 'info');
  try {
    const response = await axios.post(url, { username: username, password: password }, { headers: { 'Content-Type': 'application/json' } });
    if (response.data && response.data.token && response.data.user_id) { // Ожидаем user_id
      log('Авторизация успешна!', 'success');
      jwtToken = response.data.token;
      userId = response.data.user_id; // Сохраняем ID пользователя
      connectWebSocket(); // Устанавливаем WebSocket соединение после успешного логина
      return true; // Возвращаем true при успехе
    } else {
      log('Ошибка: Не удалось получить токен или ID пользователя из ответа сервера', 'error');
      return false;
    }
  } catch (error) {
    if (error.response && error.response.data && error.response.data.error) {
      log(`Ошибка при авторизации: ${error.response.data.error}`, 'error');
    } else {
      log(`Ошибка при авторизации: ${error.message}`, 'error');
    }
    return false;
  }
}

// Функция для получения списка новелл ПОЛЬЗОВАТЕЛЯ
async function fetchUserNovelsList() {
  const url = `${config.baseUrl}${config.api.novels.myNovels}`;
  
  log(`Запрос списка МОИХ новелл...`, 'info');
  
  if (!jwtToken) {
    log('Ошибка: JWT токен отсутствует. Невозможно запросить список новелл.', 'error');
    return [];
  }
  
  try {
    const response = await axios.get(url, {
      headers: {
        'Authorization': `Bearer ${jwtToken}`
      }
    });
    
    if (response.data) {
      log('Список МОИХ новелл успешно получен!', 'success');
      return response.data;
    } else {
      log('Ошибка: Не удалось получить список новелл из ответа сервера.', 'error');
      return [];
    }
  } catch (error) {
    log(`Ошибка при получении списка МОИХ новелл: ${error.message}`, 'error');
    if (error.response) {
      log(`Статус: ${error.response.status}`, 'error');
      log(`Ответ сервера: ${JSON.stringify(error.response.data)}`, 'error');
    }
    return [];
  }
}

// Функция для получения новеллы по ID
async function getNovel(novelId) {
  const url = `${config.baseUrl}${config.api.novels.get.replace('{id}', novelId)}`;
  
  log(`Получение данных новеллы с ID: ${novelId}...`, 'info');
  
  if (!jwtToken) {
    log('Ошибка: JWT токен отсутствует. Невозможно получить новеллу.', 'error');
    return null;
  }
  
  try {
    const response = await axios.get(url, {
      headers: {
        'Authorization': `Bearer ${jwtToken}`
      }
    });
    
    if (response.data) {
      log('Данные новеллы успешно получены!', 'success');
      
      // Сохраняем детальную информацию в файл для возможности использования офлайн
      const novelData = response.data;
      
      // Сохраняем полные данные новеллы в JSON-файл
      fs.writeFileSync(
        `novel_full_${novelId}.json`,
        JSON.stringify(novelData, null, 2)
      );
      log(`Полные данные новеллы сохранены в novel_full_${novelId}.json`, 'success');
      
      // Создаем сокращенную версию конфига для инициализации игры
      const gameConfig = {
        id: novelId,
        title: novelData.title,
        description: novelData.description,
        config: novelData.config,
        setup: novelData.setup
      };
      
      // Сохраняем игровой конфиг в отдельный файл
      fs.writeFileSync(
        `novel_config_${novelId}.json`,
        JSON.stringify(gameConfig, null, 2)
      );
      log(`Игровой конфиг сохранен в novel_config_${novelId}.json`, 'success');
      
      return novelData;
    } else {
      log('Ошибка: Не удалось получить данные новеллы.', 'error');
      return null;
    }
  } catch (error) {
    log(`Ошибка при получении новеллы: ${error.message}`, 'error');
    if (error.response) {
      log(`Статус: ${error.response.status}`, 'error');
      log(`Ответ сервера: ${JSON.stringify(error.response.data)}`, 'error');
    }
    return null;
  }
}

// Функция для получения черновиков пользователя
async function fetchUserDrafts() {
  const url = `${config.baseUrl}${config.api.novels.generate.drafts}`;
  
  log(`Получение списка черновиков...`, 'info');
  
  if (!jwtToken) {
    log('Ошибка: JWT токен отсутствует. Невозможно получить черновики.', 'error');
    return [];
  }
  
  try {
    const response = await axios.get(url, {
      headers: {
        'Authorization': `Bearer ${jwtToken}`
      }
    });
    
    if (response.data) {
      log(`Получено ${response.data.length} черновиков`, 'success');
      return response.data;
    } else {
      log('Не удалось получить черновики из ответа сервера', 'error');
      return [];
    }
  } catch (error) {
    log(`Ошибка при получении черновиков: ${error.message}`, 'error');
    if (error.response) {
      log(`Статус: ${error.response.status}`, 'error');
      log(`Ответ сервера: ${JSON.stringify(error.response.data)}`, 'error');
    }
    return [];
  }
}

// Функция для получения деталей черновика
async function fetchDraftDetails(draftId) {
  const url = `${config.baseUrl}${config.api.novels.generate.draftDetails.replace('{id}', draftId)}`;
  
  log(`Получение деталей черновика ${draftId}...`, 'info');
  
  if (!jwtToken) {
    log('Ошибка: JWT токен отсутствует. Невозможно получить детали черновика.', 'error');
    return null;
  }
  
  try {
    const response = await axios.get(url, {
      headers: {
        'Authorization': `Bearer ${jwtToken}`
      }
    });
    
    if (response.data) {
      log(`Детали черновика ${draftId} успешно получены`, 'success');
      return response.data; // Возвращаем NovelDraftView
    } else {
      log('Не удалось получить детали черновика из ответа сервера', 'error');
      return null;
    }
  } catch (error) {
    log(`Ошибка при получении деталей черновика: ${error.message}`, 'error');
    if (error.response) {
      log(`Статус: ${error.response.status}`, 'error');
      log(`Ответ сервера: ${JSON.stringify(error.response.data)}`, 'error');
    }
    return null;
  }
}

// Функция для генерации черновика новеллы
async function generateNovelDraft(prompt) {
  const url = `${config.baseUrl}${config.api.novels.generate.config}`;
  
  log(`Генерация черновика новеллы...`, 'info');
  
  if (!jwtToken) {
    log('Ошибка: JWT токен отсутствует. Невозможно сгенерировать черновик.', 'error');
    return null;
  }
  
  try {
    const response = await axios.post(url, {
      user_prompt: prompt
    }, {
      headers: {
        'Authorization': `Bearer ${jwtToken}`
      }
    });
    
    log('Отправлен запрос на генерацию черновика. Ожидание завершения...', 'info');
    
    // Получаем ID задачи
    const taskId = response.data.task_id;
    
    // Ожидаем завершения задачи
    const taskResult = await waitForTaskCompletion(taskId);
    
    if (!taskResult) {
      log('Ошибка при ожидании завершения задачи генерации черновика.', 'error');
      return null;
    }
    
    log('Черновик новеллы успешно сгенерирован!', 'success');
    return taskResult.result;
  } catch (error) {
    log(`Ошибка при генерации черновика: ${error.message}`, 'error');
    if (error.response) {
      log(`Статус: ${error.response.status}`, 'error');
      log(`Ответ сервера: ${JSON.stringify(error.response.data)}`, 'error');
    }
    return null;
  }
}

// Функция для модификации черновика новеллы
async function modifyNovelDraft(draftId, modificationPrompt) {
  // Формируем URL с ID драфта
  const url = `${config.baseUrl}${config.api.novels.generate.draftModify.replace('{id}', draftId)}`;
  
  log(`Модификация черновика ${draftId}...`, 'info');
  
  if (!jwtToken) {
    log('Ошибка: JWT токен отсутствует. Невозможно модифицировать черновик.', 'error');
    return null;
  }

  if (!draftId) {
      log('Ошибка: Отсутствует ID черновика. Невозможно модифицировать.', 'error');
      return null;
  }
  
  try {
    const response = await axios.post(url, {
      modification_prompt: modificationPrompt
    }, {
      headers: {
        'Authorization': `Bearer ${jwtToken}`,
        'Content-Type': 'application/json' // Явно указываем тип контента
      }
    });
    
    log('Отправлен запрос на модификацию черновика. Ожидание завершения...', 'info');
    
    // Получаем ID задачи
    const taskId = response.data.task_id;
    if (!taskId) {
      log('Ошибка: Не удалось получить ID задачи из ответа сервера.', 'error');
      return null;
    }
    
    // Ожидаем завершения задачи
    const taskResult = await waitForTaskCompletion(taskId);
    
    if (!taskResult) {
      log('Ошибка при ожидании завершения задачи модификации черновика.', 'error');
      return null;
    }
    
    log('Черновик новеллы успешно модифицирован!', 'success');
    return taskResult.result; // Возвращаем обновленный draftView
  } catch (error) {
    log(`Ошибка при модификации черновика: ${error.message}`, 'error');
    if (error.response) {
      log(`Статус: ${error.response.status}`, 'error');
      log(`Ответ сервера: ${JSON.stringify(error.response.data)}`, 'error');
    }
    return null;
  }
}

// Функция для ожидания завершения задачи (переделанная под WebSocket)
async function waitForTaskCompletion(taskId, timeout = 120000) { // Таймаут по умолчанию 2 минуты
  log(`Ожидание завершения задачи ${taskId} (через WebSocket)...`, 'info');

  if (!ws || ws.readyState !== WebSocket.OPEN) {
    log('WebSocket не подключен. Попытка проверки статуса через HTTP...', 'warning');
    // Можно оставить старый HTTP polling как fallback, но для чистоты примера пока уберем
    // return await pollTaskStatusFallback(taskId); // Пример функции fallback
     return Promise.reject(new Error('WebSocket не подключен.'));
  }

  return new Promise((resolve, reject) => {
    // Устанавливаем таймаут
    const timeoutId = setTimeout(() => {
      if (pendingTasks.has(taskId)) {
        log(`Таймаут ожидания (${timeout / 1000} сек) для задачи ${taskId} истек.`, 'error');
        pendingTasks.delete(taskId);
        reject(new Error(`Таймаут ожидания задачи ${taskId}`));
      }
    }, timeout);

    // Сохраняем промис и ID таймаута
    pendingTasks.set(taskId, { resolve, reject, timeoutId });

    // Отправляем сообщение на сервер для подписки на обновления (опционально)
    // Зависит от реализации сервера, нужно ли явно подписываться
    // if (ws && ws.readyState === WebSocket.OPEN) {
    //   ws.send(JSON.stringify({ action: 'subscribe', topic: `task:${taskId}` }));
    // }
  });
}

// Функция для генерации содержимого новеллы
async function generateNovelContent(novelId, userChoices = null) {
  const url = `${config.baseUrl}${config.api.novels.generate.content}`;
  
  log(`Генерация содержимого новеллы ${novelId}...${userChoices ? ' (с историей выборов: ' + userChoices.length + ' шт.)' : ''}`, 'info');
  
  if (!jwtToken) {
    log('Ошибка: JWT токен отсутствует. Невозможно сгенерировать содержимое.', 'error');
    return null;
  }

  const requestData = {
    novel_id: novelId,
    // Добавляем user_choice с явно отрицательными значениями,
    // чтобы сервер понимал, что это запрос без выборов
    user_choice: {
      choice_number: -1,
      choice_index: -1
    }
  };
  
  // Добавляем историю выборов пользователя, если она передана и не пуста
  if (userChoices && userChoices.length > 0) {
    requestData.user_choices = userChoices;
    // При наличии новых выборов удаляем "пустой" user_choice
    delete requestData.user_choice;
  }
  
  // Удаляем поле user_choice из запроса при простой загрузке игры
  // Этот запрос будет отправлен только если пользователь сделал новый выбор
  
  try {
    const response = await axios.post(url, requestData, {
      headers: {
        'Authorization': `Bearer ${jwtToken}`
      }
    });
    
    log('Отправлен запрос на генерацию содержимого. Ожидание завершения...', 'info');
    
    // Получаем ID задачи
    const taskId = response.data.task_id;
    
    // Ожидаем завершения задачи
    const taskResult = await waitForTaskCompletion(taskId); // taskResult - это уже содержимое поля result из WebSocket
    
    if (!taskResult) {
      log('Ошибка при ожидании завершения задачи генерации содержимого.', 'error');
      return null;
    }
    
    log('Содержимое новеллы успешно сгенерировано!', 'success');
    // Возвращаем сам taskResult, так как он уже содержит нужные данные
    return taskResult;
  } catch (error) {
    log(`Ошибка при генерации содержимого: ${error.message}`, 'error');
    if (error.response) {
      log(`Статус: ${error.response.status}`, 'error');
      log(`Ответ сервера: ${JSON.stringify(error.response.data)}`, 'error');
    }
    return null;
  }
}

// Функция для запуска настройки новеллы из черновика
async function setupNovelFromDraft(draftId) {
  const url = `${config.baseUrl}${config.api.novels.generate.setup}`; // Используем эндпоинт сетапа

  log(`Запуск настройки новеллы из черновика ${draftId}...`, 'info');

  if (!jwtToken) {
    log('Ошибка: JWT токен отсутствует. Невозможно запустить настройку.', 'error');
    return null;
  }

  if (!draftId) {
      log('Ошибка: Отсутствует ID черновика.', 'error'); 
      return null;
  }

  // Формируем тело запроса для /api/generate/setup
  const requestBody = {
      draft_id: draftId, 
  };

  try {
    const response = await axios.post(url, requestBody, {
      headers: {
        'Authorization': `Bearer ${jwtToken}`,
        'Content-Type': 'application/json'
      }
    });

    log('Отправлен запрос на настройку новеллы. Ожидание завершения...', 'info');

    // Получаем ID задачи
    const taskId = response.data.task_id;
    if (!taskId) {
      log('Ошибка: Не удалось получить ID задачи из ответа сервера.', 'error');
      return null;
    }

    // Ожидаем завершения задачи
    const taskResult = await waitForTaskCompletion(taskId);

    if (!taskResult) {
      log('Ошибка при ожидании завершения задачи настройки новеллы.', 'error');
      return null;
    }

    log('Новелла успешно настроена!', 'success');
    
    // Получаем результат с данными новеллы
    const setupResult = taskResult.result;
    
    // Для отладки выведем полученный результат
    log(`Получен результат setupNovelTask: ${JSON.stringify(setupResult, null, 2)}`, 'debug');
    
    // Проверяем структуру полученного результата
    if (!setupResult || !setupResult.novel_id) {
      log('Ошибка: Ответ сервера не содержит ID новеллы.', 'error');
      return null;
    }
    
    // Сохраняем базовую информацию из ответа setupNovelTask
    fs.writeFileSync(
      `novel_setup_result_${setupResult.novel_id}.json`,
      JSON.stringify(setupResult, null, 2)
    );
    log(`Результат setup сохранен в novel_setup_result_${setupResult.novel_id}.json`, 'success');
    
    // После создания новеллы получаем полную информацию о ней
    log(`Запрашиваем полные данные новеллы ${setupResult.novel_id}...`, 'info');
    const fullNovelData = await getNovel(setupResult.novel_id);
    
    if (fullNovelData) {
      log(`Полные данные новеллы ${setupResult.novel_id} получены и сохранены`, 'success');
      // Объединяем данные из setupResult и полные данные новеллы
      return {
        ...setupResult,
        fullData: fullNovelData
      };
    }
    
    return setupResult;
  } catch (error) {
    log(`Ошибка при запуске настройки новеллы: ${error.message}`, 'error');
    if (error.response) {
      log(`Статус: ${error.response.status}`, 'error');
      log(`Ответ сервера: ${JSON.stringify(error.response.data)}`, 'error');
    }
    return null;
  }
}

// Функция для отправки уведомления о Game Over и получения концовки
async function handleLocalGameOver(novelId, reason, userChoices = null, rl) {
  const url = `${config.baseUrl}${config.api.novels.gameOver.replace('{id}', novelId)}`;
  log(`Отправка уведомления о Game Over на сервер (URL: ${url}, Reason: ${JSON.stringify(reason)})...`, 'info');

  if (!jwtToken) {
    log('Ошибка: JWT токен отсутствует. Невозможно отправить уведомление о Game Over.', 'error');
    return {
      endingText: "Игра окончена (ошибка связи с сервером).",
      canContinue: false
    };
  }

  // Формируем сообщение о причине Game Over для отображения пользователю
  let reasonDescription = "";
  if (reason && reason.stat_name) {
    const condition = reason.condition || "";
    const statName = reason.stat_name;
    const value = reason.value || 0;
    const threshold = reason.threshold !== undefined ? reason.threshold : 
                     (condition === "min" ? config.stats.defaultMin : config.stats.defaultMax);
    
    reasonDescription = `${statName} ${condition === "min" ? "опустился до минимума" : "достиг максимума"} ` +
                       `(значение: ${value}, порог: ${threshold})`;
    
    log(`Причина окончания игры: ${reasonDescription}`, 'info');
  }

  const requestData = { reason: reason };
  
  // Добавляем историю выборов, если она передана
  if (userChoices && userChoices.length > 0) {
    requestData.user_choices = userChoices;
  }

  try {
    const response = await axios.post(url, requestData, {
      headers: {
        'Authorization': `Bearer ${jwtToken}`,
        'Content-Type': 'application/json'
      }
    });

    // Проверяем наличие данных в ответе
    if (!response.data) {
      log('Ошибка: Пустой ответ от сервера.', 'error');
      return {
        endingText: `Игра окончена. ${reasonDescription}`,
        canContinue: false
      };
    }

    // Отладочное логирование полного ответа сервера
    log(`Полный ответ сервера при Game Over: ${JSON.stringify(response.data)}`, 'debug');

    // Проверяем, содержит ли ответ текст концовки
    const endingText = response.data.ending_text || "";
    log(`Текст концовки: ${endingText}`, 'debug');
    
    // Проверяем возможность продолжения игры
    const canContinue = response.data.can_continue === true;
    log(`Возможность продолжения: ${canContinue}`, 'debug');
    
    const newCharacter = response.data.new_character || null;
    log(`Описание нового персонажа: ${newCharacter}`, 'debug');
    
    const newCoreStats = response.data.new_core_stats || null;
    log(`Статы нового персонажа: ${JSON.stringify(newCoreStats)}`, 'debug');
    
    // Получаем начальные выборы и проверяем их формат
    let newInitialChoices = response.data.initial_choices || null;
    log(`Начальные выборы для нового персонажа (raw): ${JSON.stringify(newInitialChoices)}`, 'debug');
    
    // Проверяем и нормализуем формат начальных выборов
    if (newInitialChoices && Array.isArray(newInitialChoices)) {
      log(`Проверка формата начальных выборов...`, 'debug');
      
      // Если в первом элементе есть поле Description (верхний регистр), это CamelCase формат
      if (newInitialChoices.length > 0 && newInitialChoices[0].Description !== undefined) {
        log(`Начальные выборы в формате CamelCase, преобразуем в snake_case`, 'debug');
        
        // Преобразуем формат из CamelCase в snake_case
        newInitialChoices = newInitialChoices.map(choice => ({
          description: choice.Description || '',
          choices: (choice.Choices || []).map(option => ({
            text: option.Text || '',
            consequences: {
              core_stats_change: option.Consequences?.CoreStatsChange || {},
              global_flags: option.Consequences?.GlobalFlags || [],
              story_variables: option.Consequences?.StoryVariables || {},
              response_text: option.Consequences?.ResponseText || ''
            }
          })),
          shuffleable: choice.Shuffleable || false
        }));
        
        log(`Преобразованный формат начальных выборов: ${JSON.stringify(newInitialChoices)}`, 'debug');
      }
    }

    log(canContinue ? 'Сервер предлагает продолжить игру за нового персонажа.' : 'Игра окончена без возможности продолжения.', canContinue ? 'info' : 'warning');

    return {
      endingText: endingText,
      canContinue: canContinue,
      newCharacter: newCharacter,
      newCoreStats: newCoreStats,
      newInitialChoices: newInitialChoices
    };
  } catch (error) {
    log(`Ошибка при отправке уведомления о Game Over: ${error.message}`, 'error');
    if (error.response) {
      log(`Статус: ${error.response.status}`, 'error');
      log(`Ответ сервера: ${JSON.stringify(error.response.data)}`, 'error');
    }
    return {
      endingText: `Игра окончена. ${reasonDescription}`,
      canContinue: false
    };
  }
}

// Функция для обработки Game Over и возможности продолжения игры
async function handleGameOverWithContinuation(novel, gameOverReason, rl) {
  // Вызываем функцию для получения текста концовки с сервера
  const gameOverResult = await handleLocalGameOver(novel.id, gameOverReason, novel.userChoices, rl);
  
  log('Получен результат Game Over:', 'debug');
  log(`canContinue: ${gameOverResult.canContinue}`, 'debug');
  log(`newCharacter: ${gameOverResult.newCharacter}`, 'debug');
  log(`newCoreStats: ${JSON.stringify(gameOverResult.newCoreStats)}`, 'debug');
  log(`newInitialChoices: ${gameOverResult.newInitialChoices ? 'present' : 'absent'}`, 'debug');
  
  // Отображаем текст концовки
  log('\n=== ФИНАЛЬНЫЙ ТЕКСТ ===', 'heading');
  log(gameOverResult.endingText, 'text');
  
  // Если продолжение не поддерживается, просто возвращаемся
  if (!gameOverResult.canContinue) {
    log('Продолжение игры не поддерживается (canContinue = false)', 'warning');
    return false;
  }
  
  if (!gameOverResult.newCharacter) {
    log('Отсутствует описание нового персонажа', 'warning');
    return false;
  }
  
  if (!gameOverResult.newCoreStats) {
    log('Отсутствуют начальные статы для нового персонажа', 'warning');
    return false;
  }
  
  // Предлагаем пользователю продолжить игру за нового персонажа
  log('\n=== ПРОДОЛЖЕНИЕ ИГРЫ ===', 'heading');
  log(`Вы можете продолжить игру за нового персонажа: ${gameOverResult.newCharacter}`, 'info');
  
  const answer = await askQuestion('Хотите продолжить игру за нового персонажа? (да/нет): ');
  
  if (answer.toLowerCase() !== 'да') {
    log('Игра завершена.', 'info');
    return false;
  }
  
  log('Продолжаем игру за нового персонажа...', 'info');
  
  // Сохраняем новые данные в файл для возможного последующего использования
  const continuationData = {
    novelId: novel.id,
    oldCharacterEndingText: gameOverResult.endingText,
    newCharacter: gameOverResult.newCharacter,
    newCoreStats: gameOverResult.newCoreStats,
    newInitialChoices: gameOverResult.newInitialChoices,
    timestamp: new Date().toISOString()
  };
  
  fs.writeFileSync(
    `novel_continuation_${novel.id}.json`,
    JSON.stringify(continuationData, null, 2)
  );
  log(`Данные о продолжении сохранены в novel_continuation_${novel.id}.json`, 'success');
  
  // Инициализируем новый объект novel с данными нового персонажа
  novel.state.core_stats = gameOverResult.newCoreStats;
  novel.history = []; // Сбрасываем историю
  novel.userChoices = []; // Сбрасываем выборы пользователя
  
  // Если есть начальные выборы, устанавливаем их как текущий контент
  if (gameOverResult.newInitialChoices && Array.isArray(gameOverResult.newInitialChoices)) {
    return {
      continue: true,
      novel: novel,
      initialChoices: gameOverResult.newInitialChoices
    };
  }
  
  return {
    continue: true,
    novel: novel,
    initialChoices: null
  };
}

// Основная логика взаимодействия с пользователем
async function startInteraction() {
  // Авторизация или регистрация
  console.log(chalk.cyan('\n===== АВТОРИЗАЦИЯ / РЕГИСТРАЦИЯ ====='));
    let authAction = '';
    while (authAction !== '1' && authAction !== '2') {
      authAction = await askQuestion(chalk.yellow('Выберите действие:\n[1] Вход\n[2] Регистрация\nВыбор: '));
      if (authAction !== '1' && authAction !== '2') {
        console.log(chalk.red('Пожалуйста, введите 1 или 2.'));
      }
    }
    
    let username, email, password;
  let loggedIn = false;
    
  while (!loggedIn) {
    if (authAction === '1') {
      // Вход
      username = await askQuestion(chalk.yellow('Введите имя пользователя: '));
      password = await askQuestion(chalk.yellow('Введите пароль: '));
      
      loggedIn = await login(username, password);
      if (!loggedIn) {
        log('Не удалось войти. Попробуйте еще раз или зарегистрируйтесь.', 'error');
        authAction = '2'; // Предлагаем регистрацию при неудачном входе
      }
    } else {
      // Регистрация
      username = await askQuestion(chalk.yellow('Введите имя пользователя для регистрации: '));
      email = await askQuestion(chalk.yellow('Введите email: '));
      password = await askQuestion(chalk.yellow('Введите пароль: '));
      
      const registrationResult = await register(username, email, password);
      if (registrationResult) {
        log('Регистрация успешна! Теперь попробуйте войти.', 'success');
        authAction = '1'; // Предлагаем войти после успешной регистрации
      } else {
        log('Не удалось зарегистрироваться. Попробуйте другое имя пользователя/email.', 'error');
        // Остаемся в цикле, предлагаем регистрацию снова
      }
    }
  }
  
  if (!jwtToken || !userId) {
    log('Не удалось авторизоваться или получить ID пользователя. Завершение работы.', 'error');
        rl.close();
        return;
    }
    
  // Отображаем основное меню
    console.log(chalk.cyan('\n===== ГЛАВНОЕ МЕНЮ ====='));
  console.log(chalk.cyan('[1] Создать новую новеллу'));
  console.log(chalk.cyan('[2] Просмотреть мои черновики'));
  console.log(chalk.cyan('[3] Просмотреть мои новеллы'));
    console.log(chalk.cyan('=========================\n'));

  rl.question(chalk.yellow('Ваш выбор (1/2/3): '), async function(choice) {
    switch(choice) {
      case '1':
        await createNewNovelFlow(rl);
        break;
      case '2':
        await viewDraftsFlow(rl);
        break;
      case '3':
        await viewNovelsFlow(rl);
        break;
      default:
        log('Неверный выбор. Выход.', 'error');
        rl.close();
    }
  });
}

// Процесс создания новой новеллы
async function createNewNovelFlow(rl) {
  rl.question('Введите описание новеллы: ', async function(prompt) {
    prompt = prompt.trim() || config.defaultPrompt;
    
    log(`Используем запрос: "${prompt}"`, 'info');
    
    // Генерируем черновик
    const draft = await generateNovelDraft(prompt);

    // <<< ДОБАВЛЕНЫ ЛОГИ >>>
    log(`Результат generateNovelDraft: ${JSON.stringify(draft)}`, 'info'); 
    console.log('Проверка draft:', draft); // Прямой вывод объекта

    if (!draft) {
      log('Не удалось создать черновик. Выход.', 'error');
      rl.close();
      return;
    }
    
    // Спрашиваем, хочет ли пользователь продолжить с этим черновиком
    rl.question('Хотите настроить новеллу из этого черновика? (да/нет): ', async function(answer) {
      if (answer.toLowerCase() === 'да') {
        // Запускаем настройку новеллы
        const setupResult = await setupNovelFromDraft(draft.id); // Передаем draft ID
        if (!setupResult) {
          log('Не удалось настроить новеллу. Выход.', 'error');
          rl.close();
          return;
        }
      
        const novelId = setupResult.novel_id;
        log(`Новелла создана с ID: ${novelId}`, 'success');
        
        // Запускаем генерацию первой сцены
        await startGameplayFlow(rl, novelId);
      } else {
        log('Черновик сохранен, но настройка не запущена.', 'info');
        rl.close();
      }
    });
  });
}

// Процесс просмотра черновиков
async function viewDraftsFlow(rl) {
  // Получаем список черновиков
  const drafts = await fetchUserDrafts();

  if (drafts.length === 0) {
    log('У вас нет сохраненных черновиков.', 'info');
    rl.close();
    return;
  }

  // Отображаем стилизованный список черновиков
  console.log(chalk.cyan('\n===== ВАШИ ЧЕРНОВИКИ ====='));
  drafts.forEach((draft, index) => {
    console.log(chalk.cyan(`[${index + 1}] ${chalk.white(draft.config?.title || 'Без названия')} (ID: ${draft.id})`));
    console.log(chalk.gray(`   Создан: ${new Date(draft.created_at).toLocaleString()}`));
    console.log(chalk.gray(`   Запрос: ${draft.user_prompt}`));
  });
  console.log(chalk.cyan('=========================\n'));

  const choice = await askQuestion(chalk.yellow('Выберите номер черновика для просмотра деталей (или 0 для выхода): '));
  const draftIndex = parseInt(choice) - 1;

  if (isNaN(draftIndex) || draftIndex < -1 || draftIndex >= drafts.length) {
    log('Неверный выбор. Выход.', 'error');
    rl.close();
    return;
  }

  if (draftIndex === -1) {
    log('Выход.', 'info');
    rl.close();
    return;
  }

  const selectedDraft = drafts[draftIndex];
  log(`Выбран черновик ID: ${selectedDraft.id}`, 'info');

  // Запускаем просмотр и взаимодействие с деталями черновика
  await viewDraftDetailsFlow(rl, selectedDraft.id);
}

// Функция для отображения деталей черновика и взаимодействия
async function viewDraftDetailsFlow(rl, draftId) {
  let currentDraftView = await fetchDraftDetails(draftId);

  if (!currentDraftView) {
    log('Не удалось загрузить детали черновика.', 'error');
        rl.close();
        return;
      }
      
  let exitFlow = false;
  while (!exitFlow) {
    // Отображаем детали черновика (NovelDraftView)
    console.log(chalk.cyan('\n===== ДЕТАЛИ ЧЕРНОВИКА ====='));
    console.log(chalk.white(`ID: ${currentDraftView.draft_id}`));
    console.log(chalk.white(`Название: ${currentDraftView.title || '-'}`));
    console.log(chalk.white(`Описание: ${currentDraftView.short_description || '-'}`));
    console.log(chalk.white(`Франшиза: ${currentDraftView.franchise || '-'}`));
    console.log(chalk.white(`Жанр: ${currentDraftView.genre || '-'}`));
    console.log(chalk.white(`18+: ${currentDraftView.is_adult_content ? 'Да' : 'Нет'}`));
    console.log(chalk.white(`Имя игрока: ${currentDraftView.player_name || '-'}`));
    console.log(chalk.white(`Пол игрока: ${currentDraftView.player_gender || '-'}`));
    console.log(chalk.white(`Описание игрока: ${currentDraftView.player_description || '-'}`));
    console.log(chalk.white(`Контекст мира: ${currentDraftView.world_context || '-'}`));
    if (currentDraftView.themes && currentDraftView.themes.length > 0) {
      console.log(chalk.white(`Темы: ${currentDraftView.themes.join(', ')}`));
    }
    if (currentDraftView.core_stats) {
      console.log(chalk.yellow('\nОсновные статы:'));
      for (const [name, stat] of Object.entries(currentDraftView.core_stats)) {
        console.log(chalk.white(`  - ${name}:`));
        console.log(chalk.gray(`    Описание: ${stat.description}`));
        console.log(chalk.gray(`    Начальное значение: ${stat.initial_value}`));
        console.log(chalk.gray(`    Game Over (Min): ${stat.game_over_conditions.min}`));
        console.log(chalk.gray(`    Game Over (Max): ${stat.game_over_conditions.max}`));
      }
    }
    console.log(chalk.cyan('===========================\n'));

    // Меню действий
    console.log(chalk.cyan('===== ДЕЙСТВИЯ С ЧЕРНОВИКОМ ====='));
    console.log(chalk.cyan('[1] Настроить новеллу из этого черновика'));
    console.log(chalk.cyan('[2] Изменить этот черновик'));
    console.log(chalk.cyan('[0] Назад к списку черновиков'));
    console.log(chalk.cyan('=================================\n'));

    const actionChoice = await askQuestion(chalk.yellow('Ваш выбор (1/2/0): '));

    switch (actionChoice) {
      case '1': // Настроить новеллу
        log('Запуск настройки новеллы...', 'info');
        const setupResult = await setupNovelFromDraft(draftId); // Передаем только ID
        if (!setupResult) {
          log('Не удалось настроить новеллу. Возврат к деталям черновика.', 'error');
        } else { 
          const novelId = setupResult.novel_id;
          log(`Новелла создана с ID: ${novelId}`, 'success');
          
          exitFlow = true; // Выходим из этого потока после успешной настройки
          await startGameplayFlow(rl, novelId); // Запускаем игру
        }
        break;
      case '2': // Изменить черновик
        const modificationPrompt = await askQuestion(chalk.yellow('Введите, что вы хотите изменить: '));
        if (modificationPrompt.trim()) {
          log('Запуск модификации черновика...', 'info');
          const modifiedDraftView = await modifyNovelDraft(draftId, modificationPrompt);
          if (modifiedDraftView) {
            currentDraftView = modifiedDraftView; // Обновляем данные для отображения
            log('Черновик успешно изменен.', 'success');
          } else {
            log('Не удалось изменить черновик.', 'error');
          }
        } else {
          log('Запрос на модификацию пуст.', 'warning');
        }
        // Остаемся в цикле, показываем обновленные детали
        break;
      case '0': // Назад
        exitFlow = true;
        log('Возврат к списку черновиков...', 'info');
        // Нужно перезапустить viewDraftsFlow или вернуться в главное меню
        // Для простоты пока просто выйдем
        rl.close();
        break;
      default:
        log('Неверный выбор. Пожалуйста, выберите 1, 2 или 0.', 'error');
        break;
    }
  }
}

// Процесс просмотра новелл
async function viewNovelsFlow(rl) {
  // Получаем список новелл пользователя
  const novels = await fetchUserNovelsList();
  
  if (!novels || novels.length === 0) {
    log('У вас нет созданных новелл.', 'info');
    rl.close();
    return;
  }

  log('Ваши новеллы:', 'info');
  novels.forEach((novel, index) => {
    log(`${index + 1}. ${novel.title || 'Без названия'} (ID: ${novel.id})`, 'info');
  });
  
  rl.question('Выберите номер новеллы для игры (или 0 для выхода): ', async function(choice) {
    const novelIndex = parseInt(choice) - 1;
    
    if (isNaN(novelIndex) || novelIndex < -1 || novelIndex >= novels.length) {
      log('Неверный выбор. Выход.', 'error');
      rl.close();
      return;
    }
    
    if (novelIndex === -1) {
      log('Выход.', 'info');
      rl.close();
      return;
    }
    
    const selectedNovel = novels[novelIndex];
    log(`Выбрана новелла: ${selectedNovel.title || 'Без названия'}`, 'info');
    
    // Получаем полные данные новеллы перед запуском игры
    const novelData = await getNovel(selectedNovel.id);
    if (!novelData) {
      log('Не удалось получить данные новеллы. Выход.', 'error');
      rl.close();
      return;
    }
    
    // Запускаем игровой процесс
    await startGameplayFlow(rl, selectedNovel.id);
  });
}

// Функция для логирования ответа сервера в отдельный файл
function logServerResponse(content, novelId, batchNumber) {
  if (!content) return;
  
  try {
    const timestamp = new Date().toISOString().replace(/[:.]/g, '-');
    const filename = `server_response_${novelId}_batch${batchNumber}_${timestamp}.json`;
    fs.writeFileSync(filename, JSON.stringify(content, null, 2));
    log(`Ответ сервера сохранен в файл ${filename}`, 'debug');
    
    // Анализируем ответ на наличие core_stats_change
    let hasChanges = false;
    if (content.choices && Array.isArray(content.choices)) {
      content.choices.forEach((event, eventIndex) => {
        if (event.choices && Array.isArray(event.choices)) {
          event.choices.forEach((choice, choiceIndex) => {
            if (choice.consequences) {
              if (choice.consequences.core_stats_change) {
                hasChanges = true;
                log(`[Анализ] Найден core_stats_change в выборе [${eventIndex}][${choiceIndex}]: ${JSON.stringify(choice.consequences.core_stats_change)}`, 'info');
              } else if (choice.consequences.core_stats) {
                hasChanges = true;
                log(`[Анализ] Найден core_stats (старый формат) в выборе [${eventIndex}][${choiceIndex}]: ${JSON.stringify(choice.consequences.core_stats)}`, 'info');
              }
            }
          });
        }
      });
    }
    
    if (!hasChanges) {
      log(`[Анализ] В ответе сервера НЕ найдены изменения core_stats`, 'warning');
    }
  } catch (error) {
    log(`Ошибка при сохранении ответа сервера: ${error.message}`, 'error');
  }
}

// Функция для отображения статов персонажа
function displayStats(stats, definitions) {
  log('\nХарактеристики персонажа:', 'info');
  
  // Проверяем наличие статов
  if (!stats || Object.keys(stats).length === 0) {
    log(`Внимание: Объект core_stats пуст или не инициализирован: ${JSON.stringify(stats)}`, 'warning');
    log(`Определения: ${JSON.stringify(definitions)}`, 'debug');
    return;
  }

  if (!definitions || Object.keys(definitions).length === 0) {
    log(`Внимание: Определения статов отсутствуют или не инициализированы: ${JSON.stringify(definitions)}`, 'warning');
  }

  // Отображаем статы с их определениями, если они доступны
  let displayed = false;
  Object.entries(stats).forEach(([statName, value]) => {
    const statDef = definitions && definitions[statName] ? definitions[statName] : null;
    const initial = statDef && statDef.initial_value !== undefined ? statDef.initial_value : '?';
    const diff = initial !== '?' ? value - initial : '?';
    const diffStr = diff !== '?' ? (diff >= 0 ? `+${diff}` : `${diff}`) : '';
    
    let display = `${statName}: ${value} (начальное: ${initial}${diffStr ? ', изменение: ' + diffStr : ''})`;
    
    // Добавляем описание, если доступно
    if (statDef && statDef.description) {
      display += `\n    Описание: ${statDef.description}`;
    }
    
    // Добавляем информацию о наличии game over conditions
    if (statDef && statDef.game_over_conditions) {
      const conditions = statDef.game_over_conditions;
      const gameOverInfo = [];
      
      if (conditions.min === true) {
        gameOverInfo.push(`min: Game Over при 0 (текущее: ${value})`);
      }
      if (conditions.max === true) {
        gameOverInfo.push(`max: Game Over при 100 (текущее: ${value})`);
      }
      
      if (gameOverInfo.length > 0) {
        display += `\n    Условия окончания игры: ${gameOverInfo.join(", ")}`;
      }
    }
    
    log(`  ${display}`, 'stat');
    displayed = true;
  });
  
  if (!displayed) {
    log('  Нет доступных характеристик', 'warning');
  }
}

// Функция для отображения переменных состояния
function displayStateVariables(state) {
  // Проверяем, инициализировано ли состояние
  if (!state) {
    log('Ошибка: state не инициализирован', 'error');
    return;
  }

  log('\nCore Stats:', 'info');
  if (!state.core_stats || Object.keys(state.core_stats).length === 0) {
    log('  Нет доступных Core Stats', 'warning');
  } else {
    Object.entries(state.core_stats).forEach(([key, value]) => {
      log(`  ${key}: ${value}`, 'variable');
    });
  }

  log('\nGlobal Flags:', 'info');
  if (!state.global_flags || state.global_flags.length === 0) {
    log('  Нет активных Global Flags', 'warning');
  } else {
    state.global_flags.forEach(flag => {
      log(`  ${flag}`, 'flag');
    });
  }

  log('\nStory Variables:', 'info');
  if (!state.story_variables || Object.keys(state.story_variables).length === 0) {
    log('  Нет доступных Story Variables', 'warning');
  } else {
    // Сортируем ключи для более структурированного вывода
    const keys = Object.keys(state.story_variables).sort();
    
    for (const key of keys) {
      // Пропускаем флаги, которые начинаются с "flag_" так как они уже отображены в Global Flags
      if (key.startsWith('flag_')) continue;
      
      const value = state.story_variables[key];
      // Форматируем значение в зависимости от типа
      const formattedValue = typeof value === 'object' ? JSON.stringify(value) : value;
      log(`  ${key}: ${formattedValue}`, 'variable');
    }
  }
}

// Запускаем игровой процесс для новеллы
async function startGameplayFlow(rl, novelId) {
  log(`Начало игрового процесса для новеллы ${novelId}...`, 'info');
  
  // Создаем структуру для отслеживания состояния игры
  let novel = {
    id: novelId,
    userChoices: [], // История всех выборов пользователя (для отправки на сервер)
    history: [],     // История всех событий и выборов (для сохранения локально)
    state: {
      core_stats: {},       // Текущие статы
      global_flags: [],     // Глобальные флаги
      story_variables: {}   // Переменные сюжета
    },
    core_stats_definition: {} // Определение статов из конфига
  };

  // --- Шаг 1: Загружаем конфиг новеллы ---
  let novelConfig = await getNovel(novelId);

  if (novelConfig) {
    log(`Загружен конфиг новеллы ${novelId}`, 'info');
    log(`Title: ${novelConfig.title}`, 'info');
    log(`Description: ${novelConfig.description}`, 'info');
    
    // Добавляем поле Config в структуру novel
    novel.config = novelConfig.config;
    
    // Инициализируем core_stats из конфига
    if (novelConfig.config && novelConfig.config.core_stats) {
      // Сохраняем определения
      novel.core_stats_definition = novelConfig.config.core_stats;
      log(`Загружены определения core_stats из конфига`, 'info');
      
      // Инициализируем начальные значения статов
      if (!novel.state.core_stats) {
        novel.state.core_stats = {};
      }
      
      // Заполняем начальные значения из определений
      Object.entries(novelConfig.config.core_stats).forEach(([statName, statDef]) => {
        novel.state.core_stats[statName] = statDef.initial_value;
        log(`Инициализирован стат ${statName} = ${statDef.initial_value}`, 'debug');
      });
    } else {
      log(`Внимание: В конфиге новеллы отсутствуют core_stats`, 'warning');
    }
    
    // Инициализируем setup из конфига, если доступен
    if (novelConfig.setup) {
      novel.setup = novelConfig.setup;
      
      // Если в setup есть определение статов, используем его как более приоритетное
      if (novel.setup.core_stats_definition) {
        novel.core_stats_definition = novel.setup.core_stats_definition;
        log(`Загружены определения core_stats из setup`, 'info');
        
        // Переинициализируем начальные значения статов из setup
        Object.entries(novel.setup.core_stats_definition).forEach(([statName, statDef]) => {
          novel.state.core_stats[statName] = statDef.initial_value;
          log(`Инициализирован стат из setup ${statName} = ${statDef.initial_value}`, 'debug');
        });
      }
    }
  }

  // --- Шаг 2: Получаем первую сцену/данные --- 
  let currentContent = await generateNovelContent(novelId);

  // --- Проверка на ошибку или неверный формат начальных данных --- 
  if (!currentContent) {
    log('Не удалось получить начальные данные новеллы.', 'error');
    rl.close();
    return;
  }
  
  // Логируем первый ответ сервера
  logServerResponse(currentContent, novelId, 0);

  // --- Обновляем данные из первого ответа, если конфиг не был загружен ---
  if (!novelConfig || !novelConfig.config) {
    // Если не удалось загрузить конфиг, используем данные из первого ответа
    if (currentContent.core_stats) {
      novel.state.core_stats = currentContent.core_stats;
    }
    if (currentContent.core_stats_definition) {
      novel.core_stats_definition = currentContent.core_stats_definition;
    }
    log(`Инициализированы данные из первого ответа сервера`, 'info');
  }

  // --- Проверка на немедленную концовку (из первого ответа) --- 
  if (currentContent.ending_text) {
    log('\n=== КОНЕЦ ИГРЫ ===', 'heading');
    log(currentContent.ending_text, 'text');
    log('Игра завершилась сразу.', 'info');
    rl.close();
    return;
  }

  // --- Проверка наличия choices для начала игры --- 
  if (!currentContent.choices || !Array.isArray(currentContent.choices) || currentContent.choices.length === 0) {
    log('Ошибка: Начальные данные новеллы не содержат choices.', 'error');
    log(`Полученный currentContent: ${JSON.stringify(currentContent)}`, 'error');
    rl.close();
    return;
  }

  // --- Шаг 3: Основной цикл игры --- 
  let continueGame = true;
  while (continueGame) {
    // Обрабатываем все выборы в текущем батче (currentContent.choices должен быть валидным здесь)
    for (let eventIndex = 0; eventIndex < currentContent.choices.length; eventIndex++) {
      const event = currentContent.choices[eventIndex];
      if (!event || !event.description || !event.choices || event.choices.length !== 2) {
        log('Ошибка: Неверный формат события в батче.', 'error');
        log(`Проблемное событие: ${JSON.stringify(event)}`, 'error');
        continueGame = false; // Прерываем игру при ошибке формата
        break;
      }

      // Отображаем текущую ситуацию
      log(`\n=== Ситуация ===`, 'heading');
      log(event.description, 'text');

      // Показываем все переменные состояния
      displayStateVariables(novel.state);

      // Показываем текущие статы
      displayStats(novel.state.core_stats, novel.core_stats_definition);

      // Отображаем варианты выбора
      log('\nВарианты:', 'info');
      event.choices.forEach((option, index) => {
        log(`${index + 1}. ${option.text}`, 'choice');
      });

      // Получаем выбор пользователя
      let choiceIndex = -1;
      while (choiceIndex < 0 || choiceIndex >= event.choices.length) {
        const answer = await new Promise(resolve => {
          rl.question('Ваш выбор (1 или 2): ', resolve);
        });
        
        choiceIndex = parseInt(answer) - 1;
        if (isNaN(choiceIndex) || choiceIndex < 0 || choiceIndex >= event.choices.length) {
          log('Неверный выбор. Пожалуйста, введите 1 или 2.', 'error');
        }
      }

      const selectedOption = event.choices[choiceIndex];
      log(`Вы выбрали: ${selectedOption.text}`, 'info');

      // Записываем информацию о выборе пользователя для отправки на сервер
      const userChoice = {
        event_index: eventIndex,
        choice_index: choiceIndex
      };
      
      // Добавляем выбор в историю выборов
      novel.userChoices.push(userChoice);
      
      // Логируем полную структуру consequences для диагностики
      if (selectedOption.consequences) {
        log(`Последствия выбора: ${JSON.stringify(selectedOption.consequences)}`, 'debug');
      }
      
      // Применяем последствия выбора локально
      if (selectedOption.consequences) {
        // Отслеживаем изменения для суммарного логирования
        const appliedChanges = {
          core_stats: {},
          global_flags: [],
          story_variables: {}
        };
        
        // Обновляем статы
        if (selectedOption.consequences.core_stats_change) { // Правильное название поля из JSON
          Object.entries(selectedOption.consequences.core_stats_change).forEach(([stat, change]) => {
            if (!novel.state.core_stats[stat] && novel.state.core_stats[stat] !== 0) {
              // Инициализируем стат, если он еще не существует
              novel.state.core_stats[stat] = 0;
              log(`Инициализирован новый стат ${stat} = 0`, 'debug');
            }
            
            // Сохраняем предыдущее значение для логирования
            const oldValue = novel.state.core_stats[stat];
            
            // Обновляем стат с использованием вспомогательной функции
            novel.state.core_stats[stat] = updateStat(stat, novel.state.core_stats[stat], change, novel.core_stats_definition);
            
            // Записываем изменение для суммарного логирования
            appliedChanges.core_stats[stat] = {
              old: oldValue,
              new: novel.state.core_stats[stat],
              change: change
            };
            
            log(`Обновлен core_stat ${stat}: ${oldValue} -> ${novel.state.core_stats[stat]} (${change >= 0 ? '+' : ''}${change})`, 'info');
          });
        } else if (selectedOption.consequences.core_stats) { // Проверяем альтернативное название (для обратной совместимости)
          Object.entries(selectedOption.consequences.core_stats).forEach(([stat, change]) => {
            if (!novel.state.core_stats[stat] && novel.state.core_stats[stat] !== 0) {
              novel.state.core_stats[stat] = 0;
              log(`Инициализирован новый стат ${stat} = 0`, 'debug');
            }
            
            // Сохраняем предыдущее значение для логирования
            const oldValue = novel.state.core_stats[stat];
            
            novel.state.core_stats[stat] = updateStat(stat, novel.state.core_stats[stat], change, novel.core_stats_definition);
            
            // Записываем изменение для суммарного логирования
            appliedChanges.core_stats[stat] = {
              old: oldValue,
              new: novel.state.core_stats[stat],
              change: change
            };
            
            log(`Обновлен core_stat (старый формат) ${stat}: ${oldValue} -> ${novel.state.core_stats[stat]} (${change >= 0 ? '+' : ''}${change})`, 'info');
          });
        }

        // Добавляем глобальные флаги
        if (selectedOption.consequences.global_flags) {
          selectedOption.consequences.global_flags.forEach(flag => {
            if (!novel.state.global_flags.includes(flag)) {
              novel.state.global_flags.push(flag);
              // Записываем для суммарного логирования
              appliedChanges.global_flags.push(flag);
              log(`Добавлен флаг: ${flag}`, 'info');
            }
          });
        }
        
        // Добавляем переменные сюжета
        if (selectedOption.consequences.story_variables) {
            Object.entries(selectedOption.consequences.story_variables).forEach(([key, value]) => {
                // Сохраняем старое значение
                const oldValue = novel.state.story_variables[key];
                
                novel.state.story_variables[key] = value;
                
                // Записываем для суммарного логирования
                appliedChanges.story_variables[key] = {
                  old: oldValue,
                  new: value
                };
                
                log(`Установлена переменная сюжета: ${key} = ${JSON.stringify(value)}`, 'info');
            });
        }
        
        // Выводим суммарную информацию об изменениях
        let hasSummaryChanges = false;
        log('\n=== СУММАРНЫЕ ИЗМЕНЕНИЯ ===', 'heading');
        
        if (Object.keys(appliedChanges.core_stats).length > 0) {
          log('ХАРАКТЕРИСТИКИ:', 'info');
          Object.entries(appliedChanges.core_stats).forEach(([stat, info]) => {
            log(`  ${stat}: ${info.old} -> ${info.new} (${info.change >= 0 ? '+' : ''}${info.change})`, 'stat');
          });
          hasSummaryChanges = true;
        }
        
        if (appliedChanges.global_flags.length > 0) {
          log('ФЛАГИ:', 'info');
          appliedChanges.global_flags.forEach(flag => {
            log(`  + ${flag}`, 'flag');
          });
          hasSummaryChanges = true;
        }
        
        if (Object.keys(appliedChanges.story_variables).length > 0) {
          log('ПЕРЕМЕННЫЕ СЮЖЕТА:', 'info');
          Object.entries(appliedChanges.story_variables).forEach(([key, info]) => {
            log(`  ${key}: ${info.old || 'не установлено'} -> ${JSON.stringify(info.new)}`, 'variable');
          });
          hasSummaryChanges = true;
        }
        
        if (!hasSummaryChanges) {
          log('Нет изменений состояния игры', 'info');
        }
      }

      // Проверяем условия game over после каждого выбора
      let gameOver = false;
      let gameOverReasonDetails = null; // Храним детали для отправки на сервер

      Object.entries(novel.state.core_stats).forEach(([statName, currentValue]) => {
        const definition = novel.core_stats_definition[statName];
        // Проверяем наличие definition и game_over_conditions
        if (definition && definition.game_over_conditions) {
          const conditions = definition.game_over_conditions;

          // Проверка на минимум - применяем только если conditions.min === true
          if (conditions.min === true && currentValue <= 0) {
            gameOver = true;
            gameOverReasonDetails = {
              stat_name: statName,
              condition: "min",
              value: currentValue,
              threshold: 0
            };
            return; // Выходим из forEach, если нашли причину
          }

          // Проверка на максимум - применяем только если conditions.max === true
          if (conditions.max === true && currentValue >= 100) {
            gameOver = true;
            gameOverReasonDetails = {
              stat_name: statName,
              condition: "max",
              value: currentValue,
              threshold: 100
            };
            return; // Выходим из forEach, если нашли причину
          }
        }
      });

      if (gameOver) {
        log('\n=== ИГРА ОКОНЧЕНА (локально по статам) ===', 'heading');
        log(`Причина: Стат '${gameOverReasonDetails.stat_name}' (${gameOverReasonDetails.value}) нарушил условие '${gameOverReasonDetails.condition}'.`, 'text');

        // Вызываем функцию для обработки Game Over с возможностью продолжения
        const continuationResult = await handleGameOverWithContinuation(novel, gameOverReasonDetails, rl);
        
        log(`Результат продолжения игры: ${JSON.stringify(continuationResult)}`, 'debug');
        
        if (continuationResult && continuationResult.continue) {
          // Продолжаем игру за нового персонажа
          log('\n=== ПРОДОЛЖЕНИЕ ИГРЫ ЗА НОВОГО ПЕРСОНАЖА ===', 'heading');
          
          // Обновляем объект novel с данными нового персонажа (уже сделано в handleGameOverWithContinuation)
          novel = continuationResult.novel;
          
          // Если есть начальные выборы для нового персонажа, используем их
          if (continuationResult.initialChoices) {
            log(`Преобразуем начальные выборы для нового персонажа. Формат: ${typeof continuationResult.initialChoices}`, 'debug');
            log(`Содержимое: ${JSON.stringify(continuationResult.initialChoices)}`, 'debug');
            
            // Создаем объект currentContent в формате, ожидаемом игровым циклом
            currentContent = {
              choices: continuationResult.initialChoices
            };
            
            // Проверяем, что в новых выборах есть поле "description"
            if (currentContent.choices.length > 0 && !currentContent.choices[0].description) {
              log('Внимание: Начальные выборы имеют неправильный формат. Пробуем исправить...', 'warning');
              
              // Логируем ожидаемую структуру
              log(`Ожидаемая структура: array of { description, choices[] }`, 'debug');
              log(`Полученная структура: ${JSON.stringify(currentContent.choices[0])}`, 'debug');
              
              // Проверяем, нужно ли преобразовать формат данных
              if (currentContent.choices.length > 0 && currentContent.choices[0].Choices) {
                log('Преобразуем формат полей из CamelCase в snake_case', 'debug');
                currentContent.choices = currentContent.choices.map(choice => ({
                  description: choice.Description || '',
                  choices: (choice.Choices || []).map(option => ({
                    text: option.Text || '',
                    consequences: {
                      core_stats_change: option.Consequences?.CoreStatsChange || {},
                      global_flags: option.Consequences?.GlobalFlags || [],
                      story_variables: option.Consequences?.StoryVariables || {},
                      response_text: option.Consequences?.ResponseText || ''
                    }
                  })),
                  shuffleable: choice.Shuffleable || false
                }));
              }
            }
            
            // Продолжаем игровой цикл
            continue;
          } else {
            // Запрашиваем новый контент для нового персонажа
            currentContent = await generateNovelContent(novel.id);
            if (!currentContent) {
              log('Не удалось получить начальные данные для нового персонажа. Завершение игры.', 'error');
              continueGame = false;
            } else if (currentContent.ending_text) {
              log('\n=== КОНЕЦ ИГРЫ ===', 'heading');
              log(currentContent.ending_text, 'text');
              continueGame = false;
            } else if (currentContent.choices && Array.isArray(currentContent.choices) && currentContent.choices.length > 0) {
              // Логируем ответ сервера для нового персонажа
              logServerResponse(currentContent, novel.id, 0);
              // Продолжаем игровой цикл с новым контентом
              continue;
            } else {
              log('Ошибка: Получен некорректный ответ от сервера для нового персонажа.', 'error');
              continueGame = false;
            }
          }
        } else {
          // Пользователь отказался продолжать или продолжение невозможно
          continueGame = false;
        }
        
        break; // Выходим из цикла for (обработки событий батча)
      }

      // Сохраняем выбор в историю
      novel.history.push({
        description: event.description,
        choice: selectedOption.text,
        consequences: selectedOption.consequences
      });
    } // Конец цикла for по событиям текущего батча

    // Если игра не окончена (ни локально, ни из-за ошибки формата) и все выборы в текущем батче обработаны,
    // запрашиваем следующий батч
    if (continueGame) {
      log('\nЗапрос следующего батча контента...', 'info');
      
      // Логируем текущее состояние перед запросом следующего батча
      log('\nТекущие значения статов перед запросом:', 'info');
      displayStats(novel.state.core_stats, novel.core_stats_definition);
      
      // Передаем на сервер всю историю выборов пользователя
      const nextContent = await generateNovelContent(novel.id, novel.userChoices);

      if (!nextContent) {
        log('Не удалось получить следующий батч событий. Завершение игры.', 'error');
        continueGame = false;
        continue; // Переходим к следующей итерации while, где continueGame=false завершит цикл
      }
      
      // Логируем ответ сервера для следующего батча
      logServerResponse(nextContent, novel.id, novel.userChoices.length);
      
      // Проверяем, не содержит ли ответ обновленные core_stats
      if (nextContent.core_stats && Object.keys(nextContent.core_stats).length > 0) {
        log('В ответе найдены обновленные core_stats от сервера!', 'info');
        log(`Текущие статы: ${JSON.stringify(novel.state.core_stats)}`, 'debug');
        log(`Статы от сервера: ${JSON.stringify(nextContent.core_stats)}`, 'debug');
        
        // Сравниваем текущие статы с полученными от сервера
        Object.entries(nextContent.core_stats).forEach(([statName, value]) => {
          if (novel.state.core_stats[statName] !== value) {
            log(`Обновляем стат ${statName}: ${novel.state.core_stats[statName]} -> ${value} (сервер)`, 'info');
            novel.state.core_stats[statName] = value;
          }
        });
      } else {
        // Проверяем, есть ли статы, которые отсутствуют в состоянии
        // но присутствуют в определениях, и инициализируем их
        if (novel.core_stats_definition) {
          let statsInitialized = false;
          Object.entries(novel.core_stats_definition).forEach(([statName, statDef]) => {
            if (novel.state.core_stats[statName] === undefined) {
              novel.state.core_stats[statName] = statDef.initial_value;
              log(`Инициализирован отсутствующий стат ${statName} = ${statDef.initial_value}`, 'info');
              statsInitialized = true;
            }
          });
          
          if (statsInitialized) {
            log('Состояние core_stats обновлено из определений', 'info');
          }
        }
      }
      
      // Сохраняем полученный ответ во временный файл для анализа
      fs.writeFileSync(
        `last_server_response_${novel.id}.json`,
        JSON.stringify(nextContent, null, 2)
      );
      log('Ответ сервера сохранен во временный файл для анализа', 'debug');
      
      // Полное логирование структуры полученного ответа
      log(`Полная структура ответа от сервера: ${JSON.stringify(nextContent)}`, 'debug');

      // Проверяем, не пришла ли концовка
      if (nextContent.ending_text) {
        log('\n=== КОНЕЦ ИГРЫ ===', 'heading');
        log(nextContent.ending_text, 'text');
        continueGame = false; // Завершаем игру
      } else if (nextContent.choices && Array.isArray(nextContent.choices) && nextContent.choices.length > 0) {
        // Если пришел новый батч с выборами, обновляем currentContent
        currentContent = nextContent;
      } else {
        // Пришел непонятный ответ (не концовка и не choices)
        log('Ошибка: Получен некорректный ответ от сервера для следующего батча.', 'error');
        log(`Полученный ответ: ${JSON.stringify(nextContent)}`, 'error');
        continueGame = false;
      }
    }
  } // Конец основного цикла while(continueGame)

  // --- Шаг 4: Сохраняем результат --- 
  if (novel.history.length > 0) {
    fs.writeFileSync(
      config.outputFile,
      JSON.stringify({
        userId: userId,
        novel: {
          id: novel.id,
          final_stats: novel.state.core_stats,
          global_flags: novel.state.global_flags,
          story_variables: novel.state.story_variables
        },
        history: novel.history,
        userChoices: novel.userChoices // Добавляем также запись всех выборов в формате для сервера
      }, null, 2)
    );
    log(`\nИгра завершена. Результат сохранен в ${config.outputFile}`, 'success');
  }

  rl.close();
}

// Перехватываем завершение работы для закрытия WebSocket
rl.on('close', () => {
  log('Закрытие интерфейса командной строки...', 'info');
  if (ws) {
    log('Закрытие WebSocket соединения...', 'info');
    ws.close();
  }
  log('Клиент завершает работу.', 'info');
  process.exit(0);
});

// Запускаем интерактивный режим
startInteraction(); 

// Функция для обновления значения стата с учетом минимума и максимума
function updateStat(statName, currentValue, change, statDefinitions = null) {
  // Проверяем корректность входных данных
  if (currentValue === undefined || currentValue === null) {
    log(`Предупреждение: Текущее значение стата ${statName} не определено, используем 0`, 'warning');
    currentValue = 0;
  }
  
  if (change === undefined || change === null) {
    log(`Предупреждение: Изменение стата ${statName} не определено, используем 0`, 'warning');
    change = 0;
  }

  // Конвертируем в числа, если они переданы как строки
  currentValue = Number(currentValue);
  change = Number(change);
  
  // Получаем определение стата (если есть)
  const statDef = statDefinitions && statDefinitions[statName] ? statDefinitions[statName] : null;
  // Значения по умолчанию
  let min = config.stats.defaultMin;
  let max = config.stats.defaultMax;
  
  // Флаги, указывающие, применяем ли min/max ограничения
  let applyMin = true;
  let applyMax = true;
  
  // Если есть определение стата, используем его значения min/max
  if (statDef && statDef.game_over_conditions) {
    // Проверяем, нужно ли применять ограничение по минимуму
    if (statDef.game_over_conditions.min === false) {
      applyMin = false; // Если явно установлено в false, отключаем ограничение
    }
    
    // Проверяем, нужно ли применять ограничение по максимуму
    if (statDef.game_over_conditions.max === false) {
      applyMax = false; // Если явно установлено в false, отключаем ограничение
    }
  }
  
  // Вычисляем новое значение
  let newValue = currentValue + change;
  
  // Если включено принудительное ограничение и нужно применять min/max ограничения
  if (config.stats.enforceMinMax) {
    if (applyMin) {
      newValue = Math.max(min, newValue); // Применяем минимальное ограничение
    }
    if (applyMax) {
      newValue = Math.min(newValue, max); // Применяем максимальное ограничение
    }
  }
  
  // Логируем изменение, если нужно
  if (config.stats.logStatChanges) {
    const diffText = change >= 0 ? `+${change}` : `${change}`;
    log(`Стат ${statName}: ${currentValue} -> ${newValue} (${diffText})`, 'stat');
  }
  
  return newValue;
} 