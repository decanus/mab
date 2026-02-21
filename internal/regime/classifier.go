package regime

import (
	"context"
	"errors"

	"github.com/decanus/mab/pkg/types"
)

// ScoreFunc produces regime scores from OHLCV data.
type ScoreFunc interface {
	// Name returns a unique identifier for this scoring function.
	Name() string
	// Score evaluates the given OHLCV data and returns a score for each regime.
	Score(ctx context.Context, data []types.OHLCV) (types.RegimeScores, error)
	// Weight returns the relative weight of this scoring function.
	Weight() float64
}

// Classifier combines multiple ScoreFuncs to classify the current market regime.
type Classifier struct {
	scoreFuncs []ScoreFunc
}

// NewClassifier creates a new Classifier with the given scoring functions.
func NewClassifier(funcs ...ScoreFunc) *Classifier {
	return &Classifier{scoreFuncs: funcs}
}

// Classify runs all scoring functions against the provided data and returns
// the most likely market regime along with confidence and a per-function breakdown.
func (c *Classifier) Classify(ctx context.Context, data []types.OHLCV) (*types.ClassificationResult, error) {
	if len(c.scoreFuncs) == 0 {
		return nil, errors.New("no score functions configured")
	}

	breakdown := make(map[string]types.RegimeScores, len(c.scoreFuncs))
	allRegimes := types.AllRegimes()

	// Aggregate weighted scores across all scoring functions.
	aggregated := make(types.RegimeScores, len(allRegimes))
	for _, r := range allRegimes {
		aggregated[r] = 0
	}

	for _, sf := range c.scoreFuncs {
		scores, err := sf.Score(ctx, data)
		if err != nil {
			return nil, err
		}
		breakdown[sf.Name()] = scores
		w := sf.Weight()
		for _, r := range allRegimes {
			aggregated[r] += scores[r] * w
		}
	}

	// Normalize so all scores sum to 1.0.
	var total float64
	for _, r := range allRegimes {
		total += aggregated[r]
	}
	if total > 0 {
		for _, r := range allRegimes {
			aggregated[r] /= total
		}
	}

	// Find the highest scoring regime. Ties broken alphabetically via AllRegimes() order.
	bestRegime := allRegimes[0]
	bestScore := aggregated[allRegimes[0]]
	for _, r := range allRegimes[1:] {
		s := aggregated[r]
		if s > bestScore {
			bestScore = s
			bestRegime = r
		}
	}

	// Find the second-highest score for confidence calculation.
	secondBest := -1.0
	for _, r := range allRegimes {
		if r == bestRegime {
			continue
		}
		if aggregated[r] > secondBest {
			secondBest = aggregated[r]
		}
	}
	if secondBest < 0 {
		secondBest = 0
	}

	confidence := bestScore - secondBest

	return &types.ClassificationResult{
		Regime:     bestRegime,
		Confidence: confidence,
		Breakdown:  breakdown,
	}, nil
}
