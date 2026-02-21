package exchange

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"golang.org/x/sync/errgroup"

	"github.com/decanus/mab/pkg/types"
)

// Router routes orders across multiple exchanges based on liquidity and preferences.
type Router struct {
	exchanges   []Exchange
	weights     map[string]float64
	preferBatch bool
}

// NewRouter creates a new exchange router.
// weightOverrides allows manual adjustment of exchange weights (keyed by exchange name).
func NewRouter(exchanges []Exchange, preferBatch bool, weightOverrides map[string]float64) *Router {
	w := make(map[string]float64)
	if weightOverrides != nil {
		for k, v := range weightOverrides {
			w[k] = v
		}
	}
	return &Router{
		exchanges:   exchanges,
		weights:     w,
		preferBatch: preferBatch,
	}
}

// exchangeLiquidity pairs an exchange with its liquidity info.
type exchangeLiquidity struct {
	exchange  Exchange
	liquidity *types.LiquidityInfo
}

// Route determines how to split an order across available exchanges.
func (r *Router) Route(ctx context.Context, pair types.TradingPair, totalAmountUSD decimal.Decimal, slippageBps int, subOrderCount int) ([]types.RouteAllocation, error) {
	// 1. Query liquidity from all exchanges in parallel.
	results := make([]*exchangeLiquidity, len(r.exchanges))
	g, gctx := errgroup.WithContext(ctx)

	for i, ex := range r.exchanges {
		g.Go(func() error {
			liq, err := ex.GetLiquidity(gctx, pair, slippageBps)
			if err != nil {
				// Exchange is unavailable; treat as zero liquidity.
				results[i] = &exchangeLiquidity{exchange: ex, liquidity: nil}
				return nil
			}
			results[i] = &exchangeLiquidity{exchange: ex, liquidity: liq}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("route: query liquidity: %w", err)
	}

	// 2. Filter out exchanges with zero or nil liquidity.
	var available []exchangeLiquidity
	for _, el := range results {
		if el.liquidity != nil && el.liquidity.DepthUSD.IsPositive() {
			available = append(available, *el)
		}
	}

	// 3. If no exchanges have liquidity, return error.
	if len(available) == 0 {
		return nil, fmt.Errorf("route: no exchanges have available liquidity for %s", pair)
	}

	// 4. Score each by depth as proportion of total depth.
	totalDepth := decimal.Zero
	for _, el := range available {
		totalDepth = totalDepth.Add(el.liquidity.DepthUSD)
	}

	scores := make(map[string]float64, len(available))
	for _, el := range available {
		name := el.exchange.Name()
		depthFloat, _ := el.liquidity.DepthUSD.Div(totalDepth).Float64()
		scores[name] = depthFloat
	}

	// 5. If preferBatch, boost batch-auction exchanges.
	if r.preferBatch {
		for _, el := range available {
			if el.exchange.SupportsBatchAuction() {
				scores[el.exchange.Name()] *= 1.3
			}
		}
	}

	// 6. Apply weight overrides.
	for name, override := range r.weights {
		if _, exists := scores[name]; exists {
			scores[name] *= override
		}
	}

	// 7. Normalize scores to sum to 1.0.
	totalScore := 0.0
	for _, s := range scores {
		totalScore += s
	}
	if totalScore > 0 {
		for name := range scores {
			scores[name] /= totalScore
		}
	}

	// 8-10. Allocate and build RouteAllocations.
	allocations := make([]types.RouteAllocation, 0, len(available))
	for _, el := range available {
		name := el.exchange.Name()
		weight := scores[name]
		amount := totalAmountUSD.Mul(decimal.NewFromFloat(weight))

		orderType := types.OrderTypeMarket
		if el.exchange.SupportsBatchAuction() {
			orderType = types.OrderTypeBatchAuction
		}

		reason := fmt.Sprintf("depth=%.2f USD, weight=%.1f%%, type=%s",
			el.liquidity.DepthUSD.InexactFloat64(), weight*100, orderType)
		if r.preferBatch && el.exchange.SupportsBatchAuction() {
			reason += " (batch preferred, 1.3x boost)"
		}
		if override, ok := r.weights[name]; ok {
			reason += fmt.Sprintf(" (weight override: %.2fx)", override)
		}

		allocations = append(allocations, types.RouteAllocation{
			Exchange:  name,
			AmountUSD: amount,
			Weight:    weight,
			OrderType: orderType,
			Reason:    reason,
		})
	}

	return allocations, nil
}

// Execute submits orders to exchanges according to the given allocations.
func (r *Router) Execute(ctx context.Context, pair types.TradingPair, allocations []types.RouteAllocation, maxSlipBps int, subOrderCount int, jitterPct float64) ([]types.Fill, error) {
	// Build a map of exchange name -> Exchange for quick lookup.
	exMap := make(map[string]Exchange, len(r.exchanges))
	for _, ex := range r.exchanges {
		exMap[ex.Name()] = ex
	}

	type fillResult struct {
		fills []types.Fill
	}

	var mu sync.Mutex
	var allFills []types.Fill

	g, gctx := errgroup.WithContext(ctx)

	for _, alloc := range allocations {
		ex, ok := exMap[alloc.Exchange]
		if !ok {
			continue
		}

		// Determine how many sub-orders this allocation gets.
		// Distribute subOrderCount proportionally by weight, minimum 1.
		allocSubOrders := int(float64(subOrderCount)*alloc.Weight + 0.5)
		if allocSubOrders < 1 {
			allocSubOrders = 1
		}

		subAmount := alloc.AmountUSD.Div(decimal.NewFromInt(int64(allocSubOrders)))

		for j := range allocSubOrders {
			order := &types.Order{
				Pair:          pair,
				AmountUSD:     subAmount,
				MaxSlipBps:    maxSlipBps,
				OrderType:     alloc.OrderType,
				SubOrderIdx:   j,
				SubOrderTotal: allocSubOrders,
			}

			g.Go(func() error {
				// Apply time jitter.
				if jitterPct > 0 {
					maxJitter := float64(time.Second) * jitterPct
					jitter := time.Duration(rand.Float64() * maxJitter * 2 - maxJitter) //nolint:gosec
					if jitter > 0 {
						select {
						case <-time.After(jitter):
						case <-gctx.Done():
							return gctx.Err()
						}
					}
				}

				result, err := ex.SubmitOrder(gctx, order)
				if err != nil {
					return fmt.Errorf("execute: submit to %s: %w", ex.Name(), err)
				}

				fill := types.Fill{
					OrderID:     result.OrderID,
					Exchange:    result.Exchange,
					AmountUSD:   order.AmountUSD,
					AvgPrice:    decimal.Zero, // will be updated on status check
					SlippageBps: 0,
					MEVSavedUSD: decimal.Zero,
					FilledAt:    time.Now(),
				}

				// If the order was immediately filled, try to get the status for price info.
				if result.Status == types.OrderStatusFilled || result.Status == types.OrderStatusPartial {
					if status, err := ex.OrderStatus(gctx, result.OrderID); err == nil {
						fill.AvgPrice = status.AvgPrice
						fill.AmountUSD = status.FilledAmount
					}
				}

				mu.Lock()
				allFills = append(allFills, fill)
				mu.Unlock()

				return nil
			})
		}
	}

	if err := g.Wait(); err != nil {
		return allFills, fmt.Errorf("execute: %w", err)
	}

	return allFills, nil
}
