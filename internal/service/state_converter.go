package service

import (
	"novel-server/internal/domain"

	"github.com/google/uuid"
)

// ExtractUserStoryProgress извлекает из полного состояния новеллы (NovelState)
// только динамические элементы прогресса пользователя (UserStoryProgress).
func ExtractUserStoryProgress(state *domain.NovelState, novelID uuid.UUID, userID string, sceneIndex int) *domain.UserStoryProgress {
	if state == nil {
		return nil
	}

	return &domain.UserStoryProgress{
		NovelID:           novelID,
		UserID:            userID,
		SceneIndex:        sceneIndex,
		GlobalFlags:       state.GlobalFlags,
		Relationship:      state.Relationship,
		StoryVariables:    state.StoryVariables,
		PreviousChoices:   state.PreviousChoices,
		StorySummarySoFar: state.StorySummarySoFar,
		FutureDirection:   state.FutureDirection,
		StateHash:         state.StateHash,
	}
}
