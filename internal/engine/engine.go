package engine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"
	"golang.org/x/sync/errgroup"

	"github.com/decanus/mab/internal/config"
	"github.com/decanus/mab/internal/exchange"
	"github.com/decanus/mab/internal/market"
	"github.com/decanus/mab/internal/regime"
	"github.com/decanus/mab/internal/store"
	"github.com/decanus/mab/pkg/types"
)

// Engine is the core buyback execution engine. It coordinates regime classification,
// budget calculation, VWAP targeting, order routing, and persistence.
type Engine struct {
	cfg        *config.BuybackConfig
	classifier *regime.Classifier
	router     *exchange.Router
	exchanges  []exchange.Exchange
	provider   market.Provider
	store      store.Store
	logger     *slog.Logger
	dryRun     bool
}

// NewEngine creates a new Engine with the given dependencies.
func NewEngine(
	cfg *config.BuybackConfig,
	classifier *regime.Classifier,
	router *exchange.Router,
	exchanges []exchange.Exchange,
	provider market.Provider,
	st store.Store,
	logger *slog.Logger,
	dryRun bool,
) *Engine {
	return &Engine{
		cfg:        cfg,
		classifier: classifier,
		router:     router,
		exchanges:  exchanges,
		provider:   provider,
		store:      st,
		logger:     logger,
		dryRun:     dryRun,
	}
}

