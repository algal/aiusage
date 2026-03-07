package main

import (
	"aiusage/internal/cli"
	"aiusage/internal/pricing"
	"aiusage/internal/providers"
	"aiusage/internal/report"
	"aiusage/internal/subscription"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	Version = "v0.1.0"
	Build   = "dev"
)

type options struct {
	provider             string
	groupBy              string
	start                string
	end                  string
	timeout              time.Duration
	jsonOutput           bool
	colorMode            string
	showVersion          bool
	showHelp             bool
	pricingFile          string
	openAIAdminKeyEnv    string
	anthropicAdminKeyEnv string
	openAIBaseURL        string
	anthropicBaseURL     string
	openAIUsagePath      string
	anthropicUsagePath   string
	openAICostPath       string
	anthropicCostPath    string
	openAICostCSV        string
	anthropicCostCSV     string
	limit                int
}

type outputPayload struct {
	GeneratedAt string                 `json:"generated_at"`
	DateRange   map[string]string      `json:"date_range"`
	GroupBy     string                 `json:"group_by"`
	Rows        []report.AggregatedRow `json:"rows"`
	Warnings    []string               `json:"warnings,omitempty"`
}

func main() {
	if len(os.Args) > 1 {
		switch strings.ToLower(strings.TrimSpace(os.Args[1])) {
		case "help", "--help", "-h":
			opts := defaultOptions()
			printUsage(os.Stdout, opts.colorMode)
			return
		case "report":
			if err := runReport(os.Args[2:]); err != nil {
				fatal(err)
			}
			return
		case "sub", "subscription":
			if err := runSub(os.Args[2:]); err != nil {
				fatal(err)
			}
			return
		}
	}

	runAll()
}

func runAll() {
	// Subscription usage (from CLI OAuth credentials)
	subErr := runSub(nil)

	fmt.Println()

	// API key usage (from provider admin APIs)
	reportErr := runReport(os.Args[1:])

	if subErr != nil && reportErr != nil {
		fatal(fmt.Errorf("sub: %v; report: %v", subErr, reportErr))
	}
}

