package logger

import (
	// "net/http" // <<< Больше не нужен здесь
	// "os" // Не использовался
	"strings"
	// "time" // <<< Больше не нужен здесь

	// "github.com/labstack/echo/v4" // <<< Удаляем зависимость от Echo
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config хранит настройки логгера.
type Config struct {
	Level      string // Уровни: debug, info, warn, error, dpanic, panic, fatal
	Encoding   string // json или console
	OutputPath string // Путь к файлу логов (или stdout/stderr)
}

// New создает новый экземпляр zap.Logger на основе конфигурации.
func New(cfg Config) (*zap.Logger, error) {
	var zapConfig zap.Config

	// Уровень логгирования
	logLevel := zap.NewAtomicLevel()
	if err := logLevel.UnmarshalText([]byte(strings.ToLower(cfg.Level))); err != nil {
		logLevel.SetLevel(zap.InfoLevel) // По умолчанию info
	}

	// Конфиг по умолчанию для разработки (консоль, цвет, debug уровень)
	zapConfig = zap.NewDevelopmentConfig()
	zapConfig.Level = logLevel // Устанавливаем выбранный уровень
	zapConfig.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder // Цветной вывод уровня

	// Если указан формат json, используем конфиг для продакшена
	if strings.ToLower(cfg.Encoding) == "json" {
		zapConfig = zap.NewProductionConfig()
		zapConfig.Level = logLevel
	}

	// Настройка вывода логов
	if cfg.OutputPath != "" && cfg.OutputPath != "stdout" && cfg.OutputPath != "stderr" {
		// Проверяем, что директория существует или создаем ее
		// dir := filepath.Dir(cfg.OutputPath)
		// if _, err := os.Stat(dir); os.IsNotExist(err) {
		// 	if err := os.MkdirAll(dir, 0755); err != nil {
		// 		return nil, fmt.Errorf("cannot create log directory: %w", err)
		// 	}
		// }
		zapConfig.OutputPaths = []string{cfg.OutputPath}
		zapConfig.ErrorOutputPaths = []string{cfg.OutputPath, "stderr"} // Ошибки также в stderr
	} else {
		zapConfig.OutputPaths = []string{"stdout"}
		zapConfig.ErrorOutputPaths = []string{"stderr"}
	}

	// Собираем логгер
	logger, err := zapConfig.Build()
	if err != nil {
		return nil, err
	}

	return logger, nil
}

/* <<< Удаляем всю функцию EchoZapLogger
// EchoZapLogger возвращает middleware для Echo, которое логирует запросы с помощью zap.
func EchoZapLogger(log *zap.Logger) echo.MiddlewareFunc {
	// ... (весь код удален)
}
*/ 