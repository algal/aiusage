package providers

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"aiusage/internal/report"
)

type AnthropicConfig struct {
	BaseURL string
	Path    string
	APIKey  string
	Start   time.Time
	End     time.Time
	Limit   int
}

func FetchAnthropicUsage(_ context.Context, client *HTTPClient, cfg AnthropicConfig) ([]report.UsageRecord, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com"
	}
	if cfg.Path == "" {
		cfg.Path = "/v1/organizations/usage_report/messages"
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

	pageToken := ""
	records := make([]report.UsageRecord, 0, 256)

	for {
		q := url.Values{}
		q.Set("starting_at", cfg.Start.UTC().Format(time.RFC3339))
		q.Set("ending_at", cfg.End.UTC().Format(time.RFC3339))
		q.Set("bucket_width", "1d")
		q.Set("limit", fmt.Sprintf("%d", cfg.Limit))
		q.Add("group_by[]", "api_key_id")
		q.Add("group_by[]", "model")
		if pageToken != "" {
			q.Set("page", pageToken)
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
			date := parseDateLike(pickString(bucket, "date", "start_date", "bucket_start", "starting_at"))
			if date == "" {
				date = epochToDate(pickInt64(bucket, "start_time"))
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
				if len(result) == 0 {
					continue
				}
				rec := parseUsageResult("anthropic", date, result)
				if rec.InputTokens == 0 && rec.OutputTokens == 0 && rec.CacheReadTokens == 0 && rec.CacheWriteTokens == 0 && rec.CostUSD == 0 {
					continue
				}
				records = append(records, rec)
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
		pageToken = next
	}

	return records, nil
}
