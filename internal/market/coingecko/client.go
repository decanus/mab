package coingecko

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/decanus/mab/internal/market"
	"github.com/decanus/mab/pkg/types"
)

// Compile-time interface check.
var _ market.Provider = (*Client)(nil)

// tokenIDMap maps common token symbols to CoinGecko coin IDs.
var tokenIDMap = map[string]string{
	"aave":  "aave",
	"eth":   "ethereum",
	"btc":   "bitcoin",
	"sol":   "solana",
	"usdc":  "usd-coin",
	"usdt":  "tether",
	"dai":   "dai",
	"link":  "chainlink",
	"uni":   "uniswap",
	"matic": "matic-network",
	"avax":  "avalanche-2",
	"dot":   "polkadot",
}

// vsCurrencyMap maps common quote symbols to CoinGecko vs_currency values.
var vsCurrencyMap = map[string]string{
	"usd":  "usd",
	"usdc": "usd",
	"usdt": "usd",
	"eth":  "eth",
	"btc":  "btc",
}

// Client is a CoinGecko API client that implements market.Provider.
type Client struct {
	baseURL        string
	httpClient     *http.Client
	coinIDOverride string
}

// NewClient creates a new CoinGecko client. If baseURL is empty, the default
// CoinGecko API endpoint is used. If coinID is non-empty, it overrides the
// built-in symbol-to-ID mapping for all requests.
func NewClient(baseURL string, coinID string) *Client {
	if baseURL == "" {
		baseURL = "https://api.coingecko.com/api/v3"
	}
	return &Client{
		baseURL:        strings.TrimRight(baseURL, "/"),
		httpClient:     &http.Client{},
		coinIDOverride: coinID,
	}
}

// coinID maps a token symbol to its CoinGecko coin ID.
// If a coinIDOverride is set on the client, it takes precedence.
func (c *Client) coinID(symbol string) string {
	if c.coinIDOverride != "" {
		return c.coinIDOverride
	}
	lower := strings.ToLower(symbol)
	if id, ok := tokenIDMap[lower]; ok {
		return id
	}
	return lower
}

// vsCurrency maps a quote symbol to a CoinGecko vs_currency value.
func vsCurrency(symbol string) string {
	lower := strings.ToLower(symbol)
	if cur, ok := vsCurrencyMap[lower]; ok {
		return cur
	}
	return lower
}

// intervalToDays converts an interval string and period count to a number of days.
func intervalToDays(interval string, periods int) int {
	switch strings.ToLower(interval) {
	case "4h":
		days := periods / 6
		if days < 1 {
			days = 1
		}
		return days
	case "1h":
		days := periods / 24
		if days < 1 {
			days = 1
		}
		return days
	case "1d":
		return periods
	default:
		return periods
	}
}

// GetOHLCV fetches historical OHLCV data for the given trading pair.
func (c *Client) GetOHLCV(ctx context.Context, pair types.TradingPair, interval string, periods int) ([]types.OHLCV, error) {
	id := c.coinID(pair.Base)
	cur := vsCurrency(pair.Quote)
	days := intervalToDays(interval, periods)

	// Fetch OHLC data.
	ohlcData, err := c.fetchOHLC(ctx, id, cur, days)
	if err != nil {
		return nil, fmt.Errorf("coingecko: fetching OHLC: %w", err)
	}

	// Fetch volume data from market_chart endpoint.
	volumes, err := c.fetchVolumes(ctx, id, cur, days)
	if err != nil {
		return nil, fmt.Errorf("coingecko: fetching volumes: %w", err)
	}

	// Merge OHLC with volumes by closest timestamp.
	result := make([]types.OHLCV, 0, len(ohlcData))
	for _, candle := range ohlcData {
		vol := findClosestVolume(candle.TimestampMs, volumes)
		result = append(result, types.OHLCV{
			Timestamp: time.UnixMilli(candle.TimestampMs),
			Open:      candle.Open,
			High:      candle.High,
			Low:       candle.Low,
			Close:     candle.Close,
			Volume:    vol,
		})
	}

	return result, nil
}

// GetCurrentPrice fetches the current price for the given trading pair.
func (c *Client) GetCurrentPrice(ctx context.Context, pair types.TradingPair) (decimal.Decimal, error) {
	id := c.coinID(pair.Base)
	cur := vsCurrency(pair.Quote)

	url := fmt.Sprintf("%s/simple/price?ids=%s&vs_currencies=%s", c.baseURL, id, cur)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return decimal.Zero, fmt.Errorf("coingecko: creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return decimal.Zero, fmt.Errorf("coingecko: executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return decimal.Zero, fmt.Errorf("coingecko: HTTP %d from %s", resp.StatusCode, url)
	}

	// Response format: {"aave": {"usd": 123.45}}
	var data map[string]map[string]json.Number
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return decimal.Zero, fmt.Errorf("coingecko: decoding response: %w", err)
	}

	coinData, ok := data[id]
	if !ok {
		return decimal.Zero, fmt.Errorf("coingecko: no data for coin %q", id)
	}

	priceNum, ok := coinData[cur]
	if !ok {
		return decimal.Zero, fmt.Errorf("coingecko: no price for currency %q", cur)
	}

	price, err := decimal.NewFromString(priceNum.String())
	if err != nil {
		return decimal.Zero, fmt.Errorf("coingecko: parsing price %q: %w", priceNum, err)
	}

	return price, nil
}

