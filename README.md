# aiusage

`aiusage` is a Go CLI that reports API usage in token count and dollars, split by API key and date, for OpenAI and Anthropic.

## Status

- Implemented now:
- OpenAI usage fetch (default path: `/v1/organization/usage/completions`)
- Anthropic usage fetch (default path: `/v1/organizations/usage_report/messages`)
- Aggregation by `provider + date + api_key`
- Optional breakout by model with `--group-by model` (`provider + date + api_key + model`)
- Token columns: input, output, cache read, cache write, total
  - For OpenAI, `input` uses uncached input when the API provides it, so `total` avoids cache double-counting.
- Daily cost truth ingestion:
  - OpenAI costs API (`/v1/organization/costs`) by default
  - Anthropic costs API (`/v1/organizations/cost_report`) by default
  - optional CSV overrides via `--openai-cost-csv` / `--anthropic-cost-csv`
- Truth-cost allocation to key/model rows by daily token share (`cost_src=truth_allocated`)
- Modality-aware pricing fallback (text/audio/image/cache fields) when truth cost is unavailable
- CLI table output + `--json`
- `plan` command that prints planned features

## Build

```bash
cd /home/algal/gits/aiusage
go build ./cmd/aiusage
```

## Required env vars

Set one or both, depending on provider selection:

```bash
export OPENAI_ADMIN_KEY="..."
export ANTHROPIC_ADMIN_KEY="..."
```

## Usage

```bash
# Default: last 7 days, both providers
./aiusage

# Explicit window and provider
./aiusage --provider all --start 2026-03-01 --end 2026-03-05

# Break out by model too
./aiusage --provider all --group-by model --start 2026-03-01 --end 2026-03-05

# Use explicit OpenAI cost truth CSV override
./aiusage --provider openai --openai-cost-csv /tmp/openai_cost_truth.csv --start 2026-02-04 --end 2026-03-05

# JSON mode
./aiusage report --provider openai --json

# Show roadmap
./aiusage plan
```

## Pricing overrides

Use `--pricing-file` with a JSON file to override built-in model rates:

```bash
./aiusage --pricing-file ./pricing.sample.json
```

`pricing.sample.json` schema:

```json
{
  "providers": {
    "openai": {
      "default": {
        "input_per_mtok": 0,
        "output_per_mtok": 0,
        "cache_read_per_mtok": 0,
        "cache_write_per_mtok": 0
      },
      "models": {
        "gpt-4o": {
          "input_per_mtok": 2.5,
          "output_per_mtok": 10.0,
          "cache_read_per_mtok": 1.25,
          "cache_write_per_mtok": 0
        },
        "gpt-4o-mini-tts*": {
          "input_text_per_mtok": 0.6,
          "output_audio_per_mtok": 12.0
        }
      }
    }
  }
}
```

## Planned features

See `aiusage plan` for the live roadmap. Current outline:

1. Billing precision
- More allocation modes beyond token-share (model-share, fixed-split, weighted)

2. Identity and attribution
- API key alias mapping
- Optional group-by project/workspace/model

3. Operational usability
- CSV/Parquet export
- Alerts and anomaly detection
- Incremental sync for cron workflows

4. Reliability
- Retries/backoff
- Fixture-based regression tests for API shape drift

5. Security
- Keyring/secret-manager integrations
- Key ID redaction mode

## Notes

- Provider admin usage APIs can evolve; `--openai-usage-path` and `--anthropic-usage-path` are configurable.
- Provider cost endpoints are also configurable via `--openai-cost-path` and `--anthropic-cost-path`.
- If pricing is missing for a model, cost is left at zero and surfaced as a warning.
