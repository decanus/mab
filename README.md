# Market-Aware Buybacks

DeFi protocols spend millions on token buybacks. Most execute them the dumbest way possible: fixed amounts on fixed schedules, fully predictable, trivially front-run, buying hardest exactly when they should be buying least.

Aerodrome showed there's a better way. Their Public Goods Fund runs what they call a ["programmatic market-aware buyback model"](https://x.com/aerodromefi/status/2016935022893089042), and the numbers back it up: 150M+ AERO acquired and max-locked, 17% of circulating supply absorbed, executed across varying market conditions without blowing up slippage or telegraphing entries. When AERO dropped to $0.40, the PGF stepped in with 940K tokens. When the market was running, they pulled back. Simple in theory, hard to execute well.

This project is an open-source implementation of that idea. A buyback execution engine that reads market conditions and adapts.

## What it does

The engine runs a loop. Each cycle:

1. **Classifies the market regime.** Is the token in accumulation (nobody's paying attention), markup (price is running), distribution (smart money is exiting), or markdown (everything's dumping)? It figures this out from price trend, volume behavior, volatility, and divergence signals.

2. **Adjusts the buyback budget.** Accumulation regime? Buy aggressively, you're getting cheap fills in quiet markets. Markup? Pull back, the market is doing your job. Distribution? Nearly pause, because absorbing sells at tops is lighting money on fire. Markdown? Buy steadily, the protocol has an infinite time horizon.

3. **Targets a VWAP discount.** The engine only executes when the current price is below a rolling 30-day VWAP minus a discount that scales with volatility. No chasing.

4. **Splits orders across exchanges.** Routes proportionally based on liquidity depth and fill quality. Prefers CoW Protocol for larger chunks because batch auctions give you MEV protection for free. Adds timing jitter so execution isn't predictable.

5. **Logs everything.** Regime classification with signal breakdown, budget adjustment rationale, routing decisions, fill quality. Full auditability.

## Why this matters

A protocol spending $50M/year on buybacks is running a treasury operation. The difference between smart execution and naive execution compounds every single day. Consider:

**Naive approach:** buy $137K/day, every day, same time, same venue. Bots see it coming. You're buying in markup when the market would reprice itself anyway. You're barely buying in accumulation when liquidity is cheapest. You get front-run constantly. Maybe 15-30% of your budget is wasted on slippage and adverse selection.

**Market-aware approach:** the same $50M/year, but concentrated in periods where your marginal dollar has the most impact. Accumulation phases where you're the only buyer. Prices below VWAP where you're getting a structural discount. Split across venues with MEV protection. Same budget, much better execution.

Aerodrome figured this out. Aave is spending $50M/year on buybacks. Uniswap just activated its fee switch with $590M in the treasury. Every protocol with a buyback program needs this.

## The scoring system

The regime classifier is built on pluggable scoring functions. Four ship by default: price trend (SMA slope), volume trend (short vs. long window), volatility (realized vol), and divergence (price-volume disagreement). Each scores every regime independently, gets weighted, and the classifier picks the winner.

The interface is simple. Want to add funding rate data? Whale wallet concentration? Governance proposal sentiment? Implement `ScoreFunc`, pass it to the classifier. No changes to the engine, router, or anything else. The scoring function is the unit of extensibility.

## Exchange routing

CoW Protocol is the primary venue. Batch auctions mean your orders get coincidence-of-wants matching and MEV protection without paying for it. The exchange layer is a Go interface, and the router handles splitting across multiple venues weighted by liquidity depth.

The architecture supports any exchange that can quote liquidity and fill an order. CoW ships as the real implementation. A mock exchange ships for offline testing and demos. Hyperliquid and 1inch are stubbed out for when you need CLOB depth or aggregator routing.

## Regime multipliers

The regime determines how aggressively the engine spends relative to the daily budget baseline:

| Regime | Multiplier | Rationale |
|---|---|---|
| Accumulation | 1.5-2.0x | Low attention, cheap liquidity. Buy hard. |
| Markup | 0.3-0.5x | Market is doing your job. Don't chase. |
| Distribution | 0.0-0.2x | Absorbing sells at tops is wasteful. |
| Markdown | 0.8-1.0x | Protocol has infinite horizon. Buy the fear. |

On a $50M/year budget, the daily baseline is ~$137K. In accumulation that becomes ~$240K. In distribution it drops to ~$14K. Same annual spend, radically different allocation.

## Building

Requires Go 1.21+ and CGO (for SQLite).

```bash
go build -o buyback ./cmd/buyback
```

## Running it

```bash
# Full cycle with mock data — classify regime, calculate budget, simulate execution
./buyback run --config configs/example.yaml --mock --dry-run

# Just check what regime a token is in
./buyback regime --token AAVE --quote USDC --mock

# See the execution plan without doing anything
./buyback plan --config configs/example.yaml --mock

# Run continuously, evaluating every 15 minutes
./buyback run --config configs/example.yaml --interval 15m --mock --dry-run
```

Everything is config-driven: annual budget, regime multipliers, VWAP discount parameters, slippage limits, exchange preferences. One YAML file.

## Querying history

Every cycle, order, fill, and regime classification is persisted to SQLite. Query it from the CLI:

```bash
# Execution history for the last 7 days
./buyback history --token AAVE --since 7d

# Regime classification history
./buyback regimes --token AAVE --last 30

# Fill quality stats — average slippage, fill rate, MEV saved
./buyback stats --token AAVE --since 30d
```

## Sample output

What a single cycle looks like with `--mock --dry-run`:

```
[REGIME]   token=AAVE regime=accumulation confidence=0.10
           signals: price_trend=markup, volume_trend=accumulation, volatility=accumulation, divergence=distribution
[BUDGET]   daily=136986.30 regime_mult=1.7500 liquidity_score=1.0000 adjusted=239726.03
[VWAP]     30d_vwap=181.2345 discount_target=0.0315 target_price=178.3827 current_price=100.0000 status=BELOW_TARGET
[ROUTE]    exchanges=[cow:68%, dex:32%] reason="cow preferred for batch auction, 1.3x boost"
[FILL]     exchange=cow amount_usd=80371.30 avg_price=0.9995
[FILL]     exchange=dex amount_usd=71917.81 avg_price=0.9992
[FILL]     exchange=cow amount_usd=80371.30 avg_price=0.9995
[SUMMARY]  cycle_total=232660.42 target=239726.03 fill_rate=97.05% avg_slippage_bps=0
```

The engine classified accumulation (low vol, declining volume), applied a 1.75x multiplier to the daily budget, confirmed price was below the VWAP discount target, routed 68% to CoW (batch auction preference) and 32% to the secondary venue, and filled 97% of the adjusted budget.

