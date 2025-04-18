package dto

import (
	"errors"
	"strings"
)

// RegisterDeviceTokenInput определяет данные для регистрации токена устройства.
type RegisterDeviceTokenInput struct {
	Token    string `json:"token" binding:"required"`
	Platform string `json:"platform" binding:"required,oneof=android ios"` // Платформа должна быть 'android' или 'ios'
}

// Validate проверяет корректность данных для регистрации.
func (i *RegisterDeviceTokenInput) Validate() error {
	i.Platform = strings.ToLower(strings.TrimSpace(i.Platform))
	i.Token = strings.TrimSpace(i.Token)

	if i.Token == "" {
		return errors.New("device token cannot be empty")
	}
	if i.Platform != "android" && i.Platform != "ios" {
		return errors.New("platform must be 'android' or 'ios'")
	}
	return nil
}

// UnregisterDeviceTokenInput определяет данные для удаления токена устройства.
type UnregisterDeviceTokenInput struct {
	Token string `json:"token" binding:"required"`
}

// Validate проверяет корректность данных для удаления.
func (i *UnregisterDeviceTokenInput) Validate() error {
	i.Token = strings.TrimSpace(i.Token)
	if i.Token == "" {
		return errors.New("device token cannot be empty")
	}
	return nil
}
