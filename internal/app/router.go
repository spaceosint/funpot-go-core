package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/auth"
	"github.com/funpot/funpot-go-core/internal/config"
	"github.com/funpot/funpot-go-core/internal/events"
	"github.com/funpot/funpot-go-core/internal/streamers"
	"github.com/funpot/funpot-go-core/internal/users"
)

type readinessState struct {
	Status string `json:"status"`
	Time   string `json:"time"`
}

type telegramAuthRequest struct {
	InitData string `json:"initData"`
}

type ClientConfigResponse struct {
	StarsRate  float64         `json:"starsRate"`
	MinViewers int             `json:"minViewers"`
	Features   map[string]bool `json:"features"`
	Currencies []string        `json:"currencies"`
	Limits     configLimits    `json:"limits"`
}

func ConfigResponseFromConfig(cfg config.Config) ClientConfigResponse {
	return ClientConfigResponse{
		StarsRate:  cfg.Client.StarsRate,
		MinViewers: cfg.Client.MinViewers,
		Features:   cfg.Features.Flags,
		Currencies: cfg.Client.Currencies,
		Limits: configLimits{
			VotePerMin: cfg.Client.VotePerMin,
		},
	}
}

type configLimits struct {
	VotePerMin int `json:"votePerMin"`
}

type streamerSubmitRequest struct {
	TwitchUsername string `json:"twitchUsername"`
}

// NewHandler wires the base HTTP routes for the service.
func NewHandler(
	logger *zap.Logger,
	readyFn func() bool,
	metricsHandler http.Handler,
	authService *auth.Service,
	userService *users.Service,
	streamersService *streamers.Service,
	eventsService *events.Service,
	clientConfig ClientConfigResponse,
) http.Handler {
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
			writeJSON(w, http.StatusOK, clientConfig)
		})))

		if streamersService != nil {
			mux.Handle("/api/streamers", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					page, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page")))
					if err != nil && r.URL.Query().Get("page") != "" {
						writeError(w, http.StatusBadRequest, "page must be a positive integer")
						return
					}
					items := streamersService.List(r.Context(), r.URL.Query().Get("query"), page)
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
					submission, err := streamersService.Submit(r.Context(), req.TwitchUsername, claims.Subject)
					if err != nil {
						if errors.Is(err, streamers.ErrInvalidUsername) {
							writeError(w, http.StatusBadRequest, err.Error())
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
		}
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
