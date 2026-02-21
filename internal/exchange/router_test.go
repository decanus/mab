package exchange

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/decanus/mab/pkg/types"
)

// testExchange is a minimal Exchange implementation for router tests.
type testExchange struct {
	name          string
	depth         decimal.Decimal
	supportsBatch bool
	submitted     []*types.Order
	fillRate      float64
	counter       int
}

func (te *testExchange) Name() string { return te.name }

func (te *testExchange) GetLiquidity(_ context.Context, _ types.TradingPair, slippageBps int) (*types.LiquidityInfo, error) {
	return &types.LiquidityInfo{
		Exchange:     te.name,
		DepthUSD:     te.depth,
		BestBidPrice: decimal.NewFromInt(1),
		BestAskPrice: decimal.NewFromInt(1),
		SlippageBps:  slippageBps,
	}, nil
}

func (te *testExchange) SubmitOrder(_ context.Context, order *types.Order) (*types.OrderResult, error) {
	te.submitted = append(te.submitted, order)
	te.counter++
	return &types.OrderResult{
		OrderID:  te.name + "-order-" + string(rune('0'+te.counter)),
		Status:   types.OrderStatusFilled,
		Exchange: te.name,
	}, nil
}

func (te *testExchange) OrderStatus(_ context.Context, orderID string) (*types.OrderStatusResult, error) {
	return &types.OrderStatusResult{
		OrderID:      orderID,
		Status:       types.OrderStatusFilled,
		FilledAmount: decimal.NewFromInt(100),
		AvgPrice:     decimal.NewFromInt(1),
	}, nil
}

func (te *testExchange) CancelOrder(_ context.Context, _ string) error { return nil }

func (te *testExchange) RecentFills(_ context.Context, _ types.TradingPair, _ time.Time) ([]types.Fill, error) {
	return nil, nil
}

func (te *testExchange) SupportsBatchAuction() bool { return te.supportsBatch }

func TestRouter_LiquidityWeightedRouting(t *testing.T) {
	ex1 := &testExchange{name: "ex1", depth: decimal.NewFromInt(30000)}
	ex2 := &testExchange{name: "ex2", depth: decimal.NewFromInt(70000)}

	router := NewRouter([]Exchange{ex1, ex2}, false, nil)

	pair := types.TradingPair{Base: "AAVE", Quote: "USDC"}
	allocs, err := router.Route(context.Background(), pair, decimal.NewFromInt(10000), 50, 4)
	if err != nil {
		t.Fatalf("Route failed: %v", err)
	}

	if len(allocs) != 2 {
		t.Fatalf("expected 2 allocations, got %d", len(allocs))
	}

	allocMap := make(map[string]types.RouteAllocation)
	for _, a := range allocs {
		allocMap[a.Exchange] = a
	}

	// ex1 has 30% of total depth, ex2 has 70%.
	ex1Alloc := allocMap["ex1"]
	ex2Alloc := allocMap["ex2"]

	if math.Abs(ex1Alloc.Weight-0.3) > 0.01 {
		t.Errorf("expected ex1 weight ~0.3, got %f", ex1Alloc.Weight)
	}
	if math.Abs(ex2Alloc.Weight-0.7) > 0.01 {
		t.Errorf("expected ex2 weight ~0.7, got %f", ex2Alloc.Weight)
	}

	// Amounts should be proportional.
	expectedEx1Amount := decimal.NewFromInt(3000)
	expectedEx2Amount := decimal.NewFromInt(7000)
	if !ex1Alloc.AmountUSD.Equal(expectedEx1Amount) {
		t.Errorf("expected ex1 amount=%s, got %s", expectedEx1Amount, ex1Alloc.AmountUSD)
	}
	if !ex2Alloc.AmountUSD.Equal(expectedEx2Amount) {
		t.Errorf("expected ex2 amount=%s, got %s", expectedEx2Amount, ex2Alloc.AmountUSD)
	}
}

func TestRouter_BatchAuctionPreference(t *testing.T) {
	exBatch := &testExchange{name: "batch", depth: decimal.NewFromInt(50000), supportsBatch: true}
	exMarket := &testExchange{name: "market", depth: decimal.NewFromInt(50000), supportsBatch: false}

	// Without batch preference.
	routerNoPref := NewRouter([]Exchange{exBatch, exMarket}, false, nil)
	pair := types.TradingPair{Base: "AAVE", Quote: "USDC"}
	allocsNoPref, err := routerNoPref.Route(context.Background(), pair, decimal.NewFromInt(10000), 50, 2)
	if err != nil {
		t.Fatalf("Route failed: %v", err)
	}

	noPrefMap := make(map[string]types.RouteAllocation)
	for _, a := range allocsNoPref {
		noPrefMap[a.Exchange] = a
	}

	// Without preference, weights should be 50/50.
	if math.Abs(noPrefMap["batch"].Weight-0.5) > 0.01 {
		t.Errorf("without pref: expected batch weight ~0.5, got %f", noPrefMap["batch"].Weight)
	}

	// With batch preference.
	routerPref := NewRouter([]Exchange{exBatch, exMarket}, true, nil)
	allocsPref, err := routerPref.Route(context.Background(), pair, decimal.NewFromInt(10000), 50, 2)
	if err != nil {
		t.Fatalf("Route failed: %v", err)
	}

	prefMap := make(map[string]types.RouteAllocation)
	for _, a := range allocsPref {
		prefMap[a.Exchange] = a
	}

	// With 1.3x boost on batch, batch should get 1.3/(1.3+1.0) ~= 0.565.
	if prefMap["batch"].Weight <= 0.5 {
		t.Errorf("with pref: expected batch weight > 0.5, got %f", prefMap["batch"].Weight)
	}
	expectedBatchWeight := 1.3 / (1.3 + 1.0)
	if math.Abs(prefMap["batch"].Weight-expectedBatchWeight) > 0.01 {
		t.Errorf("with pref: expected batch weight ~%.3f, got %f", expectedBatchWeight, prefMap["batch"].Weight)
	}

	// Verify order types.
	if prefMap["batch"].OrderType != types.OrderTypeBatchAuction {
		t.Errorf("expected batch order type, got %s", prefMap["batch"].OrderType)
	}
	if prefMap["market"].OrderType != types.OrderTypeMarket {
		t.Errorf("expected market order type, got %s", prefMap["market"].OrderType)
	}
}

