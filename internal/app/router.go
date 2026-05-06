package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/admin"
	"github.com/funpot/funpot-go-core/internal/auth"
	"github.com/funpot/funpot-go-core/internal/config"
	"github.com/funpot/funpot-go-core/internal/events"
	"github.com/funpot/funpot-go-core/internal/games"
	"github.com/funpot/funpot-go-core/internal/media"
	"github.com/funpot/funpot-go-core/internal/prompts"
	"github.com/funpot/funpot-go-core/internal/realtime"
	"github.com/funpot/funpot-go-core/internal/streamers"
	"github.com/funpot/funpot-go-core/internal/users"
	"github.com/funpot/funpot-go-core/internal/wallet"
)

type readinessState struct {
	Status string `json:"status"`
	Time   string `json:"time"`
}

type telegramAuthRequest struct {
	InitData string `json:"initData"`
}

type refreshTokenRequest struct {
	RefreshToken string `json:"refreshToken"`
}

type ClientConfigResponse struct {
	StarsRate  float64         `json:"starsRate"`
	MinViewers int             `json:"minViewers"`
	Features   map[string]bool `json:"features"`
	Limits     configLimits    `json:"limits"`
}

func ConfigResponseFromConfig(cfg config.Config) ClientConfigResponse {
	return ClientConfigResponse{
		StarsRate:  cfg.Client.StarsRate,
		MinViewers: cfg.Client.MinViewers,
		Features:   cfg.Features.Flags,
		Limits: configLimits{
			VotePerMin: cfg.Client.VotePerMin,
		},
	}
}

func scenarioPackageRequestToCreateRequest(req scenarioPackageCreateRequest, actorID string) prompts.ScenarioPackageCreateRequest {
	steps := make([]prompts.ScenarioStep, 0, len(req.Steps))
	for _, step := range req.Steps {
		steps = append(steps, prompts.ScenarioStep{
			ID:                 step.ID,
			Name:               step.Name,
			GameSlug:           step.GameSlug,
			Folder:             step.Folder,
			EntryCondition:     step.EntryCondition,
			PromptTemplate:     step.PromptTemplate,
			ResponseSchemaJSON: step.ResponseSchemaJSON,
			SegmentSeconds:     step.SegmentSeconds,
			MaxRequests:        step.MaxRequests,
			Initial:            step.Initial,
			Order:              step.Order,
		})
	}
	transitions := make([]prompts.ScenarioTransition, 0, len(req.Transitions))
	for _, transition := range req.Transitions {
		transitions = append(transitions, prompts.ScenarioTransition{
			FromStepID: transition.FromStepID,
			ToStepID:   transition.ToStepID,
			Condition:  transition.Condition,
			Priority:   transition.Priority,
		})
	}
	packageTransitions := make([]prompts.ScenarioPackageTransition, 0, len(req.PackageTransitions))
	for _, transition := range req.PackageTransitions {
		packageTransitions = append(packageTransitions, prompts.ScenarioPackageTransition{
			ToPackageID:        transition.ToPackageID,
			Condition:          transition.Condition,
			Priority:           transition.Priority,
			Action:             transition.Action,
			FinalStateOptionID: transition.FinalStateOptionID,
		})
	}
	finalStateOptions := make([]prompts.ScenarioFinalStateOption, 0, len(req.FinalStateOptions))
	for _, option := range req.FinalStateOptions {
		finalStateOptions = append(finalStateOptions, prompts.ScenarioFinalStateOption{
			ID:             option.ID,
			Name:           option.Name,
			Condition:      option.Condition,
			FinalStateJSON: option.FinalStateJSON,
			FinalLabel:     option.FinalLabel,
		})
	}
	return prompts.ScenarioPackageCreateRequest{
		Name:               req.Name,
		GameSlug:           req.GameSlug,
		LLMModelConfigID:   req.LLMModelConfigID,
		Steps:              steps,
		Transitions:        transitions,
		PackageTransitions: packageTransitions,
		FinalStateOptions:  finalStateOptions,
		FinalCondition:     req.FinalCondition,
		ActorID:            actorID,
	}
}

func gameScenarioRequestToCreateRequest(req gameScenarioCreateRequest, actorID string) prompts.GameScenarioCreateRequest {
	nodes := make([]prompts.GameScenarioNode, 0, len(req.Nodes))
	for _, node := range req.Nodes {
		nodes = append(nodes, prompts.GameScenarioNode{
			ID:                node.ID,
			Alias:             node.Alias,
			ScenarioPackageID: node.ScenarioPackageID,
		})
	}
	transitions := make([]prompts.GameScenarioTransition, 0, len(req.Transitions))
	for _, tr := range req.Transitions {
		terminalConditions := make([]prompts.GameScenarioTerminalCondition, 0, len(tr.TerminalConditions))
		for _, item := range tr.TerminalConditions {
			outcomeTemplates := make([]prompts.GameScenarioOutcomeTemplate, 0, len(item.OutcomeTemplates))
			for _, outcome := range item.OutcomeTemplates {
				outcomeTemplates = append(outcomeTemplates, prompts.GameScenarioOutcomeTemplate{
					ID:        outcome.ID,
					Title:     outcome.Title,
					Condition: outcome.Condition,
					Priority:  outcome.Priority,
				})
			}
			terminalConditions = append(terminalConditions, prompts.GameScenarioTerminalCondition{
				ID:               item.ID,
				GameTitle:        item.GameTitle,
				DefaultLanguage:  item.DefaultLanguage,
				OutcomeTemplates: outcomeTemplates,
				Priority:         item.Priority,
			})
		}
		transitions = append(transitions, prompts.GameScenarioTransition{
			ID:                 tr.ID,
			FromNodeID:         tr.FromNodeID,
			ToNodeID:           tr.ToNodeID,
			Condition:          tr.Condition,
			Priority:           tr.Priority,
			TerminalConditions: terminalConditions,
		})
	}
	return prompts.GameScenarioCreateRequest{
		Name:          req.Name,
		GameSlug:      req.GameSlug,
		InitialNodeID: req.InitialNodeID,
		Nodes:         nodes,
		Transitions:   transitions,
		ActorID:       actorID,
	}
}

type configLimits struct {
	VotePerMin int `json:"votePerMin"`
}

type streamerSubmitRequest struct {
	TwitchNickname string `json:"twitchNickname"`
	TwitchUsername string `json:"twitchUsername"`
}

type gameUpsertRequest struct {
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Rules       []string `json:"rules"`
	Status      string   `json:"status"`
}

type scenarioStepRequest struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	GameSlug           string `json:"gameSlug"`
	Folder             string `json:"folder"`
	EntryCondition     string `json:"entryCondition"`
	PromptTemplate     string `json:"promptTemplate"`
	ResponseSchemaJSON string `json:"responseSchemaJson"`
	SegmentSeconds     int    `json:"segmentSeconds"`
	MaxRequests        int    `json:"maxRequests"`
	Initial            bool   `json:"initial"`
	Order              int    `json:"order"`
}

type scenarioTransitionRequest struct {
	FromStepID string `json:"fromStepId"`
	ToStepID   string `json:"toStepId"`
	Condition  string `json:"condition"`
	Priority   int    `json:"priority"`
}

type scenarioPackageTransitionRequest struct {
	ToPackageID        string `json:"toPackageId"`
	Condition          string `json:"condition"`
	Priority           int    `json:"priority"`
	Action             string `json:"action"`
	FinalStateOptionID string `json:"finalStateOptionId"`
}

type scenarioFinalStateOptionRequest struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Condition      string `json:"condition"`
	FinalStateJSON string `json:"finalStateJson"`
	FinalLabel     string `json:"finalLabel"`
}

type scenarioPackageCreateRequest struct {
	Name               string                             `json:"name"`
	GameSlug           string                             `json:"gameSlug"`
	LLMModelConfigID   string                             `json:"llmModelConfigId"`
	Steps              []scenarioStepRequest              `json:"steps"`
	Transitions        []scenarioTransitionRequest        `json:"transitions"`
	PackageTransitions []scenarioPackageTransitionRequest `json:"packageTransitions"`
	FinalStateOptions  []scenarioFinalStateOptionRequest  `json:"finalStateOptions"`
	FinalCondition     string                             `json:"finalCondition"`
}

