package games

import "time"

const (
	StatusDraft    = "draft"
	StatusActive   = "active"
	StatusArchived = "archived"
)

var supportedStatuses = map[string]struct{}{
	StatusDraft:    {},
	StatusActive:   {},
	StatusArchived: {},
}

// Game contains configurable game rules managed by admins.
type Game struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Rules       []string  `json:"rules"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// UpsertRequest describes the payload for creating and updating games.
type UpsertRequest struct {
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Rules       []string `json:"rules"`
	Status      string   `json:"status"`
}

func IsSupportedStatus(value string) bool {
	_, ok := supportedStatuses[value]
	return ok
}
