package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	// Получаем директорию проекта
	projectDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("Ошибка при определении рабочей директории: %v", err)
	}

	// Путь к исполняемому файлу сервера
	serverPath := filepath.Join(projectDir, "cmd", "server")

	// Запуск сервера
	cmd := exec.Command("go", "run", ".")
	cmd.Dir = serverPath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Println("Запуск сервера из директории", serverPath)
	if err := cmd.Run(); err != nil {
		log.Fatalf("Ошибка при запуске сервера: %v", err)
	}
}
