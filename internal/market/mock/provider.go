package mock

import (
	"context"
	"math/rand"
	"time"

	"github.com/shopspring/decimal"

	"github.com/decanus/mab/internal/market"
	"github.com/decanus/mab/pkg/types"
)

// Compile-time interface check.
var _ market.Provider = (*MockProvider)(nil)

// MockProvider generates synthetic market data based on a configurable market regime.
type MockProvider struct {
	regime     types.MarketRegime
	basePrice  decimal.Decimal
	baseVolume decimal.Decimal
	seed       int64
}

// NewMockProvider creates a new mock provider with the given configuration.
func NewMockProvider(regime types.MarketRegime, basePrice, baseVolume decimal.Decimal, seed int64) *MockProvider {
	return &MockProvider{
		regime:     regime,
		basePrice:  basePrice,
		baseVolume: baseVolume,
		seed:       seed,
	}
}

// GetOHLCV generates synthetic OHLCV data for the given number of periods.
func (m *MockProvider) GetOHLCV(_ context.Context, _ types.TradingPair, _ string, periods int) ([]types.OHLCV, error) {
	return m.generateCandles(periods), nil
}

// GetCurrentPrice returns the close price of the most recent generated candle.
func (m *MockProvider) GetCurrentPrice(ctx context.Context, pair types.TradingPair) (decimal.Decimal, error) {
	candles := m.generateCandles(1)
	return candles[0].Close, nil
}

// GetVWAP calculates a real VWAP from the generated OHLCV data.
func (m *MockProvider) GetVWAP(ctx context.Context, pair types.TradingPair, periods int) (decimal.Decimal, error) {
	candles := m.generateCandles(periods)

	three := decimal.NewFromInt(3)
	sumTPV := decimal.Zero
	sumVol := decimal.Zero
	sumClose := decimal.Zero

	for _, c := range candles {
		tp := c.High.Add(c.Low).Add(c.Close).Div(three)
		sumTPV = sumTPV.Add(tp.Mul(c.Volume))
		sumVol = sumVol.Add(c.Volume)
		sumClose = sumClose.Add(c.Close)
	}

	if sumVol.IsZero() {
		return sumClose.Div(decimal.NewFromInt(int64(len(candles)))), nil
	}

	return sumTPV.Div(sumVol), nil
}

// generateCandles creates synthetic OHLCV candles based on the configured regime.
func (m *MockProvider) generateCandles(periods int) []types.OHLCV {
	rng := rand.New(rand.NewSource(m.seed))
	candles := make([]types.OHLCV, periods)

	prevClose := m.basePrice
	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < periods; i++ {
		open := prevClose

		// Determine trend and noise based on regime.
		var trendPct, noisePct, volTrendFactor float64

		switch m.regime {
		case types.RegimeAccumulation:
			// Flat prices with slight noise (+-1%), declining volume trend.
			trendPct = 0.0
			noisePct = 0.01
			// Volume declines linearly from 1.0 to 0.5 over the period.
			volTrendFactor = 1.0 - 0.5*float64(i)/float64(max(periods-1, 1))

		case types.RegimeMarkup:
			// Steadily rising prices (+0.5-1.5% per period), rising volume.
			trendPct = 0.005 + rng.Float64()*0.01 // 0.5% to 1.5%
			noisePct = 0.005
			// Volume rises linearly from 0.5 to 1.5 over the period.
			volTrendFactor = 0.5 + 1.0*float64(i)/float64(max(periods-1, 1))

		case types.RegimeDistribution:
			// Flat-to-slightly-rising prices, declining volume (divergence).
			trendPct = 0.001 + rng.Float64()*0.002 // 0.1% to 0.3%
			noisePct = 0.005
			// Volume declines linearly from 1.0 to 0.3 over the period.
			volTrendFactor = 1.0 - 0.7*float64(i)/float64(max(periods-1, 1))

		case types.RegimeMarkdown:
			// Declining prices (-0.5 to -1.5% per period), rising volume (capitulation).
			trendPct = -(0.005 + rng.Float64()*0.01) // -0.5% to -1.5%
			noisePct = 0.005
			// Volume rises linearly from 0.5 to 1.5 over the period.
			volTrendFactor = 0.5 + 1.0*float64(i)/float64(max(periods-1, 1))

		default:
			trendPct = 0.0
			noisePct = 0.01
			volTrendFactor = 1.0
		}

		// Calculate noise: random value in [-noisePct, +noisePct].
		noise := (rng.Float64()*2 - 1) * noisePct

		// close = open * (1 + trend + noise)
		changeFactor := decimal.NewFromFloat(1.0 + trendPct + noise)
		cl := open.Mul(changeFactor)

		// Ensure close is positive.
		if cl.LessThanOrEqual(decimal.Zero) {
			cl = open.Mul(decimal.NewFromFloat(0.01))
		}

		// High = max(open, close) * (1 + random small amount)
		highBase := decMax(open, cl)
		highFactor := decimal.NewFromFloat(1.0 + rng.Float64()*0.005)
		high := highBase.Mul(highFactor)

		// Low = min(open, close) * (1 - random small amount)
		lowBase := decMin(open, cl)
		lowFactor := decimal.NewFromFloat(1.0 - rng.Float64()*0.005)
		low := lowBase.Mul(lowFactor)

		// Ensure low is positive.
		if low.LessThanOrEqual(decimal.Zero) {
			low = decimal.NewFromFloat(0.01)
		}

		// Ensure high >= low (should already be the case, but be safe).
		if high.LessThan(low) {
			high, low = low, high
		}

		// Volume = baseVolume * volTrendFactor * (1 + noise)
		volNoise := 1.0 + (rng.Float64()*2-1)*0.1 // +-10% noise
		if volNoise < 0 {
			volNoise = 0
		}
		vol := m.baseVolume.Mul(decimal.NewFromFloat(volTrendFactor * volNoise))
		if vol.LessThan(decimal.Zero) {
			vol = decimal.Zero
		}

		candles[i] = types.OHLCV{
			Timestamp: baseTime.Add(time.Duration(i) * 24 * time.Hour),
			Open:      open,
			High:      high,
			Low:       low,
			Close:     cl,
			Volume:    vol,
		}

		prevClose = cl
	}

	return candles
}

// decMax returns the larger of two decimals.
func decMax(a, b decimal.Decimal) decimal.Decimal {
	if a.GreaterThanOrEqual(b) {
		return a
	}
	return b
}

// decMin returns the smaller of two decimals.
func decMin(a, b decimal.Decimal) decimal.Decimal {
	if a.LessThanOrEqual(b) {
		return a
	}
	return b
}
