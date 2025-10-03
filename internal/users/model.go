package users

import "time"

// Profile represents the public portion of a FunPot user record.
type Profile struct {
	ID           string    `json:"id"`
	TelegramID   int64     `json:"telegramId"`
	Username     string    `json:"username"`
	FirstName    string    `json:"firstName"`
	LastName     string    `json:"lastName"`
	LanguageCode string    `json:"languageCode"`
	ReferralCode string    `json:"referralCode"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// TelegramProfile carries the subset of Telegram fields required for syncing users.
type TelegramProfile struct {
	ID           int64
	Username     string
	FirstName    string
	LastName     string
	LanguageCode string
}
