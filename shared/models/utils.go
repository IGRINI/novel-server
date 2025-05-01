package models

// StringPtr returns a pointer to the given string.
// Useful for creating pointers to string literals or variables for optional fields.
func StringPtr(s string) *string {
	// If the input string is empty, should we return nil or a pointer to an empty string?
	// Current behavior: returns pointer to empty string if s is "".
	// If nil is desired for empty strings, add: if s == "" { return nil }
	return &s
}

// BoolPtr returns a pointer to the given boolean.
func BoolPtr(b bool) *bool {
	return &b
}

// IntPtr returns a pointer to the given integer.
func IntPtr(i int) *int {
	return &i
}

// Add other pointer helpers as needed (e.g., for time.Time, uuid.UUID)
