const axios = require('axios');
const fs = require('fs');
const path = require('path');
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

// История для сохранения всех ответов
const novelHistory = {
  userId: null,
  novel: null,
  scenes: []
};

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

// Функция сохранения истории в файл
function saveHistory() {
  try {
    const outputPath = path.join(__dirname, config.outputFile);
    fs.writeFileSync(outputPath, JSON.stringify(novelHistory, null, 2), 'utf8');
    log(`История сохранена в файл: ${outputPath}`, 'success');
  } catch (error) {
    log(`Ошибка при сохранении истории: ${error.message}`, 'error');
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

// Функция для создания новой новеллы
async function createNovel(draftData) {
  const url = `${config.baseUrl}${config.api.novels.create}`;
  
  log(`Создание новой новеллы...`, 'info');
  
  if (!jwtToken) {
    log('Ошибка: JWT токен отсутствует. Невозможно создать новеллу.', 'error');
    return null;
  }
  
  try {
    const response = await axios.post(url, draftData, {
          headers: {
              'Authorization': `Bearer ${jwtToken}`
      }
    });
    
    log('Новелла успешно создана!', 'success');
    return response.data;
  } catch (error) {
    log(`Ошибка при создании новеллы: ${error.message}`, 'error');
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
async function generateNovelContent(novelId, userChoice = null) {
  const url = `${config.baseUrl}${config.api.novels.generate.content}`;
  
  log(`Генерация содержимого новеллы ${novelId}...`, 'info');
  
    if (!jwtToken) {
    log('Ошибка: JWT токен отсутствует. Невозможно сгенерировать содержимое.', 'error');
        return null;
    }

  const requestData = {
    novel_id: novelId
    };
    
    if (userChoice) {
    requestData.user_choice = userChoice;
  }
  
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

// Функция для отображения сцены
function displayScene(scene) {
  if (!scene || !scene.new_content) {
    log('Ошибка: нет данных для отображения сцены.', 'error');
    return;
  }
  
  const content = scene.new_content;
  
  // Отображаем заголовок сцены
  console.log(chalk.cyan('\n===== СЦЕНА =====\n'));
  
  // Отображаем текст сцены
  if (content.scene && content.scene.title) {
    console.log(chalk.yellow(`Название: ${content.scene.title}`));
  }
  
  if (content.scene && content.scene.text) {
    console.log(chalk.white(content.scene.text));
  }
  
  // Отображаем диалоги
  if (content.dialogues && content.dialogues.length > 0) {
    console.log('');
    content.dialogues.forEach(dialogue => {
      if (dialogue.character) {
        console.log(chalk.yellow(`${dialogue.character}:`), chalk.white(dialogue.text));
        } else {
        console.log(chalk.italic(chalk.gray(dialogue.text)));
      }
    });
  }
  
  console.log(chalk.cyan('\n================\n'));
}

// Функция для отображения выборов
function displayChoices(choices) {
  if (!choices || choices.length === 0) {
    log('Нет доступных выборов.', 'warning');
    return null;
  }
  
  console.log(chalk.cyan('\n===== ВЫБЕРИТЕ ДЕЙСТВИЕ ====='));
  
  choices.forEach((choice, index) => {
    console.log(chalk.cyan(`[${index + 1}] ${choice.text}`));
  });
  
  console.log(chalk.cyan('============================\n'));
  
  return choices;
}

// Функция для выбора пользователем
async function makeUserChoice(choices) {
  if (!choices || choices.length === 0) {
    log('Нет доступных выборов!', 'error');
    return null;
  }
  
  let selectedIndex = -1;
  
  while (selectedIndex < 0 || selectedIndex >= choices.length) {
    const answer = await askQuestion(chalk.yellow('Введите номер выбранного варианта: '));
    selectedIndex = parseInt(answer) - 1;
    
    if (isNaN(selectedIndex) || selectedIndex < 0 || selectedIndex >= choices.length) {
      console.log(chalk.red(`Пожалуйста, введите число от 1 до ${choices.length}`));
      selectedIndex = -1;
    }
  }
  
  return {
    choice_id: choices[selectedIndex].id,
    text: choices[selectedIndex].text
  };
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
    // Результат задачи setupNovelTask содержит { novel_id: ..., setup: ... }
    return taskResult.result;
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
async function handleLocalGameOver(novelId, reason) {
  const url = `${config.baseUrl}${config.api.novels.gameOver.replace('{id}', novelId)}`;
  log(`Отправка уведомления о Game Over на сервер (Reason: ${JSON.stringify(reason)})...`, 'info');

  if (!jwtToken) {
    log('Ошибка: JWT токен отсутствует. Невозможно отправить уведомление о Game Over.', 'error');
    return "Игра окончена (ошибка связи с сервером)."; // Возвращаем заглушку
  }

  try {
    const response = await axios.post(url, { reason: reason }, {
      headers: {
        'Authorization': `Bearer ${jwtToken}`,
        'Content-Type': 'application/json'
      }
    });

    if (response.data && response.data.ending_text) {
      log('Получен текст концовки от сервера.', 'success');
      return response.data.ending_text;
    } else {
      log('Ошибка: Не удалось получить текст концовки из ответа сервера.', 'error');
      return "Игра окончена (ошибка обработки ответа сервера).";
    }
  } catch (error) {
    log(`Ошибка при отправке уведомления о Game Over: ${error.message}`, 'error');
    if (error.response) {
      log(`Статус: ${error.response.status}`, 'error');
      log(`Ответ сервера: ${JSON.stringify(error.response.data)}`, 'error');
    }
    return "Игра окончена (ошибка сети при запросе концовки).";
  }
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
    
    // Запускаем игровой процесс
    await startGameplayFlow(rl, selectedNovel.id);
  });
}

// Запускаем игровой процесс для новеллы
async function startGameplayFlow(rl, novelId) {
  log(`Запуск игрового процесса для новеллы: ${novelId}`, 'info');

  // --- Шаг 1: Получаем первую сцену/данные --- 
  let currentContent = await generateNovelContent(novelId);

  // --- Проверка на ошибку или неверный формат начальных данных --- 
  if (!currentContent) {
    log('Не удалось получить начальные данные новеллы.', 'error');
    rl.close();
    return;
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

  // --- Шаг 2: Инициализируем объект новеллы --- 
  let novel = {
    id: novelId,
    state: {
      core_stats: currentContent.core_stats || {}, // Начальные статы из первого ответа
      global_flags: [], // Флаги для отслеживания состояния игры
      story_variables: {} // Переменные для хранения данных
    },
    history: [], // История выборов
    core_stats_definition: currentContent.core_stats_definition || {} // Определения статов (min/max значения)
  };

  // --- Шаг 3: Основной цикл игры --- 
  let continueGame = true;
  while (continueGame) {
    // Обрабатываем все выборы в текущем батче (currentContent.choices должен быть валидным здесь)
    for (const event of currentContent.choices) {
      if (!event || !event.description || !event.options || event.options.length !== 2) {
        log('Ошибка: Неверный формат события в батче.', 'error');
        log(`Проблемное событие: ${JSON.stringify(event)}`, 'error');
        continueGame = false; // Прерываем игру при ошибке формата
        break;
      }

      // Отображаем текущую ситуацию
      log(`\n=== Ситуация ===`, 'heading');
      log(event.description, 'text');

      // Показываем текущие статы
      log('\nТекущие характеристики:', 'info');
      Object.entries(novel.state.core_stats).forEach(([stat, value]) => {
        log(`${stat}: ${value}`, 'stat');
      });

      // Отображаем варианты выбора
      log('\nВарианты:', 'info');
      event.options.forEach((option, index) => {
        log(`${index + 1}. ${option.text}`, 'choice');
      });

      // Получаем выбор пользователя
      const answer = await new Promise(resolve => {
        rl.question('Ваш выбор (1 или 2): ', resolve);
      });

      const choiceIndex = parseInt(answer) - 1;
      // Добавим проверку, что ввод - это число и оно в диапазоне
      if (isNaN(choiceIndex) || choiceIndex < 0 || choiceIndex >= event.options.length) {
          log('Неверный выбор. Пожалуйста, введите 1 или 2.', 'error');
          // Повторяем итерацию для ЭТОГО ЖЕ события, не переходя к следующему
          // Для этого можно использовать `continue` во внешнем цикле или рефакторинг
          // Пока для простоты сделаем повторный запрос у пользователя в рамках текущей итерации (что не идеально)
          // Лучше было бы сделать вложенный while для получения корректного ввода
          // *** TODO: Refactor input validation loop ***
          continue; // Это пропустит остаток цикла for и перейдет к след. событию. Не совсем то.
                    // Правильнее было бы сделать вложенный while.
                    // Пока оставим так, но это место для улучшения.
      }

      const selectedOption = event.options[choiceIndex];
      log(`Вы выбрали: ${selectedOption.text}`, 'info');

      // Применяем последствия выбора локально
      if (selectedOption.consequences) {
        // Обновляем статы
        if (selectedOption.consequences.core_stats) {
          Object.entries(selectedOption.consequences.core_stats).forEach(([stat, change]) => {
            novel.state.core_stats[stat] = (novel.state.core_stats[stat] || 0) + change;
            log(`${stat} изменился на ${change > 0 ? '+' : ''}${change}`, 'stat');
          });
        }

        // Добавляем глобальные флаги
        if (selectedOption.consequences.global_flags) {
          selectedOption.consequences.global_flags.forEach(flag => {
            if (!novel.state.global_flags.includes(flag)) {
              novel.state.global_flags.push(flag);
              log(`Добавлен флаг: ${flag}`, 'info');
            }
          });
        }
        // Добавляем переменные сюжета
        if (selectedOption.consequences.story_variables) {
            Object.entries(selectedOption.consequences.story_variables).forEach(([key, value]) => {
                novel.state.story_variables[key] = value;
                log(`Установлена переменная сюжета: ${key} = ${JSON.stringify(value)}`, 'info');
            });
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

          // Проверка на минимум (<= 0)
          if (conditions.min && currentValue <= 0) {
            gamOver = true;
            gamOverReasonDetails = {
              stat_name: statName,
              condition: "min",
              value: currentValue
            };
            return; // Выходим из forEach, если нашли причину
          }

          // Проверка на максимум (>= 100)
          if (conditions.max && currentValue >= 100) {
            gamOver = true;
            gamOverReasonDetails = {
              stat_name: statName,
              condition: "max",
              value: currentValue
            };
            return; // Выходим из forEach, если нашли причину
          }
        }
      });

      if (gameOver) {
        log('\n=== ИГРА ОКОНЧЕНА (локально по статам) ===', 'heading');
        log(`Причина: Стат '${gameOverReasonDetails.stat_name}' (${gameOverReasonDetails.value}) нарушил условие '${gameOverReasonDetails.condition}'.`, 'text');

        // Вызываем функцию для получения текста концовки с сервера
        const endingText = await handleLocalGameOver(novel.id, gameOverReasonDetails);
        log('\n=== ФИНАЛЬНЫЙ ТЕКСТ ===', 'heading');
        log(endingText, 'text'); // Выводим полученную концовку

        continueGame = false; // Устанавливаем флаг для завершения цикла while
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
      const nextContent = await generateNovelContent(novelId, novel.state); // Передаем актуальное состояние

      if (!nextContent) {
        log('Не удалось получить следующий батч событий. Завершение игры.', 'error');
        continueGame = false;
        continue; // Переходим к следующей итерации while, где continueGame=false завершит цикл
      }

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
          global_flags: novel.state.global_flags
        },
        history: novel.history
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