package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
)

// normalizeValue рекурсивно нормализует значения для стабильного хеширования.
// - Числа приводятся к float64.
// - Срезы сортируются (если элементы сравнимы).
// - Карты рекурсивно нормализуются.
func normalizeValue(value interface{}) interface{} {
	if value == nil {
		return nil
	}

	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Map:
		// Рекурсивно нормализуем карту
		normalizedMap := make(map[string]interface{}, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			key := iter.Key().String() // Ключи map[string]interface{} должны быть строками
			normalizedMap[key] = normalizeValue(iter.Value().Interface())
		}
		return normalizedMap

	case reflect.Slice, reflect.Array:
		// Нормализуем и сортируем срез
		length := v.Len()
		normalizedSlice := make([]interface{}, length)
		for i := 0; i < length; i++ {
			normalizedSlice[i] = normalizeValue(v.Index(i).Interface())
		}
		// Попытка сортировки (сработает для строк, чисел)
		sort.SliceStable(normalizedSlice, func(i, j int) bool {
			// Простая сортировка для базовых типов
			iStr := fmt.Sprintf("%v", normalizedSlice[i])
			jStr := fmt.Sprintf("%v", normalizedSlice[j])
			// Если оба - числа, сравниваем как float64
			iFloat, iErr := strconv.ParseFloat(iStr, 64)
			jFloat, jErr := strconv.ParseFloat(jStr, 64)
			if iErr == nil && jErr == nil {
				return iFloat < jFloat
			}
			// Иначе сравниваем как строки
			return iStr < jStr
		})
		return normalizedSlice

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// Приводим целые числа к float64
		return float64(v.Int())

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		// Приводим беззнаковые целые числа к float64
		return float64(v.Uint())

	case reflect.Float32, reflect.Float64:
		// Float уже в нужном формате
		return v.Float()

	case reflect.String, reflect.Bool:
		// Строки и булевы значения оставляем как есть
		return v.Interface()

	default:
		// Для других типов (structs, pointers, etc.) возвращаем как есть
		// Возможно, понадобится более сложная логика для специфичных случаев
		// Полагаемся на то, что json.Marshal справится
		// Если это json.Number, пытаемся конвертировать
		if num, ok := value.(json.Number); ok {
			if f, err := num.Float64(); err == nil {
				return f
			}
			// Если не float, может быть int?
			if i, err := num.Int64(); err == nil {
				return float64(i)
			}
			// Иначе возвращаем как строку
			return num.String()
		}
		return v.Interface() // Возвращаем исходное значение
	}
}

// CalculateStateHash создает стабильный SHA256 хеш из релевантных полей NovelState
func CalculateStateHash(coreStats map[string]int, globalFlags []string, storyVariables map[string]interface{}) (string, error) {
	// 1. Нормализация и подготовка данных

	// Нормализуем StoryVariables
	normalizedStoryVars := normalizeValue(storyVariables).(map[string]interface{}) // Приведение типа безопасно, т.к. normalizeValue вернет map

	// Создаем копию globalFlags и сортируем её (или используем пустой срез, если nil)
	sortedGlobalFlags := make([]string, len(globalFlags))
	copy(sortedGlobalFlags, globalFlags)
	if sortedGlobalFlags != nil {
		sort.Strings(sortedGlobalFlags)
	} else {
		sortedGlobalFlags = []string{}
	}

	// CoreStats уже map[string]int, json.Marshal сам отсортирует ключи.
	// Ручная сортировка ключей не нужна.

	// 2. Собираем структуру для хеширования
	data := struct {
		CoreStats      map[string]int         `json:"core_stats"`
		GlobalFlags    []string               `json:"global_flags"`
		StoryVariables map[string]interface{} `json:"story_variables"` // Используем нормализованные данные
	}{
		CoreStats:      coreStats,
		GlobalFlags:    sortedGlobalFlags,   // Используем отсортированные флаги
		StoryVariables: normalizedStoryVars, // Используем нормализованные переменные
	}

	// 3. Сериализуем подготовленную структуру в JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("ошибка сериализации состояния для хеширования: %w", err)
	}

	// 4. Вычисляем SHA256 хеш
	hashBytes := sha256.Sum256(jsonData)
	hashString := hex.EncodeToString(hashBytes[:])

	return hashString, nil
}
