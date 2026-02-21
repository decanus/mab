package regime

import (
	"context"
	"math"

	"github.com/decanus/mab/pkg/types"
)

// VolatilityScore calculates 30-day annualized realized volatility from
// daily log returns to determine market regime scores.
type VolatilityScore struct {
	weight float64
}

// NewVolatilityScore creates a new VolatilityScore with the given weight.
func NewVolatilityScore(weight float64) *VolatilityScore {
	return &VolatilityScore{weight: weight}
}

// Name returns the identifier for this scoring function.
func (v *VolatilityScore) Name() string {
	return "volatility"
}

// Weight returns the relative weight of this scoring function.
func (v *VolatilityScore) Weight() float64 {
	return v.weight
}

// Score calculates regime scores based on annualized realized volatility.
// It computes daily log returns, their standard deviation, and annualizes
// by multiplying by sqrt(365).
func (v *VolatilityScore) Score(_ context.Context, data []types.OHLCV) (types.RegimeScores, error) {
	scores := make(types.RegimeScores)

	if len(data) < 2 {
		scores[types.RegimeAccumulation] = 0.25
		scores[types.RegimeMarkup] = 0.25
		scores[types.RegimeDistribution] = 0.25
		scores[types.RegimeMarkdown] = 0.25
		return scores, nil
	}

	// Calculate daily log returns.
	returns := make([]float64, 0, len(data)-1)
	for i := 1; i < len(data); i++ {
		prev := data[i-1].Close.InexactFloat64()
		curr := data[i].Close.InexactFloat64()
		if prev > 0 && curr > 0 {
			returns = append(returns, math.Log(curr/prev))
		}
	}

	if len(returns) == 0 {
		scores[types.RegimeAccumulation] = 0.25
		scores[types.RegimeMarkup] = 0.25
		scores[types.RegimeDistribution] = 0.25
		scores[types.RegimeMarkdown] = 0.25
		return scores, nil
	}

	// Calculate mean of returns.
	var sum float64
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(len(returns))

	// Calculate standard deviation of returns.
	var varianceSum float64
	for _, r := range returns {
		diff := r - mean
		varianceSum += diff * diff
	}
	stdDev := math.Sqrt(varianceSum / float64(len(returns)))

	// Annualize volatility.
	annualizedVol := stdDev * math.Sqrt(365)

	if annualizedVol < 0.30 {
		// Low volatility (<30%).
		scores[types.RegimeAccumulation] = 0.7
		scores[types.RegimeMarkup] = 0.1
		scores[types.RegimeMarkdown] = 0.1
		scores[types.RegimeDistribution] = 0.1
	} else if annualizedVol > 0.80 {
		// High volatility (>80%).
		scores[types.RegimeDistribution] = 0.7
		scores[types.RegimeAccumulation] = 0.1
		scores[types.RegimeMarkup] = 0.1
		scores[types.RegimeMarkdown] = 0.1
	} else {
		// Medium volatility.
		scores[types.RegimeAccumulation] = 0.25
		scores[types.RegimeMarkup] = 0.25
		scores[types.RegimeMarkdown] = 0.25
		scores[types.RegimeDistribution] = 0.25
	}

	return scores, nil
}
