package models

import "context"

// contextKey - приватный тип для ключей контекста, чтобы избежать коллизий.
type contextKey string

const (
	// UserContextKey используется как ключ для хранения UserID в контексте запроса.
	UserContextKey contextKey = "userID"
	// RolesContextKey используется как ключ для хранения []string ролей пользователя в контексте запроса.
	RolesContextKey contextKey = "userRoles"
)

// GetUserIDFromContext извлекает UserID из контекста.
// Возвращает ID и true, если ключ найден и значение корректного типа (uint64).
// В противном случае возвращает 0 и false.
func GetUserIDFromContext(ctx context.Context) (uint64, bool) {
	userID, ok := ctx.Value(UserContextKey).(uint64)
	return userID, ok
}

// GetRolesFromContext извлекает срез ролей из контекста.
// Возвращает []string и true, если ключ найден и значение корректного типа ([]string).
// В противном случае возвращает nil и false.
func GetRolesFromContext(ctx context.Context) ([]string, bool) {
	roles, ok := ctx.Value(RolesContextKey).([]string)
	return roles, ok
}
