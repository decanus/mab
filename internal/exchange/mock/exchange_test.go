package mock

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/decanus/mab/internal/exchange"
	"github.com/decanus/mab/pkg/types"
)

// TestMockExchange_InterfaceCompliance verifies the compile-time interface check.
func TestMockExchange_InterfaceCompliance(t *testing.T) {
	var _ exchange.Exchange = (*MockExchange)(nil)
}

func TestMockExchange_SubmitAndFill(t *testing.T) {
	m := NewMockExchange("test-ex",
		WithDepth(decimal.NewFromInt(50000)),
		WithSlippage(10),
		WithFillRate(1.0),
	)

	pair := types.TradingPair{Base: "AAVE", Quote: "USDC"}
	order := &types.Order{
		Pair:      pair,
		AmountUSD: decimal.NewFromInt(1000),
		MaxSlipBps: 50,
		OrderType: types.OrderTypeMarket,
	}

	ctx := context.Background()
	result, err := m.SubmitOrder(ctx, order)
	if err != nil {
		t.Fatalf("SubmitOrder failed: %v", err)
	}

	if result.Status != types.OrderStatusFilled {
		t.Errorf("expected status=filled, got %s", result.Status)
	}
	if result.Exchange != "test-ex" {
		t.Errorf("expected exchange=test-ex, got %s", result.Exchange)
	}

	// Check order status.
	status, err := m.OrderStatus(ctx, result.OrderID)
	if err != nil {
		t.Fatalf("OrderStatus failed: %v", err)
	}
	if status.Status != types.OrderStatusFilled {
		t.Errorf("expected filled status, got %s", status.Status)
	}
	// Filled amount should be the full 1000.
	if !status.FilledAmount.Equal(decimal.NewFromInt(1000)) {
		t.Errorf("expected filled amount=1000, got %s", status.FilledAmount)
	}
	// With 10 bps slippage, avg price = 1.0 - 10/10000 = 0.999.
	expectedPrice := decimal.NewFromFloat(0.999)
	if !status.AvgPrice.Equal(expectedPrice) {
		t.Errorf("expected avg price=%s, got %s", expectedPrice, status.AvgPrice)
	}

	// Check recent fills.
	fills, err := m.RecentFills(ctx, pair, time.Now().Add(-1*time.Minute))
	if err != nil {
		t.Fatalf("RecentFills failed: %v", err)
	}
	if len(fills) != 1 {
		t.Fatalf("expected 1 fill, got %d", len(fills))
	}
	if fills[0].SlippageBps != 10 {
		t.Errorf("expected slippage=10, got %f", fills[0].SlippageBps)
	}
}

func TestMockExchange_PartialFill(t *testing.T) {
	m := NewMockExchange("partial-ex",
		WithFillRate(0.5),
		WithSlippage(5),
	)

	order := &types.Order{
		Pair:      types.TradingPair{Base: "AAVE", Quote: "USDC"},
		AmountUSD: decimal.NewFromInt(2000),
		MaxSlipBps: 100,
		OrderType: types.OrderTypeMarket,
	}

	result, err := m.SubmitOrder(context.Background(), order)
	if err != nil {
		t.Fatalf("SubmitOrder failed: %v", err)
	}

	if result.Status != types.OrderStatusPartial {
		t.Errorf("expected status=partial, got %s", result.Status)
	}

	status, err := m.OrderStatus(context.Background(), result.OrderID)
	if err != nil {
		t.Fatalf("OrderStatus failed: %v", err)
	}

	// 50% fill rate of 2000 = 1000.
	expectedFill := decimal.NewFromInt(1000)
	if !status.FilledAmount.Equal(expectedFill) {
		t.Errorf("expected filled=1000, got %s", status.FilledAmount)
	}
}

func TestMockExchange_Latency(t *testing.T) {
	latency := 50 * time.Millisecond
	m := NewMockExchange("latency-ex",
		WithLatency(latency),
		WithFillRate(1.0),
	)

	order := &types.Order{
		Pair:      types.TradingPair{Base: "AAVE", Quote: "USDC"},
		AmountUSD: decimal.NewFromInt(100),
		MaxSlipBps: 50,
		OrderType: types.OrderTypeMarket,
	}

	start := time.Now()
	_, err := m.SubmitOrder(context.Background(), order)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("SubmitOrder failed: %v", err)
	}

	if elapsed < latency {
		t.Errorf("expected at least %v latency, got %v", latency, elapsed)
	}
	// Allow generous upper bound for CI variability.
	if elapsed > latency*10 {
		t.Errorf("latency exceeded expected bounds: %v > %v", elapsed, latency*10)
	}
}

func TestMockExchange_CancelOrder(t *testing.T) {
	m := NewMockExchange("cancel-ex",
		WithFillRate(1.0),
	)

	order := &types.Order{
		Pair:      types.TradingPair{Base: "AAVE", Quote: "USDC"},
		AmountUSD: decimal.NewFromInt(500),
		MaxSlipBps: 50,
		OrderType: types.OrderTypeMarket,
	}

	ctx := context.Background()
	result, err := m.SubmitOrder(ctx, order)
	if err != nil {
		t.Fatalf("SubmitOrder failed: %v", err)
	}

	// Cancel the order.
	if err := m.CancelOrder(ctx, result.OrderID); err != nil {
		t.Fatalf("CancelOrder failed: %v", err)
	}

	// Verify status is cancelled.
	status, err := m.OrderStatus(ctx, result.OrderID)
	if err != nil {
		t.Fatalf("OrderStatus failed: %v", err)
	}
	if status.Status != types.OrderStatusCancelled {
		t.Errorf("expected status=cancelled, got %s", status.Status)
	}

	// Cancel a non-existent order should fail.
	if err := m.CancelOrder(ctx, "nonexistent-id"); err == nil {
		t.Error("expected error for non-existent order, got nil")
	}
}

func TestMockExchange_GetLiquidity(t *testing.T) {
	depth := decimal.NewFromInt(75000)
	m := NewMockExchange("liq-ex",
		WithDepth(depth),
		WithSlippage(20),
	)

	liq, err := m.GetLiquidity(context.Background(), types.TradingPair{Base: "AAVE", Quote: "USDC"}, 30)
	if err != nil {
		t.Fatalf("GetLiquidity failed: %v", err)
	}

	if liq.Exchange != "liq-ex" {
		t.Errorf("expected exchange=liq-ex, got %s", liq.Exchange)
	}
	if !liq.DepthUSD.Equal(depth) {
		t.Errorf("expected depth=%s, got %s", depth, liq.DepthUSD)
	}
	if liq.SlippageBps != 30 {
		t.Errorf("expected slippageBps=30, got %d", liq.SlippageBps)
	}
	if liq.BestBidPrice.GreaterThanOrEqual(liq.BestAskPrice) {
		t.Errorf("expected bid < ask, got bid=%s ask=%s", liq.BestBidPrice, liq.BestAskPrice)
	}
}

func TestMockExchange_BatchAuction(t *testing.T) {
	m := NewMockExchange("batch-ex", WithBatchAuction(true))
	if !m.SupportsBatchAuction() {
		t.Error("expected SupportsBatchAuction=true")
	}

	m2 := NewMockExchange("no-batch-ex", WithBatchAuction(false))
	if m2.SupportsBatchAuction() {
		t.Error("expected SupportsBatchAuction=false")
	}
}