// RunCycle executes a single buyback cycle. It classifies the market regime,
// calculates budgets, checks VWAP conditions, routes and executes orders,
// and persists results to the store.
func (e *Engine) RunCycle(ctx context.Context) (*types.CycleSummary, error) {
	pair := types.TradingPair{
		Base:         e.cfg.Token,
		Quote:        e.cfg.QuoteAsset,
		BaseAddress:  e.cfg.TokenAddress,
		QuoteAddress: e.cfg.QuoteAssetAddress,
	}
	log := e.logger.With("token", e.cfg.Token, "pair", pair.String())

	// 1. Get OHLCV data for 30 daily periods.
	ohlcv, err := e.provider.GetOHLCV(ctx, pair, "1d", 30)
	if err != nil {
		return nil, fmt.Errorf("engine: get ohlcv: %w", err)
	}

	// 2. Classify regime.
	classification, err := e.classifier.Classify(ctx, ohlcv)
	if err != nil {
		return nil, fmt.Errorf("engine: classify regime: %w", err)
	}

	// 3. Log regime.
	log.Info("[REGIME]",
		"regime", classification.Regime,
		"confidence", classification.Confidence,
		"signals", classification.Breakdown,
	)

	// 4. Calculate daily budget.
	daysPerYear := decimal.NewFromInt(365)
	dailyBudget := e.cfg.AnnualBudgetUSD.Div(daysPerYear)

	// 5. Get regime multiplier (midpoint of range).
	regimeRange := e.cfg.RegimeMultipliers[classification.Regime]
	regimeMultiplier := regimeRange.Mid()

	// 6. Calculate realized volatility from OHLCV data.
	realizedVol := CalculateRealizedVol(ohlcv)

	// 7. Get current price from provider; fall back to latest OHLCV close.
	currentPrice, err := e.provider.GetCurrentPrice(ctx, pair)
	if err != nil {
		log.Warn("failed to get live price, using latest OHLCV close", "error", err)
		currentPrice = ohlcv[len(ohlcv)-1].Close
	}

	// 8. Calculate 30d VWAP from the already-fetched OHLCV data.
	vwap30d := CalculateVWAP(ohlcv)

	// 9. Calculate target price.
	targetPrice := CalculateTargetPrice(vwap30d, e.cfg.BaseDiscount, e.cfg.VolScalingFactor, realizedVol, classification.Regime)

	// 10-11. Calculate liquidity score.
	preLiquidityBudget := dailyBudget.Mul(regimeMultiplier)
	totalDepth, err := e.queryTotalDepth(ctx, pair, e.cfg.MaxSlippageBps)
	if err != nil {
		return nil, fmt.Errorf("engine: query total depth: %w", err)
	}

	liquidityScore := decimal.NewFromInt(1)
	if preLiquidityBudget.IsPositive() {
		ls := totalDepth.Div(preLiquidityBudget)
		if ls.LessThan(decimal.NewFromInt(1)) {
			liquidityScore = ls
		}
	}

	adjustedBudget := preLiquidityBudget.Mul(liquidityScore)

	// 12. Log budget.
	log.Info("[BUDGET]",
		"daily", dailyBudget.StringFixed(2),
		"regime_mult", regimeMultiplier.StringFixed(4),
		"liquidity_score", liquidityScore.StringFixed(4),
		"adjusted", adjustedBudget.StringFixed(2),
	)

	// 13. Log VWAP.
	belowTarget := ShouldExecute(currentPrice, targetPrice)
	status := "ABOVE_TARGET"
	if belowTarget {
		status = "BELOW_TARGET"
	}

	discountTarget := e.cfg.BaseDiscount.Add(realizedVol.Mul(e.cfg.VolScalingFactor))
	log.Info("[VWAP]",
		"30d_vwap", vwap30d.StringFixed(4),
		"discount_target", discountTarget.StringFixed(4),
		"target_price", targetPrice.StringFixed(4),
		"current_price", currentPrice.StringFixed(4),
		"status", status,
	)

	summary := &types.CycleSummary{
		Regime:         classification.Regime,
		Confidence:     classification.Confidence,
		DailyBudget:    dailyBudget,
		AdjustedBudget: adjustedBudget,
		VWAP30d:        vwap30d,
		TargetPrice:    targetPrice,
		CurrentPrice:   currentPrice,
		BelowTarget:    belowTarget,
		DryRun:         e.dryRun,
	}

	// 14. Check if current price is below target.
	if !belowTarget {
		log.Info("[SKIP]", "reason", "price above target")
		e.persistCycle(ctx, log, pair, classification, summary)
		return summary, nil
	}

	// 15. Check minimum execution size.
	if adjustedBudget.LessThan(e.cfg.MinExecutionSize) {
		log.Info("[SKIP]",
			"reason", "adjusted budget below minimum execution size",
			"adjusted_budget", adjustedBudget.StringFixed(2),
			"min_execution_size", e.cfg.MinExecutionSize.StringFixed(2),
		)
		e.persistCycle(ctx, log, pair, classification, summary)
		return summary, nil
	}

	// 16. Route the order across exchanges.
	allocations, err := e.router.Route(ctx, pair, adjustedBudget, e.cfg.MaxSlippageBps, e.cfg.OrderSplitCount)
	if err != nil {
		return nil, fmt.Errorf("engine: route order: %w", err)
	}
	summary.Allocations = allocations

	// 17. Log route.
	routeDesc := make([]string, 0, len(allocations))
	reasons := make([]string, 0, len(allocations))
	for _, a := range allocations {
		routeDesc = append(routeDesc, fmt.Sprintf("%s:%.0f%%", a.Exchange, a.Weight*100))
		reasons = append(reasons, a.Reason)
	}
	log.Info("[ROUTE]",
		"exchanges", routeDesc,
		"reasons", reasons,
	)

	// 18. Execute or simulate.
	var fills []types.Fill
	if e.dryRun {
		log.Info("[DRY-RUN]", "msg", "skipping order execution")
		// Simulate fills for dry-run: each allocation produces one fill with zero values.
		for _, a := range allocations {
			fills = append(fills, types.Fill{
				OrderID:     "dry-run",
				Exchange:    a.Exchange,
				AmountUSD:   a.AmountUSD,
				AvgPrice:    currentPrice,
				SlippageBps: 0,
				MEVSavedUSD: decimal.Zero,
				FilledAt:    time.Now(),
			})
		}
	} else {
		fills, err = e.router.Execute(ctx, pair, allocations, e.cfg.MaxSlippageBps, e.cfg.OrderSplitCount, e.cfg.ExecutionJitter)
		if err != nil {
			return nil, fmt.Errorf("engine: execute orders: %w", err)
		}
	}
	summary.Fills = fills

	// 19. Log orders and fills.
	for _, f := range fills {
		log.Info("[FILL]",
			"order_id", f.OrderID,
			"exchange", f.Exchange,
			"amount_usd", f.AmountUSD.StringFixed(2),
			"avg_price", f.AvgPrice.StringFixed(4),
			"slippage_bps", f.SlippageBps,
		)
	}

	// 20. Calculate summary stats.
	totalFilled := decimal.Zero
	totalSlippage := 0.0
	for _, f := range fills {
		totalFilled = totalFilled.Add(f.AmountUSD)
		totalSlippage += f.SlippageBps
	}
	summary.TotalFilled = totalFilled

	if adjustedBudget.IsPositive() {
		summary.FillRate = totalFilled.Div(adjustedBudget)
	}
	if len(fills) > 0 {
		summary.AvgSlippageBps = totalSlippage / float64(len(fills))
	}

	// 21. Log summary.
	log.Info("[SUMMARY]",
		"cycle_total", totalFilled.StringFixed(2),
		"target", adjustedBudget.StringFixed(2),
		"fill_rate", summary.FillRate.StringFixed(4),
		"avg_slippage_bps", summary.AvgSlippageBps,
	)

	// 22. Persist to store.
	e.persistCycle(ctx, log, pair, classification, summary)

	// 23. Return summary.
	return summary, nil
}

