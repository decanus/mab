package regime

import (
	"context"

	"github.com/decanus/mab/pkg/types"
)

// DivergenceScore detects volume-price divergence to identify potential
// regime transitions. When price and volume move in opposite directions,
// it signals distribution or markdown phases.
type DivergenceScore struct {
	weight float64
}

// NewDivergenceScore creates a new DivergenceScore with the given weight.
func NewDivergenceScore(weight float64) *DivergenceScore {
	return &DivergenceScore{weight: weight}
}

// Name returns the identifier for this scoring function.
func (d *DivergenceScore) Name() string {
	return "divergence"
}

// Weight returns the relative weight of this scoring function.
func (d *DivergenceScore) Weight() float64 {
	return d.weight
}

// Score calculates regime scores based on volume-price divergence.
// It compares the direction of price movement (first half vs second half average)
// against the direction of volume movement to detect divergences.
func (d *DivergenceScore) Score(_ context.Context, data []types.OHLCV) (types.RegimeScores, error) {
	scores := make(types.RegimeScores)

	if len(data) < 5 {
		scores[types.RegimeAccumulation] = 0.25
		scores[types.RegimeMarkup] = 0.25
		scores[types.RegimeDistribution] = 0.25
		scores[types.RegimeMarkdown] = 0.25
		return scores, nil
	}

	mid := len(data) / 2
	firstHalf := data[:mid]
	secondHalf := data[mid:]

	// Calculate average closing price for each half.
	var firstPriceSum, secondPriceSum float64
	for _, d := range firstHalf {
		firstPriceSum += d.Close.InexactFloat64()
	}
	for _, d := range secondHalf {
		secondPriceSum += d.Close.InexactFloat64()
	}
	firstPriceAvg := firstPriceSum / float64(len(firstHalf))
	secondPriceAvg := secondPriceSum / float64(len(secondHalf))

	// Calculate average volume for each half.
	var firstVolSum, secondVolSum float64
	for _, d := range firstHalf {
		firstVolSum += d.Volume.InexactFloat64()
	}
	for _, d := range secondHalf {
		secondVolSum += d.Volume.InexactFloat64()
	}
	firstVolAvg := firstVolSum / float64(len(firstHalf))
	secondVolAvg := secondVolSum / float64(len(secondHalf))

	priceRising := secondPriceAvg > firstPriceAvg
	volumeRising := secondVolAvg > firstVolAvg

	if priceRising && !volumeRising {
		// Rising price + declining volume -> distribution.
		scores[types.RegimeDistribution] = 0.7
		scores[types.RegimeAccumulation] = 0.1
		scores[types.RegimeMarkup] = 0.1
		scores[types.RegimeMarkdown] = 0.1
	} else if !priceRising && volumeRising {
		// Declining price + rising volume -> markdown.
		scores[types.RegimeMarkdown] = 0.7
		scores[types.RegimeAccumulation] = 0.1
		scores[types.RegimeMarkup] = 0.1
		scores[types.RegimeDistribution] = 0.1
	} else {
		// Aligned (both rising or both declining) -> equal scores.
		scores[types.RegimeAccumulation] = 0.25
		scores[types.RegimeMarkup] = 0.25
		scores[types.RegimeDistribution] = 0.25
		scores[types.RegimeMarkdown] = 0.25
	}

	return scores, nil
}
