package notifications

import (
	"fmt"

	"novel-server/shared/constants"
	sharedModels "novel-server/shared/models"

	"github.com/google/uuid"
)

// BuildStoryReadyPushPayload creates a payload for a push notification about a story being ready to play.
// It takes a function to get the author's name (can be nil).
func BuildStoryReadyPushPayload(
	story *sharedModels.PublishedStory,
	getAuthorName func(userID uuid.UUID) string,
) (*sharedModels.PushNotificationPayload, error) {
	if story == nil {
		return nil, fmt.Errorf("cannot build story ready push payload for nil story")
	}
	if story.UserID == uuid.Nil {
		return nil, fmt.Errorf("cannot build story ready push payload for nil user ID")
	}

	storyTitle := "Your story"
	if story.Title != nil {
		storyTitle = *story.Title
	}

	authorName := "Unknown Author"
	if getAuthorName != nil {
		authorName = getAuthorName(story.UserID)
	}

	locKey := constants.PushLocKeyStoryReady
	fallbackTitle := "Story Ready!"
	fallbackBody := fmt.Sprintf("Your story \"%s\" is ready to play!", storyTitle)

	data := map[string]string{
		"published_story_id":           story.ID.String(),
		"event_type":                   constants.PushEventTypeStoryReady,
		constants.PushLocKey:           locKey,
		constants.PushLocArgStoryTitle: storyTitle,
		constants.PushFallbackTitleKey: fallbackTitle,
		constants.PushFallbackBodyKey:  fallbackBody,
		"title":                        storyTitle,
		"author_name":                  authorName,
	}

	payload := &sharedModels.PushNotificationPayload{
		UserID: story.UserID,
		Notification: sharedModels.PushNotification{
			Title: fallbackTitle,
			Body:  fallbackBody,
		},
		Data: data,
	}

	return payload, nil
}

// BuildDraftReadyPushPayload создает payload для push-уведомления о готовности черновика.
func BuildDraftReadyPushPayload(config *sharedModels.StoryConfig) (*sharedModels.PushNotificationPayload, error) {
	if config == nil {
		return nil, fmt.Errorf("cannot build draft ready push payload for nil config")
	}
	if config.UserID == uuid.Nil {
		return nil, fmt.Errorf("cannot build draft ready push payload for nil user ID")
	}

	fallbackTitle := "Draft Ready!"
	fallbackBody := fmt.Sprintf("Your draft \"%s\" is ready for setup.", config.Title)

	data := map[string]string{
		"story_config_id":              config.ID.String(),
		"event_type":                   constants.PushEventTypeDraftReady,
		constants.PushLocKey:           constants.PushLocKeyDraftReady,
		constants.PushLocArgStoryTitle: config.Title,
		constants.PushFallbackTitleKey: fallbackTitle,
		constants.PushFallbackBodyKey:  fallbackBody,
		"title":                        config.Title,
	}

	payload := &sharedModels.PushNotificationPayload{
		UserID: config.UserID,
		Notification: sharedModels.PushNotification{
			Title: fallbackTitle,
			Body:  fallbackBody,
		},
		Data: data,
	}

	return payload, nil
}

// BuildSetupPendingPushPayload создает payload для push-уведомления о том, что Setup сгенерирован
// и ожидается генерация первой сцены (или изображений).
func BuildSetupPendingPushPayload(story *sharedModels.PublishedStory) (*sharedModels.PushNotificationPayload, error) {
	if story == nil {
		return nil, fmt.Errorf("cannot build setup pending push payload for nil story")
	}
	if story.UserID == uuid.Nil {
		return nil, fmt.Errorf("cannot build setup pending push payload for nil user ID")
	}

	storyTitle := "Your story"
	if story.Title != nil {
		storyTitle = *story.Title
	}

	fallbackTitle := fmt.Sprintf("Story \"%s\" is almost ready...", storyTitle)
	fallbackBody := "You can start playing soon!"
	if story.Description != nil && *story.Description != "" {
		fallbackBody = *story.Description
	}

	data := map[string]string{
		"published_story_id":           story.ID.String(),
		"event_type":                   constants.PushEventTypeSetupPending,
		constants.PushLocKey:           constants.PushLocKeySetupReady,
		constants.PushLocArgStoryTitle: storyTitle,
		constants.PushFallbackTitleKey: fallbackTitle,
		constants.PushFallbackBodyKey:  fallbackBody,
		"title":                        storyTitle,
	}

	payload := &sharedModels.PushNotificationPayload{
		UserID: story.UserID,
		Notification: sharedModels.PushNotification{
			Title: fallbackTitle,
			Body:  fallbackBody,
		},
		Data: data,
	}

	return payload, nil
}

