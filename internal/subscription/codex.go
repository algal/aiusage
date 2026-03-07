package subscription

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	codexAuthFile    = "auth.json"
	codexUsageURL    = "https://chatgpt.com/backend-api/wham/usage"
	codexRefreshURL  = "https://auth.openai.com/oauth/token"
	codexClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexRefreshAge  = 8 * 24 * time.Hour
)

type codexTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
}

type codexAuthData struct {
	Tokens      *codexTokens `json:"tokens"`
	LastRefresh string       `json:"last_refresh"`
}

func codexHome() string {
	if env := os.Getenv("CODEX_HOME"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

func loadCodexCredentials() (*codexAuthData, error) {
	dir := codexHome()
	if dir == "" {
		return nil, fmt.Errorf("cannot determine codex home directory")
	}
	path := filepath.Join(dir, codexAuthFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var auth codexAuthData
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if auth.Tokens == nil || auth.Tokens.AccessToken == "" {
		return nil, fmt.Errorf("no access token in %s", path)
	}
	return &auth, nil
}

func codexTokenNeedsRefresh(auth *codexAuthData) bool {
	if auth.LastRefresh == "" {
		return true
	}
	t, err := parseFlexibleTime(auth.LastRefresh)
	if err != nil {
		return true
	}
	return time.Since(t) > codexRefreshAge
}

func refreshCodexToken(auth *codexAuthData) error {
	if auth.Tokens.RefreshToken == "" {
		return fmt.Errorf("no refresh token available")
	}

	payload := map[string]string{
		"client_id":     codexClientID,
		"grant_type":    "refresh_token",
		"refresh_token": auth.Tokens.RefreshToken,
		"scope":         "openid profile email",
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodPost, codexRefreshURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("refresh request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != 200 {
		return fmt.Errorf("refresh failed (%s): %s", resp.Status, truncate(respBody, 200))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("parse refresh response: %w", err)
	}
	auth.Tokens.AccessToken = result.AccessToken
	if result.RefreshToken != "" {
		auth.Tokens.RefreshToken = result.RefreshToken
	}
	auth.LastRefresh = time.Now().UTC().Format(time.RFC3339)
	return nil
}

func FetchCodexStatus(client *http.Client, force bool) (*Status, error) {
	if !force {
		if cached, ok := loadCache("openai"); ok {
			return cached, nil
		}
	}

	auth, err := loadCodexCredentials()
	if err != nil {
		return nil, err
	}

	if codexTokenNeedsRefresh(auth) {
		if refreshErr := refreshCodexToken(auth); refreshErr != nil {
			return nil, fmt.Errorf("token needs refresh: %w", refreshErr)
		}
	}

	body, err := doCodexUsageRequest(client, auth)
	if err != nil {
		return nil, err
	}
	s, parseErr := parseCodexUsageResponse(body)
	if parseErr == nil {
		saveCache("openai", s)
	}
	return s, parseErr
}

func doCodexUsageRequest(client *http.Client, auth *codexAuthData) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, codexUsageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+auth.Tokens.AccessToken)
	req.Header.Set("Accept", "application/json")
	if auth.Tokens.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", auth.Tokens.AccountID)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("usage request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	// If unauthorized, try refresh once
	if resp.StatusCode == 401 && auth.Tokens.RefreshToken != "" {
		if refreshErr := refreshCodexToken(auth); refreshErr == nil {
			return doCodexUsageRequestRetry(client, auth)
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("usage request failed (%s): %s", resp.Status, truncate(body, 200))
	}
	return body, nil
}

func doCodexUsageRequestRetry(client *http.Client, auth *codexAuthData) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, codexUsageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+auth.Tokens.AccessToken)
	req.Header.Set("Accept", "application/json")
	if auth.Tokens.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", auth.Tokens.AccountID)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("usage request failed after refresh (%s): %s", resp.Status, truncate(body, 200))
	}
	return body, nil
}

func parseCodexUsageResponse(body []byte) (*Status, error) {
	var raw struct {
		PlanType  string `json:"plan_type"`
		RateLimit struct {
			Primary   *codexWindow `json:"primary_window"`
			Secondary *codexWindow `json:"secondary_window"`
		} `json:"rate_limit"`
		Credits *struct {
			HasCredits bool            `json:"has_credits"`
			Balance    json.RawMessage `json:"balance"`
		} `json:"credits"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse usage response: %w", err)
	}

	status := &Status{
		Provider: "openai",
		Plan:     raw.PlanType,
		Windows:  make([]Window, 0, 2),
	}

	if w := raw.RateLimit.Primary; w != nil {
		status.Windows = append(status.Windows, Window{
			Provider:    "openai",
			Name:        windowName(w.LimitWindowSeconds),
			UsedPercent: float64(w.UsedPercent),
			ResetsAt:    time.Unix(w.ResetAt, 0),
		})
	}
	if w := raw.RateLimit.Secondary; w != nil {
		status.Windows = append(status.Windows, Window{
			Provider:    "openai",
			Name:        windowName(w.LimitWindowSeconds),
			UsedPercent: float64(w.UsedPercent),
			ResetsAt:    time.Unix(w.ResetAt, 0),
		})
	}

	if c := raw.Credits; c != nil {
		bal := parseBalance(c.Balance)
		status.Credits = &Credits{
			HasCredits: c.HasCredits,
			Balance:    bal,
		}
	}

	return status, nil
}

type codexWindow struct {
	UsedPercent        int   `json:"used_percent"`
	ResetAt            int64 `json:"reset_at"`
	LimitWindowSeconds int64 `json:"limit_window_seconds"`
}

func windowName(seconds int64) string {
	switch {
	case seconds <= 18000:
		return "5-hour"
	case seconds <= 86400:
		return "daily"
	case seconds <= 604800:
		return "weekly"
	default:
		return "monthly"
	}
}

func parseBalance(raw json.RawMessage) float64 {
	if raw == nil {
		return 0
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			return v
		}
	}
	return 0
}
