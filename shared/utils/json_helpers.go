package utils

import (
	"bytes"
	"encoding/json"
)

// MarshalMap marshals a map[string]interface{} or map[string]int into canonical JSON.
func MarshalMap(data interface{}) ([]byte, error) {
	// Using json.Marshal for maps already sorts keys by default in Go.
	return json.Marshal(data)
}

// UnmarshalMap unmarshals JSON data into a map[string]interface{}.
func UnmarshalMap(data []byte, v interface{}) error {
	// Ensure we handle empty or null JSON correctly
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		// If the target is a map, initialize it as empty map
		switch m := v.(type) {
		case *map[string]interface{}:
			*m = make(map[string]interface{})
		case *map[string]int:
			*m = make(map[string]int)
		// Add other map types if needed
		default:
			// Or handle non-map types as needed, maybe return error or do nothing
		}
		return nil
	}
	return json.Unmarshal(data, v)
}
