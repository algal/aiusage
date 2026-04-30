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

	// Where this credential was loaded from, so refreshed tokens can be written
	// back to the same place. Anthropic rotates refresh tokens on every
	// exchange, so failing to persist the new one will reject the next refresh.
	source claudeCredSource
}

type claudeCredSourceKind int

const (
	claudeCredSourceUnknown claudeCredSourceKind = iota
	claudeCredSourceFile
	claudeCredSourceKeychain
)

type claudeCredSource struct {
	kind claudeCredSourceKind
	path string // file path when kind == claudeCredSourceFile
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
	cred, err := parseClaudeCredentials(data, path)
	if err != nil {
		return nil, err
	}
	cred.source = claudeCredSource{kind: claudeCredSourceFile, path: path}
	return cred, nil
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
	cred, err := parseClaudeCredentials(bytes.TrimSpace(out), "keychain")
	if err != nil {
		return nil, err
	}
	cred.source = claudeCredSource{kind: claudeCredSourceKeychain}
	return cred, nil
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

// saveClaudeCredentials writes the rotated tokens back to whichever store the
// credential was loaded from. Anthropic rotates refresh tokens on every
// exchange, so without this the next refresh sees a "already used" rejection.
func saveClaudeCredentials(cred *claudeCredentials) error {
	switch cred.source.kind {
	case claudeCredSourceFile:
		return saveClaudeCredentialsToFile(cred, cred.source.path)
	case claudeCredSourceKeychain:
		return saveClaudeCredentialsToKeychain(cred)
	default:
		return fmt.Errorf("unknown credential source")
	}
}

func saveClaudeCredentialsToFile(cred *claudeCredentials, path string) error {
	// Read-modify-write so any unrelated top-level keys in the credentials file
	// (Claude Code may add fields we don't know about) survive the round-trip.
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

	inner := map[string]json.RawMessage{}
	if existing, ok := raw["claudeAiOauth"]; ok {
		_ = json.Unmarshal(existing, &inner)
	}
	accessB, err := json.Marshal(cred.AccessToken)
	if err != nil {
		return err
	}
	inner["accessToken"] = accessB
	refreshB, err := json.Marshal(cred.RefreshToken)
	if err != nil {
		return err
	}
	inner["refreshToken"] = refreshB
	expB, err := json.Marshal(cred.ExpiresAt)
	if err != nil {
		return err
	}
	inner["expiresAt"] = expB
	innerB, err := json.Marshal(inner)
	if err != nil {
		return err
	}
	raw["claudeAiOauth"] = innerB

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, out, 0600)
}

func saveClaudeCredentialsToKeychain(cred *claudeCredentials) error {
	wrapper := claudeCredWrapper{ClaudeAiOauth: claudeCredentials{
		AccessToken:  cred.AccessToken,
		RefreshToken: cred.RefreshToken,
		ExpiresAt:    cred.ExpiresAt,
	}}
	data, err := json.Marshal(wrapper)
	if err != nil {
		return err
	}
	// Preserve the existing entry's account name so we update in place rather
	// than creating a new keychain item with different metadata.
	account := lookupClaudeKeychainAccount()
	args := []string{
		"add-generic-password",
		"-U",
		"-s", "Claude Code-credentials",
		"-w", string(data),
	}
	if account != "" {
		args = append(args, "-a", account)
	}
	cmd := exec.Command("security", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("security add-generic-password: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func lookupClaudeKeychainAccount() string {
	out, err := exec.Command("security", "find-generic-password",
		"-s", "Claude Code-credentials").CombinedOutput()
	if err != nil {
		return ""
	}
	// Lines look like:    "acct"<blob>="someone@example.com"
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `"acct"<blob>=`) {
			continue
		}
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		val := strings.TrimSpace(line[idx+1:])
		if strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) && len(val) >= 2 {
			return val[1 : len(val)-1]
		}
	}
	return ""
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
		if saveErr := saveClaudeCredentials(cred); saveErr != nil {
			fmt.Fprintf(os.Stderr, "aiusage: warning: failed to persist refreshed claude credentials: %v\n", saveErr)
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
			if saveErr := saveClaudeCredentials(cred); saveErr != nil {
				fmt.Fprintf(os.Stderr, "aiusage: warning: failed to persist refreshed claude credentials: %v\n", saveErr)
			}
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
