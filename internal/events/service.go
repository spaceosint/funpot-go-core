package events

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidEvent  = errors.New("event payload is invalid")
	ErrEventNotFound = errors.New("event not found")
	ErrEventClosed   = errors.New("event is closed")
	ErrInvalidVote   = errors.New("vote payload is invalid")
	ErrAlreadyActive = errors.New("active event already exists for template")
)

type voteRecord struct {
	OptionID string
	Amount   int64
}

type liveEventState struct {
	event          LiveEvent
	processedVotes map[string]voteRecord
	userVotes      map[string]voteRecord
}

type Service struct {
	mu                 sync.RWMutex
	db                 *sql.DB
	items              map[string]*liveEventState
	historyByUser      map[string][]UserEventHistoryItem
	votePlatformFeeBPS int64
	nicknameChangeCost int64
	weeklyRewardByDay  [7]int64
	weeklyClaimsByUser map[string]weeklyClaimState
	settingsStore      SettingsStore
}

type SettingsStore interface {
	Load(ctx context.Context) (Settings, bool, error)
	Save(ctx context.Context, settings Settings) error
}

type weeklyClaimState struct {
	LastClaimAt time.Time
	StreakDay   int
}

type Settings struct {
	VotePlatformFeePercent float64  `json:"votePlatformFeePercent"`
	NicknameChangeCostINT  int64    `json:"nicknameChangeCostINT"`
	WeeklyRewardByDayINT   [7]int64 `json:"weeklyRewardByDayINT"`
}

type WeeklyRewardClaim struct {
	ClaimedDay      int    `json:"claimedDay"`
	RewardAmountINT int64  `json:"rewardAmountINT"`
	ClaimedAt       string `json:"claimedAt"`
	NextClaimAt     string `json:"nextClaimAt"`
	StreakDay       int    `json:"streakDay"`
	IdempotencyKey  string `json:"idempotencyKey"`
}

func NewService(seed []LiveEvent) *Service {
	items := make(map[string]*liveEventState, len(seed))
	for _, item := range seed {
		copyItem := item
		if copyItem.Totals == nil {
			copyItem.Totals = map[string]int64{}
		}
		items[copyItem.ID] = &liveEventState{
			event:          copyItem,
			processedVotes: map[string]voteRecord{},
			userVotes:      map[string]voteRecord{},
		}
	}
	return &Service{
		items:              items,
		historyByUser:      map[string][]UserEventHistoryItem{},
		weeklyClaimsByUser: map[string]weeklyClaimState{},
	}
}

func NewPostgresService(db *sql.DB, seed []LiveEvent) *Service {
	svc := NewService(seed)
	svc.db = db
	return svc
}

func (s *Service) CreateLiveEvent(_ context.Context, req CreateLiveEventRequest) (LiveEvent, error) {
	if strings.TrimSpace(req.StreamerID) == "" || strings.TrimSpace(req.ScenarioID) == "" || strings.TrimSpace(req.TerminalID) == "" {
		return LiveEvent{}, ErrInvalidEvent
	}
	if strings.TrimSpace(req.DefaultLanguage) == "" || strings.TrimSpace(req.Title[req.DefaultLanguage]) == "" {
		return LiveEvent{}, ErrInvalidEvent
	}
	if len(req.Options) == 0 {
		return LiveEvent{}, ErrInvalidEvent
	}
	now := time.Now().UTC()
	if req.Duration <= 0 {
		req.Duration = 5 * time.Minute
	}
	templateID := strings.TrimSpace(req.StreamerID) + ":" + strings.TrimSpace(req.TerminalID)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		if item.event.StreamerID != strings.TrimSpace(req.StreamerID) || item.event.TemplateID != templateID {
			continue
		}
		if isOpen(item.event, now) {
			return item.event, ErrAlreadyActive
		}
	}
	event := LiveEvent{
		ID:              uuid.NewString(),
		TemplateID:      templateID,
		StreamerID:      strings.TrimSpace(req.StreamerID),
		ScenarioID:      strings.TrimSpace(req.ScenarioID),
		TransitionID:    strings.TrimSpace(req.TransitionID),
		TerminalID:      strings.TrimSpace(req.TerminalID),
		Title:           req.Title,
		DefaultLanguage: strings.TrimSpace(req.DefaultLanguage),
		Options:         append([]Option(nil), req.Options...),
		CreatedAt:       now.Format(time.RFC3339Nano),
		ClosesAt:        now.Add(req.Duration).Format(time.RFC3339Nano),
		Status:          "open",
		Totals:          map[string]int64{},
	}
	for _, option := range event.Options {
		event.Totals[strings.TrimSpace(option.ID)] = 0
	}
	state := &liveEventState{
		event:          event,
		processedVotes: map[string]voteRecord{},
		userVotes:      map[string]voteRecord{},
	}
	s.items[event.ID] = state
	return event, nil
}