// BuildSceneReadyPushPayload создает payload для push-уведомления о готовности новой сцены.
func BuildSceneReadyPushPayload(
	story *sharedModels.PublishedStory,
	gameStateID uuid.UUID,
	sceneID uuid.UUID,
	getAuthorName func(userID uuid.UUID) string,
) (*sharedModels.PushNotificationPayload, error) {
	if story == nil {
		return nil, fmt.Errorf("cannot build scene ready push payload for nil story")
	}
	if story.UserID == uuid.Nil {
		return nil, fmt.Errorf("cannot build scene ready push payload for nil user ID")
	}
	if gameStateID == uuid.Nil {
		return nil, fmt.Errorf("cannot build scene ready push payload for nil game state ID")
	}
	if sceneID == uuid.Nil {
		return nil, fmt.Errorf("cannot build scene ready push payload for nil scene ID")
	}

	storyTitle := "Your story"
	if story.Title != nil {
		storyTitle = *story.Title
	}

	authorName := "Unknown Author"
	if getAuthorName != nil {
		authorName = getAuthorName(story.UserID)
	}

	locKey := constants.PushLocKeySceneReady
	fallbackTitle := "New Scene Ready!"
	fallbackBody := fmt.Sprintf("A new scene in \"%s\" is ready!", storyTitle)

	data := map[string]string{
		"published_story_id":           story.ID.String(),
		"game_state_id":                gameStateID.String(),
		"scene_id":                     sceneID.String(),
		"event_type":                   constants.PushEventTypeSceneReady,
		constants.PushLocKey:           locKey,
		constants.PushLocArgStoryTitle: storyTitle,
		constants.PushFallbackTitleKey: fallbackTitle,
		constants.PushFallbackBodyKey:  fallbackBody,
		"title":                        storyTitle,
		"author_name":                  authorName,
	}

	payload := &sharedModels.PushNotificationPayload{
		UserID: story.UserID,
		Notification: sharedModels.PushNotification{
			Title: fallbackTitle,
			Body:  fallbackBody,
		},
		Data: data,
	}

	return payload, nil
}

// BuildGameOverPushPayload создает payload для push-уведомления о завершении игры.
func BuildGameOverPushPayload(
	story *sharedModels.PublishedStory,
	gameStateID uuid.UUID,
	sceneID uuid.UUID,
	endingText string,
	getAuthorName func(userID uuid.UUID) string,
) (*sharedModels.PushNotificationPayload, error) {
	if story == nil {
		return nil, fmt.Errorf("cannot build game over push payload for nil story")
	}
	if story.UserID == uuid.Nil {
		return nil, fmt.Errorf("cannot build game over push payload for nil user ID")
	}
	if gameStateID == uuid.Nil {
		return nil, fmt.Errorf("cannot build game over push payload for nil game state ID")
	}
	if sceneID == uuid.Nil {
		return nil, fmt.Errorf("cannot build game over push payload for nil scene ID")
	}

	storyTitle := "Your story"
	if story.Title != nil {
		storyTitle = *story.Title
	}

	authorName := "Unknown Author"
	if getAuthorName != nil {
		authorName = getAuthorName(story.UserID)
	}

	locKey := constants.PushLocKeyGameOver
	fallbackTitle := "Game Over!"
	fallbackBody := fmt.Sprintf("The story \"%s\" is complete.", storyTitle)

	data := map[string]string{
		"published_story_id":           story.ID.String(),
		"game_state_id":                gameStateID.String(),
		"scene_id":                     sceneID.String(),
		"event_type":                   constants.PushEventTypeGameOver,
		constants.PushLocKey:           locKey,
		constants.PushLocArgStoryTitle: storyTitle,
		constants.PushLocArgEndingText: endingText,
		constants.PushFallbackTitleKey: fallbackTitle,
		constants.PushFallbackBodyKey:  fallbackBody,
		"title":                        storyTitle,
		"author_name":                  authorName,
	}

	payload := &sharedModels.PushNotificationPayload{
		UserID: story.UserID,
		Notification: sharedModels.PushNotification{
			Title: fallbackTitle,
			Body:  fallbackBody,
		},
		Data: data,
	}

	return payload, nil
}
