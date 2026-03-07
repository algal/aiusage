package report

import (
	"sort"
)

// UsageRecord is a normalized provider usage event at daily granularity.
type UsageRecord struct {
	Provider             string  `json:"provider"`
	Date                 string  `json:"date"`
	APIKeyID             string  `json:"api_key_id"`
	Model                string  `json:"model,omitempty"`
	InputTokens          int64   `json:"input_tokens"`
	OutputTokens         int64   `json:"output_tokens"`
	CacheReadTokens      int64   `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens     int64   `json:"cache_write_tokens,omitempty"`
	InputTextTokens      int64   `json:"input_text_tokens,omitempty"`
	OutputTextTokens     int64   `json:"output_text_tokens,omitempty"`
	InputAudioTokens     int64   `json:"input_audio_tokens,omitempty"`
	OutputAudioTokens    int64   `json:"output_audio_tokens,omitempty"`
	InputImageTokens     int64   `json:"input_image_tokens,omitempty"`
	OutputImageTokens    int64   `json:"output_image_tokens,omitempty"`
	CacheReadTextTokens  int64   `json:"cache_read_text_tokens,omitempty"`
	CacheReadAudioTokens int64   `json:"cache_read_audio_tokens,omitempty"`
	CacheReadImageTokens int64   `json:"cache_read_image_tokens,omitempty"`
	CostUSD              float64 `json:"cost_usd"`
	CostEstimated        bool    `json:"cost_estimated"`
	CostSource           string  `json:"cost_source,omitempty"`
}

// AggregatedRow is usage grouped by provider + date + API key.
type AggregatedRow struct {
	Provider         string  `json:"provider"`
	Date             string  `json:"date"`
	APIKeyID         string  `json:"api_key_id"`
	Model            string  `json:"model,omitempty"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	CostEstimated    bool    `json:"cost_estimated"`
	CostSource       string  `json:"cost_source,omitempty"`
}

func Aggregate(records []UsageRecord, groupBy string) []AggregatedRow {
	byModel := groupBy == "model"
	keyed := make(map[string]*AggregatedRow, len(records))
	for _, r := range records {
		provider := r.Provider
		if provider == "" {
			provider = "unknown"
		}
		date := r.Date
		if date == "" {
			date = "unknown"
		}
		apiKey := r.APIKeyID
		if apiKey == "" {
			apiKey = "unknown"
		}
		model := r.Model
		if model == "" {
			model = "unknown"
		}
		k := provider + "\x1f" + date + "\x1f" + apiKey
		if byModel {
			k += "\x1f" + model
		}
		row, ok := keyed[k]
		if !ok {
			row = &AggregatedRow{Provider: provider, Date: date, APIKeyID: apiKey}
			if byModel {
				row.Model = model
			}
			keyed[k] = row
		}
		row.InputTokens += r.InputTokens
		row.OutputTokens += r.OutputTokens
		row.CacheReadTokens += r.CacheReadTokens
		row.CacheWriteTokens += r.CacheWriteTokens
		row.TotalTokens += r.InputTokens + r.OutputTokens + r.CacheReadTokens + r.CacheWriteTokens
		row.CostUSD += r.CostUSD
		if r.CostEstimated {
			row.CostEstimated = true
		}
		switch {
		case row.CostSource == "":
			row.CostSource = r.CostSource
		case r.CostSource == "":
		case row.CostSource == r.CostSource:
		default:
			row.CostSource = "mixed"
		}
	}

	rows := make([]AggregatedRow, 0, len(keyed))
	for _, row := range keyed {
		rows = append(rows, *row)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Provider != rows[j].Provider {
			return rows[i].Provider < rows[j].Provider
		}
		if rows[i].Date != rows[j].Date {
			return rows[i].Date < rows[j].Date
		}
		if rows[i].APIKeyID != rows[j].APIKeyID {
			return rows[i].APIKeyID < rows[j].APIKeyID
		}
		return rows[i].Model < rows[j].Model
	})

	return rows
}
