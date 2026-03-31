package app

import (
	"bytes"
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
			Initial:            step.Initial,
			Order:              step.Order,
		})
	}
	return prompts.ScenarioPackageCreateRequest{
		Name:             req.Name,
		GameSlug:         req.GameSlug,
		LLMModelConfigID: req.LLMModelConfigID,
		Steps:            steps,
		ActorID:          actorID,
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
	Initial            bool   `json:"initial"`
	Order              int    `json:"order"`
}

type scenarioPackageCreateRequest struct {
	Name             string                `json:"name"`
	GameSlug         string                `json:"gameSlug"`
	LLMModelConfigID string                `json:"llmModelConfigId"`
	Steps            []scenarioStepRequest `json:"steps"`
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

func decodeJSONStrict(body []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	return decoder.Decode(out)
}
