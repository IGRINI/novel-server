package models

// DeviceTokenInfo содержит информацию о токене устройства.
type DeviceTokenInfo struct {
	Token    string `json:"token"`    // Сам токен (FCM, APNS)
	Platform string `json:"platform"` // Платформа ('android', 'ios')
}
