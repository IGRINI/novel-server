package utils

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// CursorSeparator separates timestamp and ID in the cursor string.
const CursorSeparator = "_"

// EncodeCursor creates a base64 encoded cursor string from time and UUID.
func EncodeCursor(t time.Time, id uuid.UUID) string {
	key := fmt.Sprintf("%d%s%s", t.UnixNano(), CursorSeparator, id.String())
	return base64.URLEncoding.EncodeToString([]byte(key))
}

// DecodeCursor parses a base64 encoded cursor string into time and UUID.
func DecodeCursor(cursor string) (time.Time, uuid.UUID, error) {
	if cursor == "" {
		return time.Time{}, uuid.Nil, nil // No cursor means start from the beginning
	}
	decodedBytes, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("invalid cursor base64 format: %w", err)
	}
	key := string(decodedBytes)
	parts := strings.SplitN(key, CursorSeparator, 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, fmt.Errorf("invalid cursor separator format")
	}

	timestampNano, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("invalid cursor timestamp format: %w", err)
	}
	t := time.Unix(0, timestampNano).UTC() // Always use UTC

	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("invalid cursor uuid format: %w", err)
	}

	return t, id, nil
}
