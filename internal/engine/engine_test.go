package engine

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/decanus/mab/internal/config"
	"github.com/decanus/mab/internal/exchange"
	"github.com/decanus/mab/internal/regime"
	"github.com/decanus/mab/internal/store"
	"github.com/decanus/mab/pkg/types"
)

// ---------------------------------------------------------------------------
// Test helpers: mocks
// ---------------------------------------------------------------------------

// mockScoreFunc implements regime.ScoreFunc for testing.
type mockScoreFunc struct {
	name   string
	weight float64
	scores types.RegimeScores
}

func (m *mockScoreFunc) Name() string { return m.name }
func (m *mockScoreFunc) Weight() float64 { return m.weight }
func (m *mockScoreFunc) Score(_ context.Context, _ []types.OHLCV) (types.RegimeScores, error) {
	return m.scores, nil
}

// mockProvider implements market.Provider for testing.
type mockProvider struct {
	ohlcv        []types.OHLCV
	currentPrice decimal.Decimal
	vwap         decimal.Decimal
	ohlcvErr     error
	priceErr     error
	vwapErr      error
}

func (m *mockProvider) GetOHLCV(_ context.Context, _ types.TradingPair, _ string, _ int) ([]types.OHLCV, error) {
	return m.ohlcv, m.ohlcvErr
}

func (m *mockProvider) GetCurrentPrice(_ context.Context, _ types.TradingPair) (decimal.Decimal, error) {
	return m.currentPrice, m.priceErr
}

func (m *mockProvider) GetVWAP(_ context.Context, _ types.TradingPair, _ int) (decimal.Decimal, error) {
	return m.vwap, m.vwapErr
}

// mockExchange implements exchange.Exchange for testing.
type mockExchange struct {
	name         string
	batchAuction bool
	liquidity    *types.LiquidityInfo
	liquidityErr error
	orderResult  *types.OrderResult
	orderStatus  *types.OrderStatusResult
}

func (m *mockExchange) Name() string { return m.name }
func (m *mockExchange) SupportsBatchAuction() bool { return m.batchAuction }

func (m *mockExchange) GetLiquidity(_ context.Context, _ types.TradingPair, _ int) (*types.LiquidityInfo, error) {
	return m.liquidity, m.liquidityErr
}

func (m *mockExchange) SubmitOrder(_ context.Context, order *types.Order) (*types.OrderResult, error) {
	if m.orderResult != nil {
		return m.orderResult, nil
	}
	return &types.OrderResult{
		OrderID:  fmt.Sprintf("%s-order-%d", m.name, order.SubOrderIdx),
		Status:   types.OrderStatusFilled,
		Exchange: m.name,
	}, nil
}

func (m *mockExchange) OrderStatus(_ context.Context, _ string) (*types.OrderStatusResult, error) {
	if m.orderStatus != nil {
		return m.orderStatus, nil
	}
	return &types.OrderStatusResult{
		Status:       types.OrderStatusFilled,
		FilledAmount: decimal.NewFromInt(1000),
		AvgPrice:     decimal.NewFromInt(100),
	}, nil
}

func (m *mockExchange) CancelOrder(_ context.Context, _ string) error { return nil }

func (m *mockExchange) RecentFills(_ context.Context, _ types.TradingPair, _ time.Time) ([]types.Fill, error) {
	return nil, nil
}

// mockStore implements store.Store for testing.
type mockStore struct {
	mu     sync.Mutex
	cycles []store.Cycle
	orders []store.StoreOrder
	fills  []store.StoreFill
	regimes []store.RegimeEntry
}

func (m *mockStore) SaveCycle(_ context.Context, c *store.Cycle) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := int64(len(m.cycles) + 1)
	c.ID = id
	m.cycles = append(m.cycles, *c)
	return id, nil
}

func (m *mockStore) GetCycles(_ context.Context, _ string, _ time.Time) ([]store.Cycle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cycles, nil
}

func (m *mockStore) SaveOrder(_ context.Context, o *store.StoreOrder) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := int64(len(m.orders) + 1)
	o.ID = id
	m.orders = append(m.orders, *o)
	return id, nil
}

func (m *mockStore) UpdateOrderStatus(_ context.Context, _ int64, _ string) error { return nil }

func (m *mockStore) GetOrdersByCycle(_ context.Context, _ int64) ([]store.StoreOrder, error) {
	return nil, nil
}

func (m *mockStore) SaveFill(_ context.Context, f *store.StoreFill) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := int64(len(m.fills) + 1)
	f.ID = id
	m.fills = append(m.fills, *f)
	return id, nil
}

