package interfaces

import (
	"context"
)

// ImageReferenceRepository defines methods for interacting with stored image references and their URLs.
type ImageReferenceRepository interface {
	// GetImageURLByReference retrieves the stored image URL for a given reference string.
	// It should return ErrNotFound if the reference is not found.
	GetImageURLByReference(ctx context.Context, reference string) (string, error)

	// SaveOrUpdateImageReference saves a new image reference and its URL, or updates the URL
	// if the reference already exists.
	SaveOrUpdateImageReference(ctx context.Context, reference string, imageURL string) error
}
