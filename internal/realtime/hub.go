package realtime

import (
	"sync"
	"time"
)

type Envelope struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

type VoteFeedItem struct {
	UserID             string  `json:"userId"`
	Nickname           string  `json:"nickname"`
	OptionID           string  `json:"optionId"`
	AmountINT          int64   `json:"amountINT"`
	OptionPoolSharePct float64 `json:"optionPoolSharePct"`
	Coefficient        float64 `json:"coefficient"`
	PotentialWinINT    int64   `json:"potentialWinINT"`
	CreatedAt          string  `json:"createdAt"`
}

type VoteFeedPayload struct {
	EventID    string         `json:"eventId"`
	Items      []VoteFeedItem `json:"items"`
	SnapshotAt string         `json:"snapshotAt"`
}

type EventOptionMarket struct {
	PoolINT     int64   `json:"poolINT"`
	SharePct    float64 `json:"sharePct"`
	Coefficient float64 `json:"coefficient"`
}

type EventUpdatedPayload struct {
	EventID          string                       `json:"eventId"`
	Totals           map[string]int64             `json:"totals"`
	TotalContributed int64                        `json:"totalContributed"`
	PlatformFeeINT   int64                        `json:"platformFeeINT"`
	DistributableINT int64                        `json:"distributableINT"`
	Options          map[string]EventOptionMarket `json:"options"`
	ClosesAt         string                       `json:"closesAt"`
}

type ScenarioStepPayload struct {
	StreamerID string `json:"streamerId"`
	ScenarioID string `json:"scenarioId"`
	StepID     string `json:"stepId"`
	StepName   string `json:"stepName"`
	UpdatedAt  string `json:"updatedAt"`
}

type Hub struct {
	mu        sync.RWMutex
	streamers map[string]map[chan Envelope]struct{}
	users     map[string]map[chan Envelope]struct{}
}

func NewHub() *Hub {
	return &Hub{
		streamers: map[string]map[chan Envelope]struct{}{},
		users:     map[string]map[chan Envelope]struct{}{},
	}
}

func (h *Hub) SubscribeStreamer(streamerID string, buffer int) (<-chan Envelope, func()) {
	if buffer <= 0 {
		buffer = 32
	}
	ch := make(chan Envelope, buffer)
	h.mu.Lock()
	if _, ok := h.streamers[streamerID]; !ok {
		h.streamers[streamerID] = map[chan Envelope]struct{}{}
	}
	h.streamers[streamerID][ch] = struct{}{}
	h.mu.Unlock()
	unsub := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if set, ok := h.streamers[streamerID]; ok {
			if _, ok := set[ch]; ok {
				delete(set, ch)
				close(ch)
			}
			if len(set) == 0 {
				delete(h.streamers, streamerID)
			}
		}
	}
	return ch, unsub
}

func (h *Hub) PublishToStreamer(streamerID string, env Envelope) {
	h.mu.RLock()
	set := h.streamers[streamerID]
	h.mu.RUnlock()
	for ch := range set {
		select {
		case ch <- env:
		default:
		}
	}
}

func (h *Hub) SubscribeUser(userID string, buffer int) (<-chan Envelope, func()) {
	if buffer <= 0 {
		buffer = 32
	}
	ch := make(chan Envelope, buffer)
	h.mu.Lock()
	if _, ok := h.users[userID]; !ok {
		h.users[userID] = map[chan Envelope]struct{}{}
	}
	h.users[userID][ch] = struct{}{}
	h.mu.Unlock()
	unsub := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if set, ok := h.users[userID]; ok {
			if _, ok := set[ch]; ok {
				delete(set, ch)
				close(ch)
			}
			if len(set) == 0 {
				delete(h.users, userID)
			}
		}
	}
	return ch, unsub
}

func (h *Hub) PublishToUser(userID string, env Envelope) {
	h.mu.RLock()
	set := h.users[userID]
	h.mu.RUnlock()
	for ch := range set {
		select {
		case ch <- env:
		default:
		}
	}
}

func NowRFC3339() string { return time.Now().UTC().Format(time.RFC3339Nano) }