func runReport(args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n\n", err)
		printUsage(os.Stderr, opts.colorMode)
		return err
	}
	if opts.showVersion {
		fmt.Printf("aiusage %s (%s)\n", Version, Build)
		return nil
	}
	if opts.showHelp {
		printUsage(os.Stdout, opts.colorMode)
		return nil
	}

	startDay, endDay, err := parseDateRange(opts.start, opts.end)
	if err != nil {
		return err
	}
	endExclusive := endDay.AddDate(0, 0, 1)

	book := pricing.DefaultBook()
	if strings.TrimSpace(opts.pricingFile) != "" {
		override, err := pricing.LoadFile(opts.pricingFile)
		if err != nil {
			return err
		}
		book = pricing.Merge(book, override)
	}

	ctx := context.Background()
	client := providers.NewHTTPClient(opts.timeout)
	warnings := make([]string, 0, 8)
	records := make([]report.UsageRecord, 0, 512)

	includeOpenAI := opts.provider == "all" || opts.provider == "openai"
	includeAnthropic := opts.provider == "all" || opts.provider == "anthropic"
	openAIKey := strings.TrimSpace(os.Getenv(opts.openAIAdminKeyEnv))
	anthropicKey := strings.TrimSpace(os.Getenv(opts.anthropicAdminKeyEnv))

	if includeOpenAI {
		if openAIKey == "" {
			err := fmt.Errorf("missing %s for OpenAI", opts.openAIAdminKeyEnv)
			if opts.provider == "openai" {
				return err
			}
			warnings = append(warnings, err.Error())
		} else {
			rows, fetchErr := providers.FetchOpenAIUsage(ctx, client, providers.OpenAIConfig{
				BaseURL: opts.openAIBaseURL,
				Path:    opts.openAIUsagePath,
				APIKey:  openAIKey,
				Start:   startDay,
				End:     endExclusive,
				Limit:   opts.limit,
			})
			if fetchErr != nil {
				if opts.provider == "openai" {
					return fmt.Errorf("openai fetch failed: %w", fetchErr)
				}
				warnings = append(warnings, fmt.Sprintf("openai fetch failed: %v", fetchErr))
			} else {
				records = append(records, rows...)
			}
		}
	}

	if includeAnthropic {
		if anthropicKey == "" {
			err := fmt.Errorf("missing %s for Anthropic", opts.anthropicAdminKeyEnv)
			if opts.provider == "anthropic" {
				return err
			}
			warnings = append(warnings, err.Error())
		} else {
			rows, fetchErr := providers.FetchAnthropicUsage(ctx, client, providers.AnthropicConfig{
				BaseURL: opts.anthropicBaseURL,
				Path:    opts.anthropicUsagePath,
				APIKey:  anthropicKey,
				Start:   startDay,
				End:     endExclusive,
				Limit:   opts.limit,
			})
			if fetchErr != nil {
				if opts.provider == "anthropic" {
					return fmt.Errorf("anthropic fetch failed: %w", fetchErr)
				}
				warnings = append(warnings, fmt.Sprintf("anthropic fetch failed: %v", fetchErr))
			} else {
				records = append(records, rows...)
			}
		}
	}

	if len(records) == 0 {
		if len(warnings) > 0 {
			return errors.New(strings.Join(warnings, "; "))
		}
		return errors.New("no usage records found for selected providers/date range")
	}

	dailyTruth := map[string]map[string]float64{
		"openai":    {},
		"anthropic": {},
	}
	if includeOpenAI && openAIKey != "" {
		costs, costErr := providers.FetchOpenAIDailyCosts(client, providers.OpenAICostConfig{
			BaseURL: opts.openAIBaseURL,
			Path:    opts.openAICostPath,
			APIKey:  openAIKey,
			Start:   startDay,
			End:     endExclusive,
			Limit:   opts.limit,
		})
		if costErr != nil {
			warnings = append(warnings, fmt.Sprintf("openai cost fetch failed: %v", costErr))
		} else {
			for date, amount := range costs {
				dailyTruth["openai"][date] += amount
			}
		}
	}
	if includeAnthropic && anthropicKey != "" {
		costs, costErr := providers.FetchAnthropicDailyCosts(client, providers.AnthropicCostConfig{
			BaseURL: opts.anthropicBaseURL,
			Path:    opts.anthropicCostPath,
			APIKey:  anthropicKey,
			Start:   startDay,
			End:     endExclusive,
			Limit:   opts.limit,
		})
		if costErr != nil {
			warnings = append(warnings, fmt.Sprintf("anthropic cost fetch failed: %v", costErr))
		} else {
			for date, amount := range costs {
				dailyTruth["anthropic"][date] += amount
			}
		}
	}

	if strings.TrimSpace(opts.openAICostCSV) != "" {
		costs, csvErr := parseDailyCostCSV(opts.openAICostCSV)
		if csvErr != nil {
			return csvErr
		}
		dailyTruth["openai"] = costs
	}
	if strings.TrimSpace(opts.anthropicCostCSV) != "" {
		costs, csvErr := parseDailyCostCSV(opts.anthropicCostCSV)
		if csvErr != nil {
			return csvErr
		}
		dailyTruth["anthropic"] = costs
	}

	allocateTruthCosts(records, dailyTruth)

	unknownModels := map[string]struct{}{}
	for i := range records {
		if records[i].CostSource == "truth_allocated" || records[i].CostSource == "api" {
			continue
		}
		cost, ok := book.Estimate(
			records[i].Provider,
			records[i].Model,
			pricing.Usage{
				InputTokens:          records[i].InputTokens,
				OutputTokens:         records[i].OutputTokens,
				CacheReadTokens:      records[i].CacheReadTokens,
				CacheWriteTokens:     records[i].CacheWriteTokens,
				InputTextTokens:      records[i].InputTextTokens,
				OutputTextTokens:     records[i].OutputTextTokens,
				InputAudioTokens:     records[i].InputAudioTokens,
				OutputAudioTokens:    records[i].OutputAudioTokens,
				InputImageTokens:     records[i].InputImageTokens,
				OutputImageTokens:    records[i].OutputImageTokens,
				CacheReadTextTokens:  records[i].CacheReadTextTokens,
				CacheReadAudioTokens: records[i].CacheReadAudioTokens,
				CacheReadImageTokens: records[i].CacheReadImageTokens,
			},
		)
		if !ok {
			key := records[i].Provider + "/" + strings.TrimSpace(records[i].Model)
			if key == records[i].Provider+"/" {
				key = records[i].Provider + "/(missing-model)"
			}
			unknownModels[key] = struct{}{}
			records[i].CostSource = "unknown"
			continue
		}
		records[i].CostUSD = cost
		records[i].CostEstimated = true
		records[i].CostSource = "estimated_pricing"
	}
	if len(unknownModels) > 0 {
		keys := make([]string, 0, len(unknownModels))
		for k := range unknownModels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		warnings = append(warnings, "missing model pricing for: "+strings.Join(keys, ", ")+" (set --pricing-file to override)")
	}

	agg := report.Aggregate(records, opts.groupBy)
	if opts.jsonOutput {
		payload := outputPayload{
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
			DateRange: map[string]string{
				"start": startDay.Format("2006-01-02"),
				"end":   endDay.Format("2006-01-02"),
			},
			GroupBy:  opts.groupBy,
			Rows:     agg,
			Warnings: warnings,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}

	printReportText(opts, agg, warnings, startDay, endDay)
	return nil
}

