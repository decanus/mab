package market

import (
	"context"

	"github.com/shopspring/decimal"

	"github.com/decanus/mab/pkg/types"
)

// Provider defines the interface for fetching market data.
type Provider interface {
	// GetOHLCV returns historical OHLCV candles for the given trading pair.
	GetOHLCV(ctx context.Context, pair types.TradingPair, interval string, periods int) ([]types.OHLCV, error)

	// GetCurrentPrice returns the latest price for the given trading pair.
	GetCurrentPrice(ctx context.Context, pair types.TradingPair) (decimal.Decimal, error)

	// GetVWAP returns the volume-weighted average price over the given number of periods.
	GetVWAP(ctx context.Context, pair types.TradingPair, periods int) (decimal.Decimal, error)
}
