package providers

import (
	"context"
	"fmt"
	"time"

	"aiusage/internal/report"
)

type OpenRouterConfig struct {
	BaseURL string
	Path    string
	APIKey  string
	Start   time.Time
	End     time.Time
}

func FetchOpenRouterUsage(_ context.Context, client *HTTPClient, cfg OpenRouterConfig) ([]report.UsageRecord, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://openrouter.ai"
	}
	if cfg.Path == "" {
		cfg.Path = "/api/v1/activity"
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("missing OpenRouter admin key")
	}

	headers := map[string]string{
		"Authorization": "Bearer " + cfg.APIKey,
	}

	// The activity endpoint returns last 30 days; no date range param.
	// We fetch everything and filter to the requested range.
	rawURL, err := withQuery(cfg.BaseURL, cfg.Path, nil)
	if err != nil {
		return nil, err
	}
	payload, err := client.GetJSON(rawURL, headers)
	if err != nil {
		return nil, err
	}

	startDate := cfg.Start.Format("2006-01-02")
	endDate := cfg.End.AddDate(0, 0, -1).Format("2006-01-02") // End is exclusive

	records := make([]report.UsageRecord, 0, 128)
	for _, itemAny := range asSlice(payload["data"]) {
		item := asMap(itemAny)
		if len(item) == 0 {
			continue
		}

		date := parseDateLike(pickString(item, "date"))
		if date == "" {
			continue
		}
		if date < startDate || date > endDate {
			continue
		}

		promptTokens := pickInt64(item, "prompt_tokens")
		completionTokens := pickInt64(item, "completion_tokens")
		cost := pickFloat64(item, "usage")

		if promptTokens == 0 && completionTokens == 0 && cost == 0 {
			continue
		}

		costSource := ""
		if cost > 0 {
			costSource = "api"
		}

		records = append(records, report.UsageRecord{
			Provider:     "openrouter",
			Date:         date,
			APIKeyID:     "account",
			Model:        pickString(item, "model"),
			InputTokens:  promptTokens,
			OutputTokens: completionTokens,
			CostUSD:      cost,
			CostEstimated: false,
			CostSource:   costSource,
		})
	}

	return records, nil
}