type gameScenarioNodeRequest struct {
	ID                string `json:"id"`
	Alias             string `json:"alias"`
	ScenarioPackageID string `json:"scenarioPackageId"`
}

type gameScenarioTransitionRequest struct {
	ID                 string                                 `json:"id"`
	FromNodeID         string                                 `json:"fromNodeId"`
	ToNodeID           string                                 `json:"toNodeId"`
	Condition          string                                 `json:"condition"`
	Priority           int                                    `json:"priority"`
	TerminalConditions []gameScenarioTerminalConditionRequest `json:"terminalConditions"`
}

type gameScenarioTerminalConditionRequest struct {
	ID               string                                     `json:"id"`
	GameTitle        map[string]string                          `json:"gameTitle"`
	DefaultLanguage  string                                     `json:"defaultLanguage"`
	OutcomeTemplates []gameScenarioTerminalOutcomeTemplateInput `json:"outcomeTemplates"`
	Priority         int                                        `json:"priority"`
}

type gameScenarioTerminalOutcomeTemplateInput struct {
	ID        string            `json:"id"`
	Title     map[string]string `json:"title"`
	Condition string            `json:"condition"`
	Priority  int               `json:"priority"`
}

type gameScenarioCreateRequest struct {
	Name          string                          `json:"name"`
	GameSlug      string                          `json:"gameSlug"`
	InitialNodeID string                          `json:"initialNodeId"`
	Nodes         []gameScenarioNodeRequest       `json:"nodes"`
	Transitions   []gameScenarioTransitionRequest `json:"transitions"`
}

type llmModelConfigUpsertRequest struct {
	Name          string  `json:"name"`
	Model         string  `json:"model"`
	MetadataJSON  string  `json:"metadataJson"`
	Temperature   float64 `json:"temperature"`
	MaxTokens     int     `json:"maxTokens"`
	TimeoutMS     int     `json:"timeoutMs"`
	RetryCount    int     `json:"retryCount"`
	BackoffMS     int     `json:"backoffMs"`
	CooldownMS    int     `json:"cooldownMs"`
	MinConfidence float64 `json:"minConfidence"`
}

type meResponse struct {
	users.Profile
	IsAdmin bool `json:"isAdmin"`
}

type meNicknameUpdateRequest struct {
	Nickname string `json:"nickname"`
}

type eventVoteRequest struct {
	StreamerID string `json:"streamerId"`
	OptionID   string `json:"optionId"`
	AmountINT  int64  `json:"amountINT"`
}

type adminUsersResponse struct {
	Page     int             `json:"page"`
	PageSize int             `json:"pageSize"`
	Total    int             `json:"total"`
	Items    []users.Profile `json:"items"`
}

type adminUserUpsertRequest struct {
	TelegramID    int64  `json:"telegramId"`
	Username      string `json:"username"`
	FirstName     string `json:"firstName"`
	LastName      string `json:"lastName"`
	LanguageCode  string `json:"languageCode"`
	BalanceDelta  *int64 `json:"balanceDeltaINT,omitempty"`
	BalanceReason string `json:"balanceReason,omitempty"`
}

type adminUserBanRequest struct {
	IsBanned   bool   `json:"isBanned"`
	Reason     string `json:"reason"`
	DurationMS int64  `json:"durationMs"`
}

type withdrawRequest struct {
	AmountINT int64 `json:"amountINT"`
}

type adminGeneralSettingsRequest struct {
	VotePlatformFeePercent float64  `json:"votePlatformFeePercent"`
	NicknameChangeCostINT  int64    `json:"nicknameChangeCostINT"`
	WeeklyRewardByDayINT   [7]int64 `json:"weeklyRewardByDayINT"`
}

type adminHistoryEvent struct {
	EventTime        string  `json:"eventTime"`
	StepName         string  `json:"stepName"`
	LLMResponse      string  `json:"llmResponse"`
	GlobalStateDelta string  `json:"globalStateDelta,omitempty"`
	Confidence       float64 `json:"confidence"`
	streamers.LLMDecision
}

type adminStreamerLLMHistoryResponse struct {
	StreamerID string                `json:"streamerId"`
	Page       int                   `json:"page"`
	PageSize   int                   `json:"pageSize"`
	Total      int                   `json:"total"`
	Items      []adminHistoryEvent   `json:"items"`
	Videos     []media.UploadedVideo `json:"videos"`
}

type adminStreamerHistoryDeleteResponse struct {
	StreamerID        string `json:"streamerId"`
	DeletedDecisions  int    `json:"deletedDecisions"`
	DeletedBunnyVideo int    `json:"deletedBunnyVideos"`
}

type adminStreamerVideoManager interface {
	ListUploadedVideos(streamerID string) []media.UploadedVideo
	DeleteStreamerVideos(ctx context.Context, streamerID string) (int, error)
}

type userEventHistoryStreamerItem struct {
	DateTime         string                        `json:"dateTime"`
	StreamerID       string                        `json:"streamerId"`
	StreamerNickname string                        `json:"streamerNickname"`
	StreamerIconURL  string                        `json:"streamerIconUrl,omitempty"`
	NetAmountINT     int64                         `json:"netAmountINT"`
	Details          []events.UserEventHistoryItem `json:"details"`
}

