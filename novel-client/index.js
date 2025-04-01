const axios = require('axios');
const fs = require('fs');
const path = require('path');
const chalk = require('chalk');
const readline = require('readline');
const config = require('./config');

console.log('Скрипт запущен');
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

// --- Получение UserID из аргументов командной строки или через ввод --- 
async function getUserId() {
    let userId = process.argv[2]; // Проверяем аргумент командной строки
    if (userId) {
        console.log(`Используется UserID из аргумента: ${userId}`);
        return userId;
    }

    // Если аргумента нет, спрашиваем у пользователя
    userId = await askQuestion(chalk.yellow('UserID не предоставлен в аргументах. Введите UserID (или нажмите Enter для генерации): '));

    if (userId && userId.trim() !== '') {
        console.log(`Используется введенный UserID: ${userId}`);
        return userId.trim();
    } else {
        userId = `user_${Math.random().toString(36).substring(2, 9)}`; // Генерируем случайный ID
        console.log(`UserID не введен. Используется сгенерированный ID: ${userId}`);
        return userId;
    }
}
// --------------------------------------------------------------------

// --- Функция для получения JWT токена ---
async function getAuthToken(userId) {
    const url = `${config.baseUrl}/auth/token`;
    log(`Запрос JWT токена для UserID: ${userId} по адресу ${url}...`, 'info');
    try {
        const response = await axios.post(url, { user_id: userId });
        if (response.data && response.data.token) {
            log('Токен успешно получен!', 'success');
            return response.data.token;
        } else {
            log('Ошибка: Не удалось получить токен из ответа сервера.', 'error');
            return null;
        }
    } catch (error) {
        log(`Ошибка при получении токена: ${error.message || 'Неизвестная ошибка'}`, 'error');
        if (error.response) {
            log(`Статус: ${error.response.status}`, 'error');
            log(`Ответ сервера: ${JSON.stringify(error.response.data)}`, 'error');
        } else if (error.request) {
            log('Ошибка: Запрос был сделан, но ответ не был получен.', 'error');
        }
        log(`Полная ошибка: ${error.stack}`, 'error');
        return null;
    }
}
// ---------------------------------------

