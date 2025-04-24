package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"novel-server/image-generator/internal/config"
	sharedMessaging "novel-server/shared/messaging"
)

// ErrImageGenerationFailed - ошибка при генерации изображения SANA сервером.
var ErrImageGenerationFailed = errors.New("image generation failed")

// ErrImageSaveFailed - ошибка при сохранении файла.
var ErrImageSaveFailed = errors.New("image save failed")

// GenerateImageResult - структура для результата генерации изображения.
type GenerateImageResult struct {
	ImageURL string
	Error    error
}

// ImageGenerationService определяет интерфейс для генерации и сохранения изображений.
type ImageGenerationService interface {
	// GenerateAndStoreImage генерирует изображение по данным задачи, сохраняет его
	// и возвращает результат (URL или ошибку).
	GenerateAndStoreImage(ctx context.Context, taskPayload sharedMessaging.CharacterImageTaskPayload) GenerateImageResult
}

// imageServiceImpl - реализация ImageGenerationService.
type imageServiceImpl struct {
	logger            *zap.Logger
	sanaConfig        config.SanaServerConfig
	sanaClient        *http.Client
	imageSavePath     string // Путь для сохранения файлов
	imageBaseURL      string // Базовый URL для доступа к файлам
	promptStyleSuffix string // Суффикс стиля для промпта
}

// NewImageGenerationService создает новый экземпляр imageServiceImpl.
// Возвращает ошибку, если не удалось подключиться к Minio.
func NewImageGenerationService(
	logger *zap.Logger,
	cfg *config.Config, // Принимаем весь конфиг
) (ImageGenerationService, error) {
	// Проверяем, что путь сохранения и URL заданы (хотя они задаются в docker-compose)
	if cfg.ImageSavePath == "" {
		return nil, errors.New("image save path (IMAGE_SAVE_PATH) is not configured")
	}
	if cfg.ImagePublicBaseURL == "" {
		return nil, errors.New("image public base URL (IMAGE_PUBLIC_BASE_URL) is not configured")
	}

	return &imageServiceImpl{
		logger:     logger,
		sanaConfig: cfg.SanaServer,
		sanaClient: &http.Client{
			Timeout: time.Duration(cfg.SanaServer.Timeout) * time.Second,
		},
		imageSavePath:     cfg.ImageSavePath,      // Добавлено
		imageBaseURL:      cfg.ImagePublicBaseURL, // Добавлено
		promptStyleSuffix: cfg.PromptStyleSuffix,  // Добавлено
	}, nil
}

// SanaAPIRequest - структура запроса к SANA API.
type SanaAPIRequest struct {
	Prompt string `json:"prompt"`
	Ratio  string `json:"ratio"`
}

