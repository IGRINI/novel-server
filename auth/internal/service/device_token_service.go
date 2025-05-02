package service

import (
	"context"
	"fmt"
	"novel-server/auth/internal/domain/dto"
	interfaces "novel-server/shared/interfaces"
	"novel-server/shared/models"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Убедимся, что deviceTokenService реализует интерфейс
var _ interfaces.DeviceTokenService = (*deviceTokenService)(nil)

type deviceTokenService struct {
	deviceTokenRepo interfaces.UserDeviceTokenRepository
	logger          *zap.Logger
}

func NewDeviceTokenService(
	deviceTokenRepo interfaces.UserDeviceTokenRepository,
	logger *zap.Logger,
) interfaces.DeviceTokenService { // Возвращаем интерфейс
	return &deviceTokenService{
		deviceTokenRepo: deviceTokenRepo,
		logger:          logger.Named("device_token_service"),
	}
}

// RegisterDeviceToken регистрирует новый токен устройства для пользователя.
// data должен быть типа dto.RegisterDeviceTokenInput.
func (s *deviceTokenService) RegisterDeviceToken(ctx context.Context, userID uuid.UUID, data interface{}) error {
	input, ok := data.(dto.RegisterDeviceTokenInput)
	if !ok {
		return fmt.Errorf("invalid data type for RegisterDeviceToken, expected dto.RegisterDeviceTokenInput")
	}

	if err := input.Validate(); err != nil {
		return fmt.Errorf("invalid input data: %w", err) // Можно вернуть кастомную ошибку для валидации
	}

	err := s.deviceTokenRepo.SaveDeviceToken(ctx, userID, input.Token, input.Platform)
	if err != nil {
		// Логирование уже внутри репозитория
		return fmt.Errorf("failed to save device token: %w", err)
	}

	s.logger.Info("Device token registered successfully",
		zap.String("userID", userID.String()),
		zap.String("platform", input.Platform),
	)
	return nil
}

// UnregisterDeviceToken удаляет токен устройства.
// data должен быть типа dto.UnregisterDeviceTokenInput.
func (s *deviceTokenService) UnregisterDeviceToken(ctx context.Context, data interface{}) error {
	input, ok := data.(dto.UnregisterDeviceTokenInput)
	if !ok {
		return fmt.Errorf("invalid data type for UnregisterDeviceToken, expected dto.UnregisterDeviceTokenInput")
	}

	if err := input.Validate(); err != nil {
		return fmt.Errorf("invalid input data: %w", err) // Можно вернуть кастомную ошибку для валидации
	}

	err := s.deviceTokenRepo.DeleteDeviceToken(ctx, input.Token)
	if err != nil {
		// Логирование уже внутри репозитория
		return fmt.Errorf("failed to delete device token: %w", err)
	}

	s.logger.Info("Device token unregistered successfully",
		zap.String("token", input.Token),
	)
	return nil
}

// GetDeviceTokensForUser возвращает все активные токены для пользователя.
func (s *deviceTokenService) GetDeviceTokensForUser(ctx context.Context, userID uuid.UUID) ([]models.DeviceTokenInfo, error) {
	s.logger.Debug("Fetching device tokens for user", zap.String("userID", userID.String()))
	tokens, err := s.deviceTokenRepo.GetDeviceTokensForUser(ctx, userID)
	if err != nil {
		// Логирование уже внутри репозитория, если была ошибка DB.
		// Репозиторий возвращает nil error, если токенов просто нет.
		s.logger.Error("Failed to get device tokens from repository", zap.String("userID", userID.String()), zap.Error(err))
		return nil, fmt.Errorf("failed to get device tokens: %w", err)
	}
	s.logger.Debug("Successfully fetched device tokens", zap.String("userID", userID.String()), zap.Int("count", len(tokens)))
	return tokens, nil
}
