package coingecko

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/decanus/mab/pkg/types"
)

// newTestServer sets up an httptest.Server that serves mock CoinGecko responses.
// The handler dispatches on the URL path to return appropriate mock data.
func newTestServer(t *testing.T, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for pattern, handler := range handlers {
		mux.HandleFunc(pattern, handler)
	}
	return httptest.NewServer(mux)
}

func TestClient_GetOHLCV(t *testing.T) {
	// Mock OHLC response: [[timestamp_ms, open, high, low, close], ...]
	ohlcData := [][]json.Number{
		{"1704067200000", "100.0", "105.0", "98.0", "103.0"},
		{"1704153600000", "103.0", "110.0", "101.0", "108.0"},
		{"1704240000000", "108.0", "112.0", "106.0", "110.0"},
	}

	// Mock market_chart response with volumes at same timestamps.
	marketChartData := map[string]interface{}{
		"prices": [][]json.Number{
			{"1704067200000", "100.0"},
			{"1704153600000", "103.0"},
			{"1704240000000", "108.0"},
		},
		"total_volumes": [][]json.Number{
			{"1704067200000", "5000000.0"},
			{"1704153600000", "6000000.0"},
			{"1704240000000", "5500000.0"},
		},
	}

	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/coins/aave/ohlc": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(ohlcData)
		},
		"/coins/aave/market_chart": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(marketChartData)
		},
	})
	defer srv.Close()

	client := NewClient(srv.URL, "aave")
	pair := types.TradingPair{Base: "AAVE", Quote: "USD"}

	candles, err := client.GetOHLCV(context.Background(), pair, "1d", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(candles) != 3 {
		t.Fatalf("expected 3 candles, got %d", len(candles))
	}

	// Verify first candle.
	if !candles[0].Open.Equal(decimal.NewFromFloat(100.0)) {
		t.Errorf("expected open=100.0, got %s", candles[0].Open)
	}
	if !candles[0].High.Equal(decimal.NewFromFloat(105.0)) {
		t.Errorf("expected high=105.0, got %s", candles[0].High)
	}
	if !candles[0].Low.Equal(decimal.NewFromFloat(98.0)) {
		t.Errorf("expected low=98.0, got %s", candles[0].Low)
	}
	if !candles[0].Close.Equal(decimal.NewFromFloat(103.0)) {
		t.Errorf("expected close=103.0, got %s", candles[0].Close)
	}
	if !candles[0].Volume.Equal(decimal.NewFromFloat(5000000.0)) {
		t.Errorf("expected volume=5000000.0, got %s", candles[0].Volume)
	}

	// Verify no negative prices in all candles.
	for i, c := range candles {
		if c.Open.LessThan(decimal.Zero) {
			t.Errorf("candle %d: negative open %s", i, c.Open)
		}
		if c.High.LessThan(decimal.Zero) {
			t.Errorf("candle %d: negative high %s", i, c.High)
		}
		if c.Low.LessThan(decimal.Zero) {
			t.Errorf("candle %d: negative low %s", i, c.Low)
		}
		if c.Close.LessThan(decimal.Zero) {
			t.Errorf("candle %d: negative close %s", i, c.Close)
		}
		if c.Volume.LessThan(decimal.Zero) {
			t.Errorf("candle %d: negative volume %s", i, c.Volume)
		}
	}
}

func TestClient_GetCurrentPrice(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/simple/price": func(w http.ResponseWriter, r *http.Request) {
			// Verify query parameters.
			ids := r.URL.Query().Get("ids")
			vs := r.URL.Query().Get("vs_currencies")
			if ids != "aave" || vs != "usd" {
				t.Errorf("unexpected params: ids=%s, vs_currencies=%s", ids, vs)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"aave": {"usd": 267.89}}`))
		},
	})
	defer srv.Close()

	client := NewClient(srv.URL, "aave")
	pair := types.TradingPair{Base: "AAVE", Quote: "USD"}

	price, err := client.GetCurrentPrice(context.Background(), pair)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := decimal.NewFromFloat(267.89)
	if !price.Equal(expected) {
		t.Errorf("expected price %s, got %s", expected, price)
	}
}

func TestClient_GetVWAP(t *testing.T) {
	// Create known OHLCV data for hand calculation.
	// Candle 1: H=105, L=98, C=103, V=5000000
	//   TP = (105+98+103)/3 = 306/3 = 102
	//   TPV = 102 * 5000000 = 510000000
	// Candle 2: H=110, L=101, C=108, V=6000000
	//   TP = (110+101+108)/3 = 319/3 = 106.333...
	//   TPV = 106.333... * 6000000 = 638000000
	// VWAP = (510000000 + 638000000) / (5000000 + 6000000)
	//      = 1148000000 / 11000000
	//      = 104.363636...

	ohlcData := [][]json.Number{
		{"1704067200000", "100.0", "105.0", "98.0", "103.0"},
		{"1704153600000", "103.0", "110.0", "101.0", "108.0"},
	}

	marketChartData := map[string]interface{}{
		"total_volumes": [][]json.Number{
			{"1704067200000", "5000000"},
			{"1704153600000", "6000000"},
		},
	}

	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/coins/aave/ohlc": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(ohlcData)
		},
		"/coins/aave/market_chart": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(marketChartData)
		},
	})
	defer srv.Close()

	client := NewClient(srv.URL, "aave")
	pair := types.TradingPair{Base: "AAVE", Quote: "USD"}

	vwap, err := client.GetVWAP(context.Background(), pair, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// VWAP = 1148000000 / 11000000 = 104.363636...
	// We use string comparison rounded to 6 decimal places.
	expected := decimal.NewFromInt(1148000000).Div(decimal.NewFromInt(11000000))
	diff := vwap.Sub(expected).Abs()
	threshold := decimal.NewFromFloat(0.01)
	if diff.GreaterThan(threshold) {
		t.Errorf("expected VWAP ~%s, got %s (diff=%s)", expected.StringFixed(6), vwap.StringFixed(6), diff)
	}
}

func TestClient_HTTPError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"rate_limited_429", http.StatusTooManyRequests},
		{"server_error_500", http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			defer srv.Close()

			client := NewClient(srv.URL, "aave")
			pair := types.TradingPair{Base: "AAVE", Quote: "USD"}

			// Test GetCurrentPrice.
			_, err := client.GetCurrentPrice(context.Background(), pair)
			if err == nil {
				t.Error("expected error for GetCurrentPrice, got nil")
			}

			// Test GetOHLCV.
			_, err = client.GetOHLCV(context.Background(), pair, "1d", 7)
			if err == nil {
				t.Error("expected error for GetOHLCV, got nil")
			}
		})
	}
}

func TestClient_VsCurrencyMapping(t *testing.T) {
	tests := []struct {
		symbol   string
		expected string
	}{
		{"usd", "usd"},
		{"USD", "usd"},
		{"usdc", "usd"},
		{"USDC", "usd"},
		{"usdt", "usd"},
		{"USDT", "usd"},
		{"eth", "eth"},
		{"ETH", "eth"},
		{"btc", "btc"},
		{"eur", "eur"},
	}

	for _, tt := range tests {
		t.Run(tt.symbol, func(t *testing.T) {
			got := vsCurrency(tt.symbol)
			if got != tt.expected {
				t.Errorf("vsCurrency(%q) = %q, want %q", tt.symbol, got, tt.expected)
			}
		})
	}
}
