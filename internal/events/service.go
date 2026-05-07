package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	ErrInvalidEvent      = errors.New("event payload is invalid")
	ErrEventNotFound     = errors.New("event not found")
	ErrEventClosed       = errors.New("event is closed")
	ErrInvalidVote       = errors.New("vote payload is invalid")
	ErrAlreadyActive     = errors.New("active event already exists for template")
	ErrInvalidSettlement = errors.New("event settlement payload is invalid")
	ErrEventSettled      = errors.New("event is already settled")
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
	redis              redis.Cmdable
	liveTTL            time.Duration
	items              map[string]*liveEventState
	historyByUser      map[string][]UserEventHistoryItem
	settlementsByEvent map[string]Settlement
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
		settlementsByEvent: map[string]Settlement{},
		weeklyClaimsByUser: map[string]weeklyClaimState{},
	}
}

func NewPostgresService(db *sql.DB, seed []LiveEvent) *Service {
	svc := NewService(seed)
	svc.db = db
	return svc
}

func (s *Service) WithRedisLiveState(client redis.Cmdable, ttl time.Duration) {
	s.redis = client
	if ttl <= 0 {
		ttl = 6 * time.Hour
	}
	s.liveTTL = ttl
}

func (s *Service) CreateLiveEvent(ctx context.Context, req CreateLiveEventRequest) (LiveEvent, error) {
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
	streamerID := strings.TrimSpace(req.StreamerID)
	templateID := streamerID + ":" + strings.TrimSpace(req.TerminalID)
	if s.redis != nil {
		if existing, ok := s.findActiveEventByTemplateRedis(ctx, streamerID, templateID, now); ok {
			return existing, ErrAlreadyActive
		}
	} else if s.db != nil {
		if existing, ok, err := s.findActiveEventByTemplateDB(ctx, streamerID, templateID, now); err != nil {
			return LiveEvent{}, err
		} else if ok {
			return existing, ErrAlreadyActive
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.redis == nil {
		for _, item := range s.items {
			if item.event.StreamerID != streamerID || item.event.TemplateID != templateID {
				continue
			}
			if isOpen(item.event, now) {
				return item.event, ErrAlreadyActive
			}
		}
	}
	event := LiveEvent{
		ID:              uuid.NewString(),
		TemplateID:      templateID,
		StreamerID:      streamerID,
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
	if s.redis != nil {
		if err := s.persistLiveState(ctx, event); err != nil {
			delete(s.items, event.ID)
			return LiveEvent{}, err
		}
		if s.db != nil {
			if err := s.persistLiveEventHistoryFromRedis(ctx, event.ID, now); err != nil {
				delete(s.items, event.ID)
				_ = s.deleteLiveState(ctx, event)
				return LiveEvent{}, err
			}
		}
	} else if s.db != nil {
		if err := s.insertLiveEventDB(ctx, event, now); err != nil {
			delete(s.items, event.ID)
			return LiveEvent{}, err
		}
	}
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

func (s *Service) ListLiveByStreamer(ctx context.Context, streamerID string) []LiveEvent {
	if s.redis != nil {
		items := s.listOpenEventsFromRedis(ctx, streamerID, time.Now().UTC())
		if len(items) > 0 {
			s.cacheLoadedEvents(items)
		}
		return items
	}
	if s.db != nil {
		if items, err := s.listOpenEventsByStreamerDB(ctx, strings.TrimSpace(streamerID), time.Now().UTC()); err == nil && len(items) > 0 {
			s.cacheLoadedEvents(items)
			return items
		}
	}
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

func (s *Service) ValidateVote(ctx context.Context, req VoteRequest) error {
	if strings.TrimSpace(req.EventID) == "" || strings.TrimSpace(req.StreamerID) == "" || strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.OptionID) == "" || req.Amount <= 0 || strings.TrimSpace(req.IdempotencyKey) == "" {
		return ErrInvalidVote
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if s.db != nil {
		if existing, ok, err := s.findVoteByIdempotencyDB(ctx, strings.TrimSpace(req.IdempotencyKey)); err != nil {
			return err
		} else if ok {
			if strings.TrimSpace(existing.EventID) == strings.TrimSpace(req.EventID) {
				return nil
			}
			return ErrInvalidVote
		}
		if s.redis != nil {
			if err := s.ensureLiveEventLoadedFromRedis(ctx, strings.TrimSpace(req.EventID), strings.TrimSpace(req.StreamerID)); err != nil {
				return err
			}
		} else if err := s.ensureLiveEventLoaded(ctx, strings.TrimSpace(req.EventID), strings.TrimSpace(req.StreamerID)); err != nil {
			return err
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.items[strings.TrimSpace(req.EventID)]
	if !ok || item.event.StreamerID != strings.TrimSpace(req.StreamerID) {
		return ErrEventNotFound
	}
	if !isOpen(item.event, time.Now().UTC()) {
		return ErrEventClosed
	}
	if existing, ok := item.processedVotes[strings.TrimSpace(req.IdempotencyKey)]; ok {
		if strings.TrimSpace(existing.OptionID) == strings.TrimSpace(req.OptionID) {
			return nil
		}
		return ErrInvalidVote
	}
	if _, ok := item.event.Totals[strings.TrimSpace(req.OptionID)]; !ok {
		return ErrInvalidVote
	}
	return nil
}

func (s *Service) Vote(ctx context.Context, req VoteRequest) (LiveEvent, error) {
	if strings.TrimSpace(req.EventID) == "" || strings.TrimSpace(req.StreamerID) == "" || strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.OptionID) == "" || req.Amount <= 0 || strings.TrimSpace(req.IdempotencyKey) == "" {
		return LiveEvent{}, ErrInvalidVote
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if s.db != nil {
		if strings.TrimSpace(req.WalletLedgerID) == "" {
			return LiveEvent{}, ErrInvalidVote
		}
		if existing, ok, err := s.findVoteByIdempotencyDB(ctx, strings.TrimSpace(req.IdempotencyKey)); err != nil {
			return LiveEvent{}, err
		} else if ok {
			if event, ok, err := s.loadLiveEventDB(ctx, strings.TrimSpace(existing.EventID), strings.TrimSpace(req.StreamerID)); err != nil {
				return LiveEvent{}, err
			} else if ok {
				event.UserVote = &UserVote{OptionID: existing.OptionID, TotalAmount: existing.Amount}
				return event, nil
			}
		}
		if s.redis != nil {
			if err := s.ensureLiveEventLoadedFromRedis(ctx, strings.TrimSpace(req.EventID), strings.TrimSpace(req.StreamerID)); err != nil {
				return LiveEvent{}, err
			}
		} else if err := s.ensureLiveEventLoaded(ctx, strings.TrimSpace(req.EventID), strings.TrimSpace(req.StreamerID)); err != nil {
			return LiveEvent{}, err
		}
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
	userID := strings.TrimSpace(req.UserID)
	previousEvent := cloneLiveEvent(item.event)
	previousUserVote, hadPreviousUserVote := item.userVotes[userID]
	rollbackVoteMutation := func() {
		item.event = previousEvent
		if hadPreviousUserVote {
			item.userVotes[userID] = previousUserVote
		} else {
			delete(item.userVotes, userID)
		}
		delete(item.processedVotes, strings.TrimSpace(req.IdempotencyKey))
		if s.redis != nil {
			_ = s.persistLiveState(ctx, previousEvent)
		}
	}
	fee := calculateFee(req.Amount, s.votePlatformFeeBPS)
	netAmount := req.Amount - fee
	item.event.Totals[optionID] += netAmount
	item.event.TotalContributed += req.Amount
	item.event.PlatformFeeINT += fee
	item.event.DistributableINT = item.event.TotalContributed - item.event.PlatformFeeINT
	userVote := item.userVotes[userID]
	if userVote.OptionID == "" {
		userVote.OptionID = optionID
	}
	if userVote.OptionID != optionID {
		userVote.OptionID = optionID
	}
	userVote.Amount += req.Amount
	item.userVotes[userID] = userVote
	item.processedVotes[strings.TrimSpace(req.IdempotencyKey)] = voteRecord{OptionID: optionID, Amount: req.Amount}
	if s.redis != nil {
		if err := s.persistLiveState(ctx, item.event); err != nil {
			rollbackVoteMutation()
			return LiveEvent{}, err
		}
	}
	if s.db != nil {
		eventForHistory := item.event
		if s.redis != nil {
			if redisEvent, ok := s.readLiveEventStateFromRedis(ctx, item.event.ID); ok {
				eventForHistory = redisEvent
			}
		}
		if err := s.persistVoteDB(ctx, eventForHistory, req, optionID); err != nil {
			rollbackVoteMutation()
			return LiveEvent{}, err
		}
	}
	optionPool := item.event.Totals[optionID]
	coefficient := calculateCoefficient(item.event.DistributableINT, optionPool)
	potentialWin := CalculateAccrualINT(
		item.event.TotalContributed,
		item.event.PlatformFeeINT,
		optionPool,
		req.Amount,
	)
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
		ResultStatus:     ResultStatusPending,
	}
	s.historyByUser[userID] = append(s.historyByUser[userID], historyItem)
	event := item.event
	event.UserVote = &UserVote{OptionID: userVote.OptionID, TotalAmount: userVote.Amount}
	return event, nil
}

func (s *Service) SettleEvent(ctx context.Context, req SettleRequest) (Settlement, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	prepared, err := normalizeSettleRequest(req)
	if err != nil {
		return Settlement{}, err
	}
	if s.db != nil {
		if s.redis != nil {
			if err := s.ensureLiveEventLoadedFromRedis(ctx, prepared.EventID, prepared.StreamerID); err != nil {
				return Settlement{}, err
			}
		} else if err := s.ensureLiveEventLoaded(ctx, prepared.EventID, prepared.StreamerID); err != nil {
			return Settlement{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if settlement, ok := s.settlementsByEvent[prepared.EventID]; ok {
		return cloneSettlement(settlement), nil
	}
	item, ok := s.items[prepared.EventID]
	if !ok || item.event.StreamerID != prepared.StreamerID {
		return Settlement{}, ErrEventNotFound
	}
	if strings.EqualFold(item.event.Status, "settled") {
		return Settlement{}, ErrEventSettled
	}
	if strings.EqualFold(item.event.Status, "cancelled") {
		return Settlement{}, ErrEventClosed
	}
	if prepared.Result == SettleResultWin {
		if _, ok := item.event.Totals[prepared.WinningOptionID]; !ok {
			return Settlement{}, ErrInvalidSettlement
		}
	}

	event := cloneLiveEvent(item.event)
	event.Status = "settled"
	if event.Totals == nil {
		event.Totals = map[string]int64{}
	}
	payouts, totalPayout := s.applySettlementToHistoryLocked(event, prepared)
	settlement := Settlement{
		Event:            event,
		WinningOptionID:  prepared.WinningOptionID,
		Result:           prepared.Result,
		Payouts:          payouts,
		TotalPayoutINT:   totalPayout,
		PlatformFeeINT:   event.PlatformFeeINT,
		DistributableINT: event.DistributableINT,
	}
	if s.db != nil {
		dbPayouts, dbTotalPayout, err := s.persistSettlementDB(ctx, event, prepared)
		if err != nil {
			return Settlement{}, err
		}
		if len(dbPayouts) > 0 || dbTotalPayout > 0 {
			settlement.Payouts = dbPayouts
			settlement.TotalPayoutINT = dbTotalPayout
		}
	}
	item.event = event
	if s.redis != nil {
		_ = s.deleteLiveState(ctx, item.event)
	}
	s.settlementsByEvent[prepared.EventID] = settlement
	return cloneSettlement(settlement), nil
}

func normalizeSettleRequest(req SettleRequest) (SettleRequest, error) {
	prepared := SettleRequest{
		EventID:         strings.TrimSpace(req.EventID),
		StreamerID:      strings.TrimSpace(req.StreamerID),
		WinningOptionID: strings.TrimSpace(req.WinningOptionID),
		Result:          req.Result,
		IdempotencyKey:  strings.TrimSpace(req.IdempotencyKey),
		ActorID:         strings.TrimSpace(req.ActorID),
	}
	if prepared.EventID == "" || prepared.StreamerID == "" {
		return SettleRequest{}, ErrInvalidSettlement
	}
	if prepared.Result == "" {
		if prepared.WinningOptionID != "" {
			prepared.Result = SettleResultWin
		} else {
			return SettleRequest{}, ErrInvalidSettlement
		}
	}
	switch prepared.Result {
	case SettleResultWin:
		if prepared.WinningOptionID == "" {
			return SettleRequest{}, ErrInvalidSettlement
		}
	case SettleResultDraw:
		prepared.WinningOptionID = ""
	default:
		return SettleRequest{}, ErrInvalidSettlement
	}
	return prepared, nil
}

func (s *Service) applySettlementToHistoryLocked(event LiveEvent, req SettleRequest) ([]SettlementPayout, int64) {
	payouts := make([]SettlementPayout, 0)
	totalPayout := int64(0)
	winningContributionINT := int64(0)
	if req.Result == SettleResultWin {
		for _, items := range s.historyByUser {
			for _, item := range items {
				if item.EventID == event.ID && strings.TrimSpace(item.OptionID) == strings.TrimSpace(req.WinningOptionID) {
					winningContributionINT += item.AmountINT
				}
			}
		}
	}
	for userID, items := range s.historyByUser {
		for idx := range items {
			if items[idx].EventID != event.ID {
				continue
			}
			winAmount, resultStatus := settlementAmountAndStatus(event, req, items[idx].OptionID, items[idx].AmountINT, winningContributionINT)
			items[idx].WinAmountINT = int64Ptr(winAmount)
			items[idx].ResultStatus = resultStatus
			if winAmount <= 0 {
				continue
			}
			payouts = append(payouts, SettlementPayout{
				UserID:         userID,
				OptionID:       items[idx].OptionID,
				AmountINT:      items[idx].AmountINT,
				WinAmountINT:   winAmount,
				ResultStatus:   resultStatus,
				IdempotencyKey: settlementPayoutIdempotencyKey(event.ID, userID, strconv.Itoa(idx)),
			})
			totalPayout += winAmount
		}
		s.historyByUser[userID] = items
	}
	return payouts, totalPayout
}

func settlementAmountAndStatus(event LiveEvent, req SettleRequest, optionID string, amountINT, winningContributionINT int64) (int64, string) {
	if amountINT <= 0 {
		return 0, ResultStatusLost
	}
	if req.Result == SettleResultDraw {
		return amountINT, ResultStatusDraw
	}
	if strings.TrimSpace(optionID) != strings.TrimSpace(req.WinningOptionID) {
		return 0, ResultStatusLost
	}
	if event.DistributableINT <= 0 || winningContributionINT <= 0 {
		return 0, ResultStatusWon
	}
	return (event.DistributableINT * amountINT) / winningContributionINT, ResultStatusWon
}

func int64Ptr(value int64) *int64 {
	return &value
}

func settlementPayoutIdempotencyKey(eventID, userID, voteRef string) string {
	return "event_settlement:" + strings.TrimSpace(eventID) + ":" + strings.TrimSpace(userID) + ":" + strings.TrimSpace(voteRef)
}

type settlementVoteRow struct {
	ID       string
	UserID   string
	OptionID string
	Amount   int64
}

func (s *Service) persistSettlementDB(ctx context.Context, event LiveEvent, req SettleRequest) ([]SettlementPayout, int64, error) {
	metadataJSON, err := json.Marshal(map[string]any{
		"settlementResult":  req.Result,
		"winningOptionId":   req.WinningOptionID,
		"settlementActorId": strings.TrimSpace(req.ActorID),
	})
	if err != nil {
		return nil, 0, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE live_event_history SET status = 'settled', closed_at = COALESCE(closed_at, NOW()), metadata = metadata || $2::jsonb, updated_at = NOW() WHERE id = $1 AND streamer_id = $3 AND status <> 'settled'`, event.ID, string(metadataJSON), event.StreamerID)
	if err != nil {
		return nil, 0, err
	}
	if rowsAffected, _ := result.RowsAffected(); rowsAffected == 0 {
		return nil, 0, ErrEventSettled
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, option_id, amount_int FROM live_event_vote_history WHERE event_id = $1 ORDER BY created_at ASC, id ASC`, event.ID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close() //nolint:errcheck
	votes := make([]settlementVoteRow, 0)
	for rows.Next() {
		var vote settlementVoteRow
		if err := rows.Scan(&vote.ID, &vote.UserID, &vote.OptionID, &vote.Amount); err != nil {
			return nil, 0, err
		}
		votes = append(votes, vote)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	winningContributionINT := int64(0)
	if req.Result == SettleResultWin {
		for _, vote := range votes {
			if strings.TrimSpace(vote.OptionID) == strings.TrimSpace(req.WinningOptionID) {
				winningContributionINT += vote.Amount
			}
		}
	}
	payouts := make([]SettlementPayout, 0, len(votes))
	totalPayout := int64(0)
	for _, vote := range votes {
		winAmount, resultStatus := settlementAmountAndStatus(event, req, vote.OptionID, vote.Amount, winningContributionINT)
		voteMetadataJSON, err := json.Marshal(map[string]any{
			"settlementResult": req.Result,
			"winningOptionId":  req.WinningOptionID,
			"resultStatus":     resultStatus,
			"winAmountINT":     winAmount,
		})
		if err != nil {
			return nil, 0, err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE live_event_vote_history SET metadata = metadata || $2::jsonb WHERE id = $1`, vote.ID, string(voteMetadataJSON)); err != nil {
			return nil, 0, err
		}
		if winAmount <= 0 {
			continue
		}
		payouts = append(payouts, SettlementPayout{
			UserID:         vote.UserID,
			OptionID:       vote.OptionID,
			AmountINT:      vote.Amount,
			WinAmountINT:   winAmount,
			ResultStatus:   resultStatus,
			IdempotencyKey: settlementPayoutIdempotencyKey(event.ID, vote.UserID, vote.ID),
		})
		totalPayout += winAmount
	}
	return payouts, totalPayout, nil
}

func cloneSettlement(settlement Settlement) Settlement {
	copySettlement := settlement
	copySettlement.Event = cloneLiveEvent(settlement.Event)
	copySettlement.Payouts = append([]SettlementPayout(nil), settlement.Payouts...)
	return copySettlement
}

func (s *Service) persistLiveState(ctx context.Context, event LiveEvent) error {
	if s.redis == nil {
		return nil
	}
	ttl := s.liveTTL
	if ttl <= 0 {
		ttl = 6 * time.Hour
	}
	stateKey := liveEventStateKey(event.ID)
	latestKey := latestActiveEventKey(event.StreamerID)
	activeSetKey := activeEventsSetKey(event.StreamerID)
	templateKey := activeTemplateKey(event.StreamerID, event.TemplateID)
	b, err := json.Marshal(redisLiveEventState{Event: event, StreamerID: event.StreamerID})
	if err != nil {
		return err
	}
	if err = s.redis.Set(ctx, stateKey, b, ttl).Err(); err != nil {
		return err
	}
	if err = s.redis.SAdd(ctx, activeSetKey, event.ID).Err(); err != nil {
		return err
	}
	if err = s.redis.Expire(ctx, activeSetKey, ttl).Err(); err != nil {
		return err
	}
	if err = s.redis.Set(ctx, latestKey, event.ID, ttl).Err(); err != nil {
		return err
	}
	return s.redis.Set(ctx, templateKey, event.ID, ttl).Err()
}

func (s *Service) deleteLiveState(ctx context.Context, event LiveEvent) error {
	if s.redis == nil {
		return nil
	}
	if err := s.redis.SRem(ctx, activeEventsSetKey(event.StreamerID), event.ID).Err(); err != nil {
		return err
	}
	return s.redis.Del(ctx,
		liveEventStateKey(event.ID),
		latestActiveEventKey(event.StreamerID),
		activeTemplateKey(event.StreamerID, event.TemplateID),
	).Err()
}

func (s *Service) findActiveEventByTemplateRedis(ctx context.Context, streamerID, templateID string, now time.Time) (LiveEvent, bool) {
	if s.redis == nil {
		return LiveEvent{}, false
	}
	eventID, err := s.redis.Get(ctx, activeTemplateKey(streamerID, templateID)).Result()
	if err != nil || strings.TrimSpace(eventID) == "" {
		return LiveEvent{}, false
	}
	event, ok := s.readLiveEventStateFromRedis(ctx, eventID)
	if !ok || !isOpen(event, now) {
		return LiveEvent{}, false
	}
	return event, true
}

func (s *Service) listOpenEventsFromRedis(ctx context.Context, streamerID string, now time.Time) []LiveEvent {
	if s.redis == nil || strings.TrimSpace(streamerID) == "" {
		return []LiveEvent{}
	}
	ids, err := s.redis.SMembers(ctx, activeEventsSetKey(streamerID)).Result()
	if err != nil || len(ids) == 0 {
		if event, ok := s.readLatestActiveEventFromRedis(ctx, streamerID); ok && isOpen(event, now) {
			return []LiveEvent{event}
		}
		return []LiveEvent{}
	}
	items := make([]LiveEvent, 0, len(ids))
	for _, id := range ids {
		event, ok := s.readLiveEventStateFromRedis(ctx, id)
		if !ok || !isOpen(event, now) {
			continue
		}
		event.Status = "open"
		items = append(items, event)
	}
	return items
}

func (s *Service) readLatestActiveEventFromRedis(ctx context.Context, streamerID string) (LiveEvent, bool) {
	eventID, err := s.redis.Get(ctx, latestActiveEventKey(streamerID)).Result()
	if err != nil || strings.TrimSpace(eventID) == "" {
		return LiveEvent{}, false
	}
	return s.readLiveEventStateFromRedis(ctx, eventID)
}

func (s *Service) readLiveEventStateFromRedis(ctx context.Context, eventID string) (LiveEvent, bool) {
	if s.redis == nil || strings.TrimSpace(eventID) == "" {
		return LiveEvent{}, false
	}
	raw, err := s.redis.Get(ctx, liveEventStateKey(eventID)).Bytes()
	if err != nil {
		return LiveEvent{}, false
	}
	var wrapped redisLiveEventState
	if err = json.Unmarshal(raw, &wrapped); err == nil && strings.TrimSpace(wrapped.Event.ID) != "" {
		wrapped.Event.StreamerID = firstNonEmpty(strings.TrimSpace(wrapped.StreamerID), strings.TrimSpace(wrapped.Event.StreamerID))
		return wrapped.Event, true
	}
	var event LiveEvent
	if err = json.Unmarshal(raw, &event); err != nil {
		return LiveEvent{}, false
	}
	return event, true
}

func (s *Service) ensureLiveEventLoadedFromRedis(ctx context.Context, eventID, streamerID string) error {
	s.mu.RLock()
	item, ok := s.items[eventID]
	s.mu.RUnlock()
	if ok && item.event.StreamerID == streamerID {
		return nil
	}
	event, found := s.readLiveEventStateFromRedis(ctx, eventID)
	if !found || event.StreamerID != streamerID {
		return ErrEventNotFound
	}
	s.cacheLoadedEvents([]LiveEvent{event})
	return nil
}

func (s *Service) persistLiveEventHistoryFromRedis(ctx context.Context, eventID string, openedAt time.Time) error {
	event, ok := s.readLiveEventStateFromRedis(ctx, eventID)
	if !ok {
		return ErrEventNotFound
	}
	return s.insertLiveEventDB(ctx, event, openedAt)
}

func liveEventStateKey(eventID string) string {
	return fmt.Sprintf("live_event:%s:state", strings.TrimSpace(eventID))
}

func latestActiveEventKey(streamerID string) string {
	return fmt.Sprintf("streamer:%s:active_event", strings.TrimSpace(streamerID))
}

func activeEventsSetKey(streamerID string) string {
	return fmt.Sprintf("streamer:%s:active_events", strings.TrimSpace(streamerID))
}

func activeTemplateKey(streamerID, templateID string) string {
	return fmt.Sprintf("streamer:%s:template:%s:active_event", strings.TrimSpace(streamerID), strings.TrimSpace(templateID))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

type redisLiveEventState struct {
	Event      LiveEvent `json:"event"`
	StreamerID string    `json:"streamerId"`
}

type dbVoteRecord struct {
	EventID  string
	OptionID string
	Amount   int64
}

func (s *Service) findActiveEventByTemplateDB(ctx context.Context, streamerID, templateID string, now time.Time) (LiveEvent, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
	SELECT id, streamer_id, scenario_id, template_id, transition_id, terminal_id,
	       title_json, options_json, final_totals_json, status, opened_at, closes_at, metadata
	FROM live_event_history
	WHERE streamer_id = $1 AND template_id = $2 AND status = 'open' AND (closes_at IS NULL OR closes_at > $3)
	ORDER BY opened_at DESC
	LIMIT 1`, streamerID, templateID, now)
	if err != nil {
		return LiveEvent{}, false, err
	}
	defer rows.Close() //nolint:errcheck
	items, err := scanLiveEventRows(rows)
	if err != nil {
		return LiveEvent{}, false, err
	}
	if len(items) == 0 {
		return LiveEvent{}, false, nil
	}
	return items[0], true, nil
}

func (s *Service) insertLiveEventDB(ctx context.Context, event LiveEvent, openedAt time.Time) error {
	titleJSON, err := json.Marshal(event.Title)
	if err != nil {
		return err
	}
	optionsJSON, err := json.Marshal(event.Options)
	if err != nil {
		return err
	}
	totalsJSON, err := json.Marshal(event.Totals)
	if err != nil {
		return err
	}
	metadataJSON, err := json.Marshal(map[string]any{"defaultLanguage": event.DefaultLanguage})
	if err != nil {
		return err
	}
	closesAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(event.ClosesAt))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
	INSERT INTO live_event_history (
		id, streamer_id, scenario_id, source, template_id, transition_id, terminal_id,
		title_json, options_json, final_totals_json, status, opened_at, closes_at, metadata
	)
	VALUES ($1, $2, $3, 'llm', $4, $5, $6, $7::jsonb, $8::jsonb, $9::jsonb, $10, $11, $12, $13::jsonb)`,
		event.ID, event.StreamerID, nullableUUID(event.ScenarioID), event.TemplateID, event.TransitionID, event.TerminalID,
		string(titleJSON), string(optionsJSON), string(totalsJSON), event.Status, openedAt, closesAt, string(metadataJSON),
	)
	return err
}

func (s *Service) listOpenEventsByStreamerDB(ctx context.Context, streamerID string, now time.Time) ([]LiveEvent, error) {
	if streamerID == "" {
		return []LiveEvent{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
	SELECT id, streamer_id, scenario_id, template_id, transition_id, terminal_id,
	       title_json, options_json, final_totals_json, status, opened_at, closes_at, metadata
	FROM live_event_history
	WHERE streamer_id = $1 AND status = 'open' AND (closes_at IS NULL OR closes_at > $2)
	ORDER BY opened_at DESC`, streamerID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	return scanLiveEventRows(rows)
}

func (s *Service) loadLiveEventDB(ctx context.Context, eventID, streamerID string) (LiveEvent, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
	SELECT id, streamer_id, scenario_id, template_id, transition_id, terminal_id,
	       title_json, options_json, final_totals_json, status, opened_at, closes_at, metadata
	FROM live_event_history
	WHERE id = $1 AND streamer_id = $2
	LIMIT 1`, eventID, streamerID)
	if err != nil {
		return LiveEvent{}, false, err
	}
	defer rows.Close() //nolint:errcheck
	items, err := scanLiveEventRows(rows)
	if err != nil {
		return LiveEvent{}, false, err
	}
	if len(items) == 0 {
		return LiveEvent{}, false, nil
	}
	return items[0], true, nil
}

func (s *Service) ensureLiveEventLoaded(ctx context.Context, eventID, streamerID string) error {
	s.mu.RLock()
	item, ok := s.items[eventID]
	s.mu.RUnlock()
	if ok && item.event.StreamerID == streamerID {
		return nil
	}
	event, found, err := s.loadLiveEventDB(ctx, eventID, streamerID)
	if err != nil {
		return err
	}
	if !found {
		return ErrEventNotFound
	}
	s.cacheLoadedEvents([]LiveEvent{event})
	return nil
}

func (s *Service) cacheLoadedEvents(events []LiveEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range events {
		copyEvent := event
		if copyEvent.Totals == nil {
			copyEvent.Totals = map[string]int64{}
		}
		if existing, ok := s.items[copyEvent.ID]; ok {
			existing.event = copyEvent
			continue
		}
		s.items[copyEvent.ID] = &liveEventState{event: copyEvent, processedVotes: map[string]voteRecord{}, userVotes: map[string]voteRecord{}}
	}
}

func (s *Service) findVoteByIdempotencyDB(ctx context.Context, idempotencyKey string) (dbVoteRecord, bool, error) {
	var rec dbVoteRecord
	err := s.db.QueryRowContext(ctx, `SELECT event_id, option_id, amount_int FROM live_event_vote_history WHERE idempotency_key = $1`, idempotencyKey).Scan(&rec.EventID, &rec.OptionID, &rec.Amount)
	if err == nil {
		return rec, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return dbVoteRecord{}, false, nil
	}
	return dbVoteRecord{}, false, err
}

func (s *Service) persistVoteDB(ctx context.Context, event LiveEvent, req VoteRequest, optionID string) error {
	totalsJSON, err := json.Marshal(event.Totals)
	if err != nil {
		return err
	}
	metadataJSON, err := json.Marshal(map[string]any{
		"totalContributed": event.TotalContributed,
		"platformFeeINT":   event.PlatformFeeINT,
		"distributableINT": event.DistributableINT,
	})
	if err != nil {
		return err
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE live_event_history SET final_totals_json = $2::jsonb, metadata = metadata || $3::jsonb, updated_at = NOW() WHERE id = $1`, event.ID, string(totalsJSON), string(metadataJSON)); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
	INSERT INTO live_event_vote_history (event_id, user_id, option_id, amount_int, wallet_ledger_id, idempotency_key, metadata)
	VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)`,
		event.ID, strings.TrimSpace(req.UserID), optionID, req.Amount, strings.TrimSpace(req.WalletLedgerID), strings.TrimSpace(req.IdempotencyKey), string(metadataJSON),
	)
	return err
}

func scanLiveEventRows(rows *sql.Rows) ([]LiveEvent, error) {
	items := make([]LiveEvent, 0)
	for rows.Next() {
		var event LiveEvent
		var scenarioID sql.NullString
		var titleRaw, optionsRaw, totalsRaw, metadataRaw []byte
		var openedAt time.Time
		var closesAt sql.NullTime
		if err := rows.Scan(&event.ID, &event.StreamerID, &scenarioID, &event.TemplateID, &event.TransitionID, &event.TerminalID, &titleRaw, &optionsRaw, &totalsRaw, &event.Status, &openedAt, &closesAt, &metadataRaw); err != nil {
			return nil, err
		}
		if scenarioID.Valid {
			event.ScenarioID = scenarioID.String
		}
		if len(titleRaw) > 0 {
			_ = json.Unmarshal(titleRaw, &event.Title)
		}
		if len(optionsRaw) > 0 {
			_ = json.Unmarshal(optionsRaw, &event.Options)
		}
		if len(totalsRaw) > 0 {
			_ = json.Unmarshal(totalsRaw, &event.Totals)
		}
		metadata := map[string]any{}
		if len(metadataRaw) > 0 {
			_ = json.Unmarshal(metadataRaw, &metadata)
		}
		event.DefaultLanguage = stringValue(metadata["defaultLanguage"])
		event.TotalContributed = int64Value(metadata["totalContributed"])
		event.PlatformFeeINT = int64Value(metadata["platformFeeINT"])
		event.DistributableINT = int64Value(metadata["distributableINT"])
		event.CreatedAt = openedAt.UTC().Format(time.RFC3339Nano)
		if closesAt.Valid {
			event.ClosesAt = closesAt.Time.UTC().Format(time.RFC3339Nano)
		}
		if event.Totals == nil {
			event.Totals = map[string]int64{}
		}
		items = append(items, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func nullableUUID(id string) any {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	return strings.TrimSpace(id)
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	default:
		parsed, _ := strconv.ParseInt(fmt.Sprint(typed), 10, 64)
		return parsed
	}
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

func cloneLiveEvent(event LiveEvent) LiveEvent {
	copyEvent := event
	copyEvent.Title = cloneStringsMap(event.Title)
	copyEvent.Options = append([]Option(nil), event.Options...)
	if event.Totals != nil {
		copyEvent.Totals = make(map[string]int64, len(event.Totals))
		for key, value := range event.Totals {
			copyEvent.Totals[key] = value
		}
	}
	if event.UserVote != nil {
		userVote := *event.UserVote
		copyEvent.UserVote = &userVote
	}
	return copyEvent
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

func (event LiveEvent) Market() LiveEventMarket {
	options := make(map[string]OptionMarket, len(event.Totals))
	for optionID, pool := range event.Totals {
		sharePct := 0.0
		if event.DistributableINT > 0 && pool > 0 {
			sharePct = (float64(pool) / float64(event.DistributableINT)) * 100
		}
		options[optionID] = OptionMarket{
			PoolINT:     pool,
			SharePct:    roundMarketFloat(sharePct),
			Coefficient: roundMarketFloat(calculateCoefficient(event.DistributableINT, pool)),
		}
	}
	return LiveEventMarket{Options: options}
}

func (event LiveEvent) PotentialWinINT(optionID string, amountINT int64) int64 {
	return CalculateAccrualINT(event.TotalContributed, event.PlatformFeeINT, event.Totals[strings.TrimSpace(optionID)], amountINT)
}

func roundMarketFloat(value float64) float64 {
	return math.Round(value*10000) / 10000
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
