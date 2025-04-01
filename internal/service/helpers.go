package service

import (
	"fmt"
	"log"
	"novel-server/internal/domain"
)

// determineSceneCount определяет количество сцен на основе длины новеллы
func (s *NovelContentService) determineSceneCount(length string) int {
	switch length {
	case "short":
		return 3
	case "medium":
		return 5 // может быть от 5 до 7
	case "long":
		return 10
	default:
		return 5 // по умолчанию medium
	}
}

// extractSceneContent извлекает содержимое сцены из состояния новеллы
func (s *NovelContentService) extractSceneContent(state *domain.NovelState, sceneIndex int) (*domain.SceneContent, error) {
	// Проверяем, что индекс сцены находится в допустимых пределах
	if sceneIndex < 0 || sceneIndex >= len(state.Scenes) {
		return nil, fmt.Errorf("scene index %d out of bounds [0, %d)", sceneIndex, len(state.Scenes))
	}

	// Получаем сцену
	scene := state.Scenes[sceneIndex]

	// Создаем объект SceneContent
	sceneContent := &domain.SceneContent{
		BackgroundID: scene.BackgroundID,
		Events:       scene.Events,
	}

	// Заполняем персонажей, если они есть
	// Это упрощенная реализация, может потребоваться дополнительная логика для полного извлечения персонажей

	log.Printf("[extractSceneContent] Extracted content for scene %d", sceneIndex)
	return sceneContent, nil
}

// Вспомогательная функция для безопасного обновления полей состояния
func updateStateField(fieldPtr interface{}, value interface{}) {
	if value == nil {
		return
	}

	switch v := fieldPtr.(type) {
	case *string:
		if strVal, ok := value.(string); ok {
			*v = strVal
		}
	case *int:
		if floatVal, ok := value.(float64); ok { // JSON числа обычно float64
			*v = int(floatVal)
		} else if intVal, ok := value.(int); ok { // На всякий случай
			*v = intVal
		}
	case *[]string:
		if arrVal, ok := value.([]interface{}); ok {
			strSlice := []string{}
			for _, item := range arrVal {
				if strItem, ok := item.(string); ok {
					strSlice = append(strSlice, strItem)
				}
			}
			*v = strSlice
		}
	case *map[string]int:
		if mapVal, ok := value.(map[string]interface{}); ok {
			intMap := make(map[string]int)
			for key, val := range mapVal {
				if floatInnerVal, ok := val.(float64); ok {
					intMap[key] = int(floatInnerVal)
				}
			}
			*v = intMap
		}
	case *map[string]interface{}:
		if mapVal, ok := value.(map[string]interface{}); ok {
			*v = mapVal
		}
		// Добавьте другие типы по необходимости
	}
}
