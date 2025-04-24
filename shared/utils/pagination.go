package utils

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Separators for different cursor types
const (
	timeCursorSeparator = "_" // Separator for time-based cursors
	intCursorSeparator  = ":" // Separator for int-based cursors
)

// --- Internal Helpers ---

// encodeCursorInternal encodes a value string and ID into a base64 cursor string using a specific separator.
func encodeCursorInternal(valueStr string, id uuid.UUID, separator string) string {
	if id == uuid.Nil || valueStr == "" {
		return "" // Cannot encode without both parts
	}
	cursorData := fmt.Sprintf("%s%s%s", valueStr, separator, id.String())
	return base64.URLEncoding.EncodeToString([]byte(cursorData))
}

// decodeCursorInternal decodes a base64 cursor string into a value string and ID using a specific separator.
func decodeCursorInternal(cursor string, separator string) (valueStr string, id uuid.UUID, err error) {
	if cursor == "" {
		return "", uuid.Nil, nil // Empty cursor is valid, means start from beginning
	}

	decodedBytes, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return "", uuid.Nil, fmt.Errorf("invalid cursor base64 format: %w", err)
	}

	cursorData := string(decodedBytes)
	parts := strings.SplitN(cursorData, separator, 2)
	if len(parts) != 2 {
		return "", uuid.Nil, fmt.Errorf("invalid cursor separator format, expected 2 parts, got %d", len(parts))
	}

	valueStr = parts[0]
	id, err = uuid.Parse(parts[1])
	if err != nil {
		return "", uuid.Nil, fmt.Errorf("invalid cursor uuid format: %w", err)
	}

	return valueStr, id, nil
}

// --- Time-based Cursor Functions ---

// EncodeCursor creates a base64 encoded cursor string from time and UUID.
func EncodeCursor(t time.Time, id uuid.UUID) string {
	// Use nanoseconds for precision
	valueStr := strconv.FormatInt(t.UnixNano(), 10)
	return encodeCursorInternal(valueStr, id, timeCursorSeparator)
}

// DecodeCursor parses a base64 encoded time-based cursor string into time and UUID.
func DecodeCursor(cursor string) (time.Time, uuid.UUID, error) {
	valueStr, id, err := decodeCursorInternal(cursor, timeCursorSeparator)
	if err != nil || cursor == "" { // Handle error or empty cursor
		return time.Time{}, id, err
	}

	timestampNano, err := strconv.ParseInt(valueStr, 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("invalid cursor timestamp format: %w", err)
	}
	t := time.Unix(0, timestampNano).UTC() // Always use UTC

	return t, id, nil
}

// --- Integer-based Cursor Functions (Moved from cursor.go) ---

// EncodeIntCursor кодирует пару (значение int64, ID uuid) в строку курсора base64.
func EncodeIntCursor(value int64, id uuid.UUID) string {
	valueStr := strconv.FormatInt(value, 10)
	return encodeCursorInternal(valueStr, id, intCursorSeparator)
}

// DecodeIntCursor декодирует строку курсора base64 в пару (значение int64, ID uuid).
// Возвращает (0, uuid.Nil, error) при ошибке.
// Пустой курсор возвращает (0, uuid.Nil, nil).
func DecodeIntCursor(cursor string) (int64, uuid.UUID, error) {
	valueStr, id, err := decodeCursorInternal(cursor, intCursorSeparator)
	if err != nil || cursor == "" { // Handle error or empty cursor
		// For empty cursor, id will be Nil, return 0 value
		var zeroValue int64
		return zeroValue, id, err
	}

	value, err := strconv.ParseInt(valueStr, 10, 64)
	if err != nil {
		return 0, uuid.Nil, fmt.Errorf("failed to parse cursor value as int64: %w", err)
	}

	return value, id, nil
}
