package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// StateKey определяет поля состояния, влияющие на следующий шаг и используемые для хеширования
type StateKey struct {
	Choice         string                 `json:"choice"`
	GlobalFlags    []string               `json:"global_flags"`
	Relationship   map[string]int         `json:"relationship"`
	StoryVariables map[string]interface{} `json:"story_variables"`
}

// hashStateKey вычисляет SHA-256 хеш ключа состояния.
// Принимает последний сделанный выбор и текущее состояние мира.
func hashStateKey(choice string, flags []string, relationship map[string]int, variables map[string]interface{}) (string, error) {
	serializedKey, err := serializeStateKey(choice, flags, relationship, variables)
	if err != nil {
		return "", fmt.Errorf("failed to serialize state key: %w", err)
	}

	return hashData(serializedKey), nil
}

// serializeStateKey сериализует ключ состояния в JSON со стабильным порядком ключей/элементов.
func serializeStateKey(choice string, flags []string, relationship map[string]int, variables map[string]interface{}) ([]byte, error) {
	// Сортируем флаги для стабильности
	sortedFlags := make([]string, len(flags))
	copy(sortedFlags, flags)
	sort.Strings(sortedFlags)

	// Сортируем ключи карт для стабильности
	sortedRelationship := make(map[string]int)
	relKeys := make([]string, 0, len(relationship))
	for k := range relationship {
		relKeys = append(relKeys, k)
	}
	sort.Strings(relKeys)
	for _, k := range relKeys {
		sortedRelationship[k] = relationship[k]
	}

	sortedVariables := make(map[string]interface{})
	varKeys := make([]string, 0, len(variables))
	for k := range variables {
		varKeys = append(varKeys, k)
	}
	sort.Strings(varKeys)
	for _, k := range varKeys {
		sortedVariables[k] = variables[k]
	}

	key := StateKey{
		Choice:         choice,
		GlobalFlags:    sortedFlags,
		Relationship:   sortedRelationship,
		StoryVariables: sortedVariables,
	}

	// Маршалинг в JSON (стандартный маршалер сортирует ключи карт)
	return json.Marshal(key)
}

// hashData вычисляет SHA-256 хеш для байтового среза и возвращает его в виде hex-строки.
func hashData(data []byte) string {
	hasher := sha256.New()
	hasher.Write(data) // Ошибка здесь маловероятна для sha256
	return hex.EncodeToString(hasher.Sum(nil))
}
