package users

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrAlreadyExists = errors.New("user already exists")
var ErrBanUntilBeforeNow = errors.New("banUntil must be in the future")

// Service orchestrates business logic around user profiles.
type Service struct {
	repo Repository
	now  func() time.Time
}

// NewService constructs a user service backed by the provided repository.
func NewService(repo Repository) *Service {
	return &Service{
		repo: repo,
		now:  time.Now,
	}
}

// WithNow overrides the clock for testing.
func (s *Service) WithNow(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

// SyncTelegramProfile ensures a user exists for the given Telegram profile.
func (s *Service) SyncTelegramProfile(ctx context.Context, profile TelegramProfile) (Profile, error) {
	existing, err := s.repo.GetByTelegramID(ctx, profile.ID)
	if err != nil {
		if err == ErrNotFound {
			created := s.newProfile(profile)
			if err := s.repo.Create(ctx, created); err != nil {
				return Profile{}, err
			}
			return created, nil
		}
		return Profile{}, err
	}

	updated := existing
	updated.Username = profile.Username
	updated.FirstName = profile.FirstName
	updated.LastName = profile.LastName
	updated.LanguageCode = profile.LanguageCode
	updated.UpdatedAt = s.now().UTC()

	if err := s.repo.Update(ctx, updated); err != nil {
		return Profile{}, err
	}
	return updated, nil
}

// Create creates a user profile from explicit profile data.
func (s *Service) Create(ctx context.Context, profile TelegramProfile) (Profile, error) {
	if _, err := s.repo.GetByTelegramID(ctx, profile.ID); err == nil {
		return Profile{}, ErrAlreadyExists
	} else if !errors.Is(err, ErrNotFound) {
		return Profile{}, err
	}

	created := s.newProfile(profile)
	if err := s.repo.Create(ctx, created); err != nil {
		return Profile{}, err
	}
	return created, nil
}

// UpdateByID updates profile fields by user id.
func (s *Service) UpdateByID(ctx context.Context, id string, profile TelegramProfile) (Profile, error) {
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	updated := existing
	updated.Username = profile.Username
	updated.FirstName = profile.FirstName
	updated.LastName = profile.LastName
	updated.LanguageCode = profile.LanguageCode
	updated.UpdatedAt = s.now().UTC()

	if err := s.repo.Update(ctx, updated); err != nil {
		return Profile{}, err
	}
	return updated, nil
}

// BanByID blocks a user either temporarily (banUntil > zero) or permanently (zero).
func (s *Service) BanByID(ctx context.Context, id, reason string, banUntil time.Time) (Profile, error) {
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	now := s.now().UTC()
	if !banUntil.IsZero() && !banUntil.UTC().After(now) {
		return Profile{}, ErrBanUntilBeforeNow
	}

	existing.IsBanned = true
	existing.BanReason = strings.TrimSpace(reason)
	existing.BannedAt = now
	existing.BannedUntil = banUntil.UTC()
	existing.UpdatedAt = now
	if err := s.repo.Update(ctx, existing); err != nil {
		return Profile{}, err
	}
	return existing, nil
}

// UnbanByID restores user access.
func (s *Service) UnbanByID(ctx context.Context, id string) (Profile, error) {
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	existing.IsBanned = false
	existing.BanReason = ""
	existing.BannedAt = time.Time{}
	existing.BannedUntil = time.Time{}
	existing.UpdatedAt = s.now().UTC()
	if err := s.repo.Update(ctx, existing); err != nil {
		return Profile{}, err
	}
	return existing, nil
}

func (s *Service) newProfile(profile TelegramProfile) Profile {
	now := s.now().UTC()
	referralCode := generateReferralCode(profile.ID)
	return Profile{
		ID:           fmt.Sprintf("tg_%d", profile.ID),
		TelegramID:   profile.ID,
		Username:     profile.Username,
		FirstName:    profile.FirstName,
		LastName:     profile.LastName,
		LanguageCode: profile.LanguageCode,
		ReferralCode: referralCode,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func generateReferralCode(telegramID int64) string {
	hasher := sha256.New()
	hasher.Write([]byte(fmt.Sprintf("funpot:%d", telegramID)))
	sum := hasher.Sum(nil)
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum)
	encoded = strings.ToUpper(encoded)
	if len(encoded) > 10 {
		return encoded[:10]
	}
	return encoded
}

// GetByID fetches a user profile by internal ID.
func (s *Service) GetByID(ctx context.Context, id string) (Profile, error) {
	return s.repo.GetByID(ctx, id)
}

// GetByTelegramID fetches a user profile without mutating it.
func (s *Service) GetByTelegramID(ctx context.Context, telegramID int64) (Profile, error) {
	return s.repo.GetByTelegramID(ctx, telegramID)
}

// List fetches paginated users with optional search query.
func (s *Service) List(ctx context.Context, query string, page, pageSize int) ([]Profile, int, error) {
	return s.repo.List(ctx, query, page, pageSize)
}

// DeleteByID removes a user by internal ID.
func (s *Service) DeleteByID(ctx context.Context, id string) error {
	return s.repo.DeleteByID(ctx, id)
}