// queryTotalDepth queries all exchanges for liquidity in parallel and returns
// the sum of all available depth in USD.
func (e *Engine) queryTotalDepth(ctx context.Context, pair types.TradingPair, slippageBps int) (decimal.Decimal, error) {
	type result struct {
		depth decimal.Decimal
	}

	results := make([]result, len(e.exchanges))
	g, gctx := errgroup.WithContext(ctx)

	for i, ex := range e.exchanges {
		g.Go(func() error {
			liq, err := ex.GetLiquidity(gctx, pair, slippageBps)
			if err != nil {
				// Exchange unavailable; treat as zero depth.
				results[i] = result{depth: decimal.Zero}
				return nil
			}
			results[i] = result{depth: liq.DepthUSD}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return decimal.Zero, err
	}

	total := decimal.Zero
	for _, r := range results {
		total = total.Add(r.depth)
	}

	return total, nil
}

// persistCycle saves the cycle, orders, fills, and regime history to the store.
func (e *Engine) persistCycle(
	ctx context.Context,
	log *slog.Logger,
	pair types.TradingPair,
	classification *types.ClassificationResult,
	summary *types.CycleSummary,
) {
	if e.store == nil {
		return
	}

	cycle := &store.Cycle{
		Timestamp:         time.Now(),
		Token:             pair.Base,
		QuoteAsset:        pair.Quote,
		Regime:            classification.Regime,
		RegimeConfidence:  classification.Confidence,
		DailyBudgetUSD:    summary.DailyBudget,
		AdjustedBudgetUSD: summary.AdjustedBudget,
		VWAP30d:           summary.VWAP30d,
		TargetPrice:       summary.TargetPrice,
		CurrentPrice:      summary.CurrentPrice,
		PriceBelowTarget:  summary.BelowTarget,
		DryRun:            summary.DryRun,
	}

	cycleID, err := e.store.SaveCycle(ctx, cycle)
	if err != nil {
		log.Error("failed to save cycle", "error", err)
		return
	}

	// Save orders and fills.
	for _, alloc := range summary.Allocations {
		order := &store.StoreOrder{
			CycleID:       cycleID,
			Exchange:      alloc.Exchange,
			AmountUSD:     alloc.AmountUSD,
			OrderType:     string(alloc.OrderType),
			SubOrderCount: e.cfg.OrderSplitCount,
			JitterPct:     e.cfg.ExecutionJitter,
			Status:        "routed",
			CreatedAt:     time.Now(),
		}

		orderID, err := e.store.SaveOrder(ctx, order)
		if err != nil {
			log.Error("failed to save order", "exchange", alloc.Exchange, "error", err)
			continue
		}

		// Find fills for this exchange allocation.
		for _, fill := range summary.Fills {
			if fill.Exchange != alloc.Exchange {
				continue
			}
			storeFill := &store.StoreFill{
				OrderID:         orderID,
				FilledAmountUSD: fill.AmountUSD,
				AvgPrice:        fill.AvgPrice,
				SlippageBps:     fill.SlippageBps,
				MEVSavedUSD:     fill.MEVSavedUSD,
				FilledAt:        fill.FilledAt,
			}
			if _, err := e.store.SaveFill(ctx, storeFill); err != nil {
				log.Error("failed to save fill", "order_id", orderID, "error", err)
			}
		}
	}

	// Save regime history.
	regimeEntry := &store.RegimeEntry{
		Timestamp:  time.Now(),
		Token:      pair.Base,
		Regime:     classification.Regime,
		Confidence: classification.Confidence,
	}
	if err := e.store.SaveRegime(ctx, regimeEntry); err != nil {
		log.Error("failed to save regime entry", "error", err)
	}
}
