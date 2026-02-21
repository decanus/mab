package exchange

import (
	"context"
	"time"

	"github.com/decanus/mab/pkg/types"
)

// Exchange defines the interface for interacting with a trading venue.
type Exchange interface {
	// Name returns the identifier of this exchange.
	Name() string

	// GetLiquidity queries current liquidity for a trading pair at a given slippage tolerance.
	GetLiquidity(ctx context.Context, pair types.TradingPair, slippageBps int) (*types.LiquidityInfo, error)

	// SubmitOrder sends an order to the exchange for execution.
	SubmitOrder(ctx context.Context, order *types.Order) (*types.OrderResult, error)

	// OrderStatus returns the current status of a previously submitted order.
	OrderStatus(ctx context.Context, orderID string) (*types.OrderStatusResult, error)

	// CancelOrder attempts to cancel an open order.
	CancelOrder(ctx context.Context, orderID string) error

	// RecentFills returns fills for a trading pair since the given time.
	RecentFills(ctx context.Context, pair types.TradingPair, since time.Time) ([]types.Fill, error)

	// SupportsBatchAuction reports whether this exchange supports batch auction order types.
	SupportsBatchAuction() bool
}