func TestRouter_SingleExchange(t *testing.T) {
	exActive := &testExchange{name: "active", depth: decimal.NewFromInt(100000)}
	exDead := &testExchange{name: "dead", depth: decimal.Zero}

	router := NewRouter([]Exchange{exActive, exDead}, false, nil)

	pair := types.TradingPair{Base: "AAVE", Quote: "USDC"}
	allocs, err := router.Route(context.Background(), pair, decimal.NewFromInt(5000), 30, 1)
	if err != nil {
		t.Fatalf("Route failed: %v", err)
	}

	if len(allocs) != 1 {
		t.Fatalf("expected 1 allocation, got %d", len(allocs))
	}
	if allocs[0].Exchange != "active" {
		t.Errorf("expected allocation to active exchange, got %s", allocs[0].Exchange)
	}
	if math.Abs(allocs[0].Weight-1.0) > 0.01 {
		t.Errorf("expected weight=1.0, got %f", allocs[0].Weight)
	}
	if !allocs[0].AmountUSD.Equal(decimal.NewFromInt(5000)) {
		t.Errorf("expected amount=5000, got %s", allocs[0].AmountUSD)
	}
}

func TestRouter_NoLiquidity(t *testing.T) {
	ex1 := &testExchange{name: "ex1", depth: decimal.Zero}
	ex2 := &testExchange{name: "ex2", depth: decimal.Zero}

	router := NewRouter([]Exchange{ex1, ex2}, false, nil)

	pair := types.TradingPair{Base: "AAVE", Quote: "USDC"}
	_, err := router.Route(context.Background(), pair, decimal.NewFromInt(10000), 50, 2)
	if err == nil {
		t.Fatal("expected error when no liquidity, got nil")
	}
}

func TestRouter_Execute(t *testing.T) {
	ex1 := &testExchange{name: "ex1", depth: decimal.NewFromInt(50000)}
	ex2 := &testExchange{name: "ex2", depth: decimal.NewFromInt(50000)}

	router := NewRouter([]Exchange{ex1, ex2}, false, nil)

	pair := types.TradingPair{Base: "AAVE", Quote: "USDC"}

	allocations := []types.RouteAllocation{
		{
			Exchange:  "ex1",
			AmountUSD: decimal.NewFromInt(3000),
			Weight:    0.3,
			OrderType: types.OrderTypeMarket,
		},
		{
			Exchange:  "ex2",
			AmountUSD: decimal.NewFromInt(7000),
			Weight:    0.7,
			OrderType: types.OrderTypeMarket,
		},
	}

	fills, err := router.Execute(context.Background(), pair, allocations, 50, 2, 0)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(fills) == 0 {
		t.Fatal("expected fills, got none")
	}

	// Verify that orders were submitted to both exchanges.
	if len(ex1.submitted) == 0 {
		t.Error("expected orders submitted to ex1")
	}
	if len(ex2.submitted) == 0 {
		t.Error("expected orders submitted to ex2")
	}

	// Verify total fills cover both exchanges.
	exchangesFilled := make(map[string]bool)
	for _, f := range fills {
		exchangesFilled[f.Exchange] = true
	}
	if !exchangesFilled["ex1"] {
		t.Error("expected fill from ex1")
	}
	if !exchangesFilled["ex2"] {
		t.Error("expected fill from ex2")
	}
}

func TestRouter_WeightOverrides(t *testing.T) {
	ex1 := &testExchange{name: "ex1", depth: decimal.NewFromInt(50000)}
	ex2 := &testExchange{name: "ex2", depth: decimal.NewFromInt(50000)}

	// Give ex1 a 2x weight override.
	overrides := map[string]float64{"ex1": 2.0}
	router := NewRouter([]Exchange{ex1, ex2}, false, overrides)

	pair := types.TradingPair{Base: "AAVE", Quote: "USDC"}
	allocs, err := router.Route(context.Background(), pair, decimal.NewFromInt(9000), 50, 3)
	if err != nil {
		t.Fatalf("Route failed: %v", err)
	}

	allocMap := make(map[string]types.RouteAllocation)
	for _, a := range allocs {
		allocMap[a.Exchange] = a
	}

	// ex1 base score = 0.5, after 2x = 1.0; ex2 base = 0.5.
	// Normalized: ex1 = 1.0/1.5 ~= 0.667, ex2 = 0.5/1.5 ~= 0.333.
	expectedEx1Weight := 1.0 / 1.5
	if math.Abs(allocMap["ex1"].Weight-expectedEx1Weight) > 0.01 {
		t.Errorf("expected ex1 weight ~%.3f, got %f", expectedEx1Weight, allocMap["ex1"].Weight)
	}
}
