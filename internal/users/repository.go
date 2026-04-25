package users

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a user is not present in the repository.
var ErrNotFound = errors.New("user not found")

// Repository abstracts user persistence operations.
type Repository interface {
	GetByID(ctx context.Context, id string) (Profile, error)
	GetByTelegramID(ctx context.Context, telegramID int64) (Profile, error)
	List(ctx context.Context, query string, page, pageSize int) ([]Profile, int, error)
	Create(ctx context.Context, profile Profile) error
	Update(ctx context.Context, profile Profile) error
	DeleteByID(ctx context.Context, id string) error
}
