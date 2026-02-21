package regime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/decanus/mab/pkg/types"
	"github.com/shopspring/decimal"
)

// makeOHLCV creates OHLCV test data from slices of prices and volumes.
// Open, High, Low are derived from Close for simplicity: Open=Close, High=Close*1.01, Low=Close*0.99.
func makeOHLCV(prices []float64, volumes []float64) []types.OHLCV {
	n := len(prices)
	if len(volumes) < n {
		n = len(volumes)
	}
	data := make([]types.OHLCV, n)
	for i := 0; i < n; i++ {
		p := decimal.NewFromFloat(prices[i])
		v := decimal.NewFromFloat(volumes[i])
		data[i] = types.OHLCV{
			Timestamp: time.Date(2025, 1, 1+i, 0, 0, 0, 0, time.UTC),
			Open:      p,
			High:      p.Mul(decimal.NewFromFloat(1.01)),
			Low:       p.Mul(decimal.NewFromFloat(0.99)),
			Close:     p,
			Volume:    v,
		}
	}
	return data
}

func TestPriceTrendScore(t *testing.T) {
	tests := []struct {
		name          string
		prices        []float64
		volumes       []float64
		expectedRegime types.MarketRegime
	}{
		{
			name:          "rising prices -> markup",
			prices:        []float64{100, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 115, 120, 125, 130, 135, 140, 145, 150, 155},
			volumes:       []float64{1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000},
			expectedRegime: types.RegimeMarkup,
		},
		{
			name:          "flat prices -> accumulation",
			prices:        []float64{100, 100.1, 99.9, 100, 100.1, 99.9, 100, 100.1, 99.9, 100, 100, 100.1, 99.9, 100, 100.1, 99.9, 100, 100.1, 99.9, 100},
			volumes:       []float64{1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000},
			expectedRegime: types.RegimeAccumulation,
		},
		{
			name:          "declining prices -> markdown",
			prices:        []float64{155, 150, 145, 140, 135, 130, 125, 120, 115, 110, 109, 108, 107, 106, 105, 104, 103, 102, 101, 100},
			volumes:       []float64{1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000},
			expectedRegime: types.RegimeMarkdown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scorer := NewPriceTrendScore(1.0)
			if scorer.Name() == "" {
				t.Fatal("Name() should not be empty")
			}

			data := makeOHLCV(tt.prices, tt.volumes)
			scores, err := scorer.Score(context.Background(), data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify all 4 regimes are present.
			for _, r := range types.AllRegimes() {
				if _, ok := scores[r]; !ok {
					t.Errorf("missing regime %s in scores", r)
				}
			}

			// Verify scores are in [0, 1].
			for r, s := range scores {
				if s < 0 || s > 1 {
					t.Errorf("score for %s out of range: %f", r, s)
				}
			}

			// Verify expected regime has the highest score.
			bestRegime := findBestRegime(scores)
			if bestRegime != tt.expectedRegime {
				t.Errorf("expected regime %s, got %s (scores: %v)", tt.expectedRegime, bestRegime, scores)
			}
		})
	}
}

func TestVolumeTrendScore(t *testing.T) {
	tests := []struct {
		name          string
		prices        []float64
		volumes       []float64
		expectedRegime types.MarketRegime
	}{
		{
			name:   "declining volume -> accumulation",
			prices: []float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100},
			// 20-day avg is higher than last 5-day avg
			volumes:        []float64{2000, 2000, 2000, 2000, 2000, 2000, 2000, 2000, 2000, 2000, 2000, 2000, 2000, 2000, 2000, 500, 500, 500, 500, 500},
			expectedRegime: types.RegimeAccumulation,
		},
		{
			name:   "rising volume -> markup or markdown split",
			prices: []float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100},
			// Last 5 days have much higher volume.
			volumes:        []float64{500, 500, 500, 500, 500, 500, 500, 500, 500, 500, 500, 500, 500, 500, 500, 3000, 3000, 3000, 3000, 3000},
			expectedRegime: types.RegimeMarkdown, // tied with markup, markdown comes first alphabetically
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scorer := NewVolumeTrendScore(1.0)
			data := makeOHLCV(tt.prices, tt.volumes)
			scores, err := scorer.Score(context.Background(), data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			bestRegime := findBestRegime(scores)
			if bestRegime != tt.expectedRegime {
				t.Errorf("expected regime %s, got %s (scores: %v)", tt.expectedRegime, bestRegime, scores)
			}
		})
	}
}

