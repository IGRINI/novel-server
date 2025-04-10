module.exports = {
  // Базовый URL API
  baseUrl: 'http://localhost:8080',
  // Базовый URL для WebSocket
  wsBaseUrl: 'ws://localhost:8080', 
  
  // Описание новеллы, которую мы хотим создать
  defaultPrompt: 'Создай визуальную новеллу о приключениях Алисы в мире, где технологии сливаются с магией',
  
  // Максимальное количество сцен для генерации
  maxScenes: 6,
  
  // Имя файла для сохранения результата
  outputFile: 'novel-output.json',
  
  // Детальный вывод в консоль
  verbose: true,
  
  // Конфигурация для core_stats
  stats: {
    defaultMin: 0,         // Минимальное значение для всех статов
    defaultMax: 100,       // Максимальное значение для всех статов
    defaultInitial: 50,    // Начальное значение по умолчанию, если не указано
    enforceMinMax: true,   // Принудительно ограничивать значения в пределах min-max
    logStatChanges: true   // Логировать изменения статов
  },
  
  api: {
    auth: {
      register: '/auth/register',
      login: '/auth/login'
    },
    novels: {
      list: '/api/novels',
      myNovels: '/api/my-novels',
      create: '/api/novels',
      get: '/api/novels/{id}',
      generate: {
        config: '/api/generate/draft',
        content: '/api/generate/content',
        draftModify: '/api/generate/draft/{id}/modify',
        setup: '/api/generate/setup',
        drafts: '/api/generate/drafts',
        draftDetails: '/api/generate/drafts/{id}'
      },
      gameOver: '/api/novels/{id}/gameover'
    },
    tasks: '/api/tasks',
    websocket: '/ws' // Путь к WebSocket эндпоинту
  }
}; 