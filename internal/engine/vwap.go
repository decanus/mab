package engine

import (
	"math"

	"github.com/shopspring/decimal"

	"github.com/decanus/mab/pkg/types"
)

// CalculateTargetPrice computes the target buy price based on VWAP, volatility,
// and the current market regime.
//
// The base formula is:
//
//	discount_target = base_discount + (realized_vol * vol_scaling_factor)
//	target_price    = vwap30d * (1 - discount_target)
//
// Regime adjustments:
//   - Accumulation: discount *= 0.5 (widen acceptable range, buy more aggressively)
//   - Markup:       discount *= 1.5 (tighten range, buy less aggressively)
func CalculateTargetPrice(
	vwap30d decimal.Decimal,
	baseDiscount decimal.Decimal,
	volScalingFactor decimal.Decimal,
	realizedVol decimal.Decimal,
	regime types.MarketRegime,
) decimal.Decimal {
	discountTarget := baseDiscount.Add(realizedVol.Mul(volScalingFactor))

	switch regime {
	case types.RegimeAccumulation:
		discountTarget = discountTarget.Mul(decimal.NewFromFloat(0.5))
	case types.RegimeMarkup:
		discountTarget = discountTarget.Mul(decimal.NewFromFloat(1.5))
	}

	one := decimal.NewFromInt(1)
	return vwap30d.Mul(one.Sub(discountTarget))
}

// ShouldExecute returns true if the current price is at or below the target price,
// indicating that a buy should be executed.
func ShouldExecute(currentPrice, targetPrice decimal.Decimal) bool {
	return currentPrice.LessThanOrEqual(targetPrice)
}

// CalculateRealizedVol computes 30-day annualized realized volatility from OHLCV data.
// It calculates daily log returns from close prices, computes the standard deviation,
// and annualizes by multiplying by sqrt(365).
func CalculateRealizedVol(data []types.OHLCV) decimal.Decimal {
	if len(data) < 2 {
		return decimal.Zero
	}

	// Compute daily log returns.
	returns := make([]float64, 0, len(data)-1)
	for i := 1; i < len(data); i++ {
		prevClose, _ := data[i-1].Close.Float64()
		curClose, _ := data[i].Close.Float64()
		if prevClose <= 0 || curClose <= 0 {
			continue
		}
		logReturn := math.Log(curClose / prevClose)
		returns = append(returns, logReturn)
	}

	if len(returns) == 0 {
		return decimal.Zero
	}

	// Calculate mean.
	sum := 0.0
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(len(returns))

	// Calculate variance.
	sumSquaredDiff := 0.0
	for _, r := range returns {
		diff := r - mean
		sumSquaredDiff += diff * diff
	}
	variance := sumSquaredDiff / float64(len(returns))

	// Standard deviation, annualized.
	stddev := math.Sqrt(variance)
	annualizedVol := stddev * math.Sqrt(365)

	return decimal.NewFromFloat(annualizedVol)
}
