package pricing

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	LiteLLMURL       = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
	pricingCacheTTL  = 24 * time.Hour
	pricingCacheFile = "pricing_litellm.json"
)

type litellmEntry struct {
	InputCostPerToken  *float64 `json:"input_cost_per_token"`
	OutputCostPerToken *float64 `json:"output_cost_per_token"`
	LiteLLMProvider    string   `json:"litellm_provider"`
}

type cachedPricing struct {
	FetchedAt time.Time `json:"fetched_at"`
	Book      Book      `json:"book"`
}

type PricingDiff struct {
	Provider string
	Model    string
	Field    string
	BuiltIn  float64
	LiteLLM  float64
}

type CheckResult struct {
	Diffs    []PricingDiff
	NotFound []string
}

// LoadOrFetchLiteLLM returns a LiteLLM-based pricing overlay and the
// time it was fetched. On any failure, returns a non-nil error; caller
// should fall back to built-in pricing.
func LoadOrFetchLiteLLM(client *http.Client, reference Book, force bool) (Book, time.Time, error) {
	if !force {
		if cached, ok := loadPricingCache(); ok {
			return cached.Book, cached.FetchedAt, nil
		}
	}

	book, err := fetchLiteLLMBook(client, reference)
	if err != nil {
		// Try stale cache as last resort
		if cached, ok := loadPricingCacheStale(); ok {
			return cached.Book, cached.FetchedAt, nil
		}
		return Book{}, time.Time{}, err
	}

	now := time.Now()
	savePricingCache(&cachedPricing{FetchedAt: now, Book: book})
	return book, now, nil
}

// fetchLiteLLMBook fetches LiteLLM pricing and builds a Book overlay
// keyed to the model names in the reference book.
func fetchLiteLLMBook(client *http.Client, reference Book) (Book, error) {
	llm, err := fetchLiteLLMRaw(client)
	if err != nil {
		return Book{}, err
	}

	overlay := Book{Providers: map[string]ProviderPricing{}}

	for provider, cfg := range reference.Providers {
		pp := ProviderPricing{Models: map[string]ModelRate{}}
		for model, builtInRate := range cfg.Models {
			base := model
			isWildcard := strings.HasSuffix(model, "*")
			if isWildcard {
				base = strings.TrimSuffix(model, "*")
			}

			le, found := findLiteLLMEntry(llm, provider, base)
			if !found {
				continue
			}

			// Start from the built-in rate so we preserve cache rates etc.
			rate := builtInRate
			if le.InputCostPerToken != nil {
				llmRate := *le.InputCostPerToken * 1_000_000
				if rate.InputPerMTok > 0 {
					rate.InputPerMTok = llmRate
				} else if rate.InputTextPerMTok > 0 {
					rate.InputTextPerMTok = llmRate
				}
			}
			if le.OutputCostPerToken != nil {
				llmRate := *le.OutputCostPerToken * 1_000_000
				if rate.OutputPerMTok > 0 {
					rate.OutputPerMTok = llmRate
				} else if rate.OutputTextPerMTok > 0 {
					rate.OutputTextPerMTok = llmRate
				}
			}
			pp.Models[model] = rate
		}
		if len(pp.Models) > 0 {
			overlay.Providers[provider] = pp
		}
	}

	return overlay, nil
}