func parseOptions(args []string) (options, error) {
	opts := defaultOptions()
	fs := flag.NewFlagSet("aiusage", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.StringVar(&opts.provider, "provider", opts.provider, "Provider: all|openai|anthropic")
	fs.StringVar(&opts.groupBy, "group-by", opts.groupBy, "Grouping mode: key,date|model")
	fs.StringVar(&opts.start, "start", opts.start, "Start date (YYYY-MM-DD)")
	fs.StringVar(&opts.end, "end", opts.end, "End date inclusive (YYYY-MM-DD)")
	fs.DurationVar(&opts.timeout, "timeout", opts.timeout, "HTTP timeout")
	fs.BoolVar(&opts.jsonOutput, "json", false, "Output JSON")
	fs.StringVar(&opts.colorMode, "color", opts.colorMode, "Color mode: auto|always|never")
	fs.StringVar(&opts.pricingFile, "pricing-file", "", "Optional pricing override JSON file")
	fs.StringVar(&opts.openAIAdminKeyEnv, "openai-admin-key-env", opts.openAIAdminKeyEnv, "Env var for OpenAI admin API key")
	fs.StringVar(&opts.anthropicAdminKeyEnv, "anthropic-admin-key-env", opts.anthropicAdminKeyEnv, "Env var for Anthropic admin API key")
	fs.StringVar(&opts.openAIBaseURL, "openai-base-url", opts.openAIBaseURL, "OpenAI API base URL")
	fs.StringVar(&opts.anthropicBaseURL, "anthropic-base-url", opts.anthropicBaseURL, "Anthropic API base URL")
	fs.StringVar(&opts.openAIUsagePath, "openai-usage-path", opts.openAIUsagePath, "OpenAI usage endpoint path")
	fs.StringVar(&opts.anthropicUsagePath, "anthropic-usage-path", opts.anthropicUsagePath, "Anthropic usage endpoint path")
	fs.StringVar(&opts.openAICostPath, "openai-cost-path", opts.openAICostPath, "OpenAI costs endpoint path")
	fs.StringVar(&opts.anthropicCostPath, "anthropic-cost-path", opts.anthropicCostPath, "Anthropic costs endpoint path")
	fs.StringVar(&opts.openAICostCSV, "openai-cost-csv", opts.openAICostCSV, "Optional OpenAI daily cost CSV override")
	fs.StringVar(&opts.anthropicCostCSV, "anthropic-cost-csv", opts.anthropicCostCSV, "Optional Anthropic daily cost CSV override")
	fs.IntVar(&opts.limit, "limit", opts.limit, "Per-page bucket limit")
	fs.BoolVar(&opts.showVersion, "version", false, "Print version and exit")
	fs.BoolVar(&opts.showHelp, "help", false, "Show help")
	fs.BoolVar(&opts.showHelp, "h", false, "Show help")

	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() > 0 {
		return opts, fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}

	opts.provider = strings.ToLower(strings.TrimSpace(opts.provider))
	switch opts.provider {
	case "all", "openai", "anthropic":
	default:
		return opts, fmt.Errorf("invalid --provider=%q (expected all|openai|anthropic)", opts.provider)
	}
	opts.colorMode = strings.ToLower(strings.TrimSpace(opts.colorMode))
	switch opts.colorMode {
	case "auto", "always", "never":
	default:
		return opts, fmt.Errorf("invalid --color=%q (expected auto|always|never)", opts.colorMode)
	}
	if opts.timeout <= 0 {
		return opts, errors.New("--timeout must be > 0")
	}
	if opts.limit <= 0 {
		return opts, errors.New("--limit must be > 0")
	}
	var groupErr error
	opts.groupBy, groupErr = normalizeGroupBy(opts.groupBy)
	if groupErr != nil {
		return opts, groupErr
	}
	return opts, nil
}

func defaultOptions() options {
	now := time.Now().UTC()
	end := now.Format("2006-01-02")
	start := now.AddDate(0, 0, -6).Format("2006-01-02")
	return options{
		provider:             "all",
		groupBy:              "key,date",
		start:                start,
		end:                  end,
		timeout:              30 * time.Second,
		colorMode:            "auto",
		openAIAdminKeyEnv:    "OPENAI_ADMIN_KEY",
		anthropicAdminKeyEnv: "ANTHROPIC_ADMIN_KEY",
		openAIBaseURL:        "https://api.openai.com",
		anthropicBaseURL:     "https://api.anthropic.com",
		openAIUsagePath:      "/v1/organization/usage/completions",
		anthropicUsagePath:   "/v1/organizations/usage_report/messages",
		openAICostPath:       "/v1/organization/costs",
		anthropicCostPath:    "/v1/organizations/cost_report",
		limit:                31,
	}
}

func parseDateRange(startRaw, endRaw string) (time.Time, time.Time, error) {
	startRaw = strings.TrimSpace(startRaw)
	endRaw = strings.TrimSpace(endRaw)
	start, err := time.ParseInLocation("2006-01-02", startRaw, time.UTC)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid --start date %q (expected YYYY-MM-DD)", startRaw)
	}
	end, err := time.ParseInLocation("2006-01-02", endRaw, time.UTC)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid --end date %q (expected YYYY-MM-DD)", endRaw)
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("--end (%s) must be on/after --start (%s)", endRaw, startRaw)
	}
	return start, end, nil
}

