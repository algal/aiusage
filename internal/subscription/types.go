package subscription

import "time"

type Window struct {
	Provider    string  `json:"provider"`
	Name        string  `json:"name"`
	UsedPercent float64 `json:"used_percent"`
	ResetsAt    time.Time `json:"resets_at"`
}

type ExtraUsage struct {
	Enabled      bool    `json:"enabled"`
	UsedUSD      float64 `json:"used_usd"`
	LimitUSD     float64 `json:"limit_usd"`
	Utilization  float64 `json:"utilization"`
}

type Credits struct {
	HasCredits bool    `json:"has_credits"`
	Balance    float64 `json:"balance"`
}

type Status struct {
	Provider   string      `json:"provider"`
	Account    string      `json:"account,omitempty"`
	Plan       string      `json:"plan,omitempty"`
	Windows    []Window    `json:"windows"`
	ExtraUsage *ExtraUsage `json:"extra_usage,omitempty"`
	Credits    *Credits    `json:"credits,omitempty"`
}
