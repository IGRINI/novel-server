package models

// Определяем константы для ролей
const (
	RoleAdmin = "ROLE_ADMIN"
	RoleUser  = "ROLE_USER"
	// Добавьте другие роли здесь, если нужно
	// RoleModerator = "ROLE_MODERATOR"
)

// AllRoles возвращает слайс всех определенных ролей.
// Этот список используется для генерации мультиселекта в админке.
func AllRoles() []string {
	return []string{
		RoleAdmin,
		RoleUser,
		// RoleModerator,
	}
}

// AllRolesMap возвращает map[string]struct{} всех определенных ролей для быстрой проверки наличия.
func AllRolesMap() map[string]struct{} {
	roles := AllRoles()
	roleMap := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		roleMap[role] = struct{}{}
	}
	return roleMap
}

// HasRole проверяет, есть ли у пользователя указанная роль.
func HasRole(userRoles []string, targetRole string) bool {
	for _, role := range userRoles {
		if role == targetRole {
			return true
		}
	}
	return false
}
