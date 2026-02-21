package mock

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/decanus/mab/pkg/types"
)

var testPair = types.TradingPair{Base: "AAVE", Quote: "USD"}

func TestMockProvider_GeneratesValidOHLCV(t *testing.T) {
	regimes := []types.MarketRegime{
		types.RegimeAccumulation,
		types.RegimeMarkup,
		types.RegimeDistribution,
		types.RegimeMarkdown,
	}

	for _, regime := range regimes {
		t.Run(string(regime), func(t *testing.T) {
			p := NewMockProvider(regime, decimal.NewFromFloat(100.0), decimal.NewFromFloat(1000000.0), 42)
			candles, err := p.GetOHLCV(context.Background(), testPair, "1d", 30)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(candles) != 30 {
				t.Fatalf("expected 30 candles, got %d", len(candles))
			}

			for i, c := range candles {
				// No negative prices.
				if c.Open.LessThanOrEqual(decimal.Zero) {
					t.Errorf("candle %d: non-positive open %s", i, c.Open)
				}
				if c.High.LessThanOrEqual(decimal.Zero) {
					t.Errorf("candle %d: non-positive high %s", i, c.High)
				}
				if c.Low.LessThanOrEqual(decimal.Zero) {
					t.Errorf("candle %d: non-positive low %s", i, c.Low)
				}
				if c.Close.LessThanOrEqual(decimal.Zero) {
					t.Errorf("candle %d: non-positive close %s", i, c.Close)
				}

				// Volume >= 0.
				if c.Volume.LessThan(decimal.Zero) {
					t.Errorf("candle %d: negative volume %s", i, c.Volume)
				}

				// High >= Low.
				if c.High.LessThan(c.Low) {
					t.Errorf("candle %d: high %s < low %s", i, c.High, c.Low)
				}

				// High >= Open and High >= Close.
				if c.High.LessThan(c.Open) {
					t.Errorf("candle %d: high %s < open %s", i, c.High, c.Open)
				}
				if c.High.LessThan(c.Close) {
					t.Errorf("candle %d: high %s < close %s", i, c.High, c.Close)
				}

				// Low <= Open and Low <= Close.
				if c.Low.GreaterThan(c.Open) {
					t.Errorf("candle %d: low %s > open %s", i, c.Low, c.Open)
				}
				if c.Low.GreaterThan(c.Close) {
					t.Errorf("candle %d: low %s > close %s", i, c.Low, c.Close)
				}
			}
		})
	}
}

