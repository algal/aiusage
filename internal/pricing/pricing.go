package pricing

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type ModelRate struct {
	InputPerMTok      float64 `json:"input_per_mtok"`
	OutputPerMTok     float64 `json:"output_per_mtok"`
	CacheReadPerMTok  float64 `json:"cache_read_per_mtok"`
	CacheWritePerMTok float64 `json:"cache_write_per_mtok"`

	InputTextPerMTok      float64 `json:"input_text_per_mtok"`
	OutputTextPerMTok     float64 `json:"output_text_per_mtok"`
	InputAudioPerMTok     float64 `json:"input_audio_per_mtok"`
	OutputAudioPerMTok    float64 `json:"output_audio_per_mtok"`
	InputImagePerMTok     float64 `json:"input_image_per_mtok"`
	OutputImagePerMTok    float64 `json:"output_image_per_mtok"`
	CacheReadTextPerMTok  float64 `json:"cache_read_text_per_mtok"`
	CacheReadAudioPerMTok float64 `json:"cache_read_audio_per_mtok"`
	CacheReadImagePerMTok float64 `json:"cache_read_image_per_mtok"`
}

type Usage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64

	InputTextTokens      int64
	OutputTextTokens     int64
	InputAudioTokens     int64
	OutputAudioTokens    int64
	InputImageTokens     int64
	OutputImageTokens    int64
	CacheReadTextTokens  int64
	CacheReadAudioTokens int64
	CacheReadImageTokens int64
}

type ProviderPricing struct {
	Default ModelRate            `json:"default"`
	Models  map[string]ModelRate `json:"models"`
}

type Book struct {
	Providers map[string]ProviderPricing `json:"providers"`
}

