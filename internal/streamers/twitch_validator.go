package streamers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TwitchAPIValidator validates streamer existence and fetches current live audience via Twitch Helix API.
type TwitchAPIValidator struct {
	clientID     string
	clientSecret string
	tokenURL     string
	apiBaseURL   string
	httpClient   *http.Client

	mu          sync.Mutex
	accessToken string
	expiresAt   time.Time
}

func NewTwitchAPIValidator(clientID, clientSecret, tokenURL, apiBaseURL string, httpClient *http.Client) *TwitchAPIValidator {
	validator := &TwitchAPIValidator{
		clientID:     strings.TrimSpace(clientID),
		clientSecret: strings.TrimSpace(clientSecret),
		tokenURL:     strings.TrimSpace(tokenURL),
		apiBaseURL:   strings.TrimSpace(apiBaseURL),
		httpClient:   httpClient,
	}
	if validator.tokenURL == "" {
		validator.tokenURL = "https://id.twitch.tv/oauth2/token"
	}
	if validator.apiBaseURL == "" {
		validator.apiBaseURL = "https://api.twitch.tv/helix"
	}
	if validator.httpClient == nil {
		validator.httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return validator
}

func (v *TwitchAPIValidator) ValidateUsername(ctx context.Context, username string) (string, error) {
	user, err := v.fetchUser(ctx, username)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(user.DisplayName) == "" {
		return strings.ToLower(strings.TrimSpace(username)), nil
	}
	return user.DisplayName, nil
}

func (v *TwitchAPIValidator) GetLiveAudience(ctx context.Context, username string) (bool, int, error) {
	user, err := v.fetchUser(ctx, username)
	if err != nil {
		return false, 0, err
	}
	stream, err := v.fetchStreamByUserID(ctx, user.ID)
	if err != nil {
		return false, 0, err
	}
	if stream == nil {
		return false, 0, nil
	}
	return true, stream.ViewerCount, nil
}

type twitchUser struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type twitchStream struct {
	ViewerCount int `json:"viewer_count"`
}

type twitchUsersResponse struct {
	Data []twitchUser `json:"data"`
}

type twitchStreamsResponse struct {
	Data []twitchStream `json:"data"`
}

type twitchAppTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

func (v *TwitchAPIValidator) fetchUser(ctx context.Context, username string) (twitchUser, error) {
	response := twitchUsersResponse{}
	if err := v.getHelix(ctx, "/users", map[string]string{"login": strings.TrimSpace(username)}, &response); err != nil {
		return twitchUser{}, err
	}
	if len(response.Data) == 0 {
		return twitchUser{}, fmt.Errorf("twitch user not found")
	}
	return response.Data[0], nil
}

func (v *TwitchAPIValidator) fetchStreamByUserID(ctx context.Context, userID string) (*twitchStream, error) {
	response := twitchStreamsResponse{}
	if err := v.getHelix(ctx, "/streams", map[string]string{"user_id": strings.TrimSpace(userID)}, &response); err != nil {
		return nil, err
	}
	if len(response.Data) == 0 {
		return nil, nil
	}
	return &response.Data[0], nil
}

func (v *TwitchAPIValidator) getHelix(ctx context.Context, path string, query map[string]string, target any) error {
	token, err := v.appAccessToken(ctx)
	if err != nil {
		return err
	}
	baseURL := strings.TrimRight(v.apiBaseURL, "/") + path
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return err
	}
	values := parsedURL.Query()
	for key, value := range query {
		if strings.TrimSpace(key) == "" {
			continue
		}
		values.Set(key, value)
	}
	parsedURL.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Client-ID", v.clientID)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("twitch api returned status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return err
	}
	return nil
}

func (v *TwitchAPIValidator) appAccessToken(ctx context.Context) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	now := time.Now().UTC()
	if v.accessToken != "" && now.Before(v.expiresAt) {
		return v.accessToken, nil
	}

	if v.clientID == "" || v.clientSecret == "" {
		return "", fmt.Errorf("twitch credentials are required")
	}

	values := url.Values{}
	values.Set("client_id", v.clientID)
	values.Set("client_secret", v.clientSecret)
	values.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("twitch token endpoint returned status %d", resp.StatusCode)
	}

	payload := twitchAppTokenResponse{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return "", fmt.Errorf("empty twitch access token")
	}
	if payload.ExpiresIn <= 0 {
		payload.ExpiresIn = 3600
	}

	v.accessToken = payload.AccessToken
	v.expiresAt = now.Add(time.Duration(payload.ExpiresIn-30) * time.Second)
	return v.accessToken, nil
}