// NewHandler wires the base HTTP routes for the service.
func NewHandler(
	logger *zap.Logger,
	readyFn func() bool,
	metricsHandler http.Handler,
	authService *auth.Service,
	adminService *admin.Service,
	userService *users.Service,
	streamersService *streamers.Service,
	gamesService *games.Service,
	promptsService *prompts.Service,
	streamerVideoManager any,
	eventsService *events.Service,
	clientConfig ClientConfigResponse,
	walletServices ...*wallet.Service,
) http.Handler {
	const rateLimitAutoBanDuration = 15 * time.Minute

	mux := http.NewServeMux()
	rtHub := realtime.NewHub()
	wsUpgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, readinessState{Status: "ok", Time: time.Now().UTC().Format(time.RFC3339Nano)})
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if readyFn != nil && !readyFn() {
			writeJSON(w, http.StatusServiceUnavailable, readinessState{Status: "not_ready", Time: time.Now().UTC().Format(time.RFC3339Nano)})
			return
		}
		writeJSON(w, http.StatusOK, readinessState{Status: "ready", Time: time.Now().UTC().Format(time.RFC3339Nano)})
	})

	if metricsHandler != nil {
		mux.Handle("/metrics", metricsHandler)
	} else {
		mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		logger.Debug("root endpoint hit", zap.String("path", r.URL.Path))
		w.WriteHeader(http.StatusNoContent)
	})

	if authService != nil {
		walletService := wallet.NewService()
		if len(walletServices) > 0 && walletServices[0] != nil {
			walletService = walletServices[0]
		}
		mux.HandleFunc("/realtime", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			streamerID := strings.TrimSpace(r.URL.Query().Get("streamerId"))
			if streamerID == "" {
				writeError(w, http.StatusBadRequest, "streamerId is required")
				return
			}
			authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
				writeError(w, http.StatusUnauthorized, "missing bearer token")
				return
			}
			if _, err := authService.ParseToken(parts[1]); err != nil {
				writeError(w, http.StatusUnauthorized, "invalid bearer token")
				return
			}
			conn, err := wsUpgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close() //nolint:errcheck
			claims, _ := authService.ParseToken(parts[1])
			streamerCh, unsubscribeStreamer := rtHub.SubscribeStreamer(streamerID, 64)
			defer unsubscribeStreamer()
			userCh, unsubscribeUser := rtHub.SubscribeUser(claims.Subject, 64)
			defer unsubscribeUser()
			_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
			conn.SetPongHandler(func(string) error {
				return conn.SetReadDeadline(time.Now().Add(45 * time.Second))
			})
			go func() {
				for {
					if _, _, err := conn.ReadMessage(); err != nil {
						_ = conn.Close()
						return
					}
				}
			}()
			pingTicker := time.NewTicker(20 * time.Second)
			defer pingTicker.Stop()
			for {
				select {
				case env, ok := <-streamerCh:
					if !ok {
						return
					}
					if err := conn.WriteJSON(env); err != nil {
						return
					}
				case env, ok := <-userCh:
					if !ok {
						return
					}
					if err := conn.WriteJSON(env); err != nil {
						return
					}
				case <-pingTicker.C:
					if err := conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(2*time.Second)); err != nil {
						return
					}
				}
			}
		})

		mux.HandleFunc("/api/auth/telegram", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}

			defer r.Body.Close() //nolint:errcheck

			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				writeError(w, http.StatusBadRequest, "failed to read request body")
				return
			}

			var req telegramAuthRequest
			if err := json.Unmarshal(body, &req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if req.InitData == "" {
				writeError(w, http.StatusBadRequest, "initData is required")
				return
			}

			resp, err := authService.Authenticate(r.Context(), req.InitData, time.Now().UTC())
			if err != nil {
				var status int
				switch {
				case errors.Is(err, auth.ErrInvalidHash), errors.Is(err, auth.ErrExpired):
					status = http.StatusUnauthorized
				case errors.Is(err, auth.ErrUserBanned):
					status = http.StatusForbidden
				case errors.Is(err, auth.ErrMissingHash), errors.Is(err, auth.ErrMissingAuthDate), errors.Is(err, auth.ErrMissingUser):
					status = http.StatusBadRequest
				default:
					var parseErr *url.Error
					var numErr *strconv.NumError
					switch {
					case errors.As(err, &parseErr), errors.As(err, &numErr):
						status = http.StatusBadRequest
					default:
						status = http.StatusInternalServerError
						logger.Error("failed to authenticate telegram init data", zap.Error(err))
					}
				}
				writeError(w, status, err.Error())
				return
			}

			writeJSON(w, http.StatusOK, resp)
		})

		mux.HandleFunc("/api/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}

			defer r.Body.Close() //nolint:errcheck
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				writeError(w, http.StatusBadRequest, "failed to read request body")
				return
			}

			var req refreshTokenRequest
			if err := json.Unmarshal(body, &req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}

			resp, err := authService.Refresh(r.Context(), req.RefreshToken, time.Now().UTC())
			if err != nil {
				switch {
				case errors.Is(err, auth.ErrRefreshTokenRequired), errors.Is(err, auth.ErrInvalidRefreshToken):
					writeError(w, http.StatusBadRequest, err.Error())
				case errors.Is(err, auth.ErrRefreshSessionNotFound), errors.Is(err, auth.ErrRefreshSessionRevoked), errors.Is(err, auth.ErrRefreshTokenMismatch):
					writeError(w, http.StatusUnauthorized, err.Error())
				default:
					logger.Error("failed to refresh auth token", zap.Error(err))
					writeError(w, http.StatusInternalServerError, "failed to refresh token")
				}
				return
			}

			writeJSON(w, http.StatusOK, resp)
		})

		mux.HandleFunc("/api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}

			defer r.Body.Close() //nolint:errcheck
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				writeError(w, http.StatusBadRequest, "failed to read request body")
				return
			}

			var req refreshTokenRequest
			if err := json.Unmarshal(body, &req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}

			if err := authService.Logout(r.Context(), req.RefreshToken, time.Now().UTC()); err != nil {
				switch {
				case errors.Is(err, auth.ErrInvalidRefreshToken), errors.Is(err, auth.ErrRefreshTokenRequired):
					writeError(w, http.StatusBadRequest, err.Error())
				case errors.Is(err, auth.ErrRefreshSessionNotFound), errors.Is(err, auth.ErrRefreshTokenMismatch):
					writeError(w, http.StatusUnauthorized, err.Error())
				default:
					logger.Error("failed to logout refresh session", zap.Error(err))
					writeError(w, http.StatusInternalServerError, "failed to logout")
				}
				return
			}

			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		})

		baseAuthed := authService.ClaimsMiddleware()
		authed := withUserBanGuard(baseAuthed, userService)

		mux.Handle("/api/auth/logout-all", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}

			claims, ok := auth.ClaimsFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "missing auth claims")
				return
			}

			if err := authService.LogoutAll(r.Context(), claims.Subject, time.Now().UTC()); err != nil {
				logger.Error("failed to revoke all sessions", zap.Error(err), zap.String("userID", claims.Subject))
				writeError(w, http.StatusInternalServerError, "failed to logout all sessions")
				return
			}

			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		})))

		mux.Handle("/api/me", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := auth.ClaimsFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "missing auth claims")
				return
			}
			switch r.Method {
			case http.MethodGet:
				profile, err := userService.GetByTelegramID(r.Context(), claims.TelegramID)
				if err != nil {
					if errors.Is(err, users.ErrNotFound) {
						writeError(w, http.StatusNotFound, "user not found")
						return
					}
					writeError(w, http.StatusInternalServerError, "failed to load profile")
					return
				}
				isAdmin := adminService != nil && adminService.IsAdmin(claims.Subject)
				writeJSON(w, http.StatusOK, meResponse{Profile: profile, IsAdmin: isAdmin})
			case http.MethodPut:
				defer r.Body.Close() //nolint:errcheck
				body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				if err != nil {
					writeError(w, http.StatusBadRequest, "failed to read request body")
					return
				}
				var req meNicknameUpdateRequest
				if err := decodeJSONStrict(body, &req); err != nil {
					writeError(w, http.StatusBadRequest, "invalid request body")
					return
				}
				profile, err := userService.UpdateNicknameByID(r.Context(), claims.Subject, req.Nickname)
				if err != nil {
					switch {
					case errors.Is(err, users.ErrNotFound):
						writeError(w, http.StatusNotFound, "user not found")
					case errors.Is(err, users.ErrInvalidNickname):
						writeError(w, http.StatusBadRequest, err.Error())
					default:
						writeError(w, http.StatusInternalServerError, "failed to update nickname")
					}
					return
				}
				if eventsService != nil && walletService != nil {
					nicknameCost := eventsService.Settings().NicknameChangeCostINT
					if nicknameCost > 0 {
						_, _, debitErr := walletService.Post(wallet.PostRequest{
							UserID:         claims.Subject,
							Type:           wallet.EntryTypeDebit,
							Amount:         nicknameCost,
							Reason:         "nickname_change",
							IdempotencyKey: "nickname_change:" + claims.Subject + ":" + profile.UpdatedAt.Format(time.RFC3339Nano),
							ActorID:        claims.Subject,
						})
						if debitErr != nil {
							switch {
							case errors.Is(debitErr, wallet.ErrInsufficientFunds):
								writeError(w, http.StatusConflict, debitErr.Error())
							case errors.Is(debitErr, wallet.ErrInvalidAmount), errors.Is(debitErr, wallet.ErrIdempotencyRequired):
								writeError(w, http.StatusBadRequest, debitErr.Error())
							default:
								writeError(w, http.StatusInternalServerError, "failed to charge nickname change")
							}
							return
						}
					}
				}
				writeJSON(w, http.StatusOK, profile)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		})))

		mux.Handle("/api/admin/users", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !requireAdmin(w, r, adminService) {
				writeError(w, http.StatusForbidden, "admin role is required")
				return
			}

			switch r.Method {
			case http.MethodGet:
				pageRaw := strings.TrimSpace(r.URL.Query().Get("page"))
				page := 1
				if pageRaw != "" {
					parsed, err := strconv.Atoi(pageRaw)
					if err != nil || parsed <= 0 {
						writeError(w, http.StatusBadRequest, "page must be a positive integer")
						return
					}
					page = parsed
				}
				pageSizeRaw := strings.TrimSpace(r.URL.Query().Get("pageSize"))
				pageSize := 20
				if pageSizeRaw != "" {
					parsed, err := strconv.Atoi(pageSizeRaw)
					if err != nil || parsed <= 0 {
						writeError(w, http.StatusBadRequest, "pageSize must be a positive integer")
						return
					}
					pageSize = parsed
				}
				items, total, err := userService.List(r.Context(), r.URL.Query().Get("query"), page, pageSize)
				if err != nil {
					logger.Error("failed to list users", zap.Error(err))
					writeError(w, http.StatusInternalServerError, "failed to list users")
					return
				}
				writeJSON(w, http.StatusOK, adminUsersResponse{
					Page:     page,
					PageSize: pageSize,
					Total:    total,
					Items:    items,
				})
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		})))

		mux.Handle("/api/admin/users/", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !requireAdmin(w, r, adminService) {
				writeError(w, http.StatusForbidden, "admin role is required")
				return
			}
			path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/users/"), "/")
			if path == "" {
				writeError(w, http.StatusBadRequest, "user id is required")
				return
			}
			parts := strings.Split(path, "/")
			userID := strings.TrimSpace(parts[0])
			if userID == "" {
				writeError(w, http.StatusBadRequest, "user id is required")
				return
			}
			action := ""
			if len(parts) > 1 {
				action = strings.TrimSpace(parts[1])
			}
			switch r.Method {
			case http.MethodGet:
				if action != "" {
					writeError(w, http.StatusNotFound, "user route not found")
					return
				}
				item, err := userService.GetByID(r.Context(), userID)
				if err != nil {
					if errors.Is(err, users.ErrNotFound) {
						writeError(w, http.StatusNotFound, err.Error())
						return
					}
					logger.Error("failed to get user", zap.String("userID", userID), zap.Error(err))
					writeError(w, http.StatusInternalServerError, "failed to get user")
					return
				}
				writeJSON(w, http.StatusOK, item)
			case http.MethodPut:
				if action == "ban" {
					defer r.Body.Close() //nolint:errcheck
					body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					if err != nil {
						writeError(w, http.StatusBadRequest, "failed to read request body")
						return
					}
					var req adminUserBanRequest
					if err := decodeJSONStrict(body, &req); err != nil {
						writeError(w, http.StatusBadRequest, "invalid request body")
						return
					}
					if !req.IsBanned {
						profile, err := userService.UnbanByID(r.Context(), userID)
						if err != nil {
							if errors.Is(err, users.ErrNotFound) {
								writeError(w, http.StatusNotFound, err.Error())
								return
							}
							logger.Error("failed to unban user", zap.String("userID", userID), zap.Error(err))
							writeError(w, http.StatusInternalServerError, "failed to update user ban")
							return
						}
						writeJSON(w, http.StatusOK, profile)
						return
					}

					banUntil := time.Time{}
					if req.DurationMS > 0 {
						banUntil = time.Now().UTC().Add(time.Duration(req.DurationMS) * time.Millisecond)
					}
					profile, err := userService.BanByID(r.Context(), userID, req.Reason, banUntil)
					if err != nil {
						switch {
						case errors.Is(err, users.ErrNotFound):
							writeError(w, http.StatusNotFound, err.Error())
						case errors.Is(err, users.ErrBanUntilBeforeNow):
							writeError(w, http.StatusBadRequest, err.Error())
						default:
							logger.Error("failed to ban user", zap.String("userID", userID), zap.Error(err))
							writeError(w, http.StatusInternalServerError, "failed to update user ban")
						}
						return
					}
					writeJSON(w, http.StatusOK, profile)
					return
				}
				if action != "" {
					writeError(w, http.StatusNotFound, "user route not found")
					return
				}

				defer r.Body.Close() //nolint:errcheck
				body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				if err != nil {
					writeError(w, http.StatusBadRequest, "failed to read request body")
					return
				}
				var req adminUserUpsertRequest
				if err := decodeJSONStrict(body, &req); err != nil {
					writeError(w, http.StatusBadRequest, "invalid request body")
					return
				}
				profile, err := userService.GetByID(r.Context(), userID)
				if err != nil {
					if errors.Is(err, users.ErrNotFound) {
						writeError(w, http.StatusNotFound, err.Error())
						return
					}
					logger.Error("failed to fetch user", zap.String("userID", userID), zap.Error(err))
					writeError(w, http.StatusInternalServerError, "failed to fetch user")
					return
				}
				if req.TelegramID != 0 || strings.TrimSpace(req.Username) != "" || strings.TrimSpace(req.FirstName) != "" || strings.TrimSpace(req.LastName) != "" || strings.TrimSpace(req.LanguageCode) != "" {
					profile, err = userService.UpdateByID(r.Context(), userID, users.TelegramProfile{
						ID:           req.TelegramID,
						Username:     req.Username,
						FirstName:    req.FirstName,
						LastName:     req.LastName,
						LanguageCode: req.LanguageCode,
					})
					if err != nil {
						if errors.Is(err, users.ErrNotFound) {
							writeError(w, http.StatusNotFound, err.Error())
							return
						}
						logger.Error("failed to update user", zap.String("userID", userID), zap.Error(err))
						writeError(w, http.StatusInternalServerError, "failed to update user")
						return
					}
				}
				if req.BalanceDelta != nil {
					idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
					if idempotencyKey == "" {
						idempotencyKey = "admin-adjust-" + uuid.NewString()
					}
					if strings.TrimSpace(req.BalanceReason) == "" {
						writeError(w, http.StatusBadRequest, "balanceReason is required when balanceDeltaINT is provided")
						return
					}
					claims, ok := auth.ClaimsFromContext(r.Context())
					if !ok {
						writeError(w, http.StatusUnauthorized, "missing auth claims")
						return
					}
					if _, _, err := walletService.Adjust(wallet.AdjustRequest{
						UserID:         userID,
						Delta:          *req.BalanceDelta,
						Reason:         req.BalanceReason,
						IdempotencyKey: idempotencyKey,
						ActorID:        claims.Subject,
					}); err != nil {
						switch {
						case errors.Is(err, wallet.ErrInvalidAmount),
							errors.Is(err, wallet.ErrInvalidDelta),
							errors.Is(err, wallet.ErrUserIDRequired),
							errors.Is(err, wallet.ErrIdempotencyRequired):
							writeError(w, http.StatusBadRequest, err.Error())
						case errors.Is(err, wallet.ErrInsufficientFunds):
							writeError(w, http.StatusConflict, err.Error())
						default:
							logger.Error("failed to apply admin user wallet adjustment", zap.String("userID", userID), zap.Error(err))
							writeError(w, http.StatusInternalServerError, "failed to adjust wallet")
						}
						return
					}
				}
				writeJSON(w, http.StatusOK, profile)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		})))

		mux.Handle("/api/config", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			writeJSON(w, http.StatusOK, clientConfig)
		})))

		mux.Handle("/api/wallet", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			claims, ok := auth.ClaimsFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "missing auth claims")
				return
			}
			writeJSON(w, http.StatusOK, walletService.Get(claims.Subject))
		})))

		mux.Handle("/api/wallet/withdraw", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			claims, ok := auth.ClaimsFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "missing auth claims")
				return
			}
			idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
			if idempotencyKey == "" {
				writeError(w, http.StatusBadRequest, wallet.ErrIdempotencyRequired.Error())
				return
			}
			defer r.Body.Close() //nolint:errcheck
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				writeError(w, http.StatusBadRequest, "failed to read request body")
				return
			}
			var req withdrawRequest
			if err := decodeJSONStrict(body, &req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			_, newBalance, err := walletService.Post(wallet.PostRequest{
				UserID:         claims.Subject,
				Type:           wallet.EntryTypeDebit,
				Amount:         req.AmountINT,
				Reason:         "withdraw",
				IdempotencyKey: idempotencyKey,
				ActorID:        claims.Subject,
			})
			if err != nil {
				switch {
				case errors.Is(err, wallet.ErrInvalidAmount), errors.Is(err, wallet.ErrIdempotencyRequired):
					writeError(w, http.StatusBadRequest, err.Error())
				case errors.Is(err, wallet.ErrInsufficientFunds):
					writeError(w, http.StatusConflict, err.Error())
				default:
					logger.Error("failed to withdraw from wallet", zap.String("userID", claims.Subject), zap.Error(err))
					writeError(w, http.StatusInternalServerError, "failed to withdraw")
				}
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"status":     "done",
				"newBalance": newBalance,
			})
		})))
		mux.Handle("/api/rewards/weekly/claim", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			claims, ok := auth.ClaimsFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "missing auth claims")
				return
			}
			if eventsService == nil {
				writeError(w, http.StatusNotFound, "rewards are not configured")
				return
			}
			claim, err := eventsService.ClaimWeeklyReward(claims.Subject, time.Now().UTC())
			if err != nil {
				writeError(w, http.StatusConflict, "weekly reward is available once per 24 hours")
				return
			}
			newBalance := walletService.Get(claims.Subject).Balance
			if claim.RewardAmountINT > 0 {
				_, newBalanceAfterReward, err := walletService.Post(wallet.PostRequest{
					UserID:         claims.Subject,
					Type:           wallet.EntryTypeCredit,
					Amount:         claim.RewardAmountINT,
					Reason:         "weekly_reward",
					IdempotencyKey: claim.IdempotencyKey,
					ActorID:        claims.Subject,
				})
				if err != nil {
					eventsService.RollbackWeeklyRewardClaim(claims.Subject, claim.ClaimedAt)
					writeError(w, http.StatusInternalServerError, "failed to credit weekly reward")
					return
				}
				newBalance = newBalanceAfterReward
			}
			writeJSON(w, http.StatusOK, map[string]any{"claim": claim, "newBalance": newBalance})
		})))

		if eventsService != nil {
			mux.Handle("/api/admin/settings/general", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !requireAdmin(w, r, adminService) {
					writeError(w, http.StatusForbidden, "admin role is required")
					return
				}
				switch r.Method {
				case http.MethodGet:
					writeJSON(w, http.StatusOK, eventsService.Settings())
				case http.MethodPut:
					defer r.Body.Close() //nolint:errcheck
					body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					if err != nil {
						writeError(w, http.StatusBadRequest, "failed to read request body")
						return
					}
					var req adminGeneralSettingsRequest
					if err := decodeJSONStrict(body, &req); err != nil {
						writeError(w, http.StatusBadRequest, "invalid request body")
						return
					}
					updated, err := eventsService.UpdateSettings(events.Settings{
						VotePlatformFeePercent: req.VotePlatformFeePercent,
						NicknameChangeCostINT:  req.NicknameChangeCostINT,
						WeeklyRewardByDayINT:   req.WeeklyRewardByDayINT,
					})
					if err != nil {
						writeError(w, http.StatusBadRequest, "votePlatformFeePercent must be in range 0..100 and nicknameChangeCostINT must be >= 0")
						return
					}
					writeJSON(w, http.StatusOK, updated)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			})))
		}

		if streamersService != nil {
			mux.Handle("/api/streamers", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					page, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page")))
					if err != nil && r.URL.Query().Get("page") != "" {
						writeError(w, http.StatusBadRequest, "page must be a positive integer")
						return
					}
					statusFilter := r.URL.Query().Get("status")
					if !streamers.IsSupportedStatus(statusFilter) {
						writeError(w, http.StatusBadRequest, streamers.ErrInvalidStatus.Error())
						return
					}
					items := streamersService.List(r.Context(), r.URL.Query().Get("query"), statusFilter, page)
					writeJSON(w, http.StatusOK, items)
				case http.MethodPost:
					defer r.Body.Close() //nolint:errcheck
					body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					if err != nil {
						writeError(w, http.StatusBadRequest, "failed to read request body")
						return
					}
					var req streamerSubmitRequest
					if err := json.Unmarshal(body, &req); err != nil {
						writeError(w, http.StatusBadRequest, "invalid request body")
						return
					}
					claims, ok := auth.ClaimsFromContext(r.Context())
					if !ok {
						writeError(w, http.StatusUnauthorized, "missing auth claims")
						return
					}
					nickname := strings.TrimSpace(req.TwitchNickname)
					if nickname == "" {
						nickname = strings.TrimSpace(req.TwitchUsername)
					}

					submission, err := streamersService.Submit(r.Context(), nickname, claims.Subject)
					if err != nil {
						if errors.Is(err, streamers.ErrInvalidUsername) ||
							errors.Is(err, streamers.ErrTwitchUnavailable) ||
							errors.Is(err, streamers.ErrStreamerOffline) ||
							errors.Is(err, streamers.ErrInsufficientLive) {
							writeError(w, http.StatusBadRequest, err.Error())
							return
						}
						if errors.Is(err, streamers.ErrRateLimited) {
							if userService != nil {
								if _, banErr := userService.BanByID(r.Context(), claims.Subject, "auto-ban: streamer submission rate limit exceeded", time.Now().UTC().Add(rateLimitAutoBanDuration)); banErr != nil && !errors.Is(banErr, users.ErrNotFound) {
									logger.Error("failed to auto-ban user after rate limit", zap.String("userID", claims.Subject), zap.Error(banErr))
								}
							}
							writeError(w, http.StatusTooManyRequests, err.Error())
							return
						}
						logger.Error("failed to submit streamer", zap.Error(err))
						writeError(w, http.StatusInternalServerError, "failed to submit streamer")
						return
					}
					writeJSON(w, http.StatusOK, submission)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			})))

			mux.Handle("/api/streamers/", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := strings.TrimPrefix(r.URL.Path, "/api/streamers/")
				if path == "" {
					writeError(w, http.StatusNotFound, "streamer route not found")
					return
				}

				parts := strings.Split(path, "/")
				if len(parts) != 2 {
					writeError(w, http.StatusNotFound, "streamer route not found")
					return
				}
				streamerID := strings.TrimSpace(parts[0])
				action := strings.TrimSpace(parts[1])
				if streamerID == "" {
					writeError(w, http.StatusBadRequest, "streamer id is required")
					return
				}

				switch action {
				case "status":
					if r.Method != http.MethodGet {
						w.WriteHeader(http.StatusMethodNotAllowed)
						return
					}
					writeJSON(w, http.StatusOK, streamersService.GetLLMStatus(r.Context(), streamerID))
				case "tracking":
					if r.Method != http.MethodDelete {
						w.WriteHeader(http.StatusMethodNotAllowed)
						return
					}
					if err := streamersService.StopTracking(r.Context(), streamerID); err != nil {
						if errors.Is(err, streamers.ErrNotFound) {
							writeError(w, http.StatusNotFound, err.Error())
							return
						}
						logger.Error("failed to stop streamer tracking", zap.String("streamerID", streamerID), zap.Error(err))
						writeError(w, http.StatusInternalServerError, "failed to stop streamer tracking")
						return
					}
					writeJSON(w, http.StatusOK, streamersService.GetLLMStatus(r.Context(), streamerID))
				default:
					writeError(w, http.StatusNotFound, "streamer route not found")
				}
			})))

			videoManager, _ := streamerVideoManager.(adminStreamerVideoManager)
			mux.Handle("/api/admin/streamers/", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !requireAdmin(w, r, adminService) {
					writeError(w, http.StatusForbidden, "admin role is required")
					return
				}
				path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/streamers/"), "/")
				parts := strings.Split(path, "/")
				if len(parts) != 2 || strings.TrimSpace(parts[1]) != "llm-history" {
					writeError(w, http.StatusNotFound, "admin streamer route not found")
					return
				}
				streamerID := strings.TrimSpace(parts[0])
				if streamerID == "" {
					writeError(w, http.StatusBadRequest, "streamer id is required")
					return
				}
				switch r.Method {
				case http.MethodGet:
					page := parsePositiveIntDefault(r.URL.Query().Get("page"), 1)
					pageSize := parsePositiveIntDefault(r.URL.Query().Get("pageSize"), 20)
					items, total := streamersService.ListLLMDecisionsPage(r.Context(), streamerID, page, pageSize)
					events := make([]adminHistoryEvent, 0, len(items))
					for _, item := range items {
						eventTime := firstNonEmpty(item.CreatedAt, item.ChunkCapturedAt)
						events = append(events, adminHistoryEvent{
							EventTime:        eventTime,
							StepName:         item.Stage,
							LLMResponse:      item.Label,
							GlobalStateDelta: item.UpdatedStateJSON,
							Confidence:       item.Confidence,
							LLMDecision:      item,
						})
					}
					videos := []media.UploadedVideo{}
					if videoManager != nil {
						videos = videoManager.ListUploadedVideos(streamerID)
					}
					writeJSON(w, http.StatusOK, adminStreamerLLMHistoryResponse{
						StreamerID: streamerID,
						Page:       page,
						PageSize:   pageSize,
						Total:      total,
						Items:      events,
						Videos:     videos,
					})
				case http.MethodDelete:
					deletedDecisions := streamersService.ClearLLMHistory(r.Context(), streamerID)
					deletedVideos := 0
					if videoManager != nil {
						count, err := videoManager.DeleteStreamerVideos(r.Context(), streamerID)
						if err != nil {
							logger.Error("failed to delete bunny videos for streamer history cleanup", zap.String("streamerID", streamerID), zap.Error(err))
							writeError(w, http.StatusBadGateway, "failed to delete bunny videos")
							return
						}
						deletedVideos = count
					}
					writeJSON(w, http.StatusOK, adminStreamerHistoryDeleteResponse{
						StreamerID:        streamerID,
						DeletedDecisions:  deletedDecisions,
						DeletedBunnyVideo: deletedVideos,
					})
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			})))
		}

		if gamesService != nil {
			mux.Handle("/api/admin/games", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !requireAdmin(w, r, adminService) {
					writeError(w, http.StatusForbidden, "admin role is required")
					return
				}

				switch r.Method {
				case http.MethodGet:
					writeJSON(w, http.StatusOK, gamesService.List(r.Context()))
				case http.MethodPost:
					defer r.Body.Close() //nolint:errcheck
					body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					if err != nil {
						writeError(w, http.StatusBadRequest, "failed to read request body")
						return
					}
					var req gameUpsertRequest
					if err := json.Unmarshal(body, &req); err != nil {
						writeError(w, http.StatusBadRequest, "invalid request body")
						return
					}
					created, err := gamesService.Create(r.Context(), games.UpsertRequest{
						Slug:        req.Slug,
						Title:       req.Title,
						Description: req.Description,
						Rules:       req.Rules,
						Status:      req.Status,
					})
					if err != nil {
						switch {
						case errors.Is(err, games.ErrInvalidSlug), errors.Is(err, games.ErrInvalidTitle), errors.Is(err, games.ErrInvalidStatus), errors.Is(err, games.ErrDuplicateSlug):
							writeError(w, http.StatusBadRequest, err.Error())
						default:
							logger.Error("failed to create game", zap.Error(err))
							writeError(w, http.StatusInternalServerError, "failed to create game")
						}
						return
					}
					writeJSON(w, http.StatusCreated, created)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			})))

			mux.Handle("/api/admin/games/", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !requireAdmin(w, r, adminService) {
					writeError(w, http.StatusForbidden, "admin role is required")
					return
				}

				gameID := strings.TrimPrefix(r.URL.Path, "/api/admin/games/")
				if strings.TrimSpace(gameID) == "" || strings.Contains(gameID, "/") {
					writeError(w, http.StatusBadRequest, "game id is required")
					return
				}

				switch r.Method {
				case http.MethodPut:
					defer r.Body.Close() //nolint:errcheck
					body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					if err != nil {
						writeError(w, http.StatusBadRequest, "failed to read request body")
						return
					}
					var req gameUpsertRequest
					if err := json.Unmarshal(body, &req); err != nil {
						writeError(w, http.StatusBadRequest, "invalid request body")
						return
					}
					updated, err := gamesService.Update(r.Context(), gameID, games.UpsertRequest{
						Slug:        req.Slug,
						Title:       req.Title,
						Description: req.Description,
						Rules:       req.Rules,
						Status:      req.Status,
					})
					if err != nil {
						switch {
						case errors.Is(err, games.ErrInvalidID), errors.Is(err, games.ErrInvalidSlug), errors.Is(err, games.ErrInvalidTitle), errors.Is(err, games.ErrInvalidStatus), errors.Is(err, games.ErrDuplicateSlug):
							writeError(w, http.StatusBadRequest, err.Error())
						case errors.Is(err, games.ErrNotFound):
							writeError(w, http.StatusNotFound, err.Error())
						default:
							logger.Error("failed to update game", zap.Error(err))
							writeError(w, http.StatusInternalServerError, "failed to update game")
						}
						return
					}
					writeJSON(w, http.StatusOK, updated)
				case http.MethodDelete:
					err := gamesService.Delete(r.Context(), gameID)
					if err != nil {
						if errors.Is(err, games.ErrInvalidID) {
							writeError(w, http.StatusBadRequest, err.Error())
							return
						}
						if errors.Is(err, games.ErrNotFound) {
							writeError(w, http.StatusNotFound, err.Error())
							return
						}
						logger.Error("failed to delete game", zap.Error(err))
						writeError(w, http.StatusInternalServerError, "failed to delete game")
						return
					}
					w.WriteHeader(http.StatusNoContent)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			})))
		}

		if promptsService != nil {
			mux.Handle("/api/admin/llm/model-configs", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				claims, ok := auth.ClaimsFromContext(r.Context())
				if !ok {
					writeError(w, http.StatusUnauthorized, "missing auth claims")
					return
				}
				if !requireAdmin(w, r, adminService) {
					writeError(w, http.StatusForbidden, "admin role is required")
					return
				}
				switch r.Method {
				case http.MethodGet:
					items, err := promptsService.ListLLMModelConfigs(r.Context())
					if err != nil {
						logger.Error("failed to list llm model configs", zap.Error(err))
						writeError(w, http.StatusInternalServerError, "failed to list llm model configs")
						return
					}
					writeJSON(w, http.StatusOK, items)
				case http.MethodPost:
					defer r.Body.Close() //nolint:errcheck
					body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					if err != nil {
						writeError(w, http.StatusBadRequest, "failed to read request body")
						return
					}
					var req llmModelConfigUpsertRequest
					if err := json.Unmarshal(body, &req); err != nil {
						writeError(w, http.StatusBadRequest, "invalid request body")
						return
					}
					created, err := promptsService.CreateLLMModelConfig(r.Context(), prompts.LLMModelConfigUpsertRequest{
						Name:          req.Name,
						Model:         req.Model,
						MetadataJSON:  req.MetadataJSON,
						Temperature:   req.Temperature,
						MaxTokens:     req.MaxTokens,
						TimeoutMS:     req.TimeoutMS,
						RetryCount:    req.RetryCount,
						BackoffMS:     req.BackoffMS,
						CooldownMS:    req.CooldownMS,
						MinConfidence: req.MinConfidence,
						ActorID:       claims.Subject,
					})
					if err != nil {
						writeError(w, http.StatusBadRequest, err.Error())
						return
					}
					writeJSON(w, http.StatusCreated, created)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			})))

			mux.Handle("/api/admin/llm/model-configs/", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				claims, ok := auth.ClaimsFromContext(r.Context())
				if !ok {
					writeError(w, http.StatusUnauthorized, "missing auth claims")
					return
				}
				if !requireAdmin(w, r, adminService) {
					writeError(w, http.StatusForbidden, "admin role is required")
					return
				}
				path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/llm/model-configs/"), "/")
				if path == "" {
					writeError(w, http.StatusBadRequest, "llm model config id is required")
					return
				}
				if strings.HasSuffix(path, "/activate") {
					id := strings.Trim(strings.TrimSuffix(path, "/activate"), "/")
					if r.Method != http.MethodPost {
						w.WriteHeader(http.StatusMethodNotAllowed)
						return
					}
					item, err := promptsService.ActivateLLMModelConfig(r.Context(), id, claims.Subject)
					if err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrLLMModelConfigNotFound) {
							status = http.StatusNotFound
						}
						writeError(w, status, err.Error())
						return
					}
					writeJSON(w, http.StatusOK, item)
					return
				}
				switch r.Method {
				case http.MethodPut:
					defer r.Body.Close() //nolint:errcheck
					body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					if err != nil {
						writeError(w, http.StatusBadRequest, "failed to read request body")
						return
					}
					var req llmModelConfigUpsertRequest
					if err := json.Unmarshal(body, &req); err != nil {
						writeError(w, http.StatusBadRequest, "invalid request body")
						return
					}
					item, err := promptsService.UpdateLLMModelConfig(r.Context(), path, prompts.LLMModelConfigUpsertRequest{
						Name:          req.Name,
						Model:         req.Model,
						MetadataJSON:  req.MetadataJSON,
						Temperature:   req.Temperature,
						MaxTokens:     req.MaxTokens,
						TimeoutMS:     req.TimeoutMS,
						RetryCount:    req.RetryCount,
						BackoffMS:     req.BackoffMS,
						CooldownMS:    req.CooldownMS,
						MinConfidence: req.MinConfidence,
						ActorID:       claims.Subject,
					})
					if err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrLLMModelConfigNotFound) {
							status = http.StatusNotFound
						}
						writeError(w, status, err.Error())
						return
					}
					writeJSON(w, http.StatusOK, item)
				case http.MethodDelete:
					if err := promptsService.DeleteLLMModelConfig(r.Context(), path); err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrLLMModelConfigNotFound) {
							status = http.StatusNotFound
						}
						writeError(w, status, err.Error())
						return
					}
					w.WriteHeader(http.StatusNoContent)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			})))

			mux.Handle("/api/admin/llm/scenario-packages", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				claims, ok := auth.ClaimsFromContext(r.Context())
				if !ok {
					writeError(w, http.StatusUnauthorized, "missing auth claims")
					return
				}
				if !requireAdmin(w, r, adminService) {
					writeError(w, http.StatusForbidden, "admin role is required")
					return
				}
				switch r.Method {
				case http.MethodGet:
					writeJSON(w, http.StatusOK, promptsService.ListScenarioPackages(r.Context()))
				case http.MethodPost:
					defer r.Body.Close() //nolint:errcheck
					body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					if err != nil {
						writeError(w, http.StatusBadRequest, "failed to read request body")
						return
					}
					var req scenarioPackageCreateRequest
					if err := decodeJSONStrict(body, &req); err != nil {
						writeError(w, http.StatusBadRequest, "invalid request body")
						return
					}
					created, err := promptsService.CreateScenarioPackage(r.Context(), scenarioPackageRequestToCreateRequest(req, claims.Subject))
					if err != nil {
						writeError(w, http.StatusBadRequest, err.Error())
						return
					}
					writeJSON(w, http.StatusCreated, created)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			})))

			mux.Handle("/api/admin/llm/scenario-packages/", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				claims, ok := auth.ClaimsFromContext(r.Context())
				if !ok {
					writeError(w, http.StatusUnauthorized, "missing auth claims")
					return
				}
				if !requireAdmin(w, r, adminService) {
					writeError(w, http.StatusForbidden, "admin role is required")
					return
				}
				path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/llm/scenario-packages/"), "/")
				if path == "" {
					writeError(w, http.StatusBadRequest, "scenario package id is required")
					return
				}
				if strings.HasSuffix(path, "/activate") {
					id := strings.Trim(strings.TrimSuffix(path, "/activate"), "/")
					if r.Method != http.MethodPost {
						w.WriteHeader(http.StatusMethodNotAllowed)
						return
					}
					item, err := promptsService.ActivateScenarioPackage(r.Context(), id, claims.Subject)
					if err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrScenarioPackageNotFound) {
							status = http.StatusNotFound
						}
						writeError(w, status, err.Error())
						return
					}
					writeJSON(w, http.StatusOK, item)
					return
				}
				if strings.HasSuffix(path, "/graph") {
					id := strings.Trim(strings.TrimSuffix(path, "/graph"), "/")
					if r.Method != http.MethodGet {
						w.WriteHeader(http.StatusMethodNotAllowed)
						return
					}
					item, err := promptsService.GetScenarioPackage(r.Context(), id)
					if err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrScenarioPackageNotFound) {
							status = http.StatusNotFound
						}
						writeError(w, status, err.Error())
						return
					}
					writeJSON(w, http.StatusOK, item.BuildVisualGraph())
					return
				}
				switch r.Method {
				case http.MethodGet:
					item, err := promptsService.GetScenarioPackage(r.Context(), path)
					if err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrScenarioPackageNotFound) {
							status = http.StatusNotFound
						}
						writeError(w, status, err.Error())
						return
					}
					writeJSON(w, http.StatusOK, item)
				case http.MethodPut:
					defer r.Body.Close() //nolint:errcheck
					body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					if err != nil {
						writeError(w, http.StatusBadRequest, "failed to read request body")
						return
					}
					var req scenarioPackageCreateRequest
					if err := decodeJSONStrict(body, &req); err != nil {
						writeError(w, http.StatusBadRequest, "invalid request body")
						return
					}
					item, err := promptsService.UpdateScenarioPackage(r.Context(), path, scenarioPackageRequestToCreateRequest(req, claims.Subject))
					if err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrScenarioPackageNotFound) {
							status = http.StatusNotFound
						}
						writeError(w, status, err.Error())
						return
					}
					writeJSON(w, http.StatusOK, item)
				case http.MethodDelete:
					if err := promptsService.DeleteScenarioPackage(r.Context(), path); err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrScenarioPackageNotFound) {
							status = http.StatusNotFound
						}
						writeError(w, status, err.Error())
						return
					}
					w.WriteHeader(http.StatusNoContent)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			})))

			mux.Handle("/api/admin/llm/game-scenarios", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				claims, ok := auth.ClaimsFromContext(r.Context())
				if !ok {
					writeError(w, http.StatusUnauthorized, "missing auth claims")
					return
				}
				if !requireAdmin(w, r, adminService) {
					writeError(w, http.StatusForbidden, "admin role is required")
					return
				}
				switch r.Method {
				case http.MethodGet:
					writeJSON(w, http.StatusOK, promptsService.ListGameScenarios(r.Context()))
				case http.MethodPost:
					defer r.Body.Close() //nolint:errcheck
					body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					if err != nil {
						writeError(w, http.StatusBadRequest, "failed to read request body")
						return
					}
					var req gameScenarioCreateRequest
					if err := decodeJSONStrict(body, &req); err != nil {
						writeError(w, http.StatusBadRequest, "invalid request body")
						return
					}
					created, err := promptsService.CreateGameScenario(r.Context(), gameScenarioRequestToCreateRequest(req, claims.Subject))
					if err != nil {
						writeError(w, http.StatusBadRequest, err.Error())
						return
					}
					writeJSON(w, http.StatusCreated, created)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			})))

			mux.Handle("/api/admin/llm/game-scenarios/", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				claims, ok := auth.ClaimsFromContext(r.Context())
				if !ok {
					writeError(w, http.StatusUnauthorized, "missing auth claims")
					return
				}
				if !requireAdmin(w, r, adminService) {
					writeError(w, http.StatusForbidden, "admin role is required")
					return
				}
				path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/llm/game-scenarios/"), "/")
				if path == "" {
					writeError(w, http.StatusBadRequest, "game scenario id is required")
					return
				}
				if strings.HasSuffix(path, "/activate") {
					id := strings.Trim(strings.TrimSuffix(path, "/activate"), "/")
					if r.Method != http.MethodPost {
						w.WriteHeader(http.StatusMethodNotAllowed)
						return
					}
					item, err := promptsService.ActivateGameScenario(r.Context(), id, claims.Subject)
					if err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrGameScenarioNotFound) {
							status = http.StatusNotFound
						}
						writeError(w, status, err.Error())
						return
					}
					writeJSON(w, http.StatusOK, item)
					return
				}
				switch r.Method {
				case http.MethodGet:
					item, err := promptsService.GetGameScenario(r.Context(), path)
					if err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrGameScenarioNotFound) {
							status = http.StatusNotFound
						}
						writeError(w, status, err.Error())
						return
					}
					writeJSON(w, http.StatusOK, item)
				case http.MethodPut:
					defer r.Body.Close() //nolint:errcheck
					body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					if err != nil {
						writeError(w, http.StatusBadRequest, "failed to read request body")
						return
					}
					var req gameScenarioCreateRequest
					if err := decodeJSONStrict(body, &req); err != nil {
						writeError(w, http.StatusBadRequest, "invalid request body")
						return
					}
					item, err := promptsService.UpdateGameScenario(r.Context(), path, gameScenarioRequestToCreateRequest(req, claims.Subject))
					if err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrGameScenarioNotFound) {
							status = http.StatusNotFound
						}
						writeError(w, status, err.Error())
						return
					}
					writeJSON(w, http.StatusOK, item)
				case http.MethodDelete:
					if err := promptsService.DeleteGameScenario(r.Context(), path); err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrGameScenarioNotFound) {
							status = http.StatusNotFound
						}
						writeError(w, status, err.Error())
						return
					}
					w.WriteHeader(http.StatusNoContent)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			})))

		}

		if eventsService != nil {
			mux.Handle("/api/events/live", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				streamerID := strings.TrimSpace(r.URL.Query().Get("streamerId"))
				if streamerID == "" {
					writeError(w, http.StatusBadRequest, "streamerId is required")
					return
				}
				writeJSON(w, http.StatusOK, eventsService.ListLiveByStreamer(r.Context(), streamerID))
			})))
			mux.Handle("/api/events/history", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				claims, ok := auth.ClaimsFromContext(r.Context())
				if !ok {
					writeError(w, http.StatusUnauthorized, "missing auth claims")
					return
				}
				items := eventsService.ListUserHistory(r.Context(), claims.Subject)
				grouped := make([]userEventHistoryStreamerItem, 0, len(items))
				indexByStreamer := make(map[string]int)
				for _, item := range items {
					idx, exists := indexByStreamer[item.StreamerID]
					if !exists {
						entry := userEventHistoryStreamerItem{
							DateTime:         item.CreatedAt,
							StreamerID:       item.StreamerID,
							StreamerNickname: item.StreamerID,
							Details:          []events.UserEventHistoryItem{},
						}
						if streamersService != nil {
							if streamer, found := streamersService.GetByID(r.Context(), item.StreamerID); found {
								entry.StreamerNickname = streamer.TwitchNickname
								entry.StreamerIconURL = streamer.MiniIconURL
							}
						}
						grouped = append(grouped, entry)
						idx = len(grouped) - 1
						indexByStreamer[item.StreamerID] = idx
					}
					grouped[idx].Details = append(grouped[idx].Details, item)
					if item.WinAmountINT != nil {
						grouped[idx].NetAmountINT += *item.WinAmountINT - item.AmountINT
					} else {
						grouped[idx].NetAmountINT -= item.AmountINT
					}
				}
				writeJSON(w, http.StatusOK, grouped)
			})))

			mux.Handle("/api/events/", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				path := strings.TrimPrefix(r.URL.Path, "/api/events/")
				if !strings.HasSuffix(path, "/vote") {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				eventID := strings.TrimSuffix(path, "/vote")
				eventID = strings.TrimSuffix(eventID, "/")
				eventID = strings.TrimSpace(eventID)
				if eventID == "" {
					writeError(w, http.StatusBadRequest, "event id is required")
					return
				}
				claims, ok := auth.ClaimsFromContext(r.Context())
				if !ok {
					writeError(w, http.StatusUnauthorized, "missing auth claims")
					return
				}
				idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
				if idempotencyKey == "" {
					writeError(w, http.StatusBadRequest, wallet.ErrIdempotencyRequired.Error())
					return
				}
				defer r.Body.Close() //nolint:errcheck
				body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				if err != nil {
					writeError(w, http.StatusBadRequest, "failed to read request body")
					return
				}
				var req eventVoteRequest
				if err := decodeJSONStrict(body, &req); err != nil {
					writeError(w, http.StatusBadRequest, "invalid request body")
					return
				}
				walletEntry, balanceAfterVote, err := walletService.Post(wallet.PostRequest{
					UserID:         claims.Subject,
					Type:           wallet.EntryTypeDebit,
					Amount:         req.AmountINT,
					Reason:         "event_vote",
					IdempotencyKey: idempotencyKey,
					ActorID:        claims.Subject,
				})
				if err != nil {
					switch {
					case errors.Is(err, wallet.ErrInvalidAmount), errors.Is(err, wallet.ErrIdempotencyRequired):
						writeError(w, http.StatusBadRequest, err.Error())
					case errors.Is(err, wallet.ErrInsufficientFunds):
						writeError(w, http.StatusConflict, err.Error())
					default:
						logger.Error("failed to debit wallet for vote", zap.String("userID", claims.Subject), zap.Error(err))
						writeError(w, http.StatusInternalServerError, "failed to process vote")
					}
					return
				}
				event, err := eventsService.Vote(r.Context(), events.VoteRequest{
					EventID:        eventID,
					StreamerID:     req.StreamerID,
					UserID:         claims.Subject,
					OptionID:       req.OptionID,
					Amount:         req.AmountINT,
					IdempotencyKey: idempotencyKey,
					WalletLedgerID: strings.TrimSpace(walletEntry.ID),
				})
				if err != nil {
					switch {
					case errors.Is(err, events.ErrInvalidVote):
						writeError(w, http.StatusBadRequest, err.Error())
					case errors.Is(err, events.ErrEventNotFound):
						writeError(w, http.StatusNotFound, err.Error())
					case errors.Is(err, events.ErrEventClosed):
						writeError(w, http.StatusConflict, err.Error())
					default:
						logger.Error("failed to process event vote", zap.String("eventID", eventID), zap.Error(err))
						writeError(w, http.StatusInternalServerError, "failed to process vote")
					}
					return
				}
				nickname := claims.Subject
				if userService != nil {
					if profile, err := userService.GetByID(r.Context(), claims.Subject); err == nil && strings.TrimSpace(profile.Nickname) != "" {
						nickname = profile.Nickname
					}
				}
				rtHub.PublishToStreamer(req.StreamerID, realtime.Envelope{
					Type: "EVENT_UPDATED",
					Payload: realtime.EventUpdatedPayload{
						EventID:  event.ID,
						Totals:   event.Totals,
						ClosesAt: event.ClosesAt,
					},
				})
				rtHub.PublishToStreamer(req.StreamerID, realtime.Envelope{
					Type: "EVENT_VOTE_FEED_UPDATED",
					Payload: realtime.VoteFeedPayload{
						EventID: event.ID,
						Items: []realtime.VoteFeedItem{{
							UserID:    claims.Subject,
							Nickname:  nickname,
							OptionID:  req.OptionID,
							AmountINT: req.AmountINT,
							CreatedAt: realtime.NowRFC3339(),
						}},
						SnapshotAt: realtime.NowRFC3339(),
					},
				})
				var myCoefficient float64
				var myPotentialWinINT int64
				for _, item := range eventsService.ListUserHistory(r.Context(), claims.Subject) {
					if item.EventID == event.ID {
						myCoefficient = item.Coefficient
						myPotentialWinINT = item.PotentialWinINT
						break
					}
				}
				rtHub.PublishToUser(claims.Subject, realtime.Envelope{
					Type: "BALANCE_UPDATED",
					Payload: map[string]int64{
						"balance": balanceAfterVote,
					},
				})
				rtHub.PublishToUser(claims.Subject, realtime.Envelope{
					Type: "USER_BET_UPDATED",
					Payload: map[string]any{
						"eventId":           event.ID,
						"myBetTotalINT":     event.UserVote.TotalAmount,
						"myOptionId":        event.UserVote.OptionID,
						"myCoefficient":     myCoefficient,
						"myPotentialWinINT": myPotentialWinINT,
						"updatedAt":         realtime.NowRFC3339(),
					},
				})
				writeJSON(w, http.StatusOK, event)
			})))
		}
	}

	return mux
}