func allocateTruthCosts(records []report.UsageRecord, truth map[string]map[string]float64) {
	buckets := map[string][]int{}
	for i := range records {
		key := records[i].Provider + "\x1f" + records[i].Date
		buckets[key] = append(buckets[key], i)
	}

	for bucketKey, idxs := range buckets {
		parts := strings.SplitN(bucketKey, "\x1f", 2)
		if len(parts) != 2 {
			continue
		}
		provider := parts[0]
		date := parts[1]
		byProvider, ok := truth[provider]
		if !ok {
			continue
		}
		totalCost, ok := byProvider[date]
		if !ok {
			continue
		}
		if totalCost < 0 {
			continue
		}

		totalWeight := 0.0
		weights := make([]float64, len(idxs))
		for i, idx := range idxs {
			r := records[idx]
			w := float64(r.InputTokens + r.OutputTokens + r.CacheReadTokens + r.CacheWriteTokens)
			if w < 0 {
				w = 0
			}
			weights[i] = w
			totalWeight += w
		}

		allocated := 0.0
		for i, idx := range idxs {
			share := 0.0
			switch {
			case i == len(idxs)-1:
				share = totalCost - allocated
			case totalWeight > 0:
				share = totalCost * (weights[i] / totalWeight)
			default:
				share = totalCost / float64(len(idxs))
			}
			allocated += share
			records[idx].CostUSD = share
			records[idx].CostEstimated = false
			records[idx].CostSource = "truth_allocated"
		}
	}
}

func parseDailyCostCSV(path string) (map[string]float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open cost csv %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read cost csv header %s: %w", path, err)
	}

	idx := map[string]int{}
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}

	dateIdx := -1
	for _, key := range []string{"usage_date_utc", "start_time_iso", "date", "start_date"} {
		if v, ok := idx[key]; ok {
			dateIdx = v
			break
		}
	}
	if dateIdx < 0 {
		return nil, fmt.Errorf("cost csv %s missing date column", path)
	}

	amountIdx := -1
	for _, key := range []string{"amount_value", "cost_usd", "amount_usd", "usd", "amount"} {
		if v, ok := idx[key]; ok {
			amountIdx = v
			break
		}
	}
	if amountIdx < 0 {
		return nil, fmt.Errorf("cost csv %s missing amount column", path)
	}

	out := map[string]float64{}
	for {
		row, readErr := r.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read cost csv %s: %w", path, readErr)
		}
		if dateIdx >= len(row) || amountIdx >= len(row) {
			continue
		}
		date := parseDateFromCell(row[dateIdx])
		if date == "" {
			continue
		}
		raw := strings.TrimSpace(row[amountIdx])
		if raw == "" {
			continue
		}
		amount, parseErr := strconv.ParseFloat(raw, 64)
		if parseErr != nil {
			continue
		}
		out[date] += amount
	}

	return out, nil
}

