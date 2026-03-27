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

	"github.com/funpot/funpot-go-core/internal/admin"
	"github.com/funpot/funpot-go-core/internal/auth"
	"github.com/funpot/funpot-go-core/internal/config"
	"github.com/funpot/funpot-go-core/internal/events"
	"github.com/funpot/funpot-go-core/internal/games"
	"github.com/funpot/funpot-go-core/internal/prompts"
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

type refreshTokenRequest struct {
	RefreshToken string `json:"refreshToken"`
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

func stateSchemaRequestToCreateRequest(req stateSchemaCreateRequest, actorID string) prompts.StateSchemaCreateRequest {
	fields := make([]prompts.StateFieldDefinition, 0, len(req.Fields))
	for _, field := range req.Fields {
		fields = append(fields, prompts.StateFieldDefinition{
			Key:                field.Key,
			Label:              field.Label,
			Description:        field.Description,
			Type:               field.Type,
			EnumValues:         field.EnumValues,
			ConfidenceRequired: field.ConfidenceRequired,
			EvidenceBearing:    field.EvidenceBearing,
			Inferred:           field.Inferred,
			FinalOnly:          field.FinalOnly,
		})
	}
	return prompts.StateSchemaCreateRequest{
		GameSlug:    req.GameSlug,
		Name:        req.Name,
		Description: req.Description,
		Fields:      fields,
		ActorID:     actorID,
	}
}

func ruleSetRequestToCreateRequest(req ruleSetCreateRequest, actorID string) prompts.RuleSetCreateRequest {
	items := make([]prompts.RuleItem, 0, len(req.RuleItems))
	for _, item := range req.RuleItems {
		items = append(items, prompts.RuleItem{
			ID:             item.ID,
			FieldKey:       item.FieldKey,
			Operation:      item.Operation,
			EvidenceKinds:  item.EvidenceKinds,
			ConfidenceMode: item.ConfidenceMode,
			FinalOnly:      item.FinalOnly,
		})
	}
	conditions := make([]prompts.RuleCondition, 0, len(req.FinalizationRules))
	for _, item := range req.FinalizationRules {
		conditions = append(conditions, prompts.RuleCondition{
			ID:          item.ID,
			Priority:    item.Priority,
			Condition:   item.Condition,
			Action:      item.Action,
			TargetField: item.TargetField,
		})
	}
	return prompts.RuleSetCreateRequest{
		GameSlug:          req.GameSlug,
		Name:              req.Name,
		Description:       req.Description,
		RuleItems:         items,
		FinalizationRules: conditions,
		ActorID:           actorID,
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

type promptCreateRequest struct {
	Stage         string  `json:"stage"`
	Position      int     `json:"position"`
	Template      string  `json:"template"`
	Model         string  `json:"model"`
	Temperature   float64 `json:"temperature"`
	MaxTokens     int     `json:"maxTokens"`
	TimeoutMS     int     `json:"timeoutMs"`
	RetryCount    int     `json:"retryCount"`
	BackoffMS     int     `json:"backoffMs"`
	CooldownMS    int     `json:"cooldownMs"`
	MinConfidence float64 `json:"minConfidence"`
}

type llmDecisionRecordRequest struct {
	RunID              string  `json:"runId"`
	Stage              string  `json:"stage"`
	Label              string  `json:"label"`
	Confidence         float64 `json:"confidence"`
	ChunkCapturedAt    string  `json:"chunkCapturedAt"`
	PromptVersionID    string  `json:"promptVersionId"`
	PromptText         string  `json:"promptText"`
	Model              string  `json:"model"`
	Temperature        float64 `json:"temperature"`
	MaxTokens          int     `json:"maxTokens"`
	TimeoutMS          int     `json:"timeoutMs"`
	ChunkRef           string  `json:"chunkRef"`
	RequestRef         string  `json:"requestRef"`
	ResponseRef        string  `json:"responseRef"`
	RawResponse        string  `json:"rawResponse"`
	TokensIn           int     `json:"tokensIn"`
	TokensOut          int     `json:"tokensOut"`
	LatencyMS          int64   `json:"latencyMs"`
	TransitionOutcome  string  `json:"transitionOutcome"`
	TransitionToStep   string  `json:"transitionToStep"`
	TransitionTerminal bool    `json:"transitionTerminal"`
}

type stateFieldRequest struct {
	Key                string   `json:"key"`
	Label              string   `json:"label"`
	Description        string   `json:"description"`
	Type               string   `json:"type"`
	EnumValues         []string `json:"enumValues"`
	ConfidenceRequired bool     `json:"confidenceRequired"`
	EvidenceBearing    bool     `json:"evidenceBearing"`
	Inferred           bool     `json:"inferred"`
	FinalOnly          bool     `json:"finalOnly"`
}

type stateSchemaCreateRequest struct {
	GameSlug    string              `json:"gameSlug"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Fields      []stateFieldRequest `json:"fields"`
}

type ruleItemRequest struct {
	ID             string   `json:"id"`
	FieldKey       string   `json:"fieldKey"`
	Operation      string   `json:"operation"`
	EvidenceKinds  []string `json:"evidenceKinds"`
	ConfidenceMode string   `json:"confidenceMode"`
	FinalOnly      bool     `json:"finalOnly"`
}

type ruleConditionRequest struct {
	ID          string `json:"id"`
	Priority    int    `json:"priority"`
	Condition   string `json:"condition"`
	Action      string `json:"action"`
	TargetField string `json:"targetField"`
}

type ruleSetCreateRequest struct {
	GameSlug          string                 `json:"gameSlug"`
	Name              string                 `json:"name"`
	Description       string                 `json:"description"`
	RuleItems         []ruleItemRequest      `json:"ruleItems"`
	FinalizationRules []ruleConditionRequest `json:"finalizationRules"`
}

type meResponse struct {
	users.Profile
	IsAdmin bool `json:"isAdmin"`
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
	_ any,
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
				var status int
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

		authed := authService.ClaimsMiddleware()

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
			isAdmin := adminService != nil && adminService.IsAdmin(claims.Subject)
			writeJSON(w, http.StatusOK, meResponse{Profile: profile, IsAdmin: isAdmin})
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
						if errors.Is(err, streamers.ErrInvalidUsername) || errors.Is(err, streamers.ErrTwitchUnavailable) {
							writeError(w, http.StatusBadRequest, err.Error())
							return
						}
						if errors.Is(err, streamers.ErrRateLimited) {
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
				case "llm-decisions":
					switch r.Method {
					case http.MethodGet:
						limit, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
						if err != nil && r.URL.Query().Get("limit") != "" {
							writeError(w, http.StatusBadRequest, "limit must be a positive integer")
							return
						}
						if limit < 0 {
							writeError(w, http.StatusBadRequest, "limit must be a positive integer")
							return
						}
						writeJSON(w, http.StatusOK, streamersService.ListLLMDecisions(r.Context(), streamerID, limit))
					case http.MethodPost:
						if !requireAdmin(w, r, adminService) {
							writeError(w, http.StatusForbidden, "admin role is required")
							return
						}
						defer r.Body.Close() //nolint:errcheck
						body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
						if err != nil {
							writeError(w, http.StatusBadRequest, "failed to read request body")
							return
						}
						var req llmDecisionRecordRequest
						if err := json.Unmarshal(body, &req); err != nil {
							writeError(w, http.StatusBadRequest, "invalid request body")
							return
						}
						var chunkCapturedAt time.Time
						if strings.TrimSpace(req.ChunkCapturedAt) != "" {
							parsed, err := time.Parse(time.RFC3339Nano, req.ChunkCapturedAt)
							if err != nil {
								writeError(w, http.StatusBadRequest, "chunkCapturedAt must be RFC3339 timestamp")
								return
							}
							chunkCapturedAt = parsed
						}
						item, err := streamersService.RecordLLMDecision(r.Context(), streamers.RecordDecisionRequest{
							RunID:              req.RunID,
							StreamerID:         streamerID,
							Stage:              req.Stage,
							Label:              req.Label,
							Confidence:         req.Confidence,
							ChunkCapturedAt:    chunkCapturedAt,
							PromptVersionID:    req.PromptVersionID,
							PromptText:         req.PromptText,
							Model:              req.Model,
							Temperature:        req.Temperature,
							MaxTokens:          req.MaxTokens,
							TimeoutMS:          req.TimeoutMS,
							ChunkRef:           req.ChunkRef,
							RequestRef:         req.RequestRef,
							ResponseRef:        req.ResponseRef,
							RawResponse:        req.RawResponse,
							TokensIn:           req.TokensIn,
							TokensOut:          req.TokensOut,
							LatencyMS:          req.LatencyMS,
							TransitionOutcome:  req.TransitionOutcome,
							TransitionToStep:   req.TransitionToStep,
							TransitionTerminal: req.TransitionTerminal,
						})
						if err != nil {
							writeError(w, http.StatusBadRequest, err.Error())
							return
						}
						writeJSON(w, http.StatusCreated, item)
					default:
						w.WriteHeader(http.StatusMethodNotAllowed)
					}
				default:
					writeError(w, http.StatusNotFound, "streamer route not found")
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
			mux.Handle("/api/admin/llm/state-schemas", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					writeJSON(w, http.StatusOK, promptsService.ListStateSchemas(r.Context()))
				case http.MethodPost:
					defer r.Body.Close() //nolint:errcheck
					body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					if err != nil {
						writeError(w, http.StatusBadRequest, "failed to read request body")
						return
					}
					var req stateSchemaCreateRequest
					if err := json.Unmarshal(body, &req); err != nil {
						writeError(w, http.StatusBadRequest, "invalid request body")
						return
					}
					created, err := promptsService.CreateStateSchema(r.Context(), stateSchemaRequestToCreateRequest(req, claims.Subject))
					if err != nil {
						writeError(w, http.StatusBadRequest, err.Error())
						return
					}
					writeJSON(w, http.StatusCreated, created)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			})))

			mux.Handle("/api/admin/llm/state-schemas/", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				claims, ok := auth.ClaimsFromContext(r.Context())
				if !ok {
					writeError(w, http.StatusUnauthorized, "missing auth claims")
					return
				}
				if !requireAdmin(w, r, adminService) {
					writeError(w, http.StatusForbidden, "admin role is required")
					return
				}
				path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/llm/state-schemas/"), "/")
				if path == "" {
					writeError(w, http.StatusBadRequest, "state schema id is required")
					return
				}
				if strings.HasSuffix(path, "/activate") {
					id := strings.Trim(strings.TrimSuffix(path, "/activate"), "/")
					if r.Method != http.MethodPost {
						w.WriteHeader(http.StatusMethodNotAllowed)
						return
					}
					item, err := promptsService.ActivateStateSchema(r.Context(), id, claims.Subject)
					if err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrStateSchemaNotFound) {
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
					item, err := promptsService.GetStateSchema(r.Context(), path)
					if err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrStateSchemaNotFound) {
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
					var req stateSchemaCreateRequest
					if err := json.Unmarshal(body, &req); err != nil {
						writeError(w, http.StatusBadRequest, "invalid request body")
						return
					}
					item, err := promptsService.UpdateStateSchema(r.Context(), path, stateSchemaRequestToCreateRequest(req, claims.Subject))
					if err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrStateSchemaNotFound) {
							status = http.StatusNotFound
						}
						writeError(w, status, err.Error())
						return
					}
					writeJSON(w, http.StatusOK, item)
				case http.MethodDelete:
					if err := promptsService.DeleteStateSchema(r.Context(), path); err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrStateSchemaNotFound) {
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

			mux.Handle("/api/admin/llm/rule-sets", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					writeJSON(w, http.StatusOK, promptsService.ListRuleSets(r.Context()))
				case http.MethodPost:
					defer r.Body.Close() //nolint:errcheck
					body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					if err != nil {
						writeError(w, http.StatusBadRequest, "failed to read request body")
						return
					}
					var req ruleSetCreateRequest
					if err := json.Unmarshal(body, &req); err != nil {
						writeError(w, http.StatusBadRequest, "invalid request body")
						return
					}
					created, err := promptsService.CreateRuleSet(r.Context(), ruleSetRequestToCreateRequest(req, claims.Subject))
					if err != nil {
						writeError(w, http.StatusBadRequest, err.Error())
						return
					}
					writeJSON(w, http.StatusCreated, created)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			})))

			mux.Handle("/api/admin/llm/rule-sets/", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				claims, ok := auth.ClaimsFromContext(r.Context())
				if !ok {
					writeError(w, http.StatusUnauthorized, "missing auth claims")
					return
				}
				if !requireAdmin(w, r, adminService) {
					writeError(w, http.StatusForbidden, "admin role is required")
					return
				}
				path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/llm/rule-sets/"), "/")
				if path == "" {
					writeError(w, http.StatusBadRequest, "rule set id is required")
					return
				}
				if strings.HasSuffix(path, "/activate") {
					id := strings.Trim(strings.TrimSuffix(path, "/activate"), "/")
					if r.Method != http.MethodPost {
						w.WriteHeader(http.StatusMethodNotAllowed)
						return
					}
					item, err := promptsService.ActivateRuleSet(r.Context(), id, claims.Subject)
					if err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrRuleSetNotFound) {
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
					item, err := promptsService.GetRuleSet(r.Context(), path)
					if err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrRuleSetNotFound) {
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
					var req ruleSetCreateRequest
					if err := json.Unmarshal(body, &req); err != nil {
						writeError(w, http.StatusBadRequest, "invalid request body")
						return
					}
					item, err := promptsService.UpdateRuleSet(r.Context(), path, ruleSetRequestToCreateRequest(req, claims.Subject))
					if err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrRuleSetNotFound) {
							status = http.StatusNotFound
						}
						writeError(w, status, err.Error())
						return
					}
					writeJSON(w, http.StatusOK, item)
				case http.MethodDelete:
					if err := promptsService.DeleteRuleSet(r.Context(), path); err != nil {
						status := http.StatusBadRequest
						if errors.Is(err, prompts.ErrRuleSetNotFound) {
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

			mux.Handle("/api/admin/prompts", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					writeJSON(w, http.StatusOK, promptsService.List(r.Context()))
				case http.MethodPost:
					defer r.Body.Close() //nolint:errcheck
					body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					if err != nil {
						writeError(w, http.StatusBadRequest, "failed to read request body")
						return
					}
					var req promptCreateRequest
					if err := json.Unmarshal(body, &req); err != nil {
						writeError(w, http.StatusBadRequest, "invalid request body")
						return
					}
					created, err := promptsService.Create(r.Context(), prompts.CreateRequest{
						Stage:         req.Stage,
						Position:      req.Position,
						Template:      req.Template,
						Model:         req.Model,
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
						switch {
						case errors.Is(err, prompts.ErrInvalidStage),
							errors.Is(err, prompts.ErrInvalidTemplate),
							errors.Is(err, prompts.ErrInvalidModel),
							errors.Is(err, prompts.ErrInvalidTemperature),
							errors.Is(err, prompts.ErrInvalidMaxTokens),
							errors.Is(err, prompts.ErrInvalidTimeoutMS),
							errors.Is(err, prompts.ErrInvalidRetryCount),
							errors.Is(err, prompts.ErrInvalidBackoffMS),
							errors.Is(err, prompts.ErrInvalidCooldownMS),
							errors.Is(err, prompts.ErrInvalidMinConfidence):
							writeError(w, http.StatusBadRequest, err.Error())
						default:
							logger.Error("failed to create prompt", zap.Error(err))
							writeError(w, http.StatusInternalServerError, "failed to create prompt")
						}
						return
					}
					writeJSON(w, http.StatusCreated, created)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			})))

			mux.Handle("/api/admin/prompts/", authed(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				claims, ok := auth.ClaimsFromContext(r.Context())
				if !ok {
					writeError(w, http.StatusUnauthorized, "missing auth claims")
					return
				}
				if !requireAdmin(w, r, adminService) {
					writeError(w, http.StatusForbidden, "admin role is required")
					return
				}

				if !strings.HasSuffix(r.URL.Path, "/activate") {
					writeError(w, http.StatusBadRequest, "prompt action is required")
					return
				}
				promptID := strings.TrimPrefix(r.URL.Path, "/api/admin/prompts/")
				promptID = strings.TrimSuffix(promptID, "/activate")
				promptID = strings.Trim(promptID, "/")
				if promptID == "" || strings.Contains(promptID, "/") {
					writeError(w, http.StatusBadRequest, "prompt id is required")
					return
				}
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}

				updated, err := promptsService.Activate(r.Context(), promptID, claims.Subject)
				if err != nil {
					if errors.Is(err, prompts.ErrNotFound) {
						writeError(w, http.StatusNotFound, err.Error())
						return
					}
					logger.Error("failed to activate prompt", zap.Error(err))
					writeError(w, http.StatusInternalServerError, "failed to activate prompt")
					return
				}

				writeJSON(w, http.StatusOK, updated)
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
