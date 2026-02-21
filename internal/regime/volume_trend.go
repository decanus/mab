package regime

import (
	"context"

	"github.com/decanus/mab/pkg/types"
)

// VolumeTrendScore compares short-term (5-day) average volume to longer-term
// (20-day) average volume to determine volume trend regime scores.
type VolumeTrendScore struct {
	weight float64
}

// NewVolumeTrendScore creates a new VolumeTrendScore with the given weight.
func NewVolumeTrendScore(weight float64) *VolumeTrendScore {
	return &VolumeTrendScore{weight: weight}
}

// Name returns the identifier for this scoring function.
func (v *VolumeTrendScore) Name() string {
	return "volume_trend"
}

// Weight returns the relative weight of this scoring function.
func (v *VolumeTrendScore) Weight() float64 {
	return v.weight
}

// Score calculates regime scores based on 5-day vs 20-day average volume comparison.
func (v *VolumeTrendScore) Score(_ context.Context, data []types.OHLCV) (types.RegimeScores, error) {
	scores := make(types.RegimeScores)

	if len(data) < 5 {
		scores[types.RegimeAccumulation] = 0.25
		scores[types.RegimeMarkup] = 0.25
		scores[types.RegimeDistribution] = 0.25
		scores[types.RegimeMarkdown] = 0.25
		return scores, nil
	}

	// Calculate 5-day average volume (last 5 data points).
	var fiveDaySum float64
	for _, d := range data[len(data)-5:] {
		fiveDaySum += d.Volume.InexactFloat64()
	}
	fiveDayAvg := fiveDaySum / 5.0

	// Calculate 20-day average volume (last min(20, len) data points).
	lookback := len(data)
	if lookback > 20 {
		lookback = 20
	}
	var twentyDaySum float64
	for _, d := range data[len(data)-lookback:] {
		twentyDaySum += d.Volume.InexactFloat64()
	}
	twentyDayAvg := twentyDaySum / float64(lookback)

	// Determine ratio.
	var ratio float64
	if twentyDayAvg > 0 {
		ratio = (fiveDayAvg - twentyDayAvg) / twentyDayAvg
	}

	if ratio > 0.10 {
		// Rising volume: 5d > 20d by more than 10%.
		scores[types.RegimeMarkup] = 0.4
		scores[types.RegimeMarkdown] = 0.4
		scores[types.RegimeAccumulation] = 0.1
		scores[types.RegimeDistribution] = 0.1
	} else if ratio < -0.10 {
		// Declining volume: 5d < 20d by more than 10%.
		scores[types.RegimeAccumulation] = 0.7
		scores[types.RegimeMarkup] = 0.1
		scores[types.RegimeMarkdown] = 0.1
		scores[types.RegimeDistribution] = 0.1
	} else {
		// Neutral.
		scores[types.RegimeAccumulation] = 0.25
		scores[types.RegimeMarkup] = 0.25
		scores[types.RegimeDistribution] = 0.25
		scores[types.RegimeMarkdown] = 0.25
	}

	return scores, nil
}