func TestVolatilityScore(t *testing.T) {
	tests := []struct {
		name          string
		prices        []float64
		expectedRegime types.MarketRegime
	}{
		{
			name: "low volatility -> accumulation",
			// Very small price changes to produce low annualized vol (<30%).
			prices:         []float64{100, 100.05, 99.95, 100.02, 99.98, 100.01, 99.99, 100.03, 99.97, 100.01, 100, 100.02, 99.98, 100.01, 99.99, 100.03, 99.97, 100.01, 100, 100.02, 99.98, 100.01, 99.99, 100.03, 99.97, 100.01, 100, 100.02, 99.98, 100.01},
			expectedRegime: types.RegimeAccumulation,
		},
		{
			name: "high volatility -> distribution",
			// Large swings to produce high annualized vol (>80%).
			prices:         []float64{100, 120, 80, 130, 70, 140, 60, 150, 50, 160, 100, 120, 80, 130, 70, 140, 60, 150, 50, 160, 100, 120, 80, 130, 70, 140, 60, 150, 50, 160},
			expectedRegime: types.RegimeDistribution,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scorer := NewVolatilityScore(1.0)
			volumes := make([]float64, len(tt.prices))
			for i := range volumes {
				volumes[i] = 1000
			}
			data := makeOHLCV(tt.prices, volumes)
			scores, err := scorer.Score(context.Background(), data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			bestRegime := findBestRegime(scores)
			if bestRegime != tt.expectedRegime {
				t.Errorf("expected regime %s, got %s (scores: %v)", tt.expectedRegime, bestRegime, scores)
			}
		})
	}
}

func TestDivergenceScore(t *testing.T) {
	tests := []struct {
		name          string
		prices        []float64
		volumes       []float64
		expectedRegime types.MarketRegime
	}{
		{
			name:          "rising price + declining volume -> distribution",
			prices:        []float64{100, 100, 100, 100, 100, 110, 110, 110, 110, 110},
			volumes:       []float64{2000, 2000, 2000, 2000, 2000, 500, 500, 500, 500, 500},
			expectedRegime: types.RegimeDistribution,
		},
		{
			name:          "declining price + rising volume -> markdown",
			prices:        []float64{110, 110, 110, 110, 110, 100, 100, 100, 100, 100},
			volumes:       []float64{500, 500, 500, 500, 500, 2000, 2000, 2000, 2000, 2000},
			expectedRegime: types.RegimeMarkdown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scorer := NewDivergenceScore(1.0)
			data := makeOHLCV(tt.prices, tt.volumes)
			scores, err := scorer.Score(context.Background(), data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			bestRegime := findBestRegime(scores)
			if bestRegime != tt.expectedRegime {
				t.Errorf("expected regime %s, got %s (scores: %v)", tt.expectedRegime, bestRegime, scores)
			}
		})
	}
}

