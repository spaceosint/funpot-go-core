package users

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a user is not present in the repository.
var ErrNotFound = errors.New("user not found")

// Repository abstracts user persistence operations.
type Repository interface {
	GetByTelegramID(ctx context.Context, telegramID int64) (Profile, error)
	Create(ctx context.Context, profile Profile) error
	Update(ctx context.Context, profile Profile) error
}
