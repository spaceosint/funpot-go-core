package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/auth"
	"github.com/funpot/funpot-go-core/internal/events"
	"github.com/funpot/funpot-go-core/internal/games"
	"github.com/funpot/funpot-go-core/internal/media"
	"github.com/funpot/funpot-go-core/internal/payments"
	"github.com/funpot/funpot-go-core/internal/referrals"
	"github.com/funpot/funpot-go-core/internal/streamers"
	"github.com/funpot/funpot-go-core/internal/users"
	"github.com/funpot/funpot-go-core/internal/votes"
	"github.com/funpot/funpot-go-core/internal/wallet"
)

type readinessState struct {
	Status string `json:"status"`
	Time   string `json:"time"`
}

type telegramAuthRequest struct {
	InitData string `json:"initData"`
}

type configResponse struct {
	StarsRate  float64                `json:"starsRate"`
	MinViewers int                    `json:"minViewers"`
	Features   configFeaturesResponse `json:"features"`
	Currencies []string               `json:"currencies"`
	Limits     configLimitsResponse   `json:"limits"`
}

type configFeaturesResponse struct {
	PaymentsEnabled  bool `json:"paymentsEnabled"`
	ReferralsEnabled bool `json:"referralsEnabled"`
	MediaEnabled     bool `json:"mediaEnabled"`
	AdminEnabled     bool `json:"adminEnabled"`
}

type configLimitsResponse struct {
	VotePerMin int `json:"votePerMin"`
}

type createStreamerRequest struct {
	TwitchUsername string `json:"twitchUsername"`
}

type createInvoiceRequest struct {
	AmountINT int `json:"amountINT"`
}

type castVoteRequest struct {
	EventID  string `json:"eventId"`
	OptionID string `json:"optionId"`
	Cost     int    `json:"cost"`
}