// GenerateAndStoreImage - реализует основную логику.
func (s *imageServiceImpl) GenerateAndStoreImage(ctx context.Context, taskPayload sharedMessaging.CharacterImageTaskPayload) GenerateImageResult {
	log := s.logger.With(
		// zap.String("user_id", taskPayload.UserID), // UserID нет в taskPayload
		zap.String("character_id", taskPayload.CharacterID.String()), // Use .String()
		zap.String("image_reference", taskPayload.ImageReference),
		zap.String("prompt_hash", fmt.Sprintf("%x", uuid.NewSHA1(uuid.NameSpaceDNS, []byte(taskPayload.Prompt+s.promptStyleSuffix)))),
		zap.String("task_id", taskPayload.TaskID),
	)
	log.Info("Generating character image...")

	// Конкатенация промпта
	fullPrompt := taskPayload.Prompt + s.promptStyleSuffix
	log.Debug("Full prompt for SANA API", zap.String("prompt", fullPrompt))

	// Используем ratio из полезной нагрузки задачи
	imageRatio := taskPayload.Ratio
	if imageRatio == "" {
		log.Warn("Ratio not provided in task payload, defaulting to 2:3", zap.String("reference", taskPayload.ImageReference))
		imageRatio = "2:3" // Значение по умолчанию, если не передано
	} else {
		log.Info("Using image ratio from task payload", zap.String("ratio", imageRatio), zap.String("reference", taskPayload.ImageReference))
	}

	// 1. Вызов SANA Sprint API
	imageData, err := s.callSanaAPI(ctx, fullPrompt, imageRatio)
	if err != nil {
		log.Error("SANA API call failed", zap.Error(err))
		return GenerateImageResult{Error: fmt.Errorf("%w: %v", ErrImageGenerationFailed, err)}
	}
	if len(imageData) == 0 {
		log.Error("SANA API returned empty image data")
		return GenerateImageResult{Error: fmt.Errorf("%w: API returned empty data", ErrImageGenerationFailed)}
	}
	log.Info("Image data received from SANA", zap.Int("size_bytes", len(imageData)))

	// 2. Сохранение изображения в локальный файл
	// Генерируем имя файла, используя TaskID для уникальности.
	// Убедимся, что директория существует (если нет - создать? Пока предполагаем, что volume смонтирован)
	if taskPayload.ImageReference == "" {
		log.Error("Image reference is empty, cannot generate filename")
		return GenerateImageResult{Error: fmt.Errorf("%w: ImageReference is required but empty", ErrImageSaveFailed)}
	}
	fileName := fmt.Sprintf("%s.jpg", taskPayload.ImageReference)
	filePath := filepath.Join(s.imageSavePath, fileName) // Используем filepath.Join

	// Запись файла
	err = os.WriteFile(filePath, imageData, 0644) // Права доступа rw-r--r--
	if err != nil {
		log.Error("Failed to save image to file", zap.String("path", filePath), zap.Error(err))
		return GenerateImageResult{Error: fmt.Errorf("%w: %v", ErrImageSaveFailed, err)}
	}
	log.Info("Image saved to file", zap.String("path", filePath))

	// 3. Формируем публичный URL
	// Объединяем базовый URL и имя файла.
	imageURL := s.imageBaseURL + "/" + fileName
	// Убираем двойные слеши, если imageBaseURL содержит / в конце
	imageURL = strings.Replace(imageURL, "//", "/", -1)
	if !strings.HasPrefix(imageURL, "https://") && !strings.HasPrefix(imageURL, "http://") {
		// По умолчанию добавляем https, если протокол не указан
		imageURL = "https://" + imageURL // Предполагаем HTTPS для публичных URL
	}
	log.Info("Public image URL generated", zap.String("url", imageURL))

	// 4. Вернуть URL
	return GenerateImageResult{ImageURL: imageURL, Error: nil}
}

// callSanaAPI - вызывает SANA API.
func (s *imageServiceImpl) callSanaAPI(ctx context.Context, prompt string, ratio string) ([]byte, error) {
	log := s.logger.With(zap.String("api_url", s.sanaConfig.BaseURL))

	// Формируем тело запроса
	reqPayload := SanaAPIRequest{
		Prompt: prompt,
		Ratio:  ratio,
	}
	reqBodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		log.Error("Failed to marshal SANA API request payload", zap.Error(err))
		return nil, fmt.Errorf("failed to marshal request payload: %w", err)
	}

	// Создаем HTTP запрос
	endpointURL := s.sanaConfig.BaseURL + "/generate"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(reqBodyBytes))
	if err != nil {
		log.Error("Failed to create SANA API request", zap.String("url", endpointURL), zap.Error(err))
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "image/*")

	log.Debug("Sending request to SANA API", zap.String("url", endpointURL))
	resp, err := s.sanaClient.Do(req)
	if err != nil {
		log.Error("Failed to execute SANA API request", zap.Error(err))
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		log.Error("SANA API returned non-OK status",
			zap.Int("status_code", resp.StatusCode),
			zap.ByteString("response_body", bodyBytes),
		)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	if readErr != nil {
		log.Error("Failed to read SANA API response body", zap.Error(readErr))
		return nil, fmt.Errorf("failed to read response body: %w", readErr)
	}

	log.Debug("SANA API call successful")
	return bodyBytes, nil
}
