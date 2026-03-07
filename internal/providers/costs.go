package providers

import (
	"fmt"
	"net/url"
	"time"
)

type OpenAICostConfig struct {
	BaseURL string
	Path    string
	APIKey  string
	Start   time.Time
	End     time.Time
	Limit   int
}

type AnthropicCostConfig struct {
	BaseURL string
	Path    string
	APIKey  string
	Start   time.Time
	End     time.Time
	Limit   int
}

func FetchOpenAIDailyCosts(client *HTTPClient, cfg OpenAICostConfig) (map[string]float64, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com"
	}
	if cfg.Path == "" {
		cfg.Path = "/v1/organization/costs"
	}
	if cfg.Limit <= 0 {
		cfg.Limit = 31
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("missing OpenAI admin key")
	}

	headers := map[string]string{"Authorization": "Bearer " + cfg.APIKey}
	page := ""
	out := map[string]float64{}

	for {
		q := url.Values{}
		q.Set("start_time", fmt.Sprintf("%d", cfg.Start.Unix()))
		q.Set("end_time", fmt.Sprintf("%d", cfg.End.Unix()))
		q.Set("bucket_width", "1d")
		q.Set("limit", fmt.Sprintf("%d", cfg.Limit))
		if page != "" {
			q.Set("page", page)
		}
		rawURL, err := withQuery(cfg.BaseURL, cfg.Path, q)
		if err != nil {
			return nil, err
		}
		payload, err := client.GetJSON(rawURL, headers)
		if err != nil {
			return nil, err
		}

		buckets := asSlice(payload["data"])
		if len(buckets) == 0 {
			buckets = asSlice(payload["buckets"])
		}
		for _, bucketAny := range buckets {
			bucket := asMap(bucketAny)
			if len(bucket) == 0 {
				continue
			}
			date := epochToDate(pickInt64(bucket, "start_time"))
			if date == "" {
				date = parseDateLike(pickString(bucket, "date", "start_date", "starting_at"))
			}
			if date == "" {
				continue
			}
			results := asSlice(bucket["results"])
			if len(results) == 0 {
				results = asSlice(bucket["data"])
			}
			if len(results) == 0 {
				results = []any{bucket}
			}
			for _, resultAny := range results {
				result := asMap(resultAny)
				amount, ok := extractUSDAmount(result)
				if !ok {
					continue
				}
				out[date] += amount
			}
		}

		next := pickString(payload, "next_page", "next_page_token")
		if next == "" {
			break
		}
		page = next
	}

	return out, nil
}

func FetchAnthropicDailyCosts(client *HTTPClient, cfg AnthropicCostConfig) (map[string]float64, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com"
	}
	if cfg.Path == "" {
		cfg.Path = "/v1/organizations/cost_report"
	}
	if cfg.Limit <= 0 {
		cfg.Limit = 31
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("missing Anthropic admin key")
	}

	headers := map[string]string{
		"x-api-key":         cfg.APIKey,
		"anthropic-version": "2023-06-01",
	}
	page := ""
	out := map[string]float64{}

	for {
		q := url.Values{}
		q.Set("starting_at", cfg.Start.UTC().Format(time.RFC3339))
		q.Set("ending_at", cfg.End.UTC().Format(time.RFC3339))
		q.Set("bucket_width", "1d")
		q.Set("limit", fmt.Sprintf("%d", cfg.Limit))
		if page != "" {
			q.Set("page", page)
		}
		rawURL, err := withQuery(cfg.BaseURL, cfg.Path, q)
		if err != nil {
			return nil, err
		}
		payload, err := client.GetJSON(rawURL, headers)
		if err != nil {
			return nil, err
		}

		buckets := asSlice(payload["data"])
		if len(buckets) == 0 {
			buckets = asSlice(payload["results"])
		}
		if len(buckets) == 0 {
			buckets = asSlice(payload["buckets"])
		}
		for _, bucketAny := range buckets {
			bucket := asMap(bucketAny)
			if len(bucket) == 0 {
				continue
			}
			date := parseDateLike(pickString(bucket, "date", "start_date", "starting_at"))
			if date == "" {
				date = epochToDate(pickInt64(bucket, "start_time"))
			}
			if date == "" {
				continue
			}
			results := asSlice(bucket["results"])
			if len(results) == 0 {
				results = asSlice(bucket["data"])
			}
			if len(results) == 0 {
				results = []any{bucket}
			}
			for _, resultAny := range results {
				result := asMap(resultAny)
				amount, ok := extractUSDAmount(result)
				if !ok {
					continue
				}
				out[date] += amount
			}
		}

		next := pickString(payload, "next_page", "next_page_token")
		if next == "" {
			pagination := asMap(payload["pagination"])
			next = pickString(pagination, "next", "next_page", "next_page_token")
		}
		if next == "" {
			break
		}
		page = next
	}

	return out, nil
}

func extractUSDAmount(m map[string]any) (float64, bool) {
	if v, ok := m["amount_value"]; ok {
		return asFloat64(v), true
	}
	if v, ok := m["cost_usd"]; ok {
		return asFloat64(v), true
	}
	if v, ok := m["amount_usd"]; ok {
		return asFloat64(v), true
	}
	if v, ok := m["usd"]; ok {
		return asFloat64(v), true
	}
	if amt := asMap(m["amount"]); len(amt) > 0 {
		if v, ok := amt["value"]; ok {
			return asFloat64(v), true
		}
	}
	return 0, false
}