func fetchLiteLLMRaw(client *http.Client) (map[string]litellmEntry, error) {
	resp, err := client.Get(LiteLLMURL)
	if err != nil {
		return nil, fmt.Errorf("fetch litellm pricing: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 10<<20))

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("litellm pricing fetch failed (%s)", resp.Status)
	}

	var llm map[string]litellmEntry
	if err := json.Unmarshal(body, &llm); err != nil {
		return nil, fmt.Errorf("parse litellm pricing: %w", err)
	}
	return llm, nil
}

// CheckAgainstLiteLLM compares built-in pricing against LiteLLM.
func CheckAgainstLiteLLM(client *http.Client, book Book) (*CheckResult, error) {
	llm, err := fetchLiteLLMRaw(client)
	if err != nil {
		return nil, err
	}

	result := &CheckResult{}

	for _, e := range book.ListModels() {
		le, found := findLiteLLMEntry(llm, e.Provider, e.Model)
		if !found {
			result.NotFound = append(result.NotFound, e.Provider+"/"+e.Model)
			continue
		}

		builtInInput := e.Rate.InputPerMTok
		if builtInInput == 0 {
			builtInInput = e.Rate.InputTextPerMTok
		}
		builtInOutput := e.Rate.OutputPerMTok
		if builtInOutput == 0 {
			builtInOutput = e.Rate.OutputTextPerMTok
		}

		if le.InputCostPerToken != nil && builtInInput > 0 {
			llmRate := *le.InputCostPerToken * 1_000_000
			if !closeEnough(builtInInput, llmRate) {
				result.Diffs = append(result.Diffs, PricingDiff{
					Provider: e.Provider, Model: e.Model,
					Field: "input/MTok", BuiltIn: builtInInput, LiteLLM: llmRate,
				})
			}
		}
		if le.OutputCostPerToken != nil && builtInOutput > 0 {
			llmRate := *le.OutputCostPerToken * 1_000_000
			if !closeEnough(builtInOutput, llmRate) {
				result.Diffs = append(result.Diffs, PricingDiff{
					Provider: e.Provider, Model: e.Model,
					Field: "output/MTok", BuiltIn: builtInOutput, LiteLLM: llmRate,
				})
			}
		}
	}

	return result, nil
}

// OverrideBookFromDiffs builds a Book containing only the corrected rates
// from LiteLLM, suitable for use with --pricing-file.
func OverrideBookFromDiffs(diffs []PricingDiff) Book {
	book := Book{Providers: map[string]ProviderPricing{}}
	for _, d := range diffs {
		pp, ok := book.Providers[d.Provider]
		if !ok {
			pp = ProviderPricing{Models: map[string]ModelRate{}}
		}
		rate := pp.Models[d.Model]
		switch d.Field {
		case "input/MTok":
			rate.InputPerMTok = d.LiteLLM
		case "output/MTok":
			rate.OutputPerMTok = d.LiteLLM
		}
		pp.Models[d.Model] = rate
		book.Providers[d.Provider] = pp
	}
	return book
}

// Cache helpers

func pricingCacheDir() string {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, "aiusage")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "aiusage")
}

func pricingCachePath() string {
	dir := pricingCacheDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, pricingCacheFile)
}

func loadPricingCache() (*cachedPricing, bool) {
	return loadPricingCacheWithTTL(pricingCacheTTL)
}

func loadPricingCacheStale() (*cachedPricing, bool) {
	return loadPricingCacheWithTTL(0) // any age
}

func loadPricingCacheWithTTL(ttl time.Duration) (*cachedPricing, bool) {
	path := pricingCachePath()
	if path == "" {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var c cachedPricing
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, false
	}
	if ttl > 0 && time.Since(c.FetchedAt) > ttl {
		return nil, false
	}
	return &c, true
}

func savePricingCache(c *cachedPricing) {
	path := pricingCachePath()
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0600)
}

func findLiteLLMEntry(data map[string]litellmEntry, provider, model string) (litellmEntry, bool) {
	// Exact match
	if e, ok := data[model]; ok {
		return e, true
	}
	// Try with provider prefix (e.g., "anthropic/claude-sonnet-4")
	prefixed := provider + "/" + model
	if e, ok := data[prefixed]; ok {
		return e, true
	}
	// Try matching by prefix (LiteLLM often has date-suffixed names)
	var best string
	for key, entry := range data {
		name := key
		if strings.Contains(key, "/") {
			parts := strings.SplitN(key, "/", 2)
			if parts[0] != provider {
				continue
			}
			name = parts[1]
		} else if entry.LiteLLMProvider != provider {
			continue
		}
		if strings.HasPrefix(name, model) && (best == "" || len(key) < len(best)) {
			best = key
		}
	}
	if best != "" {
		return data[best], true
	}
	return litellmEntry{}, false
}

func closeEnough(a, b float64) bool {
	if a == b {
		return true
	}
	diff := math.Abs(a - b)
	avg := (math.Abs(a) + math.Abs(b)) / 2
	if avg == 0 {
		return diff < 0.001
	}
	return diff/avg < 0.01 // within 1%
}
