package logger

import (
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config содержит настройки для логгера.
type Config struct {
	Level      string // Уровень логирования (debug, info, warn, error)
	Encoding   string // Формат вывода (json или console)
	OutputPath string // Путь к файлу лога (если пусто, используется stdout)
}

// New создает новый экземпляр zap.Logger на основе конфигурации.
func New(cfg Config) (*zap.Logger, error) {
	// Устанавливаем уровень логирования
	level := zap.NewAtomicLevel()
	logLevel := strings.ToLower(cfg.Level)
	if logLevel == "" {
		logLevel = "info" // Уровень по умолчанию
	}
	if err := level.UnmarshalText([]byte(logLevel)); err != nil {
		// Логируем ошибку в stderr, так как логгер еще не создан
		fmt.Fprintf(os.Stderr, "Invalid log level '%s', using 'info'. Error: %v\n", cfg.Level, err)
		level.SetLevel(zap.InfoLevel)
	}

	// Настройка кодировщика
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "timestamp"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderCfg.EncodeLevel = zapcore.CapitalLevelEncoder // Уровни будут выглядеть как INFO, WARN

	encoding := strings.ToLower(cfg.Encoding)
	if encoding != "console" && encoding != "json" {
		encoding = "json" // По умолчанию json
	}

	// Настройка вывода
	outputPath := cfg.OutputPath
	if outputPath == "" {
		outputPath = "stdout"
	}

	// Создание конфигурации Zap
	zapConfig := zap.Config{
		Level:             level,
		Development:       false,
		DisableCaller:     true,     // Отключаем информацию о вызывающем для производительности
		DisableStacktrace: true,     // Отключаем стектрейсы по умолчанию
		Encoding:          encoding, // Используем строку encoding
		EncoderConfig:     encoderCfg,
		OutputPaths:       []string{outputPath}, // Куда писать основные логи
		ErrorOutputPaths:  []string{"stderr"},   // Куда писать ошибки самого логгера
	}

	// Сборка логгера
	logger, err := zapConfig.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build logger: %w", err)
	}

	return logger, nil
}
