package service

import "errors"

var (
	ErrCannotUpdateSubmittedStory    = errors.New("cannot update story config that is not in draft status")
	ErrStoryAlreadyGeneratingOrReady = errors.New("story generation already in progress or completed")
	ErrCannotRevise                  = errors.New("cannot revise story config in its current status")
	ErrUserHasActiveGeneration       = errors.New("user already has an active generation task")
	// Можно добавить другие специфичные ошибки
)
