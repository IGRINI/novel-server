package models

// UserChoiceInfo stores information about a choice the user made in a previous step.
// Used to inform the AI about the context leading to the current state.
type UserChoiceInfo struct {
	Desc         string  `json:"d"`            // Description of the situation/choice block
	Text         string  `json:"t"`            // Text of the option the user selected
	ResponseText *string `json:"rt,omitempty"` // Optional response text from the consequence
}

// GameStateSummaryDTO is used for listing game states for a story.
