package middleware

// Ключи контекста
type contextKey string

const (
	userIDKey    contextKey = "user_id"
	usernameKey  contextKey = "username"
	userEmailKey contextKey = "user_email"
)

// GetUserIDFromContext извлекает ID пользователя из контекста
func GetUserIDFromContext(ctx interface{}) (string, bool) {
	if ctx == nil {
		return "", false
	}
	if c, ok := ctx.(interface{ Value(interface{}) interface{} }); ok {
		userID, ok := c.Value(userIDKey).(string)
		return userID, ok
	}
	return "", false
}

// GetUsernameFromContext извлекает имя пользователя из контекста
func GetUsernameFromContext(ctx interface{}) (string, bool) {
	if ctx == nil {
		return "", false
	}
	if c, ok := ctx.(interface{ Value(interface{}) interface{} }); ok {
		username, ok := c.Value(usernameKey).(string)
		return username, ok
	}
	return "", false
}

// GetUserEmailFromContext извлекает email пользователя из контекста
func GetUserEmailFromContext(ctx interface{}) (string, bool) {
	if ctx == nil {
		return "", false
	}
	if c, ok := ctx.(interface{ Value(interface{}) interface{} }); ok {
		email, ok := c.Value(userEmailKey).(string)
		return email, ok
	}
	return "", false
}
