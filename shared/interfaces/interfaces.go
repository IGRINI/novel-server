package interfaces

import "errors"

// ErrNotFound стандартная ошибка, когда запись не найдена в репозитории.
var ErrNotFound = errors.New("not found")

// TODO: Добавить сюда другие общие интерфейсы репозиториев и сервисов,
// например, StoryConfigRepository, PublishedStoryRepository и т.д.,
// если они еще не здесь.
