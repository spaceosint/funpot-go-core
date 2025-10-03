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
	"github.com/funpot/funpot-go-core/internal/users"
)

type readinessState struct {
	Status string `json:"status"`
	Time   string `json:"time"`
}

type telegramAuthRequest struct {
	InitData string `json:"initData"`
}

type configResponse struct {
	Features map[string]bool `json:"features"`
}

// NewHandler wires the base HTTP routes for the service.
func NewHandler(logger *zap.Logger, readyFn func() bool, metricsHandler http.Handler, authService *auth.Service, userService *users.Service, featureFlags map[string]bool) http.Handler {
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
			writeJSON(w, http.StatusOK, configResponse{Features: featureFlags})
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
