package subscription

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// saveCodexCredentials persists the rotated tokens back to auth.json.
// OpenAI rotates refresh tokens on every exchange, so failing to write the new
// one back means the next refresh will be rejected with "already used".
// Reads the existing file and merges in only the fields we own, so any other
// keys the Codex CLI keeps there (e.g. OPENAI_API_KEY) survive the round-trip.
func saveCodexCredentials(auth *codexAuthData) error {
	dir := codexHome()
	if dir == "" {
		return fmt.Errorf("cannot determine codex home directory")
	}
	path := filepath.Join(dir, codexAuthFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}

	tokens := map[string]json.RawMessage{}
	if existing, ok := raw["tokens"]; ok {
		_ = json.Unmarshal(existing, &tokens)
	}
	accessB, err := json.Marshal(auth.Tokens.AccessToken)
	if err != nil {
		return err
	}
	tokens["access_token"] = accessB
	refreshB, err := json.Marshal(auth.Tokens.RefreshToken)
	if err != nil {
		return err
	}
	tokens["refresh_token"] = refreshB
	if auth.Tokens.AccountID != "" {
		accountB, err := json.Marshal(auth.Tokens.AccountID)
		if err != nil {
			return err
		}
		tokens["account_id"] = accountB
	}
	tokensB, err := json.Marshal(tokens)
	if err != nil {
		return err
	}
	raw["tokens"] = tokensB
	lastB, err := json.Marshal(auth.LastRefresh)
	if err != nil {
		return err
	}
	raw["last_refresh"] = lastB

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, out, 0600)
}

// atomicWrite writes data to a temp file in the same directory and renames it
// over path, so a crash mid-write can't leave a half-written credentials file.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return err
	}
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
		if saveErr := saveCodexCredentials(auth); saveErr != nil {
			fmt.Fprintf(os.Stderr, "aiusage: warning: failed to persist refreshed codex credentials: %v\n", saveErr)
		}
	}

	body, err := doCodexUsageRequest(client, auth)
	if err != nil {
		return nil, err
	}
	s, parseErr := parseCodexUsageResponse(body)
	if parseErr == nil {
		s.Account = emailFromJWT(auth.Tokens.AccessToken)
		saveCache("openai", s)
	}
	return s, parseErr
}

// emailFromJWT extracts email from the OpenAI JWT access token's
// embedded profile claim. Returns "" if not available.
func emailFromJWT(token string) string {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	payload := parts[1]
	if m := len(payload) % 4; m != 0 {
		payload += strings.Repeat("=", 4-m)
	}
	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return ""
	}
	var claims struct {
		Profile *struct {
			Email string `json:"email"`
		} `json:"https://api.openai.com/profile"`
	}
	if json.Unmarshal(decoded, &claims) != nil || claims.Profile == nil {
		return ""
	}
	return claims.Profile.Email
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
			if saveErr := saveCodexCredentials(auth); saveErr != nil {
				fmt.Fprintf(os.Stderr, "aiusage: warning: failed to persist refreshed codex credentials: %v\n", saveErr)
			}
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
