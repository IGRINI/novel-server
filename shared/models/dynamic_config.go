package models

import "time"

// DynamicConfig представляет динамически настраиваемый параметр.
type DynamicConfig struct {
	Key       string    `json:"key" db:"key"`               // Уникальный ключ параметра
	Value     string    `json:"value" db:"value"`           // Текущее значение параметра
	CreatedAt time.Time `json:"created_at" db:"created_at"` // Добавлено для соответствия SELECT
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"` // Время последнего обновления
	// CreatedAt нам не особо нужен в логике, но есть в БД
}