func parseDateFromCell(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if len(raw) >= 10 {
		candidate := raw[:10]
		if _, err := time.Parse("2006-01-02", candidate); err == nil {
			return candidate
		}
	}
	if ts, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.Unix(ts, 0).UTC().Format("2006-01-02")
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC().Format("2006-01-02")
	}
	return ""
}

func normalizeGroupBy(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, " ", "")
	switch s {
	case "key,date", "date,key":
		return "key,date", nil
	case "model":
		return "model", nil
	default:
		return "", fmt.Errorf("invalid --group-by=%q (expected key,date|model)", raw)
	}
}

func printReportText(opts options, rows []report.AggregatedRow, warnings []string, start, end time.Time) {
	st := cli.NewStyle(opts.colorMode, os.Stdout)
	fmt.Println(st.Section("API Key Usage"))
	fmt.Println(st.Muted("  source: provider admin APIs (pay-per-token billing)"))
	fmt.Println(st.Header("Window"))
	fmt.Printf("  provider: %s\n", opts.provider)
	fmt.Printf("  group_by: %s\n", opts.groupBy)
	fmt.Printf("  start:    %s\n", start.Format("2006-01-02"))
	fmt.Printf("  end:      %s\n", end.Format("2006-01-02"))
	fmt.Printf("  rows:     %d\n\n", len(rows))

	tableRows := make([][]string, 0, len(rows))
	for _, row := range rows {
		source := rowCostSource(row)
		if opts.groupBy == "model" {
			tableRows = append(tableRows, []string{
				row.Provider,
				row.Date,
				trimKey(row.APIKeyID),
				row.Model,
				cli.FormatInt(row.InputTokens),
				cli.FormatInt(row.OutputTokens),
				cli.FormatInt(row.CacheReadTokens),
				cli.FormatInt(row.CacheWriteTokens),
				cli.FormatInt(row.TotalTokens),
				cli.FormatUSD(row.CostUSD),
				source,
			})
			continue
		}
		tableRows = append(tableRows, []string{
			row.Provider,
			row.Date,
			trimKey(row.APIKeyID),
			cli.FormatInt(row.InputTokens),
			cli.FormatInt(row.OutputTokens),
			cli.FormatInt(row.CacheReadTokens),
			cli.FormatInt(row.CacheWriteTokens),
			cli.FormatInt(row.TotalTokens),
			cli.FormatUSD(row.CostUSD),
			source,
		})
	}
	if opts.groupBy == "model" {
		fmt.Println(st.Header("Usage By Provider Date Key Model"))
		fmt.Println(cli.RenderTable([]string{
			"provider", "date", "api_key", "model", "input", "output", "cache_r", "cache_w", "total", "usd", "cost_src",
		}, tableRows))
	} else {
		fmt.Println(st.Header("Usage By Key And Date"))
		fmt.Println(cli.RenderTable([]string{
			"provider", "date", "api_key", "input", "output", "cache_r", "cache_w", "total", "usd", "cost_src",
		}, tableRows))
	}

	byProvider := make(map[string]report.AggregatedRow)
	providerSources := make(map[string]map[string]struct{})
	for _, row := range rows {
		t := byProvider[row.Provider]
		t.Provider = row.Provider
		t.InputTokens += row.InputTokens
		t.OutputTokens += row.OutputTokens
		t.CacheReadTokens += row.CacheReadTokens
		t.CacheWriteTokens += row.CacheWriteTokens
		t.TotalTokens += row.TotalTokens
		t.CostUSD += row.CostUSD
		if row.CostEstimated {
			t.CostEstimated = true
		}
		byProvider[row.Provider] = t
		src := rowCostSource(row)
		if providerSources[row.Provider] == nil {
			providerSources[row.Provider] = map[string]struct{}{}
		}
		providerSources[row.Provider][src] = struct{}{}
	}
	providers := make([]string, 0, len(byProvider))
	for p := range byProvider {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	fmt.Println()
	fmt.Println(st.Header("Provider Totals"))
	totalRows := make([][]string, 0, len(providers))
	for _, p := range providers {
		row := byProvider[p]
		source := providerCostSource(providerSources[p])
		totalRows = append(totalRows, []string{
			p,
			cli.FormatInt(row.TotalTokens),
			cli.FormatUSD(row.CostUSD),
			source,
		})
	}
	fmt.Println(cli.RenderTable([]string{"provider", "total_tokens", "usd", "cost_src"}, totalRows))

	if len(warnings) > 0 {
		fmt.Println()
		fmt.Println(st.Warn("Warnings"))
		for _, w := range warnings {
			fmt.Printf("  - %s\n", w)
		}
	}
}

func providerCostSource(s map[string]struct{}) string {
	if len(s) == 0 {
		return "api"
	}
	if len(s) == 1 {
		for k := range s {
			return k
		}
	}
	return "mixed"
}

func rowCostSource(row report.AggregatedRow) string {
	if strings.TrimSpace(row.CostSource) != "" {
		return row.CostSource
	}
	if row.CostEstimated {
		return "estimated"
	}
	if row.CostUSD == 0 && row.TotalTokens > 0 {
		return "unknown"
	}
	return "api"
}

func trimKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	if len(s) <= 22 {
		return s
	}
	return s[:9] + "..." + s[len(s)-9:]
}