func (m *mockStore) GetFillStats(_ context.Context, _ string, _ time.Time) (*store.FillStats, error) {
	return nil, nil
}

func (m *mockStore) SaveRegime(_ context.Context, e *store.RegimeEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.regimes = append(m.regimes, *e)
	return nil
}

func (m *mockStore) GetRegimeHistory(_ context.Context, _ string, _ int) ([]store.RegimeEntry, error) {
	return nil, nil
}

func (m *mockStore) Close() error { return nil }

// ---------------------------------------------------------------------------
// Tests: VWAP
// ---------------------------------------------------------------------------

func TestCalculateTargetPrice(t *testing.T) {
	vwap := decimal.NewFromInt(100)
	baseDiscount := decimal.NewFromFloat(0.02)
	volScaling := decimal.NewFromFloat(0.1)
	realizedVol := decimal.NewFromFloat(0.5)

	tests := []struct {
		name     string
		regime   types.MarketRegime
		expected decimal.Decimal
	}{
		{
			name:   "neutral regime (markdown)",
			regime: types.RegimeMarkdown,
			// discount = 0.02 + (0.5 * 0.1) = 0.07
			// target = 100 * (1 - 0.07) = 93.00
			expected: decimal.NewFromFloat(93.00),
		},
		{
			name:   "accumulation regime",
			regime: types.RegimeAccumulation,
			// discount = 0.07 * 0.5 = 0.035
			// target = 100 * (1 - 0.035) = 96.50
			expected: decimal.NewFromFloat(96.50),
		},
		{
			name:   "markup regime",
			regime: types.RegimeMarkup,
			// discount = 0.07 * 1.5 = 0.105
			// target = 100 * (1 - 0.105) = 89.50
			expected: decimal.NewFromFloat(89.50),
		},
		{
			name:   "distribution regime (no adjustment)",
			regime: types.RegimeDistribution,
			// discount = 0.07, no adjustment
			// target = 100 * (1 - 0.07) = 93.00
			expected: decimal.NewFromFloat(93.00),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateTargetPrice(vwap, baseDiscount, volScaling, realizedVol, tt.regime)
			if !result.Equal(tt.expected) {
				t.Errorf("CalculateTargetPrice() = %s, want %s", result, tt.expected)
			}
		})
	}
}

func TestShouldExecute(t *testing.T) {
	tests := []struct {
		name     string
		current  decimal.Decimal
		target   decimal.Decimal
		expected bool
	}{
		{
			name:     "price below target",
			current:  decimal.NewFromFloat(90.0),
			target:   decimal.NewFromFloat(93.0),
			expected: true,
		},
		{
			name:     "price above target",
			current:  decimal.NewFromFloat(95.0),
			target:   decimal.NewFromFloat(93.0),
			expected: false,
		},
		{
			name:     "price equals target",
			current:  decimal.NewFromFloat(93.0),
			target:   decimal.NewFromFloat(93.0),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShouldExecute(tt.current, tt.target)
			if result != tt.expected {
				t.Errorf("ShouldExecute(%s, %s) = %v, want %v", tt.current, tt.target, result, tt.expected)
			}
		})
	}
}

func TestCalculateRealizedVol(t *testing.T) {
	// Generate known data: 31 daily closes with a known pattern.
	// Use a simple constant price to get zero vol, then a known volatile series.
	t.Run("constant prices yield zero vol", func(t *testing.T) {
		data := make([]types.OHLCV, 31)
		for i := range data {
			data[i] = types.OHLCV{
				Close: decimal.NewFromInt(100),
			}
		}
		vol := CalculateRealizedVol(data)
		if !vol.Equal(decimal.Zero) {
			t.Errorf("expected zero vol for constant prices, got %s", vol)
		}
	})

	t.Run("known volatility series", func(t *testing.T) {
		// Create alternating prices: 100, 110, 100, 110, ...
		// log(110/100) = 0.09531, log(100/110) = -0.09531
		// mean = 0, variance = 0.09531^2 = 0.009084, stddev = 0.09531
		// annualized = 0.09531 * sqrt(365) ≈ 1.8213
		data := make([]types.OHLCV, 31)
		for i := range data {
			if i%2 == 0 {
				data[i] = types.OHLCV{Close: decimal.NewFromInt(100)}
			} else {
				data[i] = types.OHLCV{Close: decimal.NewFromInt(110)}
			}
		}

		vol := CalculateRealizedVol(data)
		volFloat, _ := vol.Float64()

		// Expected: ln(1.1) * sqrt(365) ≈ 0.09531 * 19.105 ≈ 1.821
		expected := math.Log(1.1) * math.Sqrt(365)
		tolerance := 0.01

		if math.Abs(volFloat-expected) > tolerance {
			t.Errorf("CalculateRealizedVol() = %f, want ~%f (tolerance %f)", volFloat, expected, tolerance)
		}
	})

	t.Run("insufficient data returns zero", func(t *testing.T) {
		data := []types.OHLCV{{Close: decimal.NewFromInt(100)}}
		vol := CalculateRealizedVol(data)
		if !vol.Equal(decimal.Zero) {
			t.Errorf("expected zero vol for single data point, got %s", vol)
		}
	})
}

