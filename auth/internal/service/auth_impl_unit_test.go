package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Тесты для hashPassword и checkPasswordHash

func TestHashAndCheckPassword(t *testing.T) {
	password := "mysecretpassword"
	// Используем простую соль для тестов.
	// В реальном приложении соль должна быть уникальной для пользователя или глобальной, но безопасной.
	salt := "test-salt-for-unit-tests"

	// 1. Тест hashPassword
	hashedPassword, err := hashPassword(password, salt)
	require.NoError(t, err, "hashPassword should not return an error")
	require.NotEmpty(t, hashedPassword, "hashPassword should return a non-empty string")
	// Проверяем, что хеш отличается от исходного пароля
	assert.NotEqual(t, password, hashedPassword, "Hashed password should not be equal to the original password")

	// 2. Тест checkPasswordHash - Успех
	match := checkPasswordHash(password, salt, hashedPassword)
	assert.True(t, match, "checkPasswordHash should return true for correct password and salt")

	// 3. Тест checkPasswordHash - Неверный пароль
	wrongPassword := "wrongpassword"
	match = checkPasswordHash(wrongPassword, salt, hashedPassword)
	assert.False(t, match, "checkPasswordHash should return false for incorrect password")

	// 4. Тест checkPasswordHash - Неверная соль (bcrypt хеш содержит соль, поэтому проверка не пройдет)
	// Примечание: Наша реализация checkPasswordHash добавляет соль к паролю *до* сравнения bcrypt.
	// Это не стандартный способ использования bcrypt. Обычно bcrypt.CompareHashAndPassword сравнивает пароль с хешем, который уже содержит соль.
	// Но раз уж реализация такова, тест должен это отражать.
	wrongSalt := "another-salt"
	match = checkPasswordHash(password, wrongSalt, hashedPassword)
	assert.False(t, match, "checkPasswordHash should return false for incorrect salt (given current implementation)")

	// 5. Тест checkPasswordHash - Невалидный хеш
	invalidHash := "not-a-bcrypt-hash"
	match = checkPasswordHash(password, salt, invalidHash)
	assert.False(t, match, "checkPasswordHash should return false for invalid hash format")

	// 6. Тест hashPassword - пустой пароль (должен ли он хешироваться? Зависит от требований)
	// Допустим, мы разрешаем хеширование пустого пароля
	hashedEmpty, err := hashPassword("", salt)
	require.NoError(t, err, "hashPassword should handle empty password")
	require.NotEmpty(t, hashedEmpty, "hashPassword should return non-empty hash for empty password")
	assert.True(t, checkPasswordHash("", salt, hashedEmpty), "checkPasswordHash should verify empty password")
	assert.False(t, checkPasswordHash("nonempty", salt, hashedEmpty), "checkPasswordHash should not verify non-empty password against empty hash")
}