func TestClassifier_SingleScoreFunc(t *testing.T) {
	// Rising prices should produce a markup classification.
	prices := []float64{100, 102, 104, 106, 108, 110, 112, 114, 116, 118, 120, 122, 124, 126, 128, 130, 132, 134, 136, 138}
	volumes := make([]float64, len(prices))
	for i := range volumes {
		volumes[i] = 1000
	}
	data := makeOHLCV(prices, volumes)

	c := NewClassifier(NewPriceTrendScore(1.0))
	result, err := c.Classify(context.Background(), data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if result.Regime != types.RegimeMarkup {
		t.Errorf("expected markup, got %s", result.Regime)
	}
	if result.Confidence < 0 || result.Confidence > 1 {
		t.Errorf("confidence out of range: %f", result.Confidence)
	}
}

func TestClassifier_AllDefaults(t *testing.T) {
	// Flat prices, very low volatility, declining volume: accumulation should dominate.
	prices := make([]float64, 30)
	volumes := make([]float64, 30)
	for i := 0; i < 30; i++ {
		prices[i] = 100.0 + 0.01*float64(i%3-1) // tiny oscillation around 100
		if i < 15 {
			volumes[i] = 2000
		} else {
			volumes[i] = 800 // declining volume in the second half
		}
	}
	data := makeOHLCV(prices, volumes)

	c := NewClassifier(
		NewPriceTrendScore(1.0),
		NewVolumeTrendScore(1.0),
		NewVolatilityScore(1.0),
		NewDivergenceScore(1.0),
	)
	result, err := c.Classify(context.Background(), data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Regime != types.RegimeAccumulation {
		t.Errorf("expected accumulation, got %s", result.Regime)
	}
}

// testScoreFunc is a trivial ScoreFunc for testing.
type testScoreFunc struct {
	name   string
	scores types.RegimeScores
	w      float64
	err    error
}

func (f *testScoreFunc) Name() string { return f.name }
func (f *testScoreFunc) Weight() float64 { return f.w }
func (f *testScoreFunc) Score(_ context.Context, _ []types.OHLCV) (types.RegimeScores, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.scores, nil
}

func TestClassifier_CustomScoreFunc(t *testing.T) {
	custom := &testScoreFunc{
		name: "custom",
		scores: types.RegimeScores{
			types.RegimeAccumulation: 1.0,
			types.RegimeMarkup:      0.0,
			types.RegimeDistribution: 0.0,
			types.RegimeMarkdown:    0.0,
		},
		w: 1.0,
	}

	c := NewClassifier(custom)
	result, err := c.Classify(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Regime != types.RegimeAccumulation {
		t.Errorf("expected accumulation, got %s", result.Regime)
	}
	if result.Confidence != 1.0 {
		t.Errorf("expected confidence 1.0, got %f", result.Confidence)
	}
}

func TestClassifier_WeightInfluence(t *testing.T) {
	// func1 says markup (weight 1.0), func2 says markdown (weight 5.0).
	// Higher weight should win -> markdown.
	funcMarkup := &testScoreFunc{
		name: "markup_func",
		scores: types.RegimeScores{
			types.RegimeAccumulation: 0.0,
			types.RegimeMarkup:      1.0,
			types.RegimeDistribution: 0.0,
			types.RegimeMarkdown:    0.0,
		},
		w: 1.0,
	}
	funcMarkdown := &testScoreFunc{
		name: "markdown_func",
		scores: types.RegimeScores{
			types.RegimeAccumulation: 0.0,
			types.RegimeMarkup:      0.0,
			types.RegimeDistribution: 0.0,
			types.RegimeMarkdown:    1.0,
		},
		w: 5.0,
	}

	c := NewClassifier(funcMarkup, funcMarkdown)
	result, err := c.Classify(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Regime != types.RegimeMarkdown {
		t.Errorf("expected markdown (higher weight), got %s", result.Regime)
	}
}

func TestClassifier_EmptyFuncs(t *testing.T) {
	c := NewClassifier()
	_, err := c.Classify(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty score funcs")
	}
}

func TestClassifier_ScoreFuncError(t *testing.T) {
	errFunc := &testScoreFunc{
		name: "error_func",
		err:  errors.New("data fetch failed"),
		w:    1.0,
	}
	c := NewClassifier(errFunc)
	_, err := c.Classify(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error to be propagated")
	}
	if err.Error() != "data fetch failed" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestClassifier_SingleDataPoint(t *testing.T) {
	// A single data point should be handled gracefully by all score funcs.
	data := makeOHLCV([]float64{100}, []float64{1000})

	c := NewClassifier(
		NewPriceTrendScore(1.0),
		NewVolumeTrendScore(1.0),
		NewVolatilityScore(1.0),
		NewDivergenceScore(1.0),
	)
	result, err := c.Classify(context.Background(), data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
	// With all equal scores, should pick alphabetically first regime.
	if result.Regime != types.RegimeAccumulation {
		t.Errorf("expected accumulation (alphabetically first), got %s", result.Regime)
	}
}

func TestClassifier_EqualScores(t *testing.T) {
	// Both funcs return equal scores for all regimes.
	equalFunc := &testScoreFunc{
		name: "equal",
		scores: types.RegimeScores{
			types.RegimeAccumulation: 0.25,
			types.RegimeMarkup:      0.25,
			types.RegimeDistribution: 0.25,
			types.RegimeMarkdown:    0.25,
		},
		w: 1.0,
	}

	c := NewClassifier(equalFunc)
	result, err := c.Classify(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Deterministic: alphabetically first among AllRegimes().
	if result.Regime != types.RegimeAccumulation {
		t.Errorf("expected accumulation (first alphabetically), got %s", result.Regime)
	}
	// Confidence should be 0 when all scores are equal.
	if result.Confidence != 0 {
		t.Errorf("expected confidence 0 for equal scores, got %f", result.Confidence)
	}
}

func TestClassifier_Breakdown(t *testing.T) {
	f1 := &testScoreFunc{
		name: "func_a",
		scores: types.RegimeScores{
			types.RegimeAccumulation: 0.5,
			types.RegimeMarkup:      0.2,
			types.RegimeDistribution: 0.2,
			types.RegimeMarkdown:    0.1,
		},
		w: 1.0,
	}
	f2 := &testScoreFunc{
		name: "func_b",
		scores: types.RegimeScores{
			types.RegimeAccumulation: 0.3,
			types.RegimeMarkup:      0.3,
			types.RegimeDistribution: 0.2,
			types.RegimeMarkdown:    0.2,
		},
		w: 1.0,
	}

	c := NewClassifier(f1, f2)
	result, err := c.Classify(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify breakdown contains entries for every ScoreFunc.
	if _, ok := result.Breakdown["func_a"]; !ok {
		t.Error("breakdown missing entry for func_a")
	}
	if _, ok := result.Breakdown["func_b"]; !ok {
		t.Error("breakdown missing entry for func_b")
	}
	if len(result.Breakdown) != 2 {
		t.Errorf("expected 2 breakdown entries, got %d", len(result.Breakdown))
	}

	// Verify each breakdown entry has all regimes.
	for name, scores := range result.Breakdown {
		for _, r := range types.AllRegimes() {
			if _, ok := scores[r]; !ok {
				t.Errorf("breakdown %s missing regime %s", name, r)
			}
		}
	}
}

// findBestRegime returns the regime with the highest score, breaking ties alphabetically
// using AllRegimes() order.
func findBestRegime(scores types.RegimeScores) types.MarketRegime {
	allRegimes := types.AllRegimes()
	best := allRegimes[0]
	bestScore := scores[best]
	for _, r := range allRegimes[1:] {
		if scores[r] > bestScore {
			bestScore = scores[r]
			best = r
		}
	}
	return best
}
