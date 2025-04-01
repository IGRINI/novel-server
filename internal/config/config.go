package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Config содержит все конфигурационные параметры приложения
type Config struct {
	Server   ServerConfig
	API      APIConfig
	DeepSeek DeepSeekConfig
}

// ServerConfig содержит настройки HTTP сервера
type ServerConfig struct {
	Port int
	Host string
}

// APIConfig содержит общие настройки API
type APIConfig struct {
	BasePath string
}

// DeepSeekConfig содержит настройки для работы с DeepSeek
type DeepSeekConfig struct {
	APIKey    string
	ModelName string
}

// LoadConfig загружает конфигурацию из переменных окружения
func LoadConfig() (*Config, error) {
	// Загружаем переменные окружения из .env файла
	err := godotenv.Load()
	if err != nil {
		fmt.Printf("Warning: .env file not found or could not be loaded: %v\n", err)
		// Продолжаем работу - переменные окружения могут быть установлены другим способом
	}

	config := &Config{
		Server: ServerConfig{
			Port: getEnvAsInt("SERVER_PORT", 8080),
			Host: getEnv("SERVER_HOST", ""),
		},
		API: APIConfig{
			BasePath: getEnv("API_BASE_PATH", "/api"),
		},
		DeepSeek: DeepSeekConfig{
			APIKey:    getEnv("OPENROUTER_API_KEY", ""),
			ModelName: getEnv("DEEPSEEK_MODEL", "deepseek/deepseek-chat-v3-0324:free"),
		},
	}

	// Проверка обязательных параметров
	if config.DeepSeek.APIKey == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY is not set")
	}

	return config, nil
}

// getEnv возвращает значение переменной окружения или значение по умолчанию
func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// getEnvAsInt возвращает значение переменной окружения как int или значение по умолчанию
func getEnvAsInt(key string, defaultValue int) int {
	valueStr := getEnv(key, "")
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return defaultValue
	}

	return value
}

// Здесь будет логика конфигурации