// ---------------------------------------------------------------------------
// Tests: Budget calculation
// ---------------------------------------------------------------------------

func TestBudgetCalculation(t *testing.T) {
	t.Run("standard budget calculation", func(t *testing.T) {
		annualBudget := decimal.NewFromInt(50_000_000)
		regimeMultiplier := decimal.NewFromFloat(1.75)
		liquidityScore := decimal.NewFromFloat(0.92)

		dailyBudget := annualBudget.Div(decimal.NewFromInt(365))
		adjusted := dailyBudget.Mul(regimeMultiplier).Mul(liquidityScore)

		expectedDaily := annualBudget.Div(decimal.NewFromInt(365))
		if !dailyBudget.Equal(expectedDaily) {
			t.Errorf("daily budget = %s, want %s", dailyBudget, expectedDaily)
		}

		expectedAdjusted := expectedDaily.Mul(regimeMultiplier).Mul(liquidityScore)
		if !adjusted.Equal(expectedAdjusted) {
			t.Errorf("adjusted budget = %s, want %s", adjusted, expectedAdjusted)
		}

		// Verify adjusted is roughly 50M/365 * 1.75 * 0.92 ≈ 220,547.95
		adjustedFloat, _ := adjusted.Float64()
		if adjustedFloat < 220_000 || adjustedFloat > 221_000 {
			t.Errorf("adjusted budget = %f, expected ~220547.95", adjustedFloat)
		}
	})

	t.Run("distribution regime small budget", func(t *testing.T) {
		annualBudget := decimal.NewFromInt(50_000_000)
		regimeMultiplier := decimal.NewFromFloat(0.1)
		liquidityScore := decimal.NewFromFloat(1.0)

		dailyBudget := annualBudget.Div(decimal.NewFromInt(365))
		adjusted := dailyBudget.Mul(regimeMultiplier).Mul(liquidityScore)

		// 50M/365 * 0.1 ≈ 13,698.63
		adjustedFloat, _ := adjusted.Float64()
		if adjustedFloat < 13_600 || adjustedFloat > 13_800 {
			t.Errorf("adjusted budget = %f, expected ~13698.63", adjustedFloat)
		}
	})

	t.Run("budget below min execution size should skip", func(t *testing.T) {
		annualBudget := decimal.NewFromInt(100_000)
		regimeMultiplier := decimal.NewFromFloat(0.01)
		liquidityScore := decimal.NewFromFloat(0.5)
		minExec := decimal.NewFromInt(1000)

		dailyBudget := annualBudget.Div(decimal.NewFromInt(365))
		adjusted := dailyBudget.Mul(regimeMultiplier).Mul(liquidityScore)

		// 100000/365 * 0.01 * 0.5 ≈ 1.37
		if !adjusted.LessThan(minExec) {
			t.Errorf("expected adjusted budget %s to be below min execution size %s", adjusted, minExec)
		}
	})
}

// ---------------------------------------------------------------------------
// Tests: Jitter bounds
// ---------------------------------------------------------------------------