// NewHandler wires the base HTTP routes for the service.
func NewHandler(logger *zap.Logger, readyFn func() bool, metricsHandler http.Handler, authService *auth.Service, userService *users.Service, featureFlags map[string]bool, streamerService *streamers.Service, gamesService *games.Service, eventsService *events.Service, votesService *votes.Service, walletService *wallet.Service, paymentsService *payments.Service, referralsService *referrals.Service, mediaService *media.Service) http.Handler {
	mux := http.NewServeMux()

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
				status := http.StatusBadRequest
				switch {
				case errors.Is(err, auth.ErrInvalidHash), errors.Is(err, auth.ErrExpired):
					status = http.StatusUnauthorized
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

		authed := authService.ClaimsMiddleware()

		mux.Handle("/api/me", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			claims, ok := auth.ClaimsFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "missing auth claims")
				return
			}
			profile, err := userService.GetByTelegramID(r.Context(), claims.TelegramID)
			if err != nil {
				if errors.Is(err, users.ErrNotFound) {
					writeError(w, http.StatusNotFound, "user not found")
					return
				}
				writeError(w, http.StatusInternalServerError, "failed to load profile")
				return
			}
			writeJSON(w, http.StatusOK, profile)
		})))

		mux.Handle("/api/config", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			writeJSON(w, http.StatusOK, configResponse{
				StarsRate:  1,
				MinViewers: 10,
				Features: configFeaturesResponse{
					PaymentsEnabled:  featureFlags["payments"],
					ReferralsEnabled: featureFlags["referrals"],
					MediaEnabled:     featureFlags["media"],
					AdminEnabled:     featureFlags["admin"],
				},
				Currencies: []string{"INT"},
				Limits: configLimitsResponse{
					VotePerMin: 30,
				},
			})
		})))

		mux.Handle("/api/streamers", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if streamerService == nil {
				writeError(w, http.StatusServiceUnavailable, "streamers service requires configured database")
				return
			}
			switch r.Method {
			case http.MethodGet:
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				items, err := streamerService.List(r.Context(), r.URL.Query().Get("query"), page, 20)
				if err != nil {
					writeError(w, http.StatusInternalServerError, "failed to list streamers")
					return
				}
				writeJSON(w, http.StatusOK, items)
			case http.MethodPost:
				defer r.Body.Close() //nolint:errcheck
				var req createStreamerRequest
				if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
					writeError(w, http.StatusBadRequest, "invalid request body")
					return
				}
				if req.TwitchUsername == "" {
					writeError(w, http.StatusBadRequest, "twitchUsername is required")
					return
				}
				submission, err := streamerService.Create(r.Context(), req.TwitchUsername)
				if err != nil {
					writeError(w, http.StatusInternalServerError, "failed to create streamer")
					return
				}
				writeJSON(w, http.StatusOK, submission)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		})))

		mux.Handle("/api/games", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if gamesService == nil {
				writeError(w, http.StatusServiceUnavailable, "games service requires configured database")
				return
			}
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			streamerID := r.URL.Query().Get("streamerId")
			if streamerID == "" {
				writeError(w, http.StatusBadRequest, "streamerId is required")
				return
			}
			items, err := gamesService.ListByStreamer(r.Context(), streamerID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to list games")
				return
			}
			writeJSON(w, http.StatusOK, items)
		})))

		mux.Handle("/api/events/live", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if eventsService == nil {
				writeError(w, http.StatusServiceUnavailable, "events service requires configured database")
				return
			}
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			streamerID := r.URL.Query().Get("streamerId")
			if streamerID == "" {
				writeError(w, http.StatusBadRequest, "streamerId is required")
				return
			}
			claims, ok := auth.ClaimsFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "missing auth claims")
				return
			}
			items, err := eventsService.ListLive(r.Context(), streamerID, claims.TelegramID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load live events")
				return
			}
			writeJSON(w, http.StatusOK, items)
		})))

		mux.Handle("/api/votes", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if votesService == nil {
				writeError(w, http.StatusServiceUnavailable, "votes service requires configured database")
				return
			}
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if r.Header.Get("Idempotency-Key") == "" {
				writeError(w, http.StatusBadRequest, "Idempotency-Key header is required")
				return
			}
			defer r.Body.Close() //nolint:errcheck
			var req castVoteRequest
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if req.EventID == "" || req.OptionID == "" || req.Cost < 0 {
				writeError(w, http.StatusBadRequest, "eventId, optionId and non-negative cost are required")
				return
			}
			claims, ok := auth.ClaimsFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "missing auth claims")
				return
			}
			resp, err := votesService.Cast(r.Context(), claims.TelegramID, r.Header.Get("Idempotency-Key"), votes.CastRequest{EventID: req.EventID, Option: req.OptionID, Cost: req.Cost})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to cast vote")
				return
			}
			writeJSON(w, http.StatusOK, resp)
		})))

		mux.Handle("/api/wallet", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if walletService == nil {
				writeError(w, http.StatusServiceUnavailable, "wallet service requires configured database")
				return
			}
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			claims, ok := auth.ClaimsFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "missing auth claims")
				return
			}
			snapshot, err := walletService.GetByTelegramID(r.Context(), claims.TelegramID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load wallet")
				return
			}
			writeJSON(w, http.StatusOK, snapshot)
		})))

		mux.Handle("/api/payments/stars/createInvoice", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if paymentsService == nil {
				writeError(w, http.StatusServiceUnavailable, "payments service requires configured database")
				return
			}
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			defer r.Body.Close() //nolint:errcheck
			var req createInvoiceRequest
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if req.AmountINT < 1 {
				writeError(w, http.StatusBadRequest, "amountINT must be greater than zero")
				return
			}
			claims, ok := auth.ClaimsFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "missing auth claims")
				return
			}
			invoice, err := paymentsService.CreateStarsInvoice(r.Context(), claims.TelegramID, req.AmountINT)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to create invoice")
				return
			}
			writeJSON(w, http.StatusOK, invoice)
		})))

		mux.Handle("/api/referrals/summary", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if referralsService == nil {
				writeError(w, http.StatusServiceUnavailable, "referrals service requires configured database")
				return
			}
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			claims, ok := auth.ClaimsFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "missing auth claims")
				return
			}
			summary, err := referralsService.GetSummary(r.Context(), claims.TelegramID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load referral summary")
				return
			}
			writeJSON(w, http.StatusOK, summary)
		})))

		mux.Handle("/api/referrals/payouts", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if referralsService == nil {
				writeError(w, http.StatusServiceUnavailable, "referrals service requires configured database")
				return
			}
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			claims, ok := auth.ClaimsFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "missing auth claims")
				return
			}
			items, err := referralsService.ListPayouts(r.Context(), claims.TelegramID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load referral payouts")
				return
			}
			writeJSON(w, http.StatusOK, items)
		})))

		mux.Handle("/api/media/clips", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if mediaService == nil {
				writeError(w, http.StatusServiceUnavailable, "media service requires configured database")
				return
			}
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			streamerID := r.URL.Query().Get("streamerId")
			if streamerID == "" {
				writeError(w, http.StatusBadRequest, "streamerId is required")
				return
			}
			items, err := mediaService.ListClips(r.Context(), streamerID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load media clips")
				return
			}
			writeJSON(w, http.StatusOK, items)
		})))
	}

	return mux
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
