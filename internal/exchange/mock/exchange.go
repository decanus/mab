package mock

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/decanus/mab/internal/exchange"
	"github.com/decanus/mab/pkg/types"
)

// Compile-time interface check.
var _ exchange.Exchange = (*MockExchange)(nil)

// mockOrder tracks the internal state of a submitted order.
type mockOrder struct {
	id        string
	order     *types.Order
	status    types.OrderStatus
	filledAmt decimal.Decimal
	avgPrice  decimal.Decimal
	filledAt  time.Time
}

// MockExchange is a configurable exchange for testing.
type MockExchange struct {
	name          string
	depth         decimal.Decimal
	slippageBps   float64
	fillRate      float64
	latency       time.Duration
	supportsBatch bool

	orders  map[string]*mockOrder
	counter int
	mu      sync.Mutex
}

// Option configures a MockExchange.
type Option func(*MockExchange)

// WithDepth sets the available liquidity depth.
func WithDepth(d decimal.Decimal) Option {
	return func(m *MockExchange) {
		m.depth = d
	}
}

// WithSlippage sets the simulated slippage in basis points.
func WithSlippage(bps float64) Option {
	return func(m *MockExchange) {
		m.slippageBps = bps
	}
}

// WithFillRate sets the portion of an order that fills (0.0-1.0).
func WithFillRate(rate float64) Option {
	return func(m *MockExchange) {
		m.fillRate = rate
	}
}

// WithLatency sets the simulated execution latency.
func WithLatency(d time.Duration) Option {
	return func(m *MockExchange) {
		m.latency = d
	}
}

// WithBatchAuction sets whether batch auctions are supported.
func WithBatchAuction(b bool) Option {
	return func(m *MockExchange) {
		m.supportsBatch = b
	}
}

// NewMockExchange creates a new mock exchange with the given options.
func NewMockExchange(name string, opts ...Option) *MockExchange {
	m := &MockExchange{
		name:     name,
		depth:    decimal.NewFromInt(100000),
		fillRate: 1.0,
		orders:   make(map[string]*mockOrder),
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Name returns the exchange identifier.
func (m *MockExchange) Name() string {
	return m.name
}

// SupportsBatchAuction reports batch auction support.
func (m *MockExchange) SupportsBatchAuction() bool {
	return m.supportsBatch
}

// GetLiquidity returns configured liquidity information.
func (m *MockExchange) GetLiquidity(_ context.Context, pair types.TradingPair, slippageBps int) (*types.LiquidityInfo, error) {
	// Simulate a reference price of 1.0 for mock purposes.
	refPrice := decimal.NewFromInt(1)
	spreadBps := decimal.NewFromFloat(m.slippageBps)
	halfSpread := spreadBps.Div(decimal.NewFromInt(20000)) // half spread as fraction

	bid := refPrice.Sub(refPrice.Mul(halfSpread))
	ask := refPrice.Add(refPrice.Mul(halfSpread))

	return &types.LiquidityInfo{
		Exchange:     m.name,
		DepthUSD:     m.depth,
		BestBidPrice: bid,
		BestAskPrice: ask,
		SlippageBps:  slippageBps,
	}, nil
}

// SubmitOrder simulates order submission with configurable fill behavior.
func (m *MockExchange) SubmitOrder(ctx context.Context, order *types.Order) (*types.OrderResult, error) {
	if m.latency > 0 {
		select {
		case <-time.After(m.latency):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.counter++
	id := fmt.Sprintf("mock-%s-%d", m.name, m.counter)

	filledAmt := order.AmountUSD.Mul(decimal.NewFromFloat(m.fillRate))

	// Apply slippage to price: effective price is 1.0 * (1 - slippageBps/10000).
	slippageFraction := decimal.NewFromFloat(m.slippageBps).Div(decimal.NewFromInt(10000))
	avgPrice := decimal.NewFromInt(1).Sub(slippageFraction)

	var status types.OrderStatus
	switch {
	case m.fillRate >= 1.0:
		status = types.OrderStatusFilled
	case m.fillRate > 0:
		status = types.OrderStatusPartial
	default:
		status = types.OrderStatusFailed
	}

	mo := &mockOrder{
		id:        id,
		order:     order,
		status:    status,
		filledAmt: filledAmt,
		avgPrice:  avgPrice,
		filledAt:  time.Now(),
	}
	m.orders[id] = mo

	return &types.OrderResult{
		OrderID:  id,
		Status:   status,
		Exchange: m.name,
	}, nil
}

// OrderStatus returns the status of a previously submitted order.
func (m *MockExchange) OrderStatus(_ context.Context, orderID string) (*types.OrderStatusResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	mo, ok := m.orders[orderID]
	if !ok {
		return nil, fmt.Errorf("mock: order %s not found", orderID)
	}

	return &types.OrderStatusResult{
		OrderID:      mo.id,
		Status:       mo.status,
		FilledAmount: mo.filledAmt,
		AvgPrice:     mo.avgPrice,
	}, nil
}

// CancelOrder marks an order as cancelled.
func (m *MockExchange) CancelOrder(_ context.Context, orderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	mo, ok := m.orders[orderID]
	if !ok {
		return fmt.Errorf("mock: order %s not found", orderID)
	}

	mo.status = types.OrderStatusCancelled
	return nil
}

// RecentFills returns fills for orders matching the pair since the given time.
func (m *MockExchange) RecentFills(_ context.Context, pair types.TradingPair, since time.Time) ([]types.Fill, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var fills []types.Fill
	for _, mo := range m.orders {
		if mo.order.Pair != pair {
			continue
		}
		if mo.filledAt.Before(since) {
			continue
		}
		if mo.status != types.OrderStatusFilled && mo.status != types.OrderStatusPartial {
			continue
		}

		fills = append(fills, types.Fill{
			OrderID:     mo.id,
			Exchange:    m.name,
			AmountUSD:   mo.filledAmt,
			AvgPrice:    mo.avgPrice,
			SlippageBps: m.slippageBps,
			MEVSavedUSD: decimal.Zero,
			FilledAt:    mo.filledAt,
		})
	}

	return fills, nil
}
