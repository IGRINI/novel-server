package repository

import (
	"errors"
	// models "novel-server/gameplay-service/internal/models" // <<< Удаляем локальный импорт
)

// ErrInvalidCursor сигнализирует о некорректном формате курсора пагинации.
var ErrInvalidCursor = errors.New("invalid cursor format")

// Интерфейс StoryConfigRepository теперь определен в shared/interfaces
