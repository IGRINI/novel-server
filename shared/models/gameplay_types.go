package models

// UserChoiceInfo holds information about a choice made by the user.
// Used in messaging payloads between gameplay-service and story-generator.
type UserChoiceInfo struct {
	Desc string `json:"d"` // Description of the choice block
	Text string `json:"t"` // Text of the chosen option
}
