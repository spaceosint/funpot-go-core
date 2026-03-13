package events

type Option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type UserVote struct {
	OptionID string `json:"optionId"`
}

type LiveEvent struct {
	ID          string         `json:"id"`
	GameID      *string        `json:"gameId"`
	StreamerID  string         `json:"-"`
	Title       string         `json:"title"`
	Options     []Option       `json:"options"`
	ClosesAt    string         `json:"closesAt"`
	Totals      map[string]int `json:"totals"`
	UserVote    *UserVote      `json:"userVote,omitempty"`
	CostPerVote int            `json:"costPerVote"`
}
