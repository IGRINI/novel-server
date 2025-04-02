const axios = require('axios');
const fs = require('fs');
const path = require('path');
const chalk = require('chalk');
const readline = require('readline');
const config = require('./config');

console.log('Клиент интерактивных новелл');
console.log('Конфигурация:', config);

// Переменная для хранения JWT токена
let jwtToken = null;

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
    const response = await axios.post(url, {
      username: username,
      password: password
    }, {
      headers: {
        'Content-Type': 'application/json'
      }
    });
    
    if (response.data && response.data.token) {
      log('Авторизация успешна!', 'success');
      return response.data.token;
    } else {
      log('Ошибка: Не удалось получить токен из ответа сервера', 'error');
      return null;
    }
  } catch (error) {
    if (error.response && error.response.data && error.response.data.error) {
      log(`Ошибка при авторизации: ${error.response.data.error}`, 'error');
    } else {
      log(`Ошибка при авторизации: ${error.message}`, 'error');
    }
    return null;
  }
}

// Функция для получения списка новелл
async function fetchNovelsList() {
  const url = `${config.baseUrl}${config.api.novels.list}`;
  
  log(`Запрос списка новелл...`, 'info');
  
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
      log('Список новелл успешно получен!', 'success');
      return response.data;
    } else {
      log('Ошибка: Не удалось получить список новелл из ответа сервера.', 'error');
      return [];
    }
  } catch (error) {
    log(`Ошибка при получении списка новелл: ${error.message}`, 'error');
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

// Функция для ожидания завершения асинхронной задачи
async function waitForTaskCompletion(taskId, maxAttempts = 30, interval = 2000) {
  const url = `${config.baseUrl}${config.api.tasks}/${taskId}`;
  
  log(`Ожидание завершения задачи ${taskId}...`, 'info');
  
  if (!jwtToken) {
    log('Ошибка: JWT токен отсутствует. Невозможно проверить задачу.', 'error');
      return null;
    }
    
  let attempts = 0;
  
  while (attempts < maxAttempts) {
    try {
      const response = await axios.get(url, {
        headers: {
          'Authorization': `Bearer ${jwtToken}`
        }
      });
      
      const taskStatus = response.data;
      
      if (taskStatus.status === 'completed') {
        log(`Задача ${taskId} успешно завершена!`, 'success');
        return taskStatus;
      } else if (taskStatus.status === 'failed') {
        log(`Задача ${taskId} завершилась с ошибкой: ${taskStatus.message}`, 'error');
          return null;
        }
        
      // Увеличиваем счетчик попыток
      attempts++;
      
      // Выводим информацию о прогрессе
      if (taskStatus.progress) {
        log(`Прогресс задачи ${taskId}: ${taskStatus.progress}%`, 'info');
      }
      
      // Ждем перед следующей попыткой
      await new Promise(resolve => setTimeout(resolve, interval));
    } catch (error) {
      log(`Ошибка при проверке статуса задачи: ${error.message}`, 'error');
      if (error.response) {
        log(`Статус: ${error.response.status}`, 'error');
        log(`Ответ сервера: ${JSON.stringify(error.response.data)}`, 'error');
      }
      
      // Увеличиваем счетчик попыток
      attempts++;
      
      // Ждем перед следующей попыткой
      await new Promise(resolve => setTimeout(resolve, interval));
    }
  }
  
  log(`Превышено максимальное количество попыток (${maxAttempts}) при ожидании завершения задачи.`, 'error');
    return null;
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
    const taskResult = await waitForTaskCompletion(taskId);
    
    if (!taskResult) {
      log('Ошибка при ожидании завершения задачи генерации содержимого.', 'error');
      return null;
    }
    
    log('Содержимое новеллы успешно сгенерировано!', 'success');
    return taskResult.result;
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
async function setupNovelFromDraft(draftId, draftData) {
  const url = `${config.baseUrl}${config.api.novels.generate.setup}`; // Используем эндпоинт сетапа

  log(`Запуск настройки новеллы из черновика ${draftId}...`, 'info');

  if (!jwtToken) {
    log('Ошибка: JWT токен отсутствует. Невозможно запустить настройку.', 'error');
    return null;
  }

  if (!draftId || !draftData) {
      log('Ошибка: Отсутствует ID черновика или данные черновика.', 'error');
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

// Главная функция
async function main() {
  try {
    // Авторизация
    console.log(chalk.cyan('\n===== АВТОРИЗАЦИЯ ====='));
    let authAction = '';
    while (authAction !== '1' && authAction !== '2') {
      authAction = await askQuestion(chalk.yellow('Выберите действие:\n[1] Вход\n[2] Регистрация\nВыбор: '));
      if (authAction !== '1' && authAction !== '2') {
        console.log(chalk.red('Пожалуйста, введите 1 или 2.'));
      }
    }
    
    let username, email, password;
    
    if (authAction === '1') {
      // Вход
      username = await askQuestion(chalk.yellow('Введите имя пользователя: '));
      password = await askQuestion(chalk.yellow('Введите пароль: '));
      
      jwtToken = await login(username, password);
      if (!jwtToken) {
        log('Не удалось войти в систему. Завершение работы.', 'error');
        rl.close();
        return;
      }
    } else {
      // Регистрация
      username = await askQuestion(chalk.yellow('Введите имя пользователя: '));
      email = await askQuestion(chalk.yellow('Введите email: '));
      password = await askQuestion(chalk.yellow('Введите пароль: '));
      
      const registrationResult = await register(username, email, password);
      if (!registrationResult) {
        log('Не удалось зарегистрироваться. Завершение работы.', 'error');
        rl.close();
        return;
      }
      
      jwtToken = await login(username, password);
      if (!jwtToken) {
        log('Не удалось войти в систему после регистрации. Завершение работы.', 'error');
        rl.close();
        return;
      }
    }
    
    novelHistory.userId = username;
    
    // Главное меню
    console.log(chalk.cyan('\n===== ГЛАВНОЕ МЕНЮ ====='));
    console.log(chalk.cyan('[1] Начать новую новеллу'));
    console.log(chalk.cyan('[2] Продолжить новеллу из списка'));
    console.log(chalk.cyan('=========================\n'));

    let menuChoice = '';
    while (menuChoice !== '1' && menuChoice !== '2') {
      menuChoice = await askQuestion(chalk.yellow('Выберите действие:\n[1] Начать новую новеллу\n[2] Продолжить новеллу из списка\nВыбор: '));
      if (menuChoice !== '1' && menuChoice !== '2') {
        console.log(chalk.red('Пожалуйста, введите 1 или 2.'));
      }
    }
    
    let novelId;
    let novelContent;

    if (menuChoice === '1') {
      // Начать новую новеллу
      log('Начинаем процесс генерации новой новеллы...', 'info');
      
      // Запрос промпта у пользователя
      let userPrompt = await askQuestion(chalk.yellow('Введите промпт для новеллы (или нажмите Enter для использования стандартного): '));
      if (!userPrompt || userPrompt.trim() === '') {
        userPrompt = config.defaultPrompt || "Создай фэнтезийную новеллу с элементами приключений и романтики";
        log(`Используется стандартный промпт: "${userPrompt}"`, 'info');
      } else {
        log(`Используется введенный промпт: "${userPrompt}"`, 'info');
      }
      
      // Генерация черновика
      const novelDraft = await generateNovelDraft(userPrompt);
      if (!novelDraft) {
        log('Не удалось сгенерировать черновик новеллы. Завершение работы.', 'error');
        rl.close();
        return;
      }
      
      console.log(novelDraft)

      // Отображаем информацию о черновике
      let currentDraft = novelDraft; // Сохраняем текущий драфт в переменную
      let currentDraftId = currentDraft.draft_id; // Получаем ID драфта

      while (true) {
        console.log(chalk.cyan('\n===== ПРЕДПРОСМОТР НОВЕЛЛЫ ====='));
        console.log(chalk.yellow('Название: ') + chalk.white(currentDraft.title || 'Без названия'));
        console.log(chalk.yellow('Описание: ') + chalk.white(currentDraft.short_description || 'Нет описания'));
        if (currentDraft.franchise) {
          console.log(chalk.yellow('Франшиза/Сеттинг: ') + chalk.white(currentDraft.franchise));
        }
        console.log(chalk.yellow('Жанр: ') + chalk.white(currentDraft.genre || 'Не указан'));
        console.log(chalk.yellow('Контент 18+: ') + chalk.white(currentDraft.is_adult_content ? 'Да' : 'Нет'));
        
        console.log(chalk.cyan('\n--- Персонаж ---'));
        console.log(chalk.yellow('Имя игрока: ') + chalk.white(currentDraft.player_name || 'Игрок'));
        console.log(chalk.yellow('Пол игрока: ') + chalk.white(currentDraft.player_gender || 'не указан'));
        console.log(chalk.yellow('Описание игрока: ') + chalk.white(currentDraft.player_description || 'Нет описания'));
        
        console.log(chalk.cyan('\n--- Мир ---'));
        console.log(chalk.yellow('Описание мира: ') + chalk.white(currentDraft.world_context || 'Нет описания'));
        
        if (currentDraft.themes && currentDraft.themes.length > 0) {
            console.log(chalk.yellow('Темы: ') + chalk.white(currentDraft.themes.join(', ')));
        }

        if (currentDraft.core_stats && Object.keys(currentDraft.core_stats).length > 0) {
            console.log(chalk.cyan('\n--- Основные параметры (статы) ---'));
            for (const statName in currentDraft.core_stats) {
                const stat = currentDraft.core_stats[statName];
                console.log(chalk.yellow(`  ${statName}: `) + chalk.white(`${stat.description || ''} (Начальное: ${stat.initial_value !== undefined ? stat.initial_value : 'N/A'})`));
            }
        }
        console.log(chalk.cyan('================================\n'));

        // Спрашиваем, хочет ли пользователь внести изменения
        const modifyAnswer = await askQuestion(chalk.yellow('Хотите внести изменения в этот черновик? Введите текст правки или нажмите Enter (или введите "готово"), чтобы продолжить: '));

        if (!modifyAnswer || modifyAnswer.trim().toLowerCase() === 'готово' || modifyAnswer.trim() === '') {
          log('Завершение модификации черновика.', 'info');
          break; // Выход из цикла модификации
        }

        const modificationPrompt = modifyAnswer.trim();
        log(`Отправка запроса на модификацию с текстом: "${modificationPrompt}"`, 'info');

        // Вызываем новую функцию для модификации
        const modifiedDraft = await modifyNovelDraft(currentDraftId, modificationPrompt);

        if (!modifiedDraft) {
          log('Не удалось модифицировать черновик. Прерывание модификации.', 'error');
          break; // Выходим из цикла, если модификация не удалась
        }

        // Обновляем текущий черновик и его ID (хотя ID должен остаться тем же)
        currentDraft = modifiedDraft;
        currentDraftId = currentDraft.draft_id;
        log('Черновик успешно обновлен.', 'success');
        // Цикл начнется заново с отображением обновленного черновика
      }

      // Подтверждение создания новеллы (используем currentDraft, который может быть изменен)
      const confirmation = await askQuestion(chalk.yellow('Создать новеллу на основе этого черновика? (да/нет): '));
      if (confirmation.toLowerCase() !== 'да' && confirmation.toLowerCase() !== 'yes') {
        log('Генерация новеллы отменена пользователем.', 'warning');
        rl.close();
        return;
      }
      
      // Запускаем настройку новеллы из черновика
      const setupResult = await setupNovelFromDraft(currentDraftId, currentDraft);
      if (!setupResult || !setupResult.novel_id) {
          log('Не удалось настроить новеллу из черновика. Завершение работы.', 'error');
          rl.close();
          return;
      }

      // Получаем ID созданной новеллы из результата сетапа
      novelId = setupResult.novel_id; 
      log(`Новелла успешно настроена и создана с ID: ${novelId}`, 'success');
      
      // Сохраняем информацию о новелле в историю (нужно бы получить полные данные, но пока так)
      novelHistory.novel = { id: novelId, title: currentDraft.title, description: currentDraft.short_description }; // Сохраняем базовую инфу
      saveHistory();
      
      // Генерация первой сцены
      novelContent = await generateNovelContent(novelId);
    } else { 
      // Продолжить новеллу из списка
      log('Загрузка списка доступных новелл...', 'info');
      
      const novels = await fetchNovelsList();
      if (!novels || novels.length === 0) {
        log('Нет доступных новелл для продолжения. Завершение работы.', 'warning');
        rl.close();
        return;
      }

      // Отображаем список новелл
      console.log(chalk.cyan('\n===== ДОСТУПНЫЕ НОВЕЛЛЫ ====='));
      novels.forEach((novel, index) => {
        console.log(chalk.cyan(`[${index + 1}] ${novel.title || 'Без названия'} - ${novel.description || 'Нет описания'}`));
      });
      console.log(chalk.cyan('==============================\n'));

      // Пользователь выбирает новеллу
      let selectedIndex = -1;
      while (selectedIndex < 0 || selectedIndex >= novels.length) {
        const answer = await askQuestion(chalk.yellow('Введите номер новеллы для продолжения: '));
        selectedIndex = parseInt(answer) - 1;
        if (isNaN(selectedIndex) || selectedIndex < 0 || selectedIndex >= novels.length) {
          console.log(chalk.red(`Пожалуйста, введите число от 1 до ${novels.length}`));
          selectedIndex = -1;
        }
      }

      const selectedNovel = novels[selectedIndex];
      novelId = selectedNovel.id;
      log(`Выбрана новелла: ${selectedNovel.title || 'Без названия'} (ID: ${novelId})`, 'success');
      
      // Сохраняем информацию о новелле в историю
      novelHistory.novel = selectedNovel;
      saveHistory();
      
      // Загружаем последнее состояние новеллы
      novelContent = await generateNovelContent(novelId);
    }
    
    // Главный цикл игры
    while (novelContent && !novelContent.state.game_over) {
      // Отображаем текущую сцену
      displayScene(novelContent);
      
      // Получаем выборы для текущей сцены
      const choices = novelContent.new_content.choices;
      
      // Если нет выборов, значит история завершена
      if (!choices || choices.length === 0) {
        log('История завершена.', 'success');
                    break;
                }

      // Отображаем выборы и получаем выбор пользователя
      displayChoices(choices);
      const userChoice = await makeUserChoice(choices);
      
      if (!userChoice) {
        log('Не удалось сделать выбор. Завершение игры.', 'error');
                    break;
                }
      
      log(`Выбран вариант: "${userChoice.text}"`, 'success');
      
      // Сохраняем выбор в историю
      novelHistory.scenes.push({
        state: novelContent.state,
        content: novelContent.new_content,
        user_choice: userChoice
      });
      saveHistory();
      
      // Генерируем следующую сцену на основе выбора
      novelContent = await generateNovelContent(novelId, userChoice);
      
      if (!novelContent) {
        log('Ошибка при генерации следующей сцены. Завершение игры.', 'error');
        break;
      }
    }
    
    // Отображаем финальную сцену, если история завершена
    if (novelContent && novelContent.state.game_over) {
      displayScene(novelContent);
      
      if (novelContent.state.ending) {
        console.log(chalk.cyan('\n===== ЗАВЕРШЕНИЕ ИСТОРИИ ====='));
        console.log(chalk.white(novelContent.state.ending));
        console.log(chalk.cyan('==============================\n'));
      }
      
      log('История успешно завершена!', 'success');
      
      // Сохраняем финальную сцену в историю
      novelHistory.scenes.push({
        state: novelContent.state,
        content: novelContent.new_content
      });
      saveHistory();
    }
    
    log('Спасибо за игру!', 'success');
    rl.close();
  } catch (error) {
    log(`Произошла ошибка: ${error.message}`, 'error');
    log(error.stack);
    
    // Сохраняем историю в случае ошибки
    saveHistory();
    
    rl.close();
  }
}

// Запуск клиента
main(); 