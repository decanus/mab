package cow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"

	"github.com/shopspring/decimal"

	"github.com/decanus/mab/internal/exchange"
	"github.com/decanus/mab/pkg/types"
)

// Compile-time interface check.
var _ exchange.Exchange = (*Client)(nil)

// Signer signs order data and returns a signature hex string.
// Inject a real implementation for production use.
type Signer interface {
	Sign(order *CowOrder) (signature string, scheme string, err error)
}

// Client is a CoW Protocol exchange client.
type Client struct {
	baseURL       string
	httpClient    *http.Client
	appData       string
	signerAddress string
	signer        Signer
}

// NewClient creates a new CoW Protocol client.
// If no custom HTTP client is needed, http.DefaultClient is used.
func NewClient(baseURL, appData, signerAddress string) *Client {
	return &Client{
		baseURL:       baseURL,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		appData:       appData,
		signerAddress: signerAddress,
	}
}

// WithHTTPClient sets a custom HTTP client (useful for testing).
func (c *Client) WithHTTPClient(hc *http.Client) *Client {
	c.httpClient = hc
	return c
}

// WithSigner sets a real signer for order signatures.
func (c *Client) WithSigner(s Signer) *Client {
	c.signer = s
	return c
}

// Name returns the exchange identifier.
func (c *Client) Name() string {
	return "cow"
}

// SupportsBatchAuction returns true because CoW Protocol is a batch auction exchange.
func (c *Client) SupportsBatchAuction() bool {
	return true
}

// GetLiquidity queries a CoW quote to estimate available liquidity.
func (c *Client) GetLiquidity(ctx context.Context, pair types.TradingPair, slippageBps int) (*types.LiquidityInfo, error) {
	// Use a reference sell amount of 10,000 USD (as wei, assuming 6 decimals for stablecoins).
	refAmountWei := "10000000000" // 10,000 * 1e6

	req := CowQuoteRequest{
		SellToken:           pair.QuoteAddress,
		BuyToken:            pair.BaseAddress,
		SellAmountBeforeFee: refAmountWei,
		Kind:                "sell",
		From:                c.signerAddress,
	}

	var resp CowQuoteResponse
	if err := c.doPost(ctx, "/api/v1/quote", req, &resp); err != nil {
		return nil, fmt.Errorf("cow: get liquidity quote: %w", err)
	}

	sellAmt, ok := new(big.Int).SetString(resp.Quote.SellAmount, 10)
	if !ok {
		return nil, fmt.Errorf("cow: invalid sell amount in quote: %s", resp.Quote.SellAmount)
	}
	buyAmt, ok := new(big.Int).SetString(resp.Quote.BuyAmount, 10)
	if !ok {
		return nil, fmt.Errorf("cow: invalid buy amount in quote: %s", resp.Quote.BuyAmount)
	}
	feeAmt, ok := new(big.Int).SetString(resp.Quote.FeeAmount, 10)
	if !ok {
		return nil, fmt.Errorf("cow: invalid fee amount in quote: %s", resp.Quote.FeeAmount)
	}

	sellDec := decimal.NewFromBigInt(sellAmt, 0)
	buyDec := decimal.NewFromBigInt(buyAmt, 0)
	feeDec := decimal.NewFromBigInt(feeAmt, 0)

	// Depth is approximated as the effective sell amount (sell - fee).
	depthUSD := sellDec.Sub(feeDec)
	if depthUSD.IsNegative() {
		depthUSD = decimal.Zero
	}

	// Price is buy/sell ratio.
	var price decimal.Decimal
	if !sellDec.IsZero() {
		price = buyDec.Div(sellDec)
	}

	return &types.LiquidityInfo{
		Exchange:     c.Name(),
		DepthUSD:     depthUSD,
		BestBidPrice: price,
		BestAskPrice: price,
		SlippageBps:  slippageBps,
	}, nil
}

// SubmitOrder constructs a CoW order and submits it.
func (c *Client) SubmitOrder(ctx context.Context, order *types.Order) (*types.OrderResult, error) {
	// Convert AmountUSD to wei string (assuming 6-decimal stablecoin).
	sellAmountWei := order.AmountUSD.Mul(decimal.NewFromInt(1e6)).BigInt().String()

	cowOrder := CowOrder{
		SellToken:         order.Pair.QuoteAddress,
		BuyToken:          order.Pair.BaseAddress,
		SellAmount:        sellAmountWei,
		BuyAmount:         "1", // minimum buy amount; solver determines actual
		ValidTo:           time.Now().Add(30 * time.Minute).Unix(),
		AppData:           c.appData,
		FeeAmount:         "0",
		Kind:              "sell",
		PartiallyFillable: true,
		Signature:         "0x0000000000000000000000000000000000000000000000000000000000000000",
		SigningScheme:     "eip712",
	}

	// If a real signer is injected, use it.
	if c.signer != nil {
		sig, scheme, err := c.signer.Sign(&cowOrder)
		if err != nil {
			return nil, fmt.Errorf("cow: sign order: %w", err)
		}
		cowOrder.Signature = sig
		cowOrder.SigningScheme = scheme
	}

	var uid string
	if err := c.doPost(ctx, "/api/v1/orders", cowOrder, &uid); err != nil {
		return nil, fmt.Errorf("cow: submit order: %w", err)
	}

	return &types.OrderResult{
		OrderID:  uid,
		Status:   types.OrderStatusPending,
		Exchange: c.Name(),
	}, nil
}

