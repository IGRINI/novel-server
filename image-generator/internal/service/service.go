package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.uber.org/zap"

	"novel-server/image-generator/internal/config"
	sharedMessaging "novel-server/shared/messaging"
)

// ErrImageGenerationFailed - ошибка при генерации изображения SANA сервером.
var ErrImageGenerationFailed = errors.New("image generation failed")

// ErrImageUploadFailed - ошибка при загрузке изображения в хранилище.
var ErrImageUploadFailed = errors.New("image upload failed")

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
	logger        *zap.Logger
	sanaConfig    config.SanaServerConfig
	storageConfig config.StorageConfig
	sanaClient    *http.Client
	minioClient   *minio.Client
}

// NewImageGenerationService создает новый экземпляр imageServiceImpl.
// Возвращает ошибку, если не удалось подключиться к Minio.
func NewImageGenerationService(
	logger *zap.Logger,
	sanaConfig config.SanaServerConfig,
	storageConfig config.StorageConfig,
) (ImageGenerationService, error) {
	// Инициализация Minio клиента
	minioClient, err := minio.New(storageConfig.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(storageConfig.AccessKeyID, storageConfig.SecretAccessKey, ""),
		Secure: storageConfig.UseSSL,
	})
	if err != nil {
		logger.Error("Failed to initialize Minio client", zap.Error(err))
		return nil, fmt.Errorf("failed to initialize Minio client: %w", err)
	}

	// Проверка существования бакета
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	found, err := minioClient.BucketExists(ctx, storageConfig.BucketName)
	if err != nil {
		logger.Error("Failed to check if Minio bucket exists", zap.String("bucket", storageConfig.BucketName), zap.Error(err))
		return nil, fmt.Errorf("failed to check Minio bucket %s: %w", storageConfig.BucketName, err)
	}
	if !found {
		logger.Error("Minio bucket does not exist", zap.String("bucket", storageConfig.BucketName))
		return nil, fmt.Errorf("minio bucket '%s' not found", storageConfig.BucketName)
	}
	logger.Info("Minio client initialized and bucket found", zap.String("bucket", storageConfig.BucketName))

	return &imageServiceImpl{
		logger:        logger,
		sanaConfig:    sanaConfig,
		storageConfig: storageConfig,
		sanaClient: &http.Client{
			Timeout: time.Duration(sanaConfig.Timeout) * time.Second,
		},
		minioClient: minioClient,
	}, nil
}

// SanaAPIRequest - структура запроса к SANA API (предполагаемая)
type SanaAPIRequest struct {
	Prompt         string `json:"prompt"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
	// TODO: Добавить другие параметры, если API их поддерживает (seed, steps, etc.)
}

// GenerateAndStoreImage - реализует основную логику.
func (s *imageServiceImpl) GenerateAndStoreImage(ctx context.Context, taskPayload sharedMessaging.CharacterImageTaskPayload) GenerateImageResult {
	log := s.logger.With(
		zap.String("user_id", taskPayload.UserID),
		zap.String("character_id", taskPayload.CharacterID),
		zap.String("image_reference", taskPayload.ImageReference),
		zap.String("prompt_hash", fmt.Sprintf("%x", uuid.NewSHA1(uuid.NameSpaceDNS, []byte(taskPayload.Prompt)))),
		zap.String("task_id", taskPayload.TaskID),
	)
	log.Info("Generating character image...")

	// 1. Вызов SANA Sprint API
	imageData, err := s.callSanaAPI(ctx, taskPayload.Prompt, taskPayload.NegativePrompt)
	if err != nil {
		log.Error("SANA API call failed", zap.Error(err))
		return GenerateImageResult{Error: fmt.Errorf("%w: %v", ErrImageGenerationFailed, err)}
	}
	if len(imageData) == 0 {
		log.Error("SANA API returned empty image data")
		return GenerateImageResult{Error: fmt.Errorf("%w: API returned empty data", ErrImageGenerationFailed)}
	}
	log.Info("Image data received from SANA", zap.Int("size_bytes", len(imageData)))

	// 2. Загрузка изображения в S3/Minio
	// Генерируем уникальное имя объекта, используя userID, characterID и taskID для уникальности
	objectName := fmt.Sprintf("users/%s/characters/%s/%s.jpg", taskPayload.UserID, taskPayload.CharacterID, taskPayload.TaskID)
	contentType := "image/jpeg" // Предполагаем JPEG

	imageURL, err := s.uploadToStorage(ctx, imageData, objectName, contentType)
	if err != nil {
		log.Error("Image upload to storage failed", zap.Error(err))
		return GenerateImageResult{Error: fmt.Errorf("%w: %v", ErrImageUploadFailed, err)}
	}
	log.Info("Image uploaded to storage", zap.String("url", imageURL))

	// 3. Вернуть URL
	return GenerateImageResult{ImageURL: imageURL, Error: nil}
}

// callSanaAPI - вызывает SANA API.
func (s *imageServiceImpl) callSanaAPI(ctx context.Context, prompt, negativePrompt string) ([]byte, error) {
	log := s.logger.With(zap.String("api_url", s.sanaConfig.BaseURL))

	// Формируем тело запроса (основываясь на предположениях)
	reqPayload := SanaAPIRequest{
		Prompt:         prompt,
		NegativePrompt: negativePrompt,
	}
	reqBodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		log.Error("Failed to marshal SANA API request payload", zap.Error(err))
		return nil, fmt.Errorf("failed to marshal request payload: %w", err)
	}

	// Создаем HTTP запрос
	// Предполагаем POST /generate
	endpointURL := s.sanaConfig.BaseURL + "/generate"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(reqBodyBytes))
	if err != nil {
		log.Error("Failed to create SANA API request", zap.String("url", endpointURL), zap.Error(err))
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "image/*") // Указываем, что ожидаем изображение

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
			zap.ByteString("response_body", bodyBytes), // Логируем тело ответа при ошибке
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

// uploadToStorage - загружает данные в S3/Minio.
func (s *imageServiceImpl) uploadToStorage(ctx context.Context, data []byte, objectName, contentType string) (string, error) {
	log := s.logger.With(
		zap.String("bucket", s.storageConfig.BucketName),
		zap.String("object_name", objectName),
		zap.String("content_type", contentType),
		zap.Int("size_bytes", len(data)),
	)
	log.Debug("Uploading object to Minio...")

	_, err := s.minioClient.PutObject(ctx, s.storageConfig.BucketName, objectName, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		log.Error("Failed to put object to Minio", zap.Error(err))
		return "", fmt.Errorf("minio PutObject failed: %w", err)
	}

	// Формируем URL (зависит от настроек Minio/S3 и прокси)
	// Самый простой вариант - конкатенация endpoint + bucket + objectName
	// ВАЖНО: Убедитесь, что бакет настроен на публичное чтение или используйте presigned URL.
	imageURL := fmt.Sprintf("%s/%s/%s", s.storageConfig.Endpoint, s.storageConfig.BucketName, objectName)
	// Убираем двойные слеши, если Endpoint содержит / в конце
	imageURL = strings.Replace(imageURL, "//", "/", -1)
	if !strings.HasPrefix(imageURL, "https://") && !strings.HasPrefix(imageURL, "http://") {
		if s.storageConfig.UseSSL {
			imageURL = "https://" + imageURL
		} else {
			imageURL = "http://" + imageURL
		}
	}

	log.Info("Object uploaded successfully")
	return imageURL, nil
}
