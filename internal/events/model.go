package events

import "time"

type Option struct {
	ID    string            `json:"id"`
	Title map[string]string `json:"title"`
}

type UserVote struct {
	OptionID    string `json:"optionId"`
	TotalAmount int64  `json:"totalAmount"`
}

type OptionMarket struct {
	PoolINT     int64   `json:"poolINT"`
	SharePct    float64 `json:"sharePct"`
	Coefficient float64 `json:"coefficient"`
}

type LiveEventMarket struct {
	Options map[string]OptionMarket `json:"options"`
}

type UserEventHistoryItem struct {
	EventID          string            `json:"eventId"`
	StreamerID       string            `json:"streamerId"`
	ScenarioID       string            `json:"scenarioId"`
	TransitionID     string            `json:"transitionId,omitempty"`
	TerminalID       string            `json:"terminalId"`
	Title            map[string]string `json:"title"`
	DefaultLanguage  string            `json:"defaultLanguage"`
	OptionID         string            `json:"optionId"`
	AmountINT        int64             `json:"amountINT"`
	CreatedAt        string            `json:"createdAt"`
	TotalContributed int64             `json:"totalContributed"`
	PlatformFeeINT   int64             `json:"platformFeeINT"`
	DistributableINT int64             `json:"distributableINT"`
	OptionPoolINT    int64             `json:"optionPoolINT"`
	Coefficient      float64           `json:"coefficient"`
	PotentialWinINT  int64             `json:"potentialWinINT"`
	WinAmountINT     *int64            `json:"winAmountINT,omitempty"`
	ResultStatus     string            `json:"resultStatus"`
}

type LiveEvent struct {
	ID               string            `json:"id"`
	TemplateID       string            `json:"templateId"`
	GameID           *string           `json:"gameId"`
	StreamerID       string            `json:"-"`
	ScenarioID       string            `json:"scenarioId"`
	TransitionID     string            `json:"transitionId,omitempty"`
	TerminalID       string            `json:"terminalId"`
	Title            map[string]string `json:"title"`
	DefaultLanguage  string            `json:"defaultLanguage"`
	Options          []Option          `json:"options"`
	ClosesAt         string            `json:"closesAt"`
	CreatedAt        string            `json:"createdAt"`
	Status           string            `json:"status"`
	Totals           map[string]int64  `json:"totals"`
	TotalContributed int64             `json:"totalContributed"`
	PlatformFeeINT   int64             `json:"platformFeeINT"`
	DistributableINT int64             `json:"distributableINT"`
	UserVote         *UserVote         `json:"userVote,omitempty"`
}

type CreateLiveEventRequest struct {
	StreamerID      string
	ScenarioID      string
	TransitionID    string
	TerminalID      string
	Title           map[string]string
	DefaultLanguage string
	Options         []Option
	Duration        time.Duration
}

type VoteRequest struct {
	EventID        string
	StreamerID     string
	UserID         string
	OptionID       string
	Amount         int64
	IdempotencyKey string
	WalletLedgerID string
}