func requireAdmin(w http.ResponseWriter, r *http.Request, adminService *admin.Service) bool {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing auth claims")
		return false
	}

	if adminService == nil || !adminService.IsAdmin(claims.Subject) {
		return false
	}

	return true
}

func withUserBanGuard(base func(http.Handler) http.Handler, userService *users.Service) func(http.Handler) http.Handler {
	if userService == nil {
		return base
	}
	return func(next http.Handler) http.Handler {
		return base(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := auth.ClaimsFromContext(r.Context())
			if !ok || strings.TrimSpace(claims.Subject) == "" {
				next.ServeHTTP(w, r)
				return
			}
			profile, err := userService.GetByID(r.Context(), claims.Subject)
			if err != nil {
				if errors.Is(err, users.ErrNotFound) {
					next.ServeHTTP(w, r)
					return
				}
				writeError(w, http.StatusInternalServerError, "failed to load user profile")
				return
			}
			if profile.IsAccessBlocked(time.Now().UTC()) {
				writeError(w, http.StatusForbidden, "user is banned")
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// Best effort error response when serialization fails.
		http.Error(w, "{\"status\":\"error\"}", http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"error":     message,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func decodeJSONStrict(body []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	return decoder.Decode(out)
}

func parsePositiveIntDefault(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
