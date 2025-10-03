package app

import (
	"encoding/json"
	"net/http"
	"time"

	"go.uber.org/zap"
)

type readinessState struct {
	Status string `json:"status"`
	Time   string `json:"time"`
}

// NewHandler wires the base HTTP routes for the service.
func NewHandler(logger *zap.Logger, readyFn func() bool, metricsHandler http.Handler) http.Handler {
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
