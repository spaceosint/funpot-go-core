package users

import "time"

// Profile represents the public portion of a FunPot user record.
type Profile struct {
	ID           string    `json:"id"`
	TelegramID   int64     `json:"telegramId"`
	Username     string    `json:"username"`
	Nickname     string    `json:"nickname"`
	FirstName    string    `json:"firstName"`
	LastName     string    `json:"lastName"`
	LanguageCode string    `json:"languageCode"`
	ReferralCode string    `json:"referralCode"`
	IsBanned     bool      `json:"isBanned"`
	BanReason    string    `json:"banReason,omitempty"`
	BannedAt     time.Time `json:"bannedAt,omitempty"`
	BannedUntil  time.Time `json:"bannedUntil,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// IsAccessBlocked reports whether user access should be denied at provided timestamp.
func (p Profile) IsAccessBlocked(now time.Time) bool {
	if !p.IsBanned {
		return false
	}
	if p.BannedUntil.IsZero() {
		return true
	}
	return now.UTC().Before(p.BannedUntil.UTC())
}

// TelegramProfile carries the subset of Telegram fields required for syncing users.
type TelegramProfile struct {
	ID           int64
	Username     string
	FirstName    string
	LastName     string
	LanguageCode string
}
