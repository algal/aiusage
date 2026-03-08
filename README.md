# aiusage

CLI tool that reports AI API usage and costs for OpenAI and Anthropic. Two modes:

- **API key usage** — token counts and dollar costs from provider admin APIs, broken out by provider, API key, date, and model
- **Subscription quota** — live quota percentage and reset times for Claude Code and Codex subscriptions

## Setup

### API key usage (requires admin API keys)

```bash
export OPENAI_ADMIN_KEY="..."
export ANTHROPIC_ADMIN_KEY="..."
```

### Subscription quota (requires CLI login)

Log into Claude Code (`claude`) and/or Codex (`codex`) on your machine. aiusage reads their stored OAuth credentials automatically.

## Usage

```bash
# Show both subscription quota and API key usage (last 7 days)
aiusage

# API key usage only
aiusage api
aiusage api --provider openai --start 2026-03-01 --end 2026-03-05
aiusage api --group-by model
aiusage api --json

# Subscription quota only
aiusage subs
aiusage subs --provider anthropic
aiusage subs --force   # bypass 5-minute cache
aiusage subs --json
```

## Pricing

Dollar costs come from provider cost APIs when available (`cost_src=truth_allocated`). When not available, costs are estimated from model rates (`cost_src=estimated_pricing`).

Pricing is resolved in order: built-in fallback → LiteLLM (fetched live, cached 24h) → `--pricing-file` (manual override). The report header shows the active pricing source and its date.

```bash
# Show current pricing table and source
aiusage prices

# Compare built-in rates against LiteLLM (writes pricing-override.json if mismatched)
aiusage prices --check

# Manual override
aiusage api --pricing-file ./pricing-override.json
```

## Build

```bash
go build ./cmd/aiusage
```

## Notes

- API endpoints are configurable via `--openai-usage-path`, `--anthropic-usage-path`, etc.
- If pricing is missing for a model, cost is zero and a warning is shown.
- Subscription quota is cached for 5 minutes to avoid rate limits. Use `--force` to bypass.
- LiteLLM pricing is cached for 24 hours. If LiteLLM is unreachable, built-in fallback rates are used.
