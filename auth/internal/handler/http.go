package handler

import (
	// domain "novel-server/auth/internal/domain" // <<< Удаляем неправильный импорт домена

	// Импортируем интерфейс репозитория

	"regexp" // <<< Добавляем для валидации username
	// Добавляем для парсинга ID
	// <<< Добавляем для валидации пароля
	// <<< Добавляем импорт конфига
	// Нужен для парсинга refresh token
	// <<< Добавляем импорт UUID
)

// AuthHandler handles HTTP requests related to authentication.
// <<< Удаляю структуру AuthHandler, функцию NewAuthHandler и метод RegisterRoutes >>>

// --- Request/Response Structs ---

// <<< Удаляю структуры registerRequest, loginRequest, refreshRequest, logoutRequest, tokenVerifyRequest, generateInterServiceTokenRequest, meResponse, updateUserRequest, updatePasswordRequest >>>

// --- Константы для валидации ---
const (
	minUsernameLength = 3
	maxUsernameLength = 30
	minPasswordLength = 8
	maxPasswordLength = 100
)

// Регулярное выражение для проверки допустимых символов в имени пользователя
var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