func printUsage(out *os.File, colorMode string) {
	st := cli.NewStyle(colorMode, out)
	exe := filepath.Base(os.Args[0])

	fmt.Fprintf(out, "%s: %s [report flags]\n", st.Label("Usage"), exe)
	fmt.Fprintf(out, "       %s report [flags]\n", exe)
	fmt.Fprintf(out, "       %s sub [--provider=all] [--force] [--json] [--color=auto]\n", exe)
	fmt.Fprintf(out, "%s: %s (%s)\n\n", st.Label("Build"), Version, Build)

	fmt.Fprintln(out, "Token and dollar usage reporter for OpenAI + Anthropic, split by API key and date.")
	fmt.Fprintln(out, "Also tracks subscription quota usage via 'sub' command.")
	fmt.Fprintln(out, "Dollar values come from API-provided cost fields when present, otherwise model-rate estimates.")
	fmt.Fprintln(out)

	fmt.Fprintln(out, st.Header("Flags"))
	fmt.Fprintln(out, "  -h, --help                        Show help.")
	fmt.Fprintln(out, "      --version                     Print version and exit.")
	fmt.Fprintln(out, "      --provider=all                all|openai|anthropic.")
	fmt.Fprintln(out, "      --group-by=key,date           key,date|model.")
	fmt.Fprintln(out, "      --start=YYYY-MM-DD            Start date.")
	fmt.Fprintln(out, "      --end=YYYY-MM-DD              End date (inclusive).")
	fmt.Fprintln(out, "      --timeout=30s                 HTTP timeout.")
	fmt.Fprintln(out, "      --limit=31                    Per-page bucket limit for provider APIs.")
	fmt.Fprintln(out, "      --openai-admin-key-env=OPENAI_ADMIN_KEY")
	fmt.Fprintln(out, "      --anthropic-admin-key-env=ANTHROPIC_ADMIN_KEY")
	fmt.Fprintln(out, "      --openai-base-url=https://api.openai.com")
	fmt.Fprintln(out, "      --anthropic-base-url=https://api.anthropic.com")
	fmt.Fprintln(out, "      --openai-usage-path=/v1/organization/usage/completions")
	fmt.Fprintln(out, "      --anthropic-usage-path=/v1/organizations/usage_report/messages")
	fmt.Fprintln(out, "      --openai-cost-path=/v1/organization/costs")
	fmt.Fprintln(out, "      --anthropic-cost-path=/v1/organizations/cost_report")
	fmt.Fprintln(out, "      --openai-cost-csv=PATH        Optional daily cost truth CSV override.")
	fmt.Fprintln(out, "      --anthropic-cost-csv=PATH     Optional daily cost truth CSV override.")
	fmt.Fprintln(out, "      --pricing-file=PATH           Optional JSON pricing overrides.")
	fmt.Fprintln(out, "      --json                        Output machine-readable JSON.")
	fmt.Fprintln(out, "      --color=auto                  auto|always|never.")
	fmt.Fprintln(out)

	fmt.Fprintln(out, st.Header("Examples"))
	fmt.Fprintf(out, "  %s --provider all --start 2026-03-01 --end 2026-03-05\n", exe)
	fmt.Fprintf(out, "  %s --provider openai --openai-cost-csv /tmp/openai_cost_truth.csv --start 2026-03-01 --end 2026-03-05\n", exe)
	fmt.Fprintf(out, "  %s --provider all --group-by model --start 2026-03-01 --end 2026-03-05\n", exe)
	fmt.Fprintf(out, "  %s report --provider openai --json\n", exe)
}