// GetVWAP calculates the volume-weighted average price over the given number of periods.
func (c *Client) GetVWAP(ctx context.Context, pair types.TradingPair, periods int) (decimal.Decimal, error) {
	candles, err := c.GetOHLCV(ctx, pair, "1d", periods)
	if err != nil {
		return decimal.Zero, fmt.Errorf("coingecko: fetching OHLCV for VWAP: %w", err)
	}

	return calculateVWAP(candles), nil
}

// calculateVWAP computes the VWAP from a slice of OHLCV candles.
// VWAP = sum(typical_price * volume) / sum(volume)
// typical_price = (high + low + close) / 3
// If total volume is zero, returns the average of closing prices.
func calculateVWAP(candles []types.OHLCV) decimal.Decimal {
	if len(candles) == 0 {
		return decimal.Zero
	}

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
		return sumClose.Div(decimal.NewFromInt(int64(len(candles))))
	}

	return sumTPV.Div(sumVol)
}

// ohlcCandle represents a raw OHLC candle from CoinGecko.
type ohlcCandle struct {
	TimestampMs int64
	Open        decimal.Decimal
	High        decimal.Decimal
	Low         decimal.Decimal
	Close       decimal.Decimal
}

// volumePoint represents a volume data point from market_chart.
type volumePoint struct {
	TimestampMs int64
	Volume      decimal.Decimal
}

// fetchOHLC calls GET /coins/{id}/ohlc and parses the response.
func (c *Client) fetchOHLC(ctx context.Context, id, cur string, days int) ([]ohlcCandle, error) {
	url := fmt.Sprintf("%s/coins/%s/ohlc?vs_currency=%s&days=%d", c.baseURL, id, cur, days)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	// Response: [[timestamp_ms, open, high, low, close], ...]
	var raw [][]json.Number
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding OHLC: %w", err)
	}

	candles := make([]ohlcCandle, 0, len(raw))
	for _, entry := range raw {
		if len(entry) < 5 {
			continue
		}

		ts, err := entry[0].Int64()
		if err != nil {
			continue
		}

		open, err := decimal.NewFromString(entry[1].String())
		if err != nil {
			continue
		}
		high, err := decimal.NewFromString(entry[2].String())
		if err != nil {
			continue
		}
		low, err := decimal.NewFromString(entry[3].String())
		if err != nil {
			continue
		}
		cl, err := decimal.NewFromString(entry[4].String())
		if err != nil {
			continue
		}

		candles = append(candles, ohlcCandle{
			TimestampMs: ts,
			Open:        open,
			High:        high,
			Low:         low,
			Close:       cl,
		})
	}

	return candles, nil
}

// marketChartResponse represents the response from /coins/{id}/market_chart.
type marketChartResponse struct {
	TotalVolumes [][]json.Number `json:"total_volumes"`
}

// fetchVolumes calls GET /coins/{id}/market_chart and returns volume data points.
func (c *Client) fetchVolumes(ctx context.Context, id, cur string, days int) ([]volumePoint, error) {
	url := fmt.Sprintf("%s/coins/%s/market_chart?vs_currency=%s&days=%d", c.baseURL, id, cur, days)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	var chart marketChartResponse
	if err := json.NewDecoder(resp.Body).Decode(&chart); err != nil {
		return nil, fmt.Errorf("decoding market_chart: %w", err)
	}

	points := make([]volumePoint, 0, len(chart.TotalVolumes))
	for _, entry := range chart.TotalVolumes {
		if len(entry) < 2 {
			continue
		}

		ts, err := entry[0].Int64()
		if err != nil {
			continue
		}

		vol, err := decimal.NewFromString(entry[1].String())
		if err != nil {
			continue
		}

		points = append(points, volumePoint{
			TimestampMs: ts,
			Volume:      vol,
		})
	}

	return points, nil
}

// findClosestVolume finds the volume point closest to the given timestamp.
func findClosestVolume(targetMs int64, volumes []volumePoint) decimal.Decimal {
	if len(volumes) == 0 {
		return decimal.Zero
	}

	closest := volumes[0]
	minDiff := int64(math.MaxInt64)

	for _, v := range volumes {
		diff := targetMs - v.TimestampMs
		if diff < 0 {
			diff = -diff
		}
		if diff < minDiff {
			minDiff = diff
			closest = v
		}
	}

	return closest.Volume
}