func TestMockProvider_AccumulationRegime(t *testing.T) {
	p := NewMockProvider(types.RegimeAccumulation, decimal.NewFromFloat(100.0), decimal.NewFromFloat(1000000.0), 42)
	candles, err := p.GetOHLCV(context.Background(), testPair, "1d", 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify flat prices: the last close should be within +-15% of the first open.
	firstOpen := candles[0].Open
	lastClose := candles[len(candles)-1].Close
	pctChange := lastClose.Sub(firstOpen).Div(firstOpen).Abs()
	maxDrift := decimal.NewFromFloat(0.15)
	if pctChange.GreaterThan(maxDrift) {
		t.Errorf("accumulation: price drifted too much: %s%% (first=%s, last=%s)",
			pctChange.Mul(decimal.NewFromInt(100)).StringFixed(2), firstOpen, lastClose)
	}

	// Verify declining volume trend: average volume in first half > average in second half.
	half := len(candles) / 2
	var firstHalfVol, secondHalfVol decimal.Decimal
	for i, c := range candles {
		if i < half {
			firstHalfVol = firstHalfVol.Add(c.Volume)
		} else {
			secondHalfVol = secondHalfVol.Add(c.Volume)
		}
	}
	avgFirst := firstHalfVol.Div(decimal.NewFromInt(int64(half)))
	avgSecond := secondHalfVol.Div(decimal.NewFromInt(int64(len(candles) - half)))

	if avgSecond.GreaterThanOrEqual(avgFirst) {
		t.Errorf("accumulation: expected declining volume trend, first half avg=%s, second half avg=%s",
			avgFirst.StringFixed(2), avgSecond.StringFixed(2))
	}
}

func TestMockProvider_MarkupRegime(t *testing.T) {
	p := NewMockProvider(types.RegimeMarkup, decimal.NewFromFloat(100.0), decimal.NewFromFloat(1000000.0), 42)
	candles, err := p.GetOHLCV(context.Background(), testPair, "1d", 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify generally rising prices: last close should be higher than first open.
	firstOpen := candles[0].Open
	lastClose := candles[len(candles)-1].Close

	if lastClose.LessThanOrEqual(firstOpen) {
		t.Errorf("markup: expected rising prices, first open=%s, last close=%s", firstOpen, lastClose)
	}

	// The price should have risen by at least 10% over 30 periods of 0.5-1.5% daily growth.
	minGrowth := decimal.NewFromFloat(0.10)
	actualGrowth := lastClose.Sub(firstOpen).Div(firstOpen)
	if actualGrowth.LessThan(minGrowth) {
		t.Errorf("markup: expected at least 10%% growth, got %s%%",
			actualGrowth.Mul(decimal.NewFromInt(100)).StringFixed(2))
	}
}

func TestMockProvider_MarkdownRegime(t *testing.T) {
	p := NewMockProvider(types.RegimeMarkdown, decimal.NewFromFloat(100.0), decimal.NewFromFloat(1000000.0), 42)
	candles, err := p.GetOHLCV(context.Background(), testPair, "1d", 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify generally declining prices: last close should be lower than first open.
	firstOpen := candles[0].Open
	lastClose := candles[len(candles)-1].Close

	if lastClose.GreaterThanOrEqual(firstOpen) {
		t.Errorf("markdown: expected declining prices, first open=%s, last close=%s", firstOpen, lastClose)
	}

	// The price should have dropped by at least 10% over 30 periods.
	minDrop := decimal.NewFromFloat(0.10)
	actualDrop := firstOpen.Sub(lastClose).Div(firstOpen)
	if actualDrop.LessThan(minDrop) {
		t.Errorf("markdown: expected at least 10%% drop, got %s%%",
			actualDrop.Mul(decimal.NewFromInt(100)).StringFixed(2))
	}
}

func TestMockProvider_Deterministic(t *testing.T) {
	seed := int64(12345)
	p1 := NewMockProvider(types.RegimeMarkup, decimal.NewFromFloat(100.0), decimal.NewFromFloat(1000000.0), seed)
	p2 := NewMockProvider(types.RegimeMarkup, decimal.NewFromFloat(100.0), decimal.NewFromFloat(1000000.0), seed)

	candles1, err := p1.GetOHLCV(context.Background(), testPair, "1d", 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	candles2, err := p2.GetOHLCV(context.Background(), testPair, "1d", 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(candles1) != len(candles2) {
		t.Fatalf("different lengths: %d vs %d", len(candles1), len(candles2))
	}

	for i := range candles1 {
		if !candles1[i].Open.Equal(candles2[i].Open) {
			t.Errorf("candle %d: open mismatch %s vs %s", i, candles1[i].Open, candles2[i].Open)
		}
		if !candles1[i].High.Equal(candles2[i].High) {
			t.Errorf("candle %d: high mismatch %s vs %s", i, candles1[i].High, candles2[i].High)
		}
		if !candles1[i].Low.Equal(candles2[i].Low) {
			t.Errorf("candle %d: low mismatch %s vs %s", i, candles1[i].Low, candles2[i].Low)
		}
		if !candles1[i].Close.Equal(candles2[i].Close) {
			t.Errorf("candle %d: close mismatch %s vs %s", i, candles1[i].Close, candles2[i].Close)
		}
		if !candles1[i].Volume.Equal(candles2[i].Volume) {
			t.Errorf("candle %d: volume mismatch %s vs %s", i, candles1[i].Volume, candles2[i].Volume)
		}
	}
}

func TestMockProvider_VWAPCalculation(t *testing.T) {
	// Use a known seed and small number of periods, then verify VWAP by hand.
	p := NewMockProvider(types.RegimeAccumulation, decimal.NewFromFloat(100.0), decimal.NewFromFloat(1000.0), 99)
	candles, err := p.GetOHLCV(context.Background(), testPair, "1d", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Calculate expected VWAP manually.
	three := decimal.NewFromInt(3)
	sumTPV := decimal.Zero
	sumVol := decimal.Zero

	for _, c := range candles {
		tp := c.High.Add(c.Low).Add(c.Close).Div(three)
		sumTPV = sumTPV.Add(tp.Mul(c.Volume))
		sumVol = sumVol.Add(c.Volume)
	}

	var expectedVWAP decimal.Decimal
	if sumVol.IsZero() {
		sumClose := decimal.Zero
		for _, c := range candles {
			sumClose = sumClose.Add(c.Close)
		}
		expectedVWAP = sumClose.Div(decimal.NewFromInt(int64(len(candles))))
	} else {
		expectedVWAP = sumTPV.Div(sumVol)
	}

	// Get the VWAP from the provider.
	vwap, err := p.GetVWAP(context.Background(), testPair, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	diff := vwap.Sub(expectedVWAP).Abs()
	threshold := decimal.NewFromFloat(0.0001)
	if diff.GreaterThan(threshold) {
		t.Errorf("VWAP mismatch: expected %s, got %s (diff=%s)",
			expectedVWAP.StringFixed(6), vwap.StringFixed(6), diff)
	}
}