// История для сохранения всех ответов
const novelHistory = {
  userId: null, // Будет установлен после получения ID
  config: null,
  setup: null,
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

// Функция для создания черновика новеллы
async function createNovelDraft(userPrompt) {
  const url = `${config.baseUrl}/create-draft`;
  if (!jwtToken) {
    log('Ошибка: JWT токен отсутствует. Невозможно создать черновик новеллы.', 'error');
    return null;
  }

  log(`Отправка запроса к нарратору для создания черновика новеллы...`, 'info');
  log(`Отправка запроса на ${url} с данными:\n${JSON.stringify({ user_prompt: userPrompt }, null, 2)}`, 'info');

  try {
    const response = await axios.post(url, 
      { user_prompt: userPrompt },
      { 
          headers: {
              'Authorization': `Bearer ${jwtToken}`
          }
      }
    );

    log('Черновик новеллы успешно создан!', 'success');
    
    // Получаем данные черновика напрямую из response.data
    const draftData = response.data;
    
    if (!draftData) {
        log('Ошибка: получен пустой ответ от нарратора', 'error');
        throw new Error('Empty response from narrator');
    }
    
    if (config.verbose) {
      log(`DraftID: ${draftData.draft_id}`);
      log(`Title: ${draftData.config.title}`);
      log(`Franchise: ${draftData.config.franchise}`);
      log(`Genre: ${draftData.config.genre}`);
    }
    
    return draftData;
  } catch (error) {
    log(`Ошибка при создании черновика: ${error.message || 'Неизвестная ошибка'}`, 'error');
    if (error.response) {
      log(`Статус: ${error.response.status}`, 'error');
      log(`Ответ сервера: ${JSON.stringify(error.response.data)}`, 'error');
    } else if (error.request) {
      log('Ошибка: Запрос был сделан, но ответ не был получен (возможно, сервер недоступен).', 'error');
    }
    log(`Полная ошибка: ${error.stack}`, 'error');
    throw error;
  }
}

// Функция для уточнения черновика новеллы
async function refineNovelDraft(draftID, additionalPrompt) {
  const url = `${config.baseUrl}/refine-draft`;
  if (!jwtToken) {
    log('Ошибка: JWT токен отсутствует. Невозможно уточнить черновик новеллы.', 'error');
    return null;
  }

  log(`Отправка запроса к нарратору для уточнения черновика новеллы...`, 'info');
  log(`Отправка запроса на ${url} с данными:\n${JSON.stringify({ draft_id: draftID, additional_prompt: additionalPrompt }, null, 2)}`, 'info');

  try {
    const response = await axios.post(url, 
      { 
        draft_id: draftID, 
        additional_prompt: additionalPrompt 
      },
      { 
          headers: {
              'Authorization': `Bearer ${jwtToken}`
          }
      }
    );

    log('Черновик новеллы успешно уточнен!', 'success');
    
    // Получаем обновленные данные черновика напрямую из response.data
    const draftData = response.data;
    
    if (!draftData) {
        log('Ошибка: получен пустой ответ от нарратора', 'error');
        throw new Error('Empty response from narrator');
    }
    
    if (config.verbose) {
      log(`DraftID: ${draftData.draft_id}`);
      log(`Updated Title: ${draftData.config.title}`);
      log(`Updated Genre: ${draftData.config.genre}`);
    }
    
    return draftData;
  } catch (error) {
    log(`Ошибка при уточнении черновика: ${error.message || 'Неизвестная ошибка'}`, 'error');
    if (error.response) {
      log(`Статус: ${error.response.status}`, 'error');
      log(`Ответ сервера: ${JSON.stringify(error.response.data)}`, 'error');
    } else if (error.request) {
      log('Ошибка: Запрос был сделан, но ответ не был получен (возможно, сервер недоступен).', 'error');
    }
    log(`Полная ошибка: ${error.stack}`, 'error');
    throw error;
  }
}

// Функция для подтверждения черновика новеллы
async function confirmNovelDraft(draftID) {
  const url = `${config.baseUrl}/confirm-draft`;
  if (!jwtToken) {
    log('Ошибка: JWT токен отсутствует. Невозможно подтвердить черновик новеллы.', 'error');
    return null;
  }

  log(`Подтверждение черновика новеллы...`, 'info');
  log(`Отправка запроса к нарратору для подтверждения черновика новеллы...`, 'info');
  log(`Отправка запроса на ${url} с данными:\n${JSON.stringify({ draft_id: draftID }, null, 2)}`, 'info');

  try {
    const response = await axios.post(url, 
      { draft_id: draftID },
      { 
          headers: {
              'Authorization': `Bearer ${jwtToken}`
          }
      }
    );

    log('Черновик новеллы успешно подтвержден!', 'success');
    
    // Получаем данные новеллы напрямую из response.data
    const novelData = response.data;
    
    if (!novelData) {
        log('Ошибка: получен пустой ответ от нарратора', 'error');
        throw new Error('Empty response from narrator');
    }
    
    if (config.verbose) {
      log(`NovelID: ${novelData.novel_id}`);
      log(`Message: ${novelData.message}`);
    }
    
    log(`Новелла успешно создана с ID: ${novelData.novel_id}`, 'success');
    return novelData.novel_id;
  } catch (error) {
    log(`Ошибка при подтверждении черновика: ${error.message || 'Неизвестная ошибка'}`, 'error');
    if (error.response) {
      log(`Статус: ${error.response.status}`, 'error');
      log(`Ответ сервера: ${JSON.stringify(error.response.data)}`, 'error');
    } else if (error.request) {
      log('Ошибка: Запрос был сделан, но ответ не был получен (возможно, сервер недоступен).', 'error');
    }
    log(`Полная ошибка: ${error.stack}`, 'error');
    throw error;
  }
}

// Обновленная функция для начала генерации новеллы с двухэтапным процессом
async function generateNovel(userPrompt) {
  log(`Начинаем двухэтапный процесс создания новеллы...`, 'info');

  try {
    // Шаг 1: Создаем черновик новеллы
    const draftData = await createNovelDraft(userPrompt);
    if (!draftData || !draftData.draft_id) {
      log('Ошибка: не получены данные черновика или его ID', 'error');
      return null;
    }
    
    // Показываем информацию о черновике
    console.log(chalk.cyan('\n===== ПРЕДПРОСМОТР НОВЕЛЛЫ ====='));
    console.log(chalk.yellow(`Название: ${draftData.config.title}`));
    console.log(chalk.yellow(`Описание: ${draftData.config.short_description}`));
    console.log(chalk.yellow(`Жанр: ${draftData.config.genre}`));
    console.log(chalk.yellow(`Франшиза: ${draftData.config.franchise}`));
    console.log(chalk.yellow(`Персонаж игрока: ${draftData.config.player_name} (${draftData.config.player_gender})`));
    if (draftData.config.player_description) {
      console.log(chalk.yellow(`Описание персонажа: ${draftData.config.player_description}`));
    }
    console.log(chalk.yellow(`Краткое содержание: ${draftData.config.story_summary}`));
    console.log(chalk.cyan('================================\n'));
    
    // Шаг 2: Предлагаем пользователю подтвердить или уточнить черновик
    let draftConfirmed = false;
    let currentDraftData = draftData;
    
    while (!draftConfirmed) {
      console.log(chalk.cyan('\n===== ДЕЙСТВИЯ С ЧЕРНОВИКОМ ====='));
      console.log(chalk.cyan('[1] Подтвердить и начать новеллу'));
      console.log(chalk.cyan('[2] Уточнить/изменить детали'));
      console.log(chalk.cyan('================================\n'));
      
      const action = await askQuestion(chalk.yellow('Выберите действие (1 или 2): '));
      
      if (action === '1') {
        // Подтверждаем черновик
        log('Подтверждение черновика новеллы...', 'info');
        const novelID = await confirmNovelDraft(currentDraftData.draft_id);
        if (!novelID) {
          log('Ошибка: не получен ID новеллы после подтверждения черновика', 'error');
          return null;
        }
        
        // Создаем структуру ответа, совместимую со старой версией функции
        novelHistory.config = {
          novel_id: novelID,
          config: currentDraftData.config
        };
        
        draftConfirmed = true;
        log(`Новелла успешно создана с ID: ${novelID}`, 'success');
        return {
          novel_id: novelID,
          config: currentDraftData.config
        };
        
      } else if (action === '2') {
        // Запрашиваем дополнительный промпт для уточнения
        const additionalPrompt = await askQuestion(chalk.yellow('Введите дополнительные детали или изменения: '));
        if (!additionalPrompt || additionalPrompt.trim() === '') {
          log('Не введены дополнительные детали. Пожалуйста, выберите действие снова.', 'warning');
          continue;
        }
        
        // Уточняем черновик
        log('Отправка уточнений для черновика...', 'info');
        const refinedDraftData = await refineNovelDraft(currentDraftData.draft_id, additionalPrompt);
        if (!refinedDraftData) {
          log('Ошибка: не получены обновленные данные черновика', 'error');
          return null;
        }
        
        // Обновляем текущий черновик
        currentDraftData = refinedDraftData;
        
        // Показываем обновленную информацию
        console.log(chalk.cyan('\n===== ОБНОВЛЕННЫЙ ПРЕДПРОСМОТР ====='));
        console.log(chalk.yellow(`Название: ${currentDraftData.config.title}`));
        console.log(chalk.yellow(`Описание: ${currentDraftData.config.short_description}`));
        console.log(chalk.yellow(`Жанр: ${currentDraftData.config.genre}`));
        console.log(chalk.yellow(`Франшиза: ${currentDraftData.config.franchise}`));
        console.log(chalk.yellow(`Персонаж игрока: ${currentDraftData.config.player_name} (${currentDraftData.config.player_gender})`));
        if (currentDraftData.config.player_description) {
          console.log(chalk.yellow(`Описание персонажа: ${currentDraftData.config.player_description}`));
        }
        console.log(chalk.yellow(`Краткое содержание: ${currentDraftData.config.story_summary}`));
        console.log(chalk.cyan('====================================\n'));
        
      } else {
        console.log(chalk.red('Пожалуйста, введите 1 или 2.'));
      }
    }
    
  } catch (error) {
    log(`Ошибка в процессе создания новеллы: ${error.message || 'Неизвестная ошибка'}`, 'error');
    log(error.stack, 'error');
    return null;
  }
}

// Функция для отображения ОДНОГО события сцены
function displaySingleEvent(event) {
  if (!event || !event.event_type) return;

  // Добавляем небольшую паузу перед событием для лучшего чтения
  console.log(''); 

  switch (event.event_type) {
    case 'dialogue':
      console.log(chalk.yellow(`${event.speaker}:`), chalk.white(event.text));
      break;
    case 'narration':
      console.log(chalk.italic(chalk.gray(event.text)));
      break;
    case 'monologue':
      // Отображаем монолог без явного указания спикера, если он не задан
      const speakerText = event.speaker ? chalk.yellow(`${event.speaker}:`) : chalk.yellow('Мысли:');
      console.log(speakerText, chalk.italic(chalk.white(event.text)));
      break;
    case 'description':
      console.log(chalk.magenta(event.text));
      break;
    case 'system':
      console.log(chalk.green(event.text));
      break;
    case 'emotion_change':
      // Обновленный формат без 'from'
      console.log(chalk.gray(`[emotion_change: ${event.character} → ${event.to || '??'}]`));
      break;
    case 'move':
      console.log(chalk.gray(`[move: ${event.character} ${event.from || '??'} → ${event.to || '??'}]`));
      break;
    // inline_choice и choice обрабатываются отдельно
    // inline_response обрабатывается после выбора inline_choice
    case 'inline_choice': 
    case 'inline_response':
    case 'choice':
      // Эти типы обрабатываются отдельно, здесь их просто пропускаем
      break;
    default:
      log(`Неизвестный тип события: ${event.event_type}`, 'warning');
      console.log(event); // Выводим само событие для отладки
  }
}

// Функция для запроса генерации контента новеллы (setup или сцена)
async function generateNovelContent(novelID, userChoice, restartFromSceneIndex) {
    const url = `${config.baseUrl}/generate-novel-content`;
    if (!jwtToken) {
        log('Ошибка: JWT токен отсутствует. Невозможно выполнить запрос.', 'error');
        return null;
    }

    // Создаем упрощенный запрос для API
    const payload = {
        novel_id: novelID
    };
    
    // Если передан выбор пользователя, добавляем его
    if (userChoice) {
        payload.user_choice = {
            scene_index: userChoice.scene_index,
            choice_text: userChoice.choice_text
        };
    }
    
    // Если нужно перезапустить с определенной сцены
    if (restartFromSceneIndex !== undefined && restartFromSceneIndex !== null) {
        payload.restart_from_scene_index = restartFromSceneIndex;
    }

    let requestType = 'для новеллы';
    if (userChoice) requestType += ' с выбором пользователя';
    if (restartFromSceneIndex !== undefined) requestType += ` с перезапуском от сцены ${restartFromSceneIndex}`;

    log(`Отправка запроса для генерации ${requestType}...`, 'info');
    log(`Отправка запроса на ${url} с данными:\n${JSON.stringify(payload, null, 2)}`, 'info');

    try {
        const response = await axios.post(
            url,
            payload,
            { // Добавляем заголовок Authorization
                headers: {
                    'Authorization': `Bearer ${jwtToken}`
                }
            }
        );
        
        log(`Получен ответ от API со статусом: ${response.status}`, 'info');
        
        const responseData = response.data;
        
        // Проверяем, получен ли ответ на этапе setup
        if (responseData.state && responseData.state.current_stage === 'setup') {
            log('Получена начальная настройка новеллы (setup). Первая сцена будет загружена автоматически при следующем запросе.', 'info');
            
            // Сохраняем данные setup в историю
            novelHistory.setup = {
                backgrounds: responseData.state.backgrounds,
                characters: responseData.state.characters
            };
            
            saveHistory();
        }
        
        // Проверяем, завершена ли история
        if (responseData.state && responseData.state.current_stage === 'complete') {
            log('Новелла полностью завершена!', 'success');
            if (responseData.state.story_summary) {
                log(`Итоговое резюме: ${responseData.state.story_summary}`, 'info');
                console.log(chalk.cyan('\n======= ИТОГ ИСТОРИИ =======\n'));
                console.log(chalk.white(responseData.state.story_summary));
                console.log(chalk.cyan('\n============================\n'));
            }
        }
        
        // Отображаем события сцены, если они есть
        if (responseData.new_content && responseData.new_content.events && responseData.new_content.events.length > 0) {
            responseData.new_content.events.forEach((event) => {
                displaySingleEvent(event);
            });
        }
        
        // Сохраняем текущую сцену в историю, если это не setup
        if (responseData.state && responseData.state.current_stage !== 'setup' && responseData.new_content) {
            const sceneData = {
                scene_index: responseData.state.current_scene_index,
                background_id: responseData.new_content.background_id,
                events: responseData.new_content.events || [],
                characters: responseData.new_content.characters || []
            };
            
            // Добавляем сцену в историю, если её там ещё нет
            const existingSceneIndex = novelHistory.scenes.findIndex(
                scene => scene.scene_index === responseData.state.current_scene_index
            );
            
            if (existingSceneIndex >= 0) {
                novelHistory.scenes[existingSceneIndex] = sceneData;
            } else {
                novelHistory.scenes.push(sceneData);
            }
            
            saveHistory();
        }
        
        return responseData;
    } catch (error) {
        log(`Ошибка при генерации контента новеллы: ${error.message}`, 'error');
        if (error.response) {
            log(`Статус: ${error.response.status}`, 'error');
            log(`Ответ сервера: ${JSON.stringify(error.response.data)}`, 'error');
        } else if (error.request) {
            log('Ошибка: Запрос был сделан, но ответ не был получен (возможно, сервер недоступен).', 'error');
            log(`Код ошибки Axios: ${error.code || 'N/A'}`, 'error');
        } else {
            log(`Ошибка настройки запроса Axios: ${error.message}`, 'error');
            log(`Код ошибки Axios: ${error.code || 'N/A'}`, 'error');
        }
        log(`Полная ошибка: ${error.stack}`, 'error');
        throw error;
    }
}

// Функция поиска последнего события выбора в сцене
function findLastChoiceEvent(events) {
  if (!Array.isArray(events) || events.length === 0) {
    return null;
  }
  
  for (let i = events.length - 1; i >= 0; i--) {
    // Ищем только обычные выборы, не inline_response
    if (events[i].event_type === 'choice' && events[i].choices && events[i].choices.length > 0) {
      return events[i];
    }
  }
  return null;
}

// Функция выбора варианта пользователем
async function makeUserChoice(choiceEvent) {
  if (!choiceEvent || !choiceEvent.choices || choiceEvent.choices.length === 0) {
    log('Не найдены варианты выбора!', 'error');
    return null;
  }
  
  console.log(chalk.cyan('\n===== ВЫБЕРИТЕ ВАРИАНТ ДЕЙСТВИЯ ====='));
  
  choiceEvent.choices.forEach((choice, index) => {
    console.log(chalk.cyan(`[${index + 1}] ${choice.text}`));
  });
  
  console.log(chalk.cyan('=====================================\n'));
  
  let selectedIndex = -1;
  
  while (selectedIndex < 0 || selectedIndex >= choiceEvent.choices.length) {
    const answer = await askQuestion(chalk.yellow('Введите номер выбранного варианта: '));
    selectedIndex = parseInt(answer) - 1;
    
    if (isNaN(selectedIndex) || selectedIndex < 0 || selectedIndex >= choiceEvent.choices.length) {
      console.log(chalk.red(`Пожалуйста, введите число от 1 до ${choiceEvent.choices.length}`));
      selectedIndex = -1;
    }
  }
  
  return choiceEvent.choices[selectedIndex];
}

// Функция для отправки inline_response на сервер
async function sendInlineResponse(novelID, sceneIndex, choiceID, choiceText, responseIdx) {
    const url = `${config.baseUrl}/inline-response`;
    if (!jwtToken) {
        log('Ошибка: JWT токен отсутствует. Невозможно выполнить запрос.', 'error');
        return null;
    }

    // Создаем запрос для отправки inline_response
    const payload = {
        novel_id: novelID,
        scene_index: sceneIndex,
        choice_id: choiceID,
        choice_text: choiceText,
        response_idx: responseIdx
    };

    log(`Отправка inline_response для выбора "${choiceText}" (ID: ${choiceID})...`, 'info');
    log(`Отправка запроса на ${url} с данными:\n${JSON.stringify(payload, null, 2)}`, 'info');

    try {
        const response = await axios.post(
            url,
            payload,
            {
                headers: {
                    'Authorization': `Bearer ${jwtToken}`
                }
            }
        );
        
        log(`Получен ответ от API со статусом: ${response.status}`, 'info');
        
        const responseData = response.data;
        
        if (!responseData.success) {
            log('Ошибка при обработке inline_response на сервере.', 'error');
            return null;
        }
        
        // Отображаем информацию об измененном состоянии
        if (responseData.updated_state) {
            const updatedState = responseData.updated_state;
            
            // Показываем изменения в отношениях
            if (updatedState.relationship && Object.keys(updatedState.relationship).length > 0) {
                console.log(chalk.cyan('\n=== ИЗМЕНЕНИЯ В ОТНОШЕНИЯХ ==='));
                for (const [character, value] of Object.entries(updatedState.relationship)) {
                    const color = value > 0 ? chalk.green : (value < 0 ? chalk.red : chalk.white);
                    console.log(`${character}: ${color(value)}`);
                }
                console.log(chalk.cyan('=============================\n'));
            }
            
            // Показываем добавленные флаги
            if (updatedState.global_flags && updatedState.global_flags.length > 0) {
                console.log(chalk.cyan('\n=== ДОБАВЛЕННЫЕ ФЛАГИ ==='));
                for (const flag of updatedState.global_flags) {
                    console.log(chalk.yellow(`• ${flag}`));
                }
                console.log(chalk.cyan('========================\n'));
            }
            
            // Обновляем локальную историю, если нужно
            // ...
        }
        
        // Отображаем следующие события, если они есть
        if (responseData.next_events && responseData.next_events.length > 0) {
            responseData.next_events.forEach((event) => {
                displaySingleEvent(event);
            });
        }
        
        return responseData;
    } catch (error) {
        log(`Ошибка при отправке inline_response: ${error.message}`, 'error');
        if (error.response) {
            log(`Статус: ${error.response.status}`, 'error');
            log(`Ответ сервера: ${JSON.stringify(error.response.data)}`, 'error');
        } else if (error.request) {
            log('Ошибка: Запрос был сделан, но ответ не был получен.', 'error');
        }
        log(`Полная ошибка: ${error.stack}`, 'error');
        return null;
    }
}

// --- Функция для получения списка новелл ---
async function fetchNovelsList() {
    const url = `${config.baseUrl}/novels`;
    log(`Запрос списка новелл с ${url}...`, 'info');

    // Проверяем наличие токена
    if (!jwtToken) {
        log('Ошибка: JWT токен отсутствует. Невозможно запросить список новелл.', 'error');
        return [];
    }

    try {
        // Для GET-запроса с параметрами пагинации (если нужно)
        const response = await axios.get(url, {
            // params: { limit: 10 } // Пример: запросить 10 новелл
            headers: {
                'Authorization': `Bearer ${jwtToken}` // <-- Добавляем заголовок авторизации
            }
        });
        if (response.data && response.data.novels) {
            log('Список новелл успешно получен!', 'success');
            return response.data.novels;
        } else {
            log('Ошибка: Не удалось получить список новелл из ответа сервера.', 'error');
            return [];
        }
    } catch (error) {
        log(`Ошибка при получении списка новелл: ${error.message || 'Неизвестная ошибка'}`, 'error');
        if (error.response) {
            log(`Статус: ${error.response.status}`, 'error');
            log(`Ответ сервера: ${JSON.stringify(error.response.data)}`, 'error');
        } else if (error.request) {
            log('Ошибка: Запрос был сделан, но ответ не был получен.', 'error');
        }
        log(`Полная ошибка: ${error.stack}`, 'error');
        return [];
    }
}
// -----------------------------------------

// Главная функция
async function main() {
  try {
    // Получаем UserID в начале
    const currentUserId = await getUserId();
    novelHistory.userId = currentUserId; // Устанавливаем UserID в историю

    // --- Меню выбора --- 
    console.log(chalk.cyan('\n===== ГЛАВНОЕ МЕНЮ ====='));
    console.log(chalk.cyan('[1] Начать новую новеллу'));
    console.log(chalk.cyan('[2] Продолжить новеллу из списка'));
    console.log(chalk.cyan('=========================\n'));

    let menuChoice = '';
    while (menuChoice !== '1' && menuChoice !== '2') {
      menuChoice = await askQuestion(chalk.yellow('Выберите пункт меню (1 или 2): '));
      if (menuChoice !== '1' && menuChoice !== '2') {
        console.log(chalk.red('Пожалуйста, введите 1 или 2.'));
      }
    }
    // ------------------

    let novelID;
    let novelData;

    // --- Получаем JWT токен (нужен для обоих вариантов, кроме /novels) ---
    jwtToken = await getAuthToken(currentUserId);
    if (!jwtToken) {
        log('Не удалось получить токен аутентификации. Завершение работы.', 'error');
        rl.close(); // Закрываем readline перед выходом
        return;
    }
    // ----------------------------------------------------------------------

    if (menuChoice === '1') {
      // --- Начать новую новеллу ---
      log('Начинаю процесс генерации новой новеллы...', 'info');
      
      // Запрос промпта у пользователя
      let userPrompt = await askQuestion(chalk.yellow('Введите промпт для новеллы (или нажмите Enter для использования стандартного): '));
      if (!userPrompt || userPrompt.trim() === '') {
        userPrompt = config.novelPrompt;
        log(`Используется стандартный промпт: "${userPrompt}"`, 'info');
      } else {
        log(`Используется введенный промпт: "${userPrompt}"`, 'info');
      }
      
      // Шаг 1: Генерация конфигурации новеллы
      const novelConfig = await generateNovel(userPrompt); // Передаем промпт
      if (!novelConfig || !novelConfig.novel_id) {
          log('Ошибка: не получена конфигурация или ID новеллы', 'error');
          rl.close();
          return;
      }
      novelID = novelConfig.novel_id;
      
      // Шаг 2: Генерация первой сцены (или setup)
      novelData = await generateNovelContent(novelID);
      
    } else { 
      // --- Продолжить из списка ---
      log('Загрузка списка новелл...', 'info');
      const novels = await fetchNovelsList();

      if (!novels || novels.length === 0) {
        log('Не найдено доступных новелл для продолжения.', 'warning');
        rl.close();
        return;
      }

      console.log(chalk.cyan('\n===== ДОСТУПНЫЕ НОВЕЛЛЫ ====='));
      novels.forEach((novel, index) => {
        // Используем novel.novel_id вместо novel.id
        console.log(chalk.cyan(`[${index + 1}] ${novel.title || 'Без названия'} - ${novel.short_description || 'Нет описания'} (ID: ${novel.novel_id})`));
      });
      console.log(chalk.cyan('===============================\n'));

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
      novelID = selectedNovel.novel_id; // <-- Используем novel_id
      log(`Выбрана новелла: ${selectedNovel.title || 'Без названия'} (ID: ${novelID})`, 'success');

      // Загружаем последнее состояние выбранной новеллы
      log(`Загрузка последнего состояния для новеллы ID: ${novelID}...`, 'info');
      // Вызываем без userChoice, чтобы просто загрузить последнее состояние
      novelData = await generateNovelContent(novelID); 
    }

    // --- Общий цикл обработки сцен --- 
    if (!novelData) {
        log('Ошибка: не удалось получить данные новеллы для начала/продолжения.', 'error');
        rl.close();
        return;
    }

    let iterationsCount = 0;
    
    // Главный цикл игры
    while (novelData && !novelData.is_complete && iterationsCount < config.maxScenes) {
        log(`--- Начинаем сцену ${novelData.current_scene_index} ---`, 'info');
        console.log(chalk.cyan('\n======= СЦЕНА '+ novelData.current_scene_index +' =======\n'));
        
        const currentEvents = novelData.events || [];
        let finalChoiceEvent = null; // Для хранения обычного выбора в конце сцены

        // Итерация по событиям текущей сцены
        for (let i = 0; i < currentEvents.length; i++) {
            const event = currentEvents[i];

            // Отображаем событие (кроме inline_choice/response/choice)
            displaySingleEvent(event);

            // Обработка INLINE_CHOICE
            if (event.event_type === 'inline_choice') {
                log(`Найден inline_choice (ID: ${event.choice_id}). Ожидание выбора пользователя...`, 'info');
                
                // 2. Находим соответствующий inline_response (должен идти СРАЗУ после)
                const nextEventIndex = i + 1;
                let inlineResponseEvent = null;
                if (nextEventIndex < currentEvents.length && 
                    currentEvents[nextEventIndex].event_type === 'inline_response' &&
                    currentEvents[nextEventIndex].choice_id === event.choice_id) {
                    inlineResponseEvent = currentEvents[nextEventIndex];
                    i = nextEventIndex; // Пропускаем inline_response в следующей итерации
                } else {
                    log(`Ошибка: Не найден соответствующий inline_response для choice_id ${event.choice_id}`, 'error');
                    novelData = null; // Прерываем внешний цикл
                    break;
                }

                // --- Создаем временный объект для makeUserChoice --- 
                const choicesForUser = inlineResponseEvent.responses.map(response => ({ text: response.choice_text }));
                const userChoicePromptEvent = {
                    description: event.description, // Берем описание из inline_choice
                    choices: choicesForUser
                };
                // --------------------------------------------------

                // 1. Получаем выбор пользователя, используя временный объект
                const inlineChoiceSelection = await makeUserChoice(userChoicePromptEvent); 
                if (!inlineChoiceSelection) {
                    log('Не удалось сделать inline выбор. Прерывание сцены.', 'warning');
                    novelData = null; // Прерываем внешний цикл
                    break;
                }
                const selectedInlineChoiceText = inlineChoiceSelection.text;
                // Находим индекс ВНУТРИ ОРИГИНАЛЬНОГО МАССИВА RESPONSES
                const selectedInlineIndex = inlineResponseEvent.responses.findIndex(r => r.choice_text === selectedInlineChoiceText);
                
                if (selectedInlineIndex === -1) {
                     log(`Ошибка: Не удалось найти индекс для выбранного текста "${selectedInlineChoiceText}"`, 'error');
                     novelData = null;
                     break;
                }
                log(`Выбран inline вариант [${selectedInlineIndex + 1}]: "${selectedInlineChoiceText}"`, 'success');

                // 3. Извлекаем и отображаем response_events
                if (inlineResponseEvent && inlineResponseEvent.responses && selectedInlineIndex < inlineResponseEvent.responses.length) {
                    const selectedResponseData = inlineResponseEvent.responses[selectedInlineIndex];
                    if (selectedResponseData.response_events && selectedResponseData.response_events.length > 0) {
                        log('Отображение событий ответа...', 'info');
                        selectedResponseData.response_events.forEach(respEvent => {
                            displaySingleEvent(respEvent); // Отображаем каждое событие ответа
                        });
                    }
                } else {
                    log(`Предупреждение: Не найдены response_events для выбранного inline ответа (${selectedInlineIndex})`, 'warning');
                }
                
                // 4. Отправляем результат выбора на сервер (для обновления состояния)
                log(`Отправка результата inline выбора (ID: ${event.choice_id}, Выбор: ${selectedInlineChoiceText}) на сервер...`, 'info');
                await sendInlineResponse(
                    novelID,
                    novelData.current_scene_index,
                    event.choice_id, // Используем ID из оригинального inline_choice
                    selectedInlineChoiceText,
                    selectedInlineIndex
                );

            } else if (event.event_type === 'choice') {
                // Нашли обычный выбор, сохраняем его и выходим из цикла событий
                finalChoiceEvent = event;
                log('Найден финальный выбор сцены. Завершение отображения событий.', 'info');
                break; // Прерываем цикл for по событиям
            }
        } // Конец цикла for по событиям

        // Если обработка сцены была прервана (например, ошибкой в inline_choice)
        if (!novelData) break;

        // --- Обработка финального выбора сцены --- 
        if (!finalChoiceEvent) {
            // Если нет финального выбора И история не завершена
            if (!novelData.is_complete) {
                log('Не найдено финальное событие выбора в сцене и история не завершена. Завершение.', 'warning');
            }
            // Если история завершена, просто выходим
            break; // Выходим из основного цикла while
        }

        // Делаем финальный выбор
        log('Ожидание финального выбора пользователя...', 'info');
        const finalChoice = await makeUserChoice(finalChoiceEvent);
        if (!finalChoice) {
            log('Не удалось сделать финальный выбор. Завершение.', 'warning');
            break;
        }
        log(`Выбран финальный вариант: "${finalChoice.text}"`, 'success');

        // Готовим данные для запроса следующей сцены
        const userChoicePayload = {
            scene_index: novelData.current_scene_index,
            choice_text: finalChoice.text
        };

        // Запрашиваем следующую сцену
        log('Запрос следующей сцены...', 'info');
        novelData = await generateNovelContent(novelID, userChoicePayload);
        
        if (!novelData) {
            log('Получен пустой ответ при запросе следующей сцены. Завершение.', 'error');
            break;
        }

        iterationsCount++;
    } // Конец основного цикла while
    
    console.log(chalk.cyan('\n=============================\n')); // Закрываем последнюю сцену

    // --- Финальное сообщение --- 
    if (novelData && novelData.is_complete) {
        log('Новелла завершена!', 'success');
        if (novelData.summary) {
            console.log(chalk.cyan('\n======= ИТОГ ИСТОРИИ =======\n'));
            console.log(chalk.white(novelData.summary));
            console.log(chalk.cyan('\n============================\n'));
        }
    } else if (iterationsCount >= config.maxScenes) {
        log('Достигнут лимит генерации сцен.', 'warning');
    } else {
        log('Генерация прервана.', 'warning');
    }
    
    // Закрываем интерфейс чтения
    rl.close();
    
    log(`Процесс завершен. История сохранена.`, 'success');
    
  } catch (error) {
    // Закрываем интерфейс чтения в случае ошибки
    rl.close();
    log(`Произошла глобальная ошибка: ${error.message}`, 'error');
    log(error.stack);
    saveHistory();
  }
}

// Запуск скрипта
main(); 