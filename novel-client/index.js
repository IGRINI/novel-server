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

// Функция для ожидания завершения задачи
async function waitForTaskCompletion(taskId, maxAttempts = 60, interval = 2000) {
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
      
      jwtToken = await login(username, password);
      if (jwtToken) {
        loggedIn = true;
        novelHistory.userId = username; // Сохраняем имя пользователя
      } else {
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
  
  // Если авторизация/регистрация прошли успешно, продолжаем
  if (!jwtToken) {
    log('Не удалось получить JWT токен. Завершение работы.', 'error');
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
    if (!draft) {
      log('Не удалось создать черновик. Выход.', 'error');
      rl.close();
      return;
    }
    
    // Спрашиваем, хочет ли пользователь продолжить с этим черновиком
    rl.question('Хотите настроить новеллу из этого черновика? (да/нет): ', async function(answer) {
      if (answer.toLowerCase() === 'да') {
        // Запускаем настройку новеллы
        const setupResult = await setupNovelFromDraft(draft.id, draft);
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
  // Получаем первую сцену
  let novelContent = await generateNovelContent(novelId);
  if (!novelContent) {
    log('Не удалось сгенерировать начальный контент. Выход.', 'error');
    rl.close();
    return;
  }
  
  // Обновляем наш объект новеллы
  let novel = {
    id: novelId,
    state: novelContent.state,
    scenes: []
  };
  
  // Добавляем первую сцену
  novel.scenes.push(novelContent.new_content);
  
  // Процесс игры: показываем сцену и выборы
  let continueGame = true;
  
  while (continueGame) {
    // Получаем текущую сцену (последнюю в массиве)
    const currentSceneData = novel.scenes[novel.scenes.length - 1];
    
    // Выводим информацию о сцене
    const sceneTitle = currentSceneData.title || 'Без названия';
    const sceneDescription = currentSceneData.description || 'Нет описания'; 
    log(`\n=== ${sceneTitle} ===`, 'heading');
    log(sceneDescription, 'text');
    
    // Проверяем, есть ли выборы
    if (!currentSceneData.choices || currentSceneData.choices.length === 0) {
      log('Нет доступных выборов. Завершение игры.', 'info');
      continueGame = false;
      continue;
    }
    
    // Выводим доступные выборы
    log('\nВыборы:', 'info');
    currentSceneData.choices.forEach((choice, index) => {
      log(`${index + 1}. ${choice.text}`, 'choice');
    });
    
    // Спрашиваем пользователя о выборе
    const answer = await new Promise(resolve => {
      rl.question('Ваш выбор (номер): ', resolve);
    });
    
    const choiceIndex = parseInt(answer) - 1;
    if (isNaN(choiceIndex) || choiceIndex < 0 || choiceIndex >= currentSceneData.choices.length) {
      log('Неверный выбор. Повторите.', 'error');
      continue;
    }
    
    const selectedChoice = currentSceneData.choices[choiceIndex];
    log(`Вы выбрали: ${selectedChoice.text}`, 'info');
    
    // Создаем объект выбора для отправки на сервер
    const userChoice = {
        choice_id: selectedChoice.id,
        text: selectedChoice.text
    };
    
    // Генерируем следующую сцену на основе выбора
    novelContent = await generateNovelContent(novelId, userChoice);
    
    if (!novelContent) {
      log('Не удалось сгенерировать следующую сцену. Завершение игры.', 'error');
      continueGame = false;
      continue;
    }
    
    // Проверяем, есть ли флаг game_over или ending_text
    if (novelContent.new_content.game_over || novelContent.new_content.ending_text) {
        log('\n=== ИГРА ЗАВЕРШЕНА ===', 'heading');
        if (novelContent.new_content.ending_text) {
            log(novelContent.new_content.ending_text, 'text');
        } else if (novelContent.new_content.ending && novelContent.new_content.ending.title) { 
            log(novelContent.new_content.ending.title, 'heading');
            log(novelContent.new_content.ending.description, 'text');
        } else {
            log('История подошла к концу.', 'text');
        }
        continueGame = false;
        // Добавляем финальное состояние/текст в сцены перед выходом
        novel.scenes.push(novelContent.new_content); 
        novel.state = novelContent.state;
        continue;
    }
    
    // Добавляем новую сцену
    novel.scenes.push(novelContent.new_content);
    novel.state = novelContent.state;
  }
  
  // Сохраняем результат в файл
  const firstSceneData = novel.scenes[0] || {};
  const finalTitle = firstSceneData.title || 'Без названия';
  const finalDescription = firstSceneData.description || 'Нет описания';

  fs.writeFileSync(
    config.outputFile,
    JSON.stringify({
      userId: novelHistory.userId,
      novel: {
        id: novel.id,
        title: finalTitle,
        description: finalDescription
      },
      scenes: novel.scenes
    }, null, 2)
  );
  
  log(`\nИгра завершена. Результат сохранен в ${config.outputFile}`, 'success');
  rl.close();
}

// Запускаем интерактивный режим
startInteraction(); 