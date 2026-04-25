package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/funpot/funpot-go-core/internal/admin"
	"github.com/funpot/funpot-go-core/internal/users"
)

func TestAdminUsersCRUD(t *testing.T) {
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

	createReq := httptest.NewRequest(http.MethodPost, "/api/admin/users", strings.NewReader(`{"telegramId":101,"username":"alice","firstName":"Alice","lastName":"Smith","languageCode":"en"}`))
	createReq.Header.Set("Authorization", "Bearer "+buildToken(t, "admin-1"))
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", createRes.Code, createRes.Body.String())
	}

	var created users.Profile
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if created.ID == "" {
		t.Fatalf("expected created id")
	}

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

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/admin/users/"+created.ID, nil)
	deleteReq.Header.Set("Authorization", "Bearer "+buildToken(t, "admin-1"))
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", deleteRes.Code, deleteRes.Body.String())
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/api/admin/users/"+created.ID, nil)
	missingReq.Header.Set("Authorization", "Bearer "+buildToken(t, "admin-1"))
	missingRes := httptest.NewRecorder()
	handler.ServeHTTP(missingRes, missingReq)
	if missingRes.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", missingRes.Code, missingRes.Body.String())
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
