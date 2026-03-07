package subscription

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const cacheTTL = 5 * time.Minute

type cachedStatus struct {
	FetchedAt time.Time `json:"fetched_at"`
	Status    Status    `json:"status"`
}

func cacheDir() string {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, "aiusage")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "aiusage")
}

func cachePath(provider string) string {
	dir := cacheDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "sub_"+provider+".json")
}

func loadCache(provider string) (*Status, bool) {
	path := cachePath(provider)
	if path == "" {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var c cachedStatus
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, false
	}
	if time.Since(c.FetchedAt) > cacheTTL {
		return nil, false
	}
	return &c.Status, true
}

func saveCache(provider string, s *Status) {
	path := cachePath(provider)
	if path == "" {
		return
	}
	dir := filepath.Dir(path)
	_ = os.MkdirAll(dir, 0700)
	c := cachedStatus{
		FetchedAt: time.Now(),
		Status:    *s,
	}
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0600)
}
