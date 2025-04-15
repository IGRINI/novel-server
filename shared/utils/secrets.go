package utils

import (
	"fmt"
	"os"
	"strings"
)

// ReadSecret читает секрет из файла в стандартном пути Docker Secrets.
func ReadSecret(secretName string) (string, error) {
	// Путь по умолчанию для Docker Secrets
	filePath := fmt.Sprintf("/run/secrets/%s", secretName)
	secretBytes, err := os.ReadFile(filePath)
	if err != nil {
		// Не добавляем fallback на env var, чтобы поведение было консистентным
		return "", fmt.Errorf("failed to read secret file %s: %w", filePath, err)
	}
	secret := strings.TrimSpace(string(secretBytes))
	if secret == "" {
		return "", fmt.Errorf("secret file %s is empty", filePath)
	}
	return secret, nil
}
