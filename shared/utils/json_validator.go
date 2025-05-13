package utils

import (
	"bytes"
	"encoding/json"
)

// DecodeStrict декодирует JSON-данные в out, запрещая неизвестные поля.
func DecodeStrict(data []byte, out interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}
