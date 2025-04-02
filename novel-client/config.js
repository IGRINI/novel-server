module.exports = {
  // Базовый URL API
  baseUrl: 'http://localhost:8080',
  
  // Описание новеллы, которую мы хотим создать
  defaultPrompt: 'Создай визуальную новеллу о приключениях Алисы в мире, где технологии сливаются с магией',
  
  // Максимальное количество сцен для генерации
  maxScenes: 6,
  
  // Имя файла для сохранения результата
  outputFile: 'novel-output.json',
  
  // Детальный вывод в консоль
  verbose: true,
  
  api: {
    auth: {
      register: '/auth/register',
      login: '/auth/login'
    },
    novels: {
      list: '/api/novels',
      create: '/api/novels',
      generate: {
        config: '/api/generate/draft',
        content: '/api/generate/content',
        draftModify: '/api/generate/draft/{id}/modify',
        setup: '/api/generate/setup'
      }
    },
    tasks: '/api/tasks'
  }
}; 