func DefaultBook() Book {
	return Book{Providers: map[string]ProviderPricing{
		"openai": {
			Default: ModelRate{},
			Models: map[string]ModelRate{
				"gpt-4o":           {InputPerMTok: 2.50, OutputPerMTok: 10.00, CacheReadPerMTok: 1.25},
				"gpt-4o*":          {InputPerMTok: 2.50, OutputPerMTok: 10.00, CacheReadPerMTok: 1.25},
				"gpt-4o-mini":      {InputPerMTok: 0.15, OutputPerMTok: 0.60, CacheReadPerMTok: 0.075},
				"gpt-4o-mini*":     {InputPerMTok: 0.15, OutputPerMTok: 0.60, CacheReadPerMTok: 0.075},
				"gpt-4o-mini-tts":  {InputTextPerMTok: 0.60, OutputAudioPerMTok: 12.00},
				"gpt-4o-mini-tts*": {InputTextPerMTok: 0.60, OutputAudioPerMTok: 12.00},
				"gpt-4.1":          {InputPerMTok: 2.00, OutputPerMTok: 8.00, CacheReadPerMTok: 0.50},
				"gpt-4.1*":         {InputPerMTok: 2.00, OutputPerMTok: 8.00, CacheReadPerMTok: 0.50},
				"gpt-4.1-mini":     {InputPerMTok: 0.40, OutputPerMTok: 1.60, CacheReadPerMTok: 0.10},
				"gpt-4.1-mini*":    {InputPerMTok: 0.40, OutputPerMTok: 1.60, CacheReadPerMTok: 0.10},
				"gpt-4.1-nano":     {InputPerMTok: 0.10, OutputPerMTok: 0.40, CacheReadPerMTok: 0.025},
				"gpt-4.1-nano*":    {InputPerMTok: 0.10, OutputPerMTok: 0.40, CacheReadPerMTok: 0.025},
				"gpt-3.5-turbo*":   {InputPerMTok: 0.50, OutputPerMTok: 1.50},
				"gpt-5":            {InputPerMTok: 1.25, OutputPerMTok: 10.00, CacheReadPerMTok: 0.125},
				"gpt-5*":           {InputPerMTok: 1.25, OutputPerMTok: 10.00, CacheReadPerMTok: 0.125},
				"gpt-5.2":          {InputPerMTok: 1.75, OutputPerMTok: 14.00, CacheReadPerMTok: 0.175},
				"gpt-5.2*":         {InputPerMTok: 1.75, OutputPerMTok: 14.00, CacheReadPerMTok: 0.175},
				"gpt-5-mini":       {InputPerMTok: 0.25, OutputPerMTok: 2.00, CacheReadPerMTok: 0.025},
				"gpt-5-mini*":      {InputPerMTok: 0.25, OutputPerMTok: 2.00, CacheReadPerMTok: 0.025},
				"gpt-5-nano":       {InputPerMTok: 0.05, OutputPerMTok: 0.40, CacheReadPerMTok: 0.005},
				"gpt-5-nano*":      {InputPerMTok: 0.05, OutputPerMTok: 0.40, CacheReadPerMTok: 0.005},
				"o3":               {InputPerMTok: 10.00, OutputPerMTok: 40.00},
				"o3*":              {InputPerMTok: 10.00, OutputPerMTok: 40.00},
				"o4-mini":          {InputPerMTok: 1.10, OutputPerMTok: 4.40},
				"o4-mini*":         {InputPerMTok: 1.10, OutputPerMTok: 4.40},
				"text-embedding":   {InputPerMTok: 0.10, OutputPerMTok: 0},
			},
		},
		"anthropic": {
			Default: ModelRate{},
			Models: map[string]ModelRate{
				"claude-3-7-sonnet":  {InputPerMTok: 3.00, OutputPerMTok: 15.00, CacheReadPerMTok: 0.30, CacheWritePerMTok: 3.75},
				"claude-3-7-sonnet*": {InputPerMTok: 3.00, OutputPerMTok: 15.00, CacheReadPerMTok: 0.30, CacheWritePerMTok: 3.75},
				"claude-3-5-sonnet":  {InputPerMTok: 3.00, OutputPerMTok: 15.00, CacheReadPerMTok: 0.30, CacheWritePerMTok: 3.75},
				"claude-3-5-sonnet*": {InputPerMTok: 3.00, OutputPerMTok: 15.00, CacheReadPerMTok: 0.30, CacheWritePerMTok: 3.75},
				"claude-3-5-haiku":   {InputPerMTok: 0.80, OutputPerMTok: 4.00, CacheReadPerMTok: 0.08, CacheWritePerMTok: 1.00},
				"claude-3-5-haiku*":  {InputPerMTok: 0.80, OutputPerMTok: 4.00, CacheReadPerMTok: 0.08, CacheWritePerMTok: 1.00},
				"claude-3-opus":      {InputPerMTok: 15.00, OutputPerMTok: 75.00, CacheReadPerMTok: 1.50, CacheWritePerMTok: 18.75},
				"claude-3-opus*":     {InputPerMTok: 15.00, OutputPerMTok: 75.00, CacheReadPerMTok: 1.50, CacheWritePerMTok: 18.75},
				"claude-sonnet-4":    {InputPerMTok: 3.00, OutputPerMTok: 15.00, CacheReadPerMTok: 0.30, CacheWritePerMTok: 3.75},
				"claude-sonnet-4*":   {InputPerMTok: 3.00, OutputPerMTok: 15.00, CacheReadPerMTok: 0.30, CacheWritePerMTok: 3.75},
				"claude-opus-4":      {InputPerMTok: 5.00, OutputPerMTok: 25.00, CacheReadPerMTok: 0.50, CacheWritePerMTok: 6.25},
				"claude-opus-4*":     {InputPerMTok: 5.00, OutputPerMTok: 25.00, CacheReadPerMTok: 0.50, CacheWritePerMTok: 6.25},
			},
		},
	}}
}

func LoadFile(path string) (Book, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Book{}, fmt.Errorf("read pricing file: %w", err)
	}
	var book Book
	if err := json.Unmarshal(data, &book); err != nil {
		return Book{}, fmt.Errorf("parse pricing file JSON: %w", err)
	}
	if len(book.Providers) == 0 {
		return Book{}, errors.New("pricing file has no providers")
	}
	return normalizeBook(book), nil
}

func normalizeBook(b Book) Book {
	out := Book{Providers: map[string]ProviderPricing{}}
	for provider, cfg := range b.Providers {
		p := strings.ToLower(strings.TrimSpace(provider))
		if p == "" {
			continue
		}
		norm := ProviderPricing{Default: cfg.Default, Models: map[string]ModelRate{}}
		for model, rate := range cfg.Models {
			m := strings.ToLower(strings.TrimSpace(model))
			if m == "" {
				continue
			}
			norm.Models[m] = rate
		}
		out.Providers[p] = norm
	}
	return out
}

