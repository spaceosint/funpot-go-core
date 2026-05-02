package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/admin"
	"github.com/funpot/funpot-go-core/internal/users"
)

func TestAdminUsersCRUD(t *testing.T) {
	userService := users.NewService(users.NewInMemoryRepository())
	created, err := userService.SyncTelegramProfile(context.Background(), users.TelegramProfile{
		ID:           101,
		Username:     "alice",
		FirstName:    "Alice",
		LastName:     "Smith",
		LanguageCode: "en",
	})
	if err != nil {
		t.Fatalf("SyncTelegramProfile() error = %v", err)
	}

	handler := NewHandler(
		zap.NewNop(),
		func() bool { return true },
		nil,
		buildAuthService(t),
		admin.NewService([]string{"admin-1"}),
		userService,
		nil,
		nil,
		nil,
		nil,
		nil,
		ClientConfigResponse{},
	)

	getReq := httptest.NewRequest(http.MethodGet, "/api/admin/users/"+created.ID, nil)
	getReq.Header.Set("Authorization", "Bearer "+buildToken(t, "admin-1"))
	getRes := httptest.NewRecorder()
	handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", getRes.Code, getRes.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/admin/users/"+created.ID, strings.NewReader(`{"username":"alice_2","firstName":"Alice","lastName":"Updated","languageCode":"ru"}`))
	updateReq.Header.Set("Authorization", "Bearer "+buildToken(t, "admin-1"))
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", updateRes.Code, updateRes.Body.String())
	}

	balanceReq := httptest.NewRequest(http.MethodPut, "/api/admin/users/"+created.ID, strings.NewReader(`{"balanceDeltaINT":50,"balanceReason":"manual grant"}`))
	balanceReq.Header.Set("Authorization", "Bearer "+buildToken(t, "admin-1"))
	balanceReq.Header.Set("Idempotency-Key", "bal-1")
	balanceRes := httptest.NewRecorder()
	handler.ServeHTTP(balanceRes, balanceReq)
	if balanceRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", balanceRes.Code, balanceRes.Body.String())
	}
	var balanceProfile users.Profile
	if err := json.Unmarshal(balanceRes.Body.Bytes(), &balanceProfile); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if balanceProfile.Username != "alice_2" || balanceProfile.LastName != "Updated" || balanceProfile.LanguageCode != "ru" {
		t.Fatalf("expected profile fields unchanged after balance update, got %+v", balanceProfile)
	}

	banReq := httptest.NewRequest(http.MethodPut, "/api/admin/users/"+created.ID+"/ban", strings.NewReader(`{"isBanned":true,"reason":"manual"}`))
	banReq.Header.Set("Authorization", "Bearer "+buildToken(t, "admin-1"))
	banRes := httptest.NewRecorder()
	handler.ServeHTTP(banRes, banReq)
	if banRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", banRes.Code, banRes.Body.String())
	}

	var banned users.Profile
	if err := json.Unmarshal(banRes.Body.Bytes(), &banned); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !banned.IsBanned {
		t.Fatalf("expected user to be banned")
	}

	unbanReq := httptest.NewRequest(http.MethodPut, "/api/admin/users/"+created.ID+"/ban", strings.NewReader(`{"isBanned":false}`))
	unbanReq.Header.Set("Authorization", "Bearer "+buildToken(t, "admin-1"))
	unbanRes := httptest.NewRecorder()
	handler.ServeHTTP(unbanRes, unbanReq)
	if unbanRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", unbanRes.Code, unbanRes.Body.String())
	}
}

func TestAdminUsersListSearchPagination(t *testing.T) {
	userService := users.NewService(users.NewInMemoryRepository())
	for _, profile := range []users.TelegramProfile{
		{ID: 1, Username: "alpha", FirstName: "Alpha", LastName: "One", LanguageCode: "en"},
		{ID: 2, Username: "beta", FirstName: "Beta", LastName: "Two", LanguageCode: "en"},
		{ID: 3, Username: "alpha_test", FirstName: "Gamma", LastName: "Three", LanguageCode: "ru"},
	} {
		if _, err := userService.SyncTelegramProfile(context.Background(), profile); err != nil {
			t.Fatalf("SyncTelegramProfile() error = %v", err)
		}
	}

	handler := NewHandler(
		zap.NewNop(),
		func() bool { return true },
		nil,
		buildAuthService(t),
		admin.NewService([]string{"admin-1"}),
		userService,
		nil,
		nil,
		nil,
		nil,
		nil,
		ClientConfigResponse{},
	)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/users?query=alpha&page=1&pageSize=1", nil)
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "admin-1"))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var payload struct {
		Total int             `json:"total"`
		Items []users.Profile `json:"items"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload.Total != 2 {
		t.Fatalf("expected total=2, got %d", payload.Total)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(payload.Items))
	}
	if !strings.Contains(strings.ToLower(payload.Items[0].Username), "alpha") {
		t.Fatalf("unexpected item: %+v", payload.Items[0])
	}
}

func TestAdminUsersRouteRequiresAdmin(t *testing.T) {
	userService := users.NewService(users.NewInMemoryRepository())
	handler := NewHandler(
		zap.NewNop(),
		func() bool { return true },
		nil,
		buildAuthService(t),
		admin.NewService([]string{"admin-1"}),
		userService,
		nil,
		nil,
		nil,
		nil,
		nil,
		ClientConfigResponse{},
	)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+buildToken(t, "user-1"))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", res.Code)
	}
}

func TestAdminUsersCreateAndDeleteAreDisabled(t *testing.T) {
	userService := users.NewService(users.NewInMemoryRepository())
	seeded, err := userService.SyncTelegramProfile(context.Background(), users.TelegramProfile{
		ID:           404,
		Username:     "seeded",
		FirstName:    "Seed",
		LastName:     "User",
		LanguageCode: "en",
	})
	if err != nil {
		t.Fatalf("SyncTelegramProfile() error = %v", err)
	}
	handler := NewHandler(
		zap.NewNop(),
		func() bool { return true },
		nil,
		buildAuthService(t),
		admin.NewService([]string{"admin-1"}),
		userService,
		nil,
		nil,
		nil,
		nil,
		nil,
		ClientConfigResponse{},
	)

	createReq := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(`{"telegramId":505}`))
	createReq.Header.Set("Authorization", "Bearer "+buildToken(t, "admin-1"))
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", createRes.Code)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/admin/users/"+seeded.ID, nil)
	deleteReq.Header.Set("Authorization", "Bearer "+buildToken(t, "admin-1"))
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", deleteRes.Code)
	}
}

func TestBannedUserIsBlockedByMiddleware(t *testing.T) {
	userService := users.NewService(users.NewInMemoryRepository())
	seeded, err := userService.SyncTelegramProfile(context.Background(), users.TelegramProfile{
		ID:           606,
		Username:     "blocked",
		FirstName:    "Blocked",
		LastName:     "User",
		LanguageCode: "en",
	})
	if err != nil {
		t.Fatalf("SyncTelegramProfile() error = %v", err)
	}
	if _, err := userService.BanByID(context.Background(), seeded.ID, "manual", time.Time{}); err != nil {
		t.Fatalf("BanByID() error = %v", err)
	}

	handler := NewHandler(
		zap.NewNop(),
		func() bool { return true },
		nil,
		buildAuthService(t),
		admin.NewService([]string{"admin-1"}),
		userService,
		nil,
		nil,
		nil,
		nil,
		nil,
		ClientConfigResponse{},
	)

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.Header.Set("Authorization", "Bearer "+buildToken(t, seeded.ID))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", res.Code)
	}
}
