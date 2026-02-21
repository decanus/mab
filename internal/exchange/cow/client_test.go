package cow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/decanus/mab/pkg/types"
)

func newTestClient(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	c := NewClient(srv.URL, "0xTestAppData", "0xSignerAddress")
	c.httpClient = srv.Client()
	return c, srv
}

func TestClient_GetLiquidity(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/quote" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		var req CowQuoteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if req.From != "0xSignerAddress" {
			t.Errorf("expected from=0xSignerAddress, got %s", req.From)
		}
		if req.Kind != "sell" {
			t.Errorf("expected kind=sell, got %s", req.Kind)
		}

		resp := CowQuoteResponse{
			Quote: CowQuote{
				SellAmount: "9900000000",
				BuyAmount:  "5000000000000000000",
				FeeAmount:  "100000000",
			},
			From: req.From,
			ID:   42,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	client, srv := newTestClient(t, handler)
	defer srv.Close()

	pair := types.TradingPair{Base: "0xAAVE", Quote: "0xUSDC"}
	liq, err := client.GetLiquidity(context.Background(), pair, 50)
	if err != nil {
		t.Fatalf("GetLiquidity failed: %v", err)
	}

	if liq.Exchange != "cow" {
		t.Errorf("expected exchange=cow, got %s", liq.Exchange)
	}
	if liq.SlippageBps != 50 {
		t.Errorf("expected slippageBps=50, got %d", liq.SlippageBps)
	}
	// DepthUSD should be SellAmount - FeeAmount = 9900000000 - 100000000 = 9800000000.
	expectedDepth := decimal.NewFromInt(9800000000)
	if !liq.DepthUSD.Equal(expectedDepth) {
		t.Errorf("expected depthUSD=%s, got %s", expectedDepth, liq.DepthUSD)
	}
	if liq.BestBidPrice.IsZero() {
		t.Error("expected non-zero best bid price")
	}
}

func TestClient_SubmitOrder(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/orders" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		var order CowOrder
		if err := json.NewDecoder(r.Body).Decode(&order); err != nil {
			t.Errorf("failed to decode order: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if order.AppData != "0xTestAppData" {
			t.Errorf("expected appData=0xTestAppData, got %s", order.AppData)
		}
		if order.Kind != "sell" {
			t.Errorf("expected kind=sell, got %s", order.Kind)
		}
		if !order.PartiallyFillable {
			t.Error("expected partiallyFillable=true")
		}
		if order.SellToken != "0xUSDC" {
			t.Errorf("expected sellToken=0xUSDC, got %s", order.SellToken)
		}
		if order.BuyToken != "0xAAVE" {
			t.Errorf("expected buyToken=0xAAVE, got %s", order.BuyToken)
		}

		// Verify sell amount: 1000 USD * 1e6 = 1000000000.
		if order.SellAmount != "1000000000" {
			t.Errorf("expected sellAmount=1000000000, got %s", order.SellAmount)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode("0xOrderUID123")
	})

	client, srv := newTestClient(t, handler)
	defer srv.Close()

	order := &types.Order{
		Pair:      types.TradingPair{Base: "0xAAVE", Quote: "0xUSDC"},
		AmountUSD: decimal.NewFromInt(1000),
		MaxSlipBps: 50,
		OrderType: types.OrderTypeBatchAuction,
	}

	result, err := client.SubmitOrder(context.Background(), order)
	if err != nil {
		t.Fatalf("SubmitOrder failed: %v", err)
	}

	if result.OrderID != "0xOrderUID123" {
		t.Errorf("expected orderID=0xOrderUID123, got %s", result.OrderID)
	}
	if result.Status != types.OrderStatusPending {
		t.Errorf("expected status=pending, got %s", result.Status)
	}
	if result.Exchange != "cow" {
		t.Errorf("expected exchange=cow, got %s", result.Exchange)
	}
}

func TestClient_OrderStatus(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if !strings.HasPrefix(r.URL.Path, "/api/v1/orders/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		resp := CowOrderStatusResponse{
			UID:                "0xOrderUID123",
			Status:             "fulfilled",
			ExecutedSellAmount: "1000000000",
			ExecutedBuyAmount:  "500000000000000000",
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	client, srv := newTestClient(t, handler)
	defer srv.Close()

	status, err := client.OrderStatus(context.Background(), "0xOrderUID123")
	if err != nil {
		t.Fatalf("OrderStatus failed: %v", err)
	}

	if status.OrderID != "0xOrderUID123" {
		t.Errorf("expected orderID=0xOrderUID123, got %s", status.OrderID)
	}
	if status.Status != types.OrderStatusFilled {
		t.Errorf("expected status=filled, got %s", status.Status)
	}
	if status.FilledAmount.IsZero() {
		t.Error("expected non-zero filled amount")
	}
	if status.AvgPrice.IsZero() {
		t.Error("expected non-zero avg price")
	}
}

func TestClient_HTTPErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{
			name:       "rate limited (429)",
			statusCode: http.StatusTooManyRequests,
			body:       `{"errorType":"TooManyRequests","description":"Rate limit exceeded"}`,
		},
		{
			name:       "server error (500)",
			statusCode: http.StatusInternalServerError,
			body:       `{"errorType":"InternalServerError","description":"Something went wrong"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.body))
			})

			client, srv := newTestClient(t, handler)
			defer srv.Close()

			pair := types.TradingPair{Base: "0xAAVE", Quote: "0xUSDC"}
			_, err := client.GetLiquidity(context.Background(), pair, 50)
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			if !strings.Contains(err.Error(), "cow api error") {
				t.Errorf("expected cow api error, got: %v", err)
			}
		})
	}

	// Test timeout handling.
	t.Run("timeout", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(2 * time.Second)
			w.WriteHeader(http.StatusOK)
		})

		client, srv := newTestClient(t, handler)
		defer srv.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		pair := types.TradingPair{Base: "0xAAVE", Quote: "0xUSDC"}
		_, err := client.GetLiquidity(ctx, pair, 50)
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
	})
}
