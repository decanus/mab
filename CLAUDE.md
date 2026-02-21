# CLAUDE.md

## Project

Market-aware buyback execution engine (`github.com/decanus/mab`). Classifies market regimes and adapts DeFi protocol token buyback execution accordingly.

## Commands

```bash
go build ./...                    # Build all packages
go test ./... -race -count=1      # Run all tests with race detector
go vet ./...                      # Lint
go build -o buyback ./cmd/buyback # Build CLI binary
```

## Architecture

- `cmd/buyback/` — Cobra CLI entry point
- `pkg/types/` — Shared domain types (MarketRegime, OHLCV, TradingPair, Order, Fill)
- `internal/regime/` — Pluggable regime classifier with ScoreFunc interface
- `internal/engine/` — Core execution engine (budget calc, VWAP targeting, order splitting)
- `internal/exchange/` — Exchange interface, Router, CoW Protocol client, mock exchange
- `internal/market/` — MarketDataProvider interface, CoinGecko client, mock provider
- `internal/store/` — Store interface, SQLite persistence, auto-migrations
- `internal/config/` — YAML config loading and validation

## Key Conventions

- **No float64 for money** — use `github.com/shopspring/decimal` everywhere
- **Interfaces over implementations** — Exchange, market.Provider, store.Store, regime.ScoreFunc
- **Context propagation** — all I/O functions take `context.Context`
- **Structured logging** — `log/slog` with `[REGIME]`, `[BUDGET]`, `[VWAP]`, `[ROUTE]`, `[FILL]`, `[SUMMARY]` prefixes
- **Parallel I/O** — use `golang.org/x/sync/errgroup` for concurrent exchange/market queries
- **Table-driven tests** — standard `testing.T`, no test frameworks
- **Compile-time interface checks** — `var _ Interface = (*Impl)(nil)` in each implementation file
- **Error wrapping** — `fmt.Errorf("package: operation: %w", err)`

## Configuration

Config lives in `configs/example.yaml`. Key fields:

- `token`, `quote_asset` — ticker symbols (e.g., `"UNI"`, `"USDC"`)
- `token_address`, `quote_asset_address` — ERC-20 contract addresses (required for CoW Protocol)
- `coingecko_id` — CoinGecko coin ID for market data lookup
- `coingecko_api_url` — CoinGecko API base URL (default: public API; swap for pro endpoint)
- `cow_api_url` — CoW Protocol API base URL (chain-specific: `/mainnet`, `/xdai`, `/sepolia`)
- `wallet_address` — Ethereum address for exchange interactions
- `annual_budget_usd`, `regime_multipliers`, `base_discount`, `vol_scaling_factor` — budget and targeting
- `max_slippage_bps`, `order_split_count`, `execution_jitter`, `min_execution_size` — execution params
- `prefer_batch_auction`, `exchange_weight_overrides` — routing preferences
- `db_path` — SQLite database path

## Extension Points

- New market signal: implement `regime.ScoreFunc`, pass to `regime.NewClassifier()`
- New exchange: implement `exchange.Exchange`, register in router
- New market data source: implement `market.Provider`