func runSub(args []string) error {
	fs := flag.NewFlagSet("sub", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var provider string
	var jsonOutput bool
	var colorMode string
	var timeout time.Duration
	var force bool
	fs.StringVar(&provider, "provider", "all", "Provider: all|openai|anthropic")
	fs.BoolVar(&force, "force", false, "Bypass cache and fetch fresh data")
	fs.BoolVar(&jsonOutput, "json", false, "Output JSON")
	fs.StringVar(&colorMode, "color", "auto", "Color mode: auto|always|never")
	fs.DurationVar(&timeout, "timeout", 15*time.Second, "HTTP timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	provider = strings.ToLower(strings.TrimSpace(provider))
	switch provider {
	case "all", "openai", "anthropic":
	default:
		return fmt.Errorf("invalid --provider=%q (expected all|openai|anthropic)", provider)
	}

	client := &http.Client{Timeout: timeout}
	statuses := make([]subscription.Status, 0, 2)
	warnings := make([]string, 0, 2)

	if provider == "all" || provider == "anthropic" {
		if s, err := subscription.FetchClaudeStatus(client, force); err != nil {
			warnings = append(warnings, fmt.Sprintf("anthropic: %v", err))
		} else {
			statuses = append(statuses, *s)
		}
	}

	if provider == "all" || provider == "openai" {
		if s, err := subscription.FetchCodexStatus(client, force); err != nil {
			warnings = append(warnings, fmt.Sprintf("openai: %v", err))
		} else {
			statuses = append(statuses, *s)
		}
	}

	if len(statuses) == 0 {
		if len(warnings) > 0 {
			return fmt.Errorf("no subscription data: %s", strings.Join(warnings, "; "))
		}
		return fmt.Errorf("no subscription credentials found")
	}

	if jsonOutput {
		payload := struct {
			GeneratedAt string                `json:"generated_at"`
			Statuses    []subscription.Status `json:"statuses"`
			Warnings    []string              `json:"warnings,omitempty"`
		}{
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
			Statuses:    statuses,
			Warnings:    warnings,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}

	printSubText(statuses, warnings, colorMode)
	return nil
}

func printSubText(statuses []subscription.Status, warnings []string, colorMode string) {
	st := cli.NewStyle(colorMode, os.Stdout)
	fmt.Println(st.Section("Subscription Quota"))
	fmt.Println(st.Muted("  source: CLI OAuth credentials (live quota from provider)"))

	rows := make([][]string, 0, 8)
	for _, s := range statuses {
		for _, w := range s.Windows {
			rows = append(rows, []string{
				s.Provider,
				planOrDash(s.Plan),
				w.Name,
				fmt.Sprintf("%.0f%%", w.UsedPercent),
				formatResetsIn(w.ResetsAt),
			})
		}
		if s.ExtraUsage != nil && s.ExtraUsage.Enabled {
			rows = append(rows, []string{
				s.Provider,
				planOrDash(s.Plan),
				"extra",
				fmt.Sprintf("$%.2f/$%.2f", s.ExtraUsage.UsedUSD, s.ExtraUsage.LimitUSD),
				"monthly",
			})
		}
		if s.Credits != nil && s.Credits.HasCredits {
			rows = append(rows, []string{
				s.Provider,
				planOrDash(s.Plan),
				"credits",
				fmt.Sprintf("$%.2f", s.Credits.Balance),
				"balance",
			})
		}
	}

	fmt.Println(cli.RenderTable(
		[]string{"provider", "plan", "window", "used", "resets_in"},
		rows,
	))

	if len(warnings) > 0 {
		fmt.Println()
		fmt.Println(st.Warn("Warnings"))
		for _, w := range warnings {
			fmt.Printf("  - %s\n", w)
		}
	}
}

func planOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func formatResetsIn(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Until(t)
	if d <= 0 {
		return "now"
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	if hours >= 24 {
		days := hours / 24
		hours = hours % 24
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	return fmt.Sprintf("%dh %dm", hours, mins)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
