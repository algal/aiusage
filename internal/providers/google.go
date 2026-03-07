package providers

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"aiusage/internal/report"
)

type GoogleConfig struct {
	BaseURL string
	Path    string
	APIKey  string
	Start   time.Time
	End     time.Time
	Limit   int
}

func FetchGoogleUsage(_ context.Context, client *HTTPClient, cfg GoogleConfig) ([]report.UsageRecord, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://cloudbilling.googleapis.com"
	}
	if cfg.Path == "" {
		cfg.Path = "/v1/usage"
	}
	if cfg.Limit <= 0 {
		cfg.Limit = 31
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("missing Google API key")
	}

	headers := map[string]string{
		"Authorization": "Bearer " + cfg.APIKey,
	}

	page := ""
	records := make([]report.UsageRecord, 0, 256)

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
		if len(buckets) == 0 {
			buckets = asSlice(payload["results"])
		}

		for _, bucketAny := range buckets {
			bucket := asMap(bucketAny)
			if len(bucket) == 0 {
				continue
			}

			date := epochToDate(pickInt64(bucket, "start_time"))
			if date == "" {
				date = parseDateLike(pickString(bucket, "date", "start_date", "bucket_start", "starting_at"))
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
				rec := parseUsageResult("google", date, result)
				if rec.InputTokens == 0 && rec.OutputTokens == 0 && rec.CacheReadTokens == 0 && rec.CacheWriteTokens == 0 && rec.CostUSD == 0 {
					continue
				}
				records = append(records, rec)
			}
		}

		next := pickString(payload, "next_page", "next_page_token", "nextPageToken")
		if next == "" {
			break
		}
		page = next
	}

	return records, nil
}
