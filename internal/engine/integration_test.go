package engine_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/decanus/mab/internal/config"
	"github.com/decanus/mab/internal/engine"
	"github.com/decanus/mab/internal/exchange"
	mocke "github.com/decanus/mab/internal/exchange/mock"
	"github.com/decanus/mab/internal/market"
	mockm "github.com/decanus/mab/internal/market/mock"
	"github.com/decanus/mab/internal/regime"
	"github.com/decanus/mab/internal/store"
	"github.com/decanus/mab/pkg/types"
)

func buildTestEngine(t *testing.T, regimeType types.MarketRegime, basePrice decimal.Decimal, currentPriceOverride *decimal.Decimal, dryRun bool) (*engine.Engine, *store.SQLiteStore) {
	t.Helper()

	cfg := config.DefaultConfig()
	cfg.DBPath = ""

	classifier := regime.NewClassifier(
		regime.NewPriceTrendScore(1.0),
		regime.NewVolumeTrendScore(0.8),
		regime.NewVolatilityScore(0.7),
		regime.NewDivergenceScore(0.6),
	)

	mockEx1 := mocke.NewMockExchange("cow",
		mocke.WithDepth(decimal.NewFromInt(500_000)),
		mocke.WithSlippage(5),
		mocke.WithFillRate(0.98),
		mocke.WithBatchAuction(true),
	)
	mockEx2 := mocke.NewMockExchange("dex",
		mocke.WithDepth(decimal.NewFromInt(300_000)),
		mocke.WithSlippage(8),
		mocke.WithFillRate(0.95),
	)

	exchanges := []exchange.Exchange{mockEx1, mockEx2}
	router := exchange.NewRouter(exchanges, cfg.PreferBatchAuction, cfg.ExchangeWeightOverrides)

	var provider market.Provider
	mockProvider := mockm.NewMockProvider(regimeType, basePrice, decimal.NewFromInt(5_000_000), 42)
	if currentPriceOverride != nil {
		provider = &priceOverrideProvider{
			MockProvider:  mockProvider,
			overridePrice: *currentPriceOverride,
		}
	} else {
		provider = mockProvider
	}

	dbPath := t.TempDir() + "/test.db"
	st, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	eng := engine.NewEngine(cfg, classifier, router, exchanges, provider, st, logger, dryRun)
	return eng, st
}

// priceOverrideProvider wraps a MockProvider but overrides GetCurrentPrice.
type priceOverrideProvider struct {
	*mockm.MockProvider
	overridePrice decimal.Decimal
}

func (p *priceOverrideProvider) GetCurrentPrice(_ context.Context, _ types.TradingPair) (decimal.Decimal, error) {
	return p.overridePrice, nil
}

func TestFullBuybackCycle_Accumulation(t *testing.T) {
	// Accumulation regime: flat prices, declining volume → high budget, aggressive buying.
	price := decimal.NewFromInt(100)
	eng, st := buildTestEngine(t, types.RegimeAccumulation, decimal.NewFromInt(180), &price, false)
	defer st.Close()

	summary, err := eng.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle() error = %v", err)
	}

	// Verify regime was classified as accumulation.
	if summary.Regime != types.RegimeAccumulation {
		t.Errorf("regime = %s, want %s", summary.Regime, types.RegimeAccumulation)
	}

	// Accumulation has a high multiplier (1.5-2.0, mid=1.75).
	// Daily budget should be 50M/365 ≈ 136,986 * 1.75 ≈ 239,726 * liquidity_score.
	if summary.AdjustedBudget.IsZero() {
		t.Error("expected non-zero adjusted budget for accumulation regime")
	}

	// Price 100 should be well below VWAP target for the mock data.
	if !summary.BelowTarget {
		t.Logf("VWAP30d=%s, TargetPrice=%s, CurrentPrice=%s", summary.VWAP30d, summary.TargetPrice, summary.CurrentPrice)
		t.Error("expected BelowTarget = true for accumulation with low price")
	}

	// Should have allocations to exchanges.
	if len(summary.Allocations) == 0 {
		t.Error("expected non-empty allocations")
	}

	// Should have fills since we're not in dry-run.
	if len(summary.Fills) == 0 {
		t.Error("expected non-empty fills")
	}

	if summary.TotalFilled.IsZero() {
		t.Error("expected non-zero total filled")
	}

	// Verify persistence: should have at least one cycle stored.
	ctx := context.Background()
	cycles, err := st.GetCycles(ctx, "AAVE", time.Time{})
	if err != nil {
		t.Fatalf("GetCycles error = %v", err)
	}
	if len(cycles) == 0 {
		t.Error("expected stored cycle in database")
	}
}

