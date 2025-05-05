package service

import "errors"

var (
	ErrCannotRetryInitial      = errors.New("cannot retry initial generation steps (setup, first scene text, cover image) as they already exist or are pending")
	ErrNoSaveSlotsAvailable    = errors.New("no save slots available")
	ErrSaveSlotLimitReached    = errors.New("player has reached the maximum number of save slots")
	ErrInitialSceneNotReadyYet = errors.New("initial scene for the story is not generated or ready yet")
)