func Merge(base Book, override Book) Book {
	if base.Providers == nil {
		base.Providers = map[string]ProviderPricing{}
	}
	for provider, cfg := range override.Providers {
		current := base.Providers[provider]
		current.Default = cfg.Default
		if current.Models == nil {
			current.Models = map[string]ModelRate{}
		}
		for model, rate := range cfg.Models {
			current.Models[model] = rate
		}
		base.Providers[provider] = current
	}
	return base
}

func (b Book) Estimate(provider, model string, usage Usage) (float64, bool) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.ToLower(strings.TrimSpace(model))
	cfg, ok := b.Providers[provider]
	if !ok {
		return 0, false
	}

	rate, found := findRate(cfg, model)
	if !found {
		return 0, false
	}

	remainingInput := usage.InputTokens
	remainingOutput := usage.OutputTokens
	remainingCacheRead := usage.CacheReadTokens

	cost := 0.0

	cost += perMTok(usage.InputTextTokens, rateOrFallback(rate.InputTextPerMTok, rate.InputPerMTok))
	remainingInput -= usage.InputTextTokens

	cost += perMTok(usage.OutputTextTokens, rateOrFallback(rate.OutputTextPerMTok, rate.OutputPerMTok))
	remainingOutput -= usage.OutputTextTokens

	cost += perMTok(usage.InputAudioTokens, rateOrFallback(rate.InputAudioPerMTok, rate.InputPerMTok))
	remainingInput -= usage.InputAudioTokens

	cost += perMTok(usage.OutputAudioTokens, rateOrFallback(rate.OutputAudioPerMTok, rate.OutputPerMTok))
	remainingOutput -= usage.OutputAudioTokens

	cost += perMTok(usage.InputImageTokens, rateOrFallback(rate.InputImagePerMTok, rate.InputPerMTok))
	remainingInput -= usage.InputImageTokens

	cost += perMTok(usage.OutputImageTokens, rateOrFallback(rate.OutputImagePerMTok, rate.OutputPerMTok))
	remainingOutput -= usage.OutputImageTokens

	cost += perMTok(usage.CacheReadTextTokens, rateOrFallback(rate.CacheReadTextPerMTok, rate.CacheReadPerMTok))
	remainingCacheRead -= usage.CacheReadTextTokens

	cost += perMTok(usage.CacheReadAudioTokens, rateOrFallback(rate.CacheReadAudioPerMTok, rate.CacheReadPerMTok))
	remainingCacheRead -= usage.CacheReadAudioTokens

	cost += perMTok(usage.CacheReadImageTokens, rateOrFallback(rate.CacheReadImagePerMTok, rate.CacheReadPerMTok))
	remainingCacheRead -= usage.CacheReadImageTokens

	if remainingInput < 0 {
		remainingInput = 0
	}
	if remainingOutput < 0 {
		remainingOutput = 0
	}
	if remainingCacheRead < 0 {
		remainingCacheRead = 0
	}

	cost += perMTok(remainingInput, rate.InputPerMTok)
	cost += perMTok(remainingOutput, rate.OutputPerMTok)
	cost += perMTok(remainingCacheRead, rate.CacheReadPerMTok)
	cost += perMTok(usage.CacheWriteTokens, rate.CacheWritePerMTok)

	return cost, true
}

func rateOrFallback(primary, fallback float64) float64 {
	if primary > 0 {
		return primary
	}
	return fallback
}

func perMTok(tokens int64, usdPerMTok float64) float64 {
	if tokens <= 0 || usdPerMTok <= 0 {
		return 0
	}
	return float64(tokens) / 1_000_000 * usdPerMTok
}

func findRate(cfg ProviderPricing, model string) (ModelRate, bool) {
	if rate, ok := cfg.Models[model]; ok {
		return rate, true
	}
	for prefix, rate := range cfg.Models {
		if strings.HasSuffix(prefix, "*") {
			p := strings.TrimSuffix(prefix, "*")
			if strings.HasPrefix(model, p) {
				return rate, true
			}
		}
	}
	if cfg.Default != (ModelRate{}) {
		return cfg.Default, true
	}
	return ModelRate{}, false
}