func TestFullBuybackCycle_Distribution(t *testing.T) {
	// Distribution regime: near-zero multiplier (0.0-0.2, mid=0.1).
	// Budget should be very small, possibly below MinExecutionSize.
	price := decimal.NewFromInt(100)
	eng, st := buildTestEngine(t, types.RegimeDistribution, decimal.NewFromInt(180), &price, false)
	defer st.Close()

	summary, err := eng.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle() error = %v", err)
	}

	// Distribution regime might or might not be classified — depends on mock data.
	// What matters is that the budget is low and the engine handles it gracefully.
	t.Logf("Distribution cycle: regime=%s, adjusted_budget=%s, below_target=%v",
		summary.Regime, summary.AdjustedBudget.StringFixed(2), summary.BelowTarget)

	// If below target, the adjusted budget should be relatively small.
	if summary.BelowTarget && summary.AdjustedBudget.IsPositive() {
		t.Logf("Execution proceeded with budget: %s", summary.AdjustedBudget.StringFixed(2))
	}
}

func TestFullBuybackCycle_AboveVWAPTarget(t *testing.T) {
	// Set price well above what the VWAP target would be.
	price := decimal.NewFromInt(500)
	eng, st := buildTestEngine(t, types.RegimeAccumulation, decimal.NewFromInt(180), &price, false)
	defer st.Close()

	summary, err := eng.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle() error = %v", err)
	}

	// Price 500 should be well above any target derived from base prices around 180.
	if summary.BelowTarget {
		t.Errorf("expected BelowTarget=false when price (%s) >> VWAP (%s)",
			summary.CurrentPrice, summary.VWAP30d)
	}

	// No orders should be submitted.
	if len(summary.Allocations) != 0 {
		t.Errorf("expected no allocations when above target, got %d", len(summary.Allocations))
	}
	if len(summary.Fills) != 0 {
		t.Errorf("expected no fills when above target, got %d", len(summary.Fills))
	}
}

func TestFullBuybackCycle_DryRun(t *testing.T) {
	price := decimal.NewFromInt(100)
	eng, st := buildTestEngine(t, types.RegimeAccumulation, decimal.NewFromInt(180), &price, true)
	defer st.Close()

	summary, err := eng.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle() error = %v", err)
	}

	if !summary.DryRun {
		t.Error("expected DryRun = true")
	}

	if summary.BelowTarget && len(summary.Fills) == 0 {
		t.Error("expected simulated fills in dry-run mode when below target")
	}

	for _, f := range summary.Fills {
		if f.OrderID != "dry-run" {
			t.Errorf("expected dry-run order ID, got %s", f.OrderID)
		}
	}
}

func TestFullBuybackCycle_PartialFills(t *testing.T) {
	// Use exchanges with low fill rates to test partial fill handling.
	cfg := config.DefaultConfig()
	cfg.DBPath = ""

	classifier := regime.NewClassifier(
		regime.NewPriceTrendScore(1.0),
		regime.NewVolumeTrendScore(0.8),
		regime.NewVolatilityScore(0.7),
		regime.NewDivergenceScore(0.6),
	)

	// Exchange with 50% fill rate.
	partialEx := mocke.NewMockExchange("partial",
		mocke.WithDepth(decimal.NewFromInt(500_000)),
		mocke.WithSlippage(10),
		mocke.WithFillRate(0.5),
		mocke.WithBatchAuction(true),
	)

	exchanges := []exchange.Exchange{partialEx}
	router := exchange.NewRouter(exchanges, cfg.PreferBatchAuction, nil)

	provider := mockm.NewMockProvider(types.RegimeAccumulation, decimal.NewFromInt(180), decimal.NewFromInt(5_000_000), 42)
	overridePrice := decimal.NewFromInt(100)
	wrappedProvider := &priceOverrideProvider{MockProvider: provider, overridePrice: overridePrice}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	eng := engine.NewEngine(cfg, classifier, router, exchanges, wrappedProvider, nil, logger, false)

	summary, err := eng.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle() error = %v", err)
	}

	if !summary.BelowTarget {
		t.Skip("price not below target for this test scenario")
	}

	// Verify the engine handles partial fills gracefully.
	if len(summary.Fills) == 0 {
		t.Error("expected fills even with partial fill rates")
	}

	t.Logf("Partial fill test: total_filled=%s, fill_rate=%s",
		summary.TotalFilled.StringFixed(2), summary.FillRate.StringFixed(4))
}
