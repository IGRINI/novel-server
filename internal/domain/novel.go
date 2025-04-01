package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// NovelDraft представляет сохраненную конфигурацию новеллы перед подтверждением
type NovelDraft struct {
	DraftID   uuid.UUID   `json:"draft_id"`
	UserID    string      `json:"user_id"`
	Config    NovelConfig `json:"config"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// NovelDraftRepository определяет интерфейс для хранилища черновиков новелл
type NovelDraftRepository interface {
	// SaveDraft сохраняет новый черновик
	SaveDraft(ctx context.Context, userID string, draftID uuid.UUID, configJSON []byte) error
	// GetDraftConfigJSON получает сериализованный конфиг черновика по ID
	GetDraftConfigJSON(ctx context.Context, userID string, draftID uuid.UUID) ([]byte, error)
	// UpdateDraftConfigJSON обновляет конфиг существующего черновика
	UpdateDraftConfigJSON(ctx context.Context, userID string, draftID uuid.UUID, configJSON []byte) error
	// DeleteDraft удаляет черновик
	DeleteDraft(ctx context.Context, userID string, draftID uuid.UUID) error
}
