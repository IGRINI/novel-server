package interfaces

import (
	"context"
)

// ImageReferenceRepository defines the interface for managing image references and their URLs.
//
//go:generate mockery --name ImageReferenceRepository --output ./mocks --outpkg mocks --case=underscore
type ImageReferenceRepository interface {
	// CreateOrUpdateImageReference creates a new image reference or updates the URL of an existing one.
	// CreateOrUpdateImageReference(ctx context.Context, reference string, imageURL string, userID *uuid.UUID) error // <<< Временно комментируем

	// SaveOrUpdateImageReference saves or updates the URL for the given reference.
	SaveOrUpdateImageReference(ctx context.Context, reference string, imageURL string) error

	// GetImageURLByReference retrieves the image URL associated with a specific reference string.
	GetImageURLByReference(ctx context.Context, reference string) (string, error)

	// DeleteImageReference deletes an image reference.
	// DeleteImageReference(ctx context.Context, reference string) error // <<< Временно комментируем

	// GetImageURLsByReferences retrieves multiple image URLs based on a list of reference strings.
	// Returns a map where keys are the references and values are the URLs.
	// References for which no URL is found are omitted from the map.
	GetImageURLsByReferences(ctx context.Context, refs []string) (map[string]string, error)
}
