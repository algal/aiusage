package subscription

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	claudeCredFile    = ".claude/.credentials.json"
	claudeUsageURL    = "https://api.anthropic.com/api/oauth/usage"
	claudeRefreshURL  = "https://platform.claude.com/v1/oauth/token"
	claudeOAuthBeta   = "oauth-2025-04-20"
	claudeClientID    = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
)

type claudeCredentials struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    int64  `json:"expiresAt"` // milliseconds since epoch
}

type claudeCredWrapper struct {
	ClaudeAiOauth claudeCredentials `json:"claudeAiOauth"`
}

func loadClaudeCredentials() (*claudeCredentials, error) {
	// On macOS, Keychain is authoritative (Claude Code keeps it updated)
	if runtime.GOOS == "darwin" {
		if cred, err := loadClaudeCredentialsFromKeychain(); err == nil {
			return cred, nil
		}
	}

	// Fall back to credentials file
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	path := filepath.Join(home, claudeCredFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parseClaudeCredentials(data, path)
}

func parseClaudeCredentials(data []byte, source string) (*claudeCredentials, error) {
	var f claudeCredWrapper
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", source, err)
	}
	if f.ClaudeAiOauth.AccessToken == "" {
		return nil, fmt.Errorf("no access token in %s", source)
	}
	return &f.ClaudeAiOauth, nil
}

func loadClaudeCredentialsFromKeychain() (*claudeCredentials, error) {
	out, err := exec.Command("security", "find-generic-password",
		"-s", "Claude Code-credentials", "-w").Output()
	if err != nil {
		return nil, fmt.Errorf("keychain lookup failed: %w", err)
	}
	return parseClaudeCredentials(bytes.TrimSpace(out), "keychain")
}

func refreshClaudeToken(cred *claudeCredentials) error {
	if cred.RefreshToken == "" {
		return fmt.Errorf("no refresh token available")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", cred.RefreshToken)
	form.Set("client_id", claudeClientID)

	req, err := http.NewRequest(http.MethodPost, claudeRefreshURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("refresh request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != 200 {
		return fmt.Errorf("refresh failed (%s): %s", resp.Status, truncate(body, 200))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("parse refresh response: %w", err)
	}
	cred.AccessToken = result.AccessToken
	if result.RefreshToken != "" {
		cred.RefreshToken = result.RefreshToken
	}
	cred.ExpiresAt = time.Now().UnixMilli() + result.ExpiresIn*1000
	return nil
}

func FetchClaudeStatus(client *http.Client, force bool) (*Status, error) {
	if !force {
		if cached, ok := loadCache("anthropic"); ok {
			return cached, nil
		}
	}

	cred, err := loadClaudeCredentials()
	if err != nil {
		return nil, err
	}

	// If expired, try direct refresh; if that fails, tell user to run claude
	if cred.ExpiresAt > 0 && time.Now().UnixMilli() >= cred.ExpiresAt {
		if refreshErr := refreshClaudeToken(cred); refreshErr != nil {
			return nil, fmt.Errorf("claude token expired; run 'claude' to refresh, then retry")
		}
	}

	req, err := http.NewRequest(http.MethodGet, claudeUsageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-beta", claudeOAuthBeta)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("usage request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	// If unauthorized, try direct refresh once
	if resp.StatusCode == 401 && cred.RefreshToken != "" {
		if refreshErr := refreshClaudeToken(cred); refreshErr == nil {
			s, err := fetchClaudeUsage(client, cred)
			if err == nil {
				saveCache("anthropic", s)
			}
			return s, err
		}
	}
	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("claude token unauthorized; run 'claude' to refresh, then retry")
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("usage request failed (%s): %s", resp.Status, truncate(body, 200))
	}

	s, err := parseClaudeUsageResponse(body)
	if err == nil {
		saveCache("anthropic", s)
	}
	return s, err
}

func fetchClaudeUsage(client *http.Client, cred *claudeCredentials) (*Status, error) {
	req, err := http.NewRequest(http.MethodGet, claudeUsageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-beta", claudeOAuthBeta)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("usage request failed (%s): %s", resp.Status, truncate(body, 200))
	}
	return parseClaudeUsageResponse(body)
}

func parseClaudeUsageResponse(body []byte) (*Status, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse usage response: %w", err)
	}

	status := &Status{
		Provider: "anthropic",
		Windows:  make([]Window, 0, 4),
	}

	type windowData struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    string  `json:"resets_at"`
	}

	windowNames := map[string]string{
		"five_hour": "5-hour",
		"seven_day": "7-day",
	}

	for key, name := range windowNames {
		if data, ok := raw[key]; ok {
			var w windowData
			if err := json.Unmarshal(data, &w); err == nil {
				win := Window{
					Provider:    "anthropic",
					Name:        name,
					UsedPercent: w.Utilization,
				}
				if w.ResetsAt != "" {
					if t, err := parseFlexibleTime(w.ResetsAt); err == nil {
						win.ResetsAt = t
					}
				}
				status.Windows = append(status.Windows, win)
			}
		}
	}

	if data, ok := raw["extra_usage"]; ok {
		var extra struct {
			IsEnabled    bool    `json:"is_enabled"`
			MonthlyLimit float64 `json:"monthly_limit"`
			UsedCredits  float64 `json:"used_credits"`
			Utilization  float64 `json:"utilization"`
		}
		if err := json.Unmarshal(data, &extra); err == nil && extra.IsEnabled {
			status.ExtraUsage = &ExtraUsage{
				Enabled:     true,
				UsedUSD:     extra.UsedCredits / 100,
				LimitUSD:    extra.MonthlyLimit / 100,
				Utilization: extra.Utilization,
			}
		}
	}

	return status, nil
}

func parseFlexibleTime(s string) (time.Time, error) {
	for _, layout := range []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.000Z",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time %q", s)
}

func truncate(b []byte, n int) string {
	s := strings.TrimSpace(string(bytes.TrimSpace(b)))
	if len(s) > n {
		return s[:n]
	}
	return s
}