func (s *Service) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Settings{
		VotePlatformFeePercent: float64(s.votePlatformFeeBPS) / 100.0,
		NicknameChangeCostINT:  s.nicknameChangeCost,
		WeeklyRewardByDayINT:   s.weeklyRewardByDay,
	}
}

func (s *Service) ConfigureSettingsPersistence(ctx context.Context, store SettingsStore) error {
	s.mu.Lock()
	s.settingsStore = store
	s.mu.Unlock()
	if store == nil {
		return nil
	}

	loaded, ok, err := store.Load(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if loaded.VotePlatformFeePercent < 0 || loaded.VotePlatformFeePercent > 100 {
		return ErrInvalidVote
	}
	if loaded.NicknameChangeCostINT < 0 {
		return ErrInvalidVote
	}
	for _, amount := range loaded.WeeklyRewardByDayINT {
		if amount < 0 {
			return ErrInvalidVote
		}
	}

	feeBPS := int64(math.Round(loaded.VotePlatformFeePercent * 100))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.votePlatformFeeBPS = feeBPS
	s.nicknameChangeCost = loaded.NicknameChangeCostINT
	s.weeklyRewardByDay = loaded.WeeklyRewardByDayINT
	return nil
}

func (s *Service) UpdateSettings(settings Settings) (Settings, error) {
	if settings.VotePlatformFeePercent < 0 || settings.VotePlatformFeePercent > 100 {
		return Settings{}, ErrInvalidVote
	}
	if settings.NicknameChangeCostINT < 0 {
		return Settings{}, ErrInvalidVote
	}
	for _, amount := range settings.WeeklyRewardByDayINT {
		if amount < 0 {
			return Settings{}, ErrInvalidVote
		}
	}
	feeBPS := int64(math.Round(settings.VotePlatformFeePercent * 100))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.votePlatformFeeBPS = feeBPS
	s.nicknameChangeCost = settings.NicknameChangeCostINT
	s.weeklyRewardByDay = settings.WeeklyRewardByDayINT
	store := s.settingsStore
	current := Settings{
		VotePlatformFeePercent: float64(s.votePlatformFeeBPS) / 100.0,
		NicknameChangeCostINT:  s.nicknameChangeCost,
		WeeklyRewardByDayINT:   s.weeklyRewardByDay,
	}
	if store != nil {
		if err := store.Save(context.Background(), current); err != nil {
			return Settings{}, err
		}
	}
	return current, nil
}

func (s *Service) ClaimWeeklyReward(userID string, now time.Time) (WeeklyRewardClaim, error) {
	if s.db != nil {
		return s.claimWeeklyRewardDB(userID, now)
	}

	uid := strings.TrimSpace(userID)
	if uid == "" {
		return WeeklyRewardClaim{}, ErrInvalidVote
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.weeklyClaimsByUser[uid]
	if !state.LastClaimAt.IsZero() && now.Before(state.LastClaimAt.Add(24*time.Hour)) {
		return WeeklyRewardClaim{}, ErrInvalidVote
	}
	if state.LastClaimAt.IsZero() || now.After(state.LastClaimAt.Add(48*time.Hour)) {
		state.StreakDay = 0
	}
	claimedDay := state.StreakDay + 1
	if claimedDay > 7 {
		claimedDay = 1
	}
	amount := s.weeklyRewardByDay[claimedDay-1]
	claimedAt := now.Format(time.RFC3339Nano)
	state.LastClaimAt = now
	state.StreakDay = claimedDay
	s.weeklyClaimsByUser[uid] = state
	key := "weekly_reward:" + uid + ":" + strconv.Itoa(claimedDay) + ":" + claimedAt
	return WeeklyRewardClaim{ClaimedDay: claimedDay, RewardAmountINT: amount, ClaimedAt: claimedAt, NextClaimAt: now.Add(24 * time.Hour).Format(time.RFC3339Nano), StreakDay: claimedDay, IdempotencyKey: key}, nil
}

func (s *Service) claimWeeklyRewardDB(userID string, now time.Time) (WeeklyRewardClaim, error) {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return WeeklyRewardClaim{}, ErrInvalidVote
	}
	now = now.UTC()

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return WeeklyRewardClaim{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err = tx.Exec(`INSERT INTO weekly_reward_claims (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, uid); err != nil {
		return WeeklyRewardClaim{}, err
	}

	var lastClaimAt sql.NullTime
	var streakDay int
	if err = tx.QueryRow(`SELECT last_claim_at, streak_day FROM weekly_reward_claims WHERE user_id = $1 FOR UPDATE`, uid).Scan(&lastClaimAt, &streakDay); err != nil {
		return WeeklyRewardClaim{}, err
	}

	if lastClaimAt.Valid && now.Before(lastClaimAt.Time.Add(24*time.Hour)) {
		return WeeklyRewardClaim{}, ErrInvalidVote
	}
	if !lastClaimAt.Valid || now.After(lastClaimAt.Time.Add(48*time.Hour)) {
		streakDay = 0
	}
	claimedDay := streakDay + 1
	if claimedDay > 7 {
		claimedDay = 1
	}
	amount := s.weeklyRewardByDay[claimedDay-1]
	claimedAt := now.Format(time.RFC3339Nano)

	if _, err = tx.Exec(`UPDATE weekly_reward_claims SET last_claim_at = $2, streak_day = $3, updated_at = NOW() WHERE user_id = $1`, uid, now, claimedDay); err != nil {
		return WeeklyRewardClaim{}, err
	}

	if err = tx.Commit(); err != nil {
		return WeeklyRewardClaim{}, err
	}

	key := "weekly_reward:" + uid + ":" + strconv.Itoa(claimedDay) + ":" + claimedAt
	return WeeklyRewardClaim{ClaimedDay: claimedDay, RewardAmountINT: amount, ClaimedAt: claimedAt, NextClaimAt: now.Add(24 * time.Hour).Format(time.RFC3339Nano), StreakDay: claimedDay, IdempotencyKey: key}, nil
}

func (s *Service) RollbackWeeklyRewardClaim(userID string, claimedAt string) {
	if s.db != nil {
		s.rollbackWeeklyRewardClaimDB(userID, claimedAt)
		return
	}

	uid := strings.TrimSpace(userID)
	if uid == "" || strings.TrimSpace(claimedAt) == "" {
		return
	}
	claimedTime, err := time.Parse(time.RFC3339Nano, claimedAt)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.weeklyClaimsByUser[uid]
	if !ok {
		return
	}
	if !state.LastClaimAt.Equal(claimedTime) {
		return
	}
	state.LastClaimAt = time.Time{}
	if state.StreakDay > 0 {
		state.StreakDay--
	}
	s.weeklyClaimsByUser[uid] = state
}

func (s *Service) rollbackWeeklyRewardClaimDB(userID string, claimedAt string) {
	uid := strings.TrimSpace(userID)
	if uid == "" || strings.TrimSpace(claimedAt) == "" {
		return
	}
	claimedTime, err := time.Parse(time.RFC3339Nano, claimedAt)
	if err != nil {
		return
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return
	}
	defer tx.Rollback() //nolint:errcheck

	var lastClaimAt sql.NullTime
	var streakDay int
	if err = tx.QueryRow(`SELECT last_claim_at, streak_day FROM weekly_reward_claims WHERE user_id = $1 FOR UPDATE`, uid).Scan(&lastClaimAt, &streakDay); err != nil {
		return
	}
	if !lastClaimAt.Valid || !lastClaimAt.Time.Equal(claimedTime) {
		return
	}
	if streakDay > 0 {
		streakDay--
	}
	if _, err = tx.Exec(`UPDATE weekly_reward_claims SET last_claim_at = NULL, streak_day = $2, updated_at = NOW() WHERE user_id = $1`, uid, streakDay); err != nil {
		return
	}
	_ = tx.Commit()
}

func (s *Service) ListLiveByStreamer(_ context.Context, streamerID string) []LiveEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now().UTC()
	result := make([]LiveEvent, 0)
	for _, item := range s.items {
		if item.event.StreamerID != streamerID {
			continue
		}
		event := item.event
		if !isOpen(event, now) {
			event.Status = "closed"
			continue
		}
		event.Status = "open"
		result = append(result, event)
	}
	return result
}

func (s *Service) Vote(_ context.Context, req VoteRequest) (LiveEvent, error) {
	if strings.TrimSpace(req.EventID) == "" || strings.TrimSpace(req.StreamerID) == "" || strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.OptionID) == "" || req.Amount <= 0 || strings.TrimSpace(req.IdempotencyKey) == "" {
		return LiveEvent{}, ErrInvalidVote
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[strings.TrimSpace(req.EventID)]
	if !ok || item.event.StreamerID != strings.TrimSpace(req.StreamerID) {
		return LiveEvent{}, ErrEventNotFound
	}
	now := time.Now().UTC()
	if !isOpen(item.event, now) {
		item.event.Status = "closed"
		return LiveEvent{}, ErrEventClosed
	}
	if existing, ok := item.processedVotes[req.IdempotencyKey]; ok {
		_ = existing
		event := item.event
		if user, ok := item.userVotes[strings.TrimSpace(req.UserID)]; ok {
			event.UserVote = &UserVote{OptionID: user.OptionID, TotalAmount: user.Amount}
		}
		return event, nil
	}
	optionID := strings.TrimSpace(req.OptionID)
	if _, ok := item.event.Totals[optionID]; !ok {
		return LiveEvent{}, ErrInvalidVote
	}
	fee := calculateFee(req.Amount, s.votePlatformFeeBPS)
	netAmount := req.Amount - fee
	item.event.Totals[optionID] += netAmount
	item.event.TotalContributed += req.Amount
	item.event.PlatformFeeINT += fee
	item.event.DistributableINT = item.event.TotalContributed - item.event.PlatformFeeINT
	userVote := item.userVotes[strings.TrimSpace(req.UserID)]
	if userVote.OptionID == "" {
		userVote.OptionID = optionID
	}
	if userVote.OptionID != optionID {
		userVote.OptionID = optionID
	}
	userVote.Amount += req.Amount
	item.userVotes[strings.TrimSpace(req.UserID)] = userVote
	item.processedVotes[strings.TrimSpace(req.IdempotencyKey)] = voteRecord{OptionID: optionID, Amount: req.Amount}
	optionPool := item.event.Totals[optionID]
	coefficient := calculateCoefficient(item.event.DistributableINT, optionPool)
	potentialWin := CalculateAccrualINT(
		item.event.TotalContributed,
		item.event.PlatformFeeINT,
		optionPool,
		req.Amount,
	)
	userID := strings.TrimSpace(req.UserID)
	historyItem := UserEventHistoryItem{
		EventID:          item.event.ID,
		StreamerID:       item.event.StreamerID,
		ScenarioID:       item.event.ScenarioID,
		TransitionID:     item.event.TransitionID,
		TerminalID:       item.event.TerminalID,
		Title:            cloneStringsMap(item.event.Title),
		DefaultLanguage:  item.event.DefaultLanguage,
		OptionID:         optionID,
		AmountINT:        req.Amount,
		CreatedAt:        now.Format(time.RFC3339Nano),
		TotalContributed: item.event.TotalContributed,
		PlatformFeeINT:   item.event.PlatformFeeINT,
		DistributableINT: item.event.DistributableINT,
		OptionPoolINT:    optionPool,
		Coefficient:      coefficient,
		PotentialWinINT:  potentialWin,
		ResultStatus:     "pending",
	}
	s.historyByUser[userID] = append(s.historyByUser[userID], historyItem)
	event := item.event
	event.UserVote = &UserVote{OptionID: userVote.OptionID, TotalAmount: userVote.Amount}
	return event, nil
}

func (s *Service) ListUserHistory(_ context.Context, userID string) []UserEventHistoryItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := s.historyByUser[strings.TrimSpace(userID)]
	result := make([]UserEventHistoryItem, 0, len(items))
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		item.Title = cloneStringsMap(item.Title)
		result = append(result, item)
	}
	return result
}

func calculateCoefficient(distributableINT, optionPoolINT int64) float64 {
	if distributableINT <= 0 || optionPoolINT <= 0 {
		return 0
	}
	return float64(distributableINT) / float64(optionPoolINT)
}

func cloneStringsMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func calculateFee(amount int64, feeBPS int64) int64 {
	if amount <= 0 || feeBPS <= 0 {
		return 0
	}
	if feeBPS >= 10000 {
		return amount
	}
	return (amount * feeBPS) / 10000
}

// CalculateAccrualINT calculates user's accrual from the distributable event pool.
// Formula: (totalContributed - platformFee) * userContribution / totalContributionForWinningOption.
func CalculateAccrualINT(totalContributed, platformFeeINT, totalContributionForWinningOption, userContribution int64) int64 {
	if totalContributed <= 0 || userContribution <= 0 || totalContributionForWinningOption <= 0 {
		return 0
	}
	distributable := totalContributed - platformFeeINT
	if distributable <= 0 {
		return 0
	}
	return (distributable * userContribution) / totalContributionForWinningOption
}

func isOpen(event LiveEvent, now time.Time) bool {
	closesAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(event.ClosesAt))
	if err != nil {
		return false
	}
	return now.Before(closesAt)
}
