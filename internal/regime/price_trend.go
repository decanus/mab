package regime

import (
	"context"
	"math"

	"github.com/decanus/mab/pkg/types"
)

// PriceTrendScore analyzes the slope of a simple moving average of closing prices
// to determine market regime scores.
type PriceTrendScore struct {
	weight float64
}

// NewPriceTrendScore creates a new PriceTrendScore with the given weight.
func NewPriceTrendScore(weight float64) *PriceTrendScore {
	return &PriceTrendScore{weight: weight}
}

// Name returns the identifier for this scoring function.
func (p *PriceTrendScore) Name() string {
	return "price_trend"
}

// Weight returns the relative weight of this scoring function.
func (p *PriceTrendScore) Weight() float64 {
	return p.weight
}

// Score calculates regime scores based on the SMA slope of closing prices.
// It compares the average of the first half to the average of the second half
// to determine trend direction.
func (p *PriceTrendScore) Score(_ context.Context, data []types.OHLCV) (types.RegimeScores, error) {
	scores := make(types.RegimeScores)

	if len(data) < 2 {
		// Not enough data to determine a trend; return equal scores.
		scores[types.RegimeAccumulation] = 0.25
		scores[types.RegimeMarkup] = 0.25
		scores[types.RegimeDistribution] = 0.25
		scores[types.RegimeMarkdown] = 0.25
		return scores, nil
	}

	// Compute the average closing price for the first half and second half.
	mid := len(data) / 2
	firstHalf := data[:mid]
	secondHalf := data[mid:]

	var firstSum, secondSum float64
	for _, d := range firstHalf {
		firstSum += d.Close.InexactFloat64()
	}
	for _, d := range secondHalf {
		secondSum += d.Close.InexactFloat64()
	}

	firstAvg := firstSum / float64(len(firstHalf))
	secondAvg := secondSum / float64(len(secondHalf))

	// Determine percent change from first half to second half.
	var pctChange float64
	if firstAvg != 0 {
		pctChange = (secondAvg - firstAvg) / math.Abs(firstAvg)
	}

	if pctChange > 0.02 {
		// Rising slope.
		scores[types.RegimeMarkup] = 0.7
		scores[types.RegimeMarkdown] = 0.0
		scores[types.RegimeAccumulation] = 0.1
		scores[types.RegimeDistribution] = 0.2
	} else if pctChange < -0.02 {
		// Declining slope.
		scores[types.RegimeMarkdown] = 0.7
		scores[types.RegimeMarkup] = 0.0
		scores[types.RegimeAccumulation] = 0.1
		scores[types.RegimeDistribution] = 0.2
	} else {
		// Flat slope (within 2%).
		scores[types.RegimeAccumulation] = 0.7
		scores[types.RegimeMarkup] = 0.1
		scores[types.RegimeDistribution] = 0.1
		scores[types.RegimeMarkdown] = 0.1
	}

	return scores, nil
}