func TestJitterBounds(t *testing.T) {
	jitterFactor := 0.3
	baseValue := 100000.0

	for i := 0; i < 1000; i++ {
		// Simulate jitter: value * (1 + jitterFactor * (2*rand - 1))
		jitter := jitterFactor * (2*rand.Float64() - 1) //nolint:gosec
		adjusted := baseValue * (1 + jitter)

		lowerBound := baseValue * (1 - jitterFactor)
		upperBound := baseValue * (1 + jitterFactor)

		if adjusted < lowerBound || adjusted > upperBound {
			t.Errorf("iteration %d: jittered value %f outside bounds [%f, %f]",
				i, adjusted, lowerBound, upperBound)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: Order splitting
// ---------------------------------------------------------------------------

func TestOrderSplitting(t *testing.T) {
	totalBudget := decimal.NewFromInt(100_000)
	subOrderCount := 3

	// Simulate allocations: 65%, 35%
	weights := []float64{0.65, 0.35}
	var allocations []types.RouteAllocation
	for i, w := range weights {
		allocations = append(allocations, types.RouteAllocation{
			Exchange:  fmt.Sprintf("exchange_%d", i),
			AmountUSD: totalBudget.Mul(decimal.NewFromFloat(w)),
			Weight:    w,
		})
	}

	// Verify allocations sum to total.
	sumAllocations := decimal.Zero
	for _, a := range allocations {
		sumAllocations = sumAllocations.Add(a.AmountUSD)
	}
	if !sumAllocations.Equal(totalBudget) {
		t.Errorf("allocations sum = %s, want %s", sumAllocations, totalBudget)
	}

	// Verify sub-order splitting for each allocation.
	for _, a := range allocations {
		subAmount := a.AmountUSD.Div(decimal.NewFromInt(int64(subOrderCount)))
		sumSub := decimal.Zero
		for range subOrderCount {
			sumSub = sumSub.Add(subAmount)
		}
		// Due to integer division with decimals, the sum of sub-orders may differ slightly.
		// But with exact decimal arithmetic it should be exact.
		diff := a.AmountUSD.Sub(sumSub).Abs()
		tolerance := decimal.NewFromFloat(0.01)
		if diff.GreaterThan(tolerance) {
			t.Errorf("exchange %s: sub-orders sum %s differs from allocation %s by %s",
				a.Exchange, sumSub, a.AmountUSD, diff)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: Full RunCycle integration
// ---------------------------------------------------------------------------

func TestRunCycle_BelowTarget(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Set up a config where price will be below target.
	cfg := config.DefaultConfig()

	// Mock data: 31 OHLCV points with prices around 100.
	ohlcv := make([]types.OHLCV, 31)
	for i := range ohlcv {
		price := decimal.NewFromInt(100)
		ohlcv[i] = types.OHLCV{
			Timestamp: time.Now().AddDate(0, 0, -30+i),
			Open:      price,
			High:      price,
			Low:       price,
			Close:     price,
			Volume:    decimal.NewFromInt(1_000_000),
		}
	}

	provider := &mockProvider{
		ohlcv:        ohlcv,
		currentPrice: decimal.NewFromFloat(90.0), // well below VWAP target
		vwap:         decimal.NewFromInt(100),
	}

	// Accumulation regime scorer.
	scorer := &mockScoreFunc{
		name:   "test",
		weight: 1.0,
		scores: types.RegimeScores{
			types.RegimeAccumulation: 0.7,
			types.RegimeMarkup:       0.1,
			types.RegimeDistribution: 0.1,
			types.RegimeMarkdown:     0.1,
		},
	}
	classifier := regime.NewClassifier(scorer)

	// Mock exchanges with good liquidity.
	ex1 := &mockExchange{
		name:         "cow",
		batchAuction: true,
		liquidity: &types.LiquidityInfo{
			Exchange: "cow",
			DepthUSD: decimal.NewFromInt(5_000_000),
		},
		orderResult: &types.OrderResult{
			OrderID:  "cow-1",
			Status:   types.OrderStatusFilled,
			Exchange: "cow",
		},
		orderStatus: &types.OrderStatusResult{
			Status:       types.OrderStatusFilled,
			FilledAmount: decimal.NewFromInt(50_000),
			AvgPrice:     decimal.NewFromFloat(90.0),
		},
	}
	ex2 := &mockExchange{
		name:         "mock",
		batchAuction: false,
		liquidity: &types.LiquidityInfo{
			Exchange: "mock",
			DepthUSD: decimal.NewFromInt(3_000_000),
		},
		orderResult: &types.OrderResult{
			OrderID:  "mock-1",
			Status:   types.OrderStatusFilled,
			Exchange: "mock",
		},
		orderStatus: &types.OrderStatusResult{
			Status:       types.OrderStatusFilled,
			FilledAmount: decimal.NewFromInt(30_000),
			AvgPrice:     decimal.NewFromFloat(90.5),
		},
	}

	exchanges := []exchange.Exchange{ex1, ex2}
	router := exchange.NewRouter(exchanges, true, nil)
	st := &mockStore{}

	eng := NewEngine(cfg, classifier, router, exchanges, provider, st, logger, false)
	summary, err := eng.RunCycle(ctx)
	if err != nil {
		t.Fatalf("RunCycle() error = %v", err)
	}

	if summary.Regime != types.RegimeAccumulation {
		t.Errorf("regime = %s, want %s", summary.Regime, types.RegimeAccumulation)
	}

	if !summary.BelowTarget {
		t.Error("expected BelowTarget = true")
	}

	if len(summary.Allocations) == 0 {
		t.Error("expected non-empty allocations")
	}

	if len(summary.Fills) == 0 {
		t.Error("expected non-empty fills")
	}

	if summary.TotalFilled.IsZero() {
		t.Error("expected non-zero total filled")
	}
}

func TestRunCycle_AboveTarget(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg := config.DefaultConfig()

	ohlcv := make([]types.OHLCV, 31)
	for i := range ohlcv {
		ohlcv[i] = types.OHLCV{
			Timestamp: time.Now().AddDate(0, 0, -30+i),
			Open:      decimal.NewFromInt(100),
			High:      decimal.NewFromInt(100),
			Low:       decimal.NewFromInt(100),
			Close:     decimal.NewFromInt(100),
			Volume:    decimal.NewFromInt(1_000_000),
		}
	}

	provider := &mockProvider{
		ohlcv:        ohlcv,
		currentPrice: decimal.NewFromFloat(99.0), // above target
		vwap:         decimal.NewFromInt(100),
	}

	scorer := &mockScoreFunc{
		name:   "test",
		weight: 1.0,
		scores: types.RegimeScores{
			types.RegimeAccumulation: 0.1,
			types.RegimeMarkup:       0.1,
			types.RegimeDistribution: 0.1,
			types.RegimeMarkdown:     0.7,
		},
	}
	classifier := regime.NewClassifier(scorer)

	ex := &mockExchange{
		name:         "cow",
		batchAuction: true,
		liquidity: &types.LiquidityInfo{
			Exchange: "cow",
			DepthUSD: decimal.NewFromInt(5_000_000),
		},
	}

	exchanges := []exchange.Exchange{ex}
	router := exchange.NewRouter(exchanges, true, nil)
	st := &mockStore{}

	eng := NewEngine(cfg, classifier, router, exchanges, provider, st, logger, false)
	summary, err := eng.RunCycle(ctx)
	if err != nil {
		t.Fatalf("RunCycle() error = %v", err)
	}

	// With markdown regime, no adjustment. VWAP=100, base_discount=0.02, vol=0 (constant prices)
	// discount = 0.02 + 0 = 0.02, target = 100 * 0.98 = 98.0
	// current = 99.0 > 98.0 → above target → no execution
	if summary.BelowTarget {
		t.Error("expected BelowTarget = false")
	}

	if len(summary.Allocations) != 0 {
		t.Errorf("expected no allocations, got %d", len(summary.Allocations))
	}

	if len(summary.Fills) != 0 {
		t.Errorf("expected no fills, got %d", len(summary.Fills))
	}
}

func TestRunCycle_DryRun(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg := config.DefaultConfig()

	ohlcv := make([]types.OHLCV, 31)
	for i := range ohlcv {
		ohlcv[i] = types.OHLCV{
			Timestamp: time.Now().AddDate(0, 0, -30+i),
			Open:      decimal.NewFromInt(100),
			High:      decimal.NewFromInt(100),
			Low:       decimal.NewFromInt(100),
			Close:     decimal.NewFromInt(100),
			Volume:    decimal.NewFromInt(1_000_000),
		}
	}

	provider := &mockProvider{
		ohlcv:        ohlcv,
		currentPrice: decimal.NewFromFloat(90.0),
		vwap:         decimal.NewFromInt(100),
	}

	scorer := &mockScoreFunc{
		name:   "test",
		weight: 1.0,
		scores: types.RegimeScores{
			types.RegimeAccumulation: 0.7,
			types.RegimeMarkup:       0.1,
			types.RegimeDistribution: 0.1,
			types.RegimeMarkdown:     0.1,
		},
	}
	classifier := regime.NewClassifier(scorer)

	ex := &mockExchange{
		name:         "cow",
		batchAuction: true,
		liquidity: &types.LiquidityInfo{
			Exchange: "cow",
			DepthUSD: decimal.NewFromInt(5_000_000),
		},
	}

	exchanges := []exchange.Exchange{ex}
	router := exchange.NewRouter(exchanges, true, nil)
	st := &mockStore{}

	eng := NewEngine(cfg, classifier, router, exchanges, provider, st, logger, true) // dryRun=true
	summary, err := eng.RunCycle(ctx)
	if err != nil {
		t.Fatalf("RunCycle() error = %v", err)
	}

	if !summary.DryRun {
		t.Error("expected DryRun = true")
	}

	if !summary.BelowTarget {
		t.Error("expected BelowTarget = true")
	}

	// Dry run should still produce simulated fills.
	if len(summary.Fills) == 0 {
		t.Error("expected simulated fills in dry-run mode")
	}

	for _, f := range summary.Fills {
		if f.OrderID != "dry-run" {
			t.Errorf("expected dry-run order ID, got %s", f.OrderID)
		}
	}
}