// OrderStatus queries the status of an existing order by UID.
func (c *Client) OrderStatus(ctx context.Context, orderID string) (*types.OrderStatusResult, error) {
	var resp CowOrderStatusResponse
	if err := c.doGet(ctx, fmt.Sprintf("/api/v1/orders/%s", orderID), &resp); err != nil {
		return nil, fmt.Errorf("cow: order status: %w", err)
	}

	status := mapCowStatus(resp.Status)

	filledAmt := decimal.Zero
	if resp.ExecutedSellAmount != "" {
		if v, ok := new(big.Int).SetString(resp.ExecutedSellAmount, 10); ok {
			filledAmt = decimal.NewFromBigInt(v, 0)
		}
	}

	avgPrice := decimal.Zero
	if resp.ExecutedBuyAmount != "" && !filledAmt.IsZero() {
		if v, ok := new(big.Int).SetString(resp.ExecutedBuyAmount, 10); ok {
			avgPrice = decimal.NewFromBigInt(v, 0).Div(filledAmt)
		}
	}

	return &types.OrderStatusResult{
		OrderID:      resp.UID,
		Status:       status,
		FilledAmount: filledAmt,
		AvgPrice:     avgPrice,
	}, nil
}

// CancelOrder requests cancellation of an order.
func (c *Client) CancelOrder(ctx context.Context, orderID string) error {
	url := fmt.Sprintf("%s/api/v1/orders/%s", c.baseURL, orderID)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("cow: cancel order request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cow: cancel order: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return c.parseHTTPError(resp)
	}

	return nil
}

// RecentFills fetches trades for the signer address since the given time.
func (c *Client) RecentFills(ctx context.Context, pair types.TradingPair, since time.Time) ([]types.Fill, error) {
	path := fmt.Sprintf("/api/v1/trades?owner=%s", c.signerAddress)

	var trades []CowTrade
	if err := c.doGet(ctx, path, &trades); err != nil {
		return nil, fmt.Errorf("cow: recent fills: %w", err)
	}

	var fills []types.Fill
	for _, t := range trades {
		execTime, err := time.Parse(time.RFC3339, t.ExecutionDate)
		if err != nil {
			// Try alternate format.
			execTime, err = time.Parse("2006-01-02T15:04:05.999999999Z", t.ExecutionDate)
			if err != nil {
				continue
			}
		}

		if execTime.Before(since) {
			continue
		}

		sellAmt := decimal.Zero
		if v, ok := new(big.Int).SetString(t.SellAmount, 10); ok {
			sellAmt = decimal.NewFromBigInt(v, 0)
		}

		buyAmt := decimal.Zero
		if v, ok := new(big.Int).SetString(t.BuyAmount, 10); ok {
			buyAmt = decimal.NewFromBigInt(v, 0)
		}

		avgPrice := decimal.Zero
		if !sellAmt.IsZero() {
			avgPrice = buyAmt.Div(sellAmt)
		}

		fills = append(fills, types.Fill{
			OrderID:     t.OrderUID,
			Exchange:    c.Name(),
			AmountUSD:   sellAmt,
			AvgPrice:    avgPrice,
			SlippageBps: 0, // not available from trades endpoint
			MEVSavedUSD: decimal.Zero,
			FilledAt:    execTime,
		})
	}

	return fills, nil
}

// doPost performs a POST request with JSON body and decodes the response.
func (c *Client) doPost(ctx context.Context, path string, body interface{}, result interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return c.parseHTTPError(resp)
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}

// doGet performs a GET request and decodes the JSON response.
func (c *Client) doGet(ctx context.Context, path string, result interface{}) error {
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return c.parseHTTPError(resp)
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}

// parseHTTPError reads the response body and returns a structured error.
func (c *Client) parseHTTPError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)

	var cowErr CowErrorResponse
	if json.Unmarshal(body, &cowErr) == nil && cowErr.Description != "" {
		return fmt.Errorf("cow api error (HTTP %d): %s: %s", resp.StatusCode, cowErr.ErrorType, cowErr.Description)
	}

	return fmt.Errorf("cow api error (HTTP %d): %s", resp.StatusCode, string(body))
}

// mapCowStatus maps CoW Protocol status strings to OrderStatus.
func mapCowStatus(s string) types.OrderStatus {
	switch s {
	case "open":
		return types.OrderStatusPending
	case "fulfilled":
		return types.OrderStatusFilled
	case "partiallyFilled":
		return types.OrderStatusPartial
	case "cancelled":
		return types.OrderStatusCancelled
	case "expired":
		return types.OrderStatusCancelled
	default:
		return types.OrderStatusFailed
	}
}
