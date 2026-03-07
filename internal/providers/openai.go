package providers

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"aiusage/internal/report"
)

type OpenAIConfig struct {
	BaseURL string
	Path    string
	APIKey  string
	Start   time.Time
	End     time.Time
	Limit   int
}

func FetchOpenAIUsage(_ context.Context, client *HTTPClient, cfg OpenAIConfig) ([]report.UsageRecord, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com"
	}
	if cfg.Path == "" {
		cfg.Path = "/v1/organization/usage/completions"
	}
	if cfg.Limit <= 0 {
		cfg.Limit = 31
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("missing OpenAI admin key")
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
		q.Add("group_by[]", "api_key_id")
		q.Add("group_by[]", "model")
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
				date = parseDateLike(pickString(bucket, "date", "start_date", "bucket_start"))
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
				rec := parseUsageResult("openai", date, result)
				if rec.InputTokens == 0 && rec.OutputTokens == 0 && rec.CacheReadTokens == 0 && rec.CacheWriteTokens == 0 && rec.CostUSD == 0 {
					continue
				}
				records = append(records, rec)
			}
		}

		next := pickString(payload, "next_page", "next_page_token")
		if next == "" {
			break
		}
		page = next
	}

	return records, nil
}

func parseUsageResult(provider, bucketDate string, m map[string]any) report.UsageRecord {
	date := bucketDate
	if date == "" {
		date = parseDateLike(pickString(m, "date", "start_date", "bucket_start", "starting_at", "timestamp"))
	}
	if date == "" {
		date = epochToDate(pickInt64(m, "start_time", "timestamp"))
	}

	input := pickInt64(m,
		"input_uncached_tokens",
		"uncached_input_tokens",
		"input_tokens",
		"input_tokens_total",
		"input_token_count",
	)
	output := pickInt64(m,
		"output_tokens",
		"output_tokens_total",
		"output_token_count",
	)
	cacheRead := pickInt64(m,
		"cache_read_input_tokens",
		"input_cached_tokens",
		"cached_input_tokens",
	)
	cacheWrite := pickInt64(m,
		"cache_creation_input_tokens",
		"cache_write_input_tokens",
	)
	if cacheCreation := asMap(m["cache_creation"]); len(cacheCreation) > 0 {
		cacheWrite += pickInt64(cacheCreation, "ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens")
	}
	if input == 0 && output == 0 && cacheRead == 0 && cacheWrite == 0 {
		total := pickInt64(m, "total_tokens", "token_count")
		input = total
	}

	cost := pickFloat64(m,
		"cost_usd",
		"cost",
		"usd",
		"amount_usd",
	)
	costSource := ""
	if cost > 0 {
		costSource = "api"
	}

	apiKey := pickString(m,
		"api_key_id",
		"api_key",
		"key_id",
		"key",
	)
	if apiKey == "" {
		apiKey = "unknown"
	}

	model := pickString(m,
		"model",
		"model_id",
		"model_name",
	)

	return report.UsageRecord{
		Provider:             provider,
		Date:                 date,
		APIKeyID:             apiKey,
		Model:                model,
		InputTokens:          input,
		OutputTokens:         output,
		CacheReadTokens:      cacheRead,
		CacheWriteTokens:     cacheWrite,
		InputTextTokens:      pickInt64(m, "input_text_tokens"),
		OutputTextTokens:     pickInt64(m, "output_text_tokens"),
		InputAudioTokens:     pickInt64(m, "input_audio_tokens"),
		OutputAudioTokens:    pickInt64(m, "output_audio_tokens"),
		InputImageTokens:     pickInt64(m, "input_image_tokens"),
		OutputImageTokens:    pickInt64(m, "output_image_tokens"),
		CacheReadTextTokens:  pickInt64(m, "input_cached_text_tokens"),
		CacheReadAudioTokens: pickInt64(m, "input_cached_audio_tokens"),
		CacheReadImageTokens: pickInt64(m, "input_cached_image_tokens"),
		CostUSD:              cost,
		CostEstimated:        false,
		CostSource:           costSource,
	}
}
