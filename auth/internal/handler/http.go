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

// <<< Определение и регистрация кастомных метрик >>>

// --- Коды ошибок API ---
const (
	// 4xx Клиентские ошибки
	ErrCodeBadRequest         = 40001
	ErrCodeInvalidCredentials = 40101
	ErrCodeInvalidToken       = 40102
	ErrCodeExpiredToken       = 40103
	ErrCodeRevokedToken       = 40104 // Токен не найден в хранилище (отозван/вышли)
	ErrCodeForbidden          = 40301 // Доступ запрещен (если понадобится)
	ErrCodeNotFound           = 40401 // Общая "не найдено"
	ErrCodeUserNotFound       = 40402
	ErrCodeUserAlreadyExists  = 40901 // Конфликт

	// 5xx Серверные ошибки
	ErrCodeInternalError = 50001
)

// --- Константы для валидации ---
const (
	minUsernameLength = 3
	maxUsernameLength = 30
	minPasswordLength = 8
	maxPasswordLength = 100
)

// Регулярное выражение для проверки допустимых символов в имени пользователя
// (латинские буквы, цифры, подчеркивание, дефис)
var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// AuthHandler handles HTTP requests related to authentication.
// <<< Удаляю структуру AuthHandler, функцию NewAuthHandler и метод RegisterRoutes >>>

// --- Request/Response Structs ---

// <<< Удаляю структуры registerRequest, loginRequest, refreshRequest, logoutRequest, tokenVerifyRequest, generateInterServiceTokenRequest, meResponse, updateUserRequest, updatePasswordRequest >>>
