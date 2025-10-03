package users

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"
	"time"
)

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

// GetByTelegramID fetches a user profile without mutating it.
func (s *Service) GetByTelegramID(ctx context.Context, telegramID int64) (Profile, error) {
	return s.repo.GetByTelegramID(ctx, telegramID)
}
