package types

import (
	"time"

	"github.com/shopspring/decimal"
)

// MarketRegime represents the current market phase.
type MarketRegime string

const (
	RegimeAccumulation MarketRegime = "accumulation"
	RegimeMarkup       MarketRegime = "markup"
	RegimeDistribution MarketRegime = "distribution"
	RegimeMarkdown     MarketRegime = "markdown"
)

// AllRegimes returns all valid market regimes in a deterministic order.
func AllRegimes() []MarketRegime {
	return []MarketRegime{
		RegimeAccumulation,
		RegimeDistribution,
		RegimeMarkdown,
		RegimeMarkup,
	}
}

// Range represents a min/max range for a value.
type Range struct {
	Min decimal.Decimal `yaml:"min"`
	Max decimal.Decimal `yaml:"max"`
}

// Mid returns the midpoint of the range.
func (r Range) Mid() decimal.Decimal {
	return r.Min.Add(r.Max).Div(decimal.NewFromInt(2))
}

// TradingPair represents a token pair for trading.
type TradingPair struct {
	Base  string // token to buy (e.g., "AAVE")
	Quote string // token to spend (e.g., "USDC")
}

func (tp TradingPair) String() string {
	return tp.Base + "/" + tp.Quote
}

// OHLCV represents a single candlestick data point.
type OHLCV struct {
	Timestamp time.Time
	Open      decimal.Decimal
	High      decimal.Decimal
	Low       decimal.Decimal
	Close     decimal.Decimal
	Volume    decimal.Decimal
}

// LiquidityInfo describes available liquidity at a given slippage tolerance.
type LiquidityInfo struct {
	Exchange     string
	DepthUSD     decimal.Decimal // total available depth in USD
	BestBidPrice decimal.Decimal
	BestAskPrice decimal.Decimal
	SlippageBps  int
}

// Order represents a buy order to be submitted to an exchange.
type Order struct {
	Pair         TradingPair
	AmountUSD    decimal.Decimal
	MaxSlipBps   int
	OrderType    OrderType
	SubOrderIdx  int
	SubOrderTotal int
}

// OrderType categorizes how an order is executed.
type OrderType string

const (
	OrderTypeBatchAuction OrderType = "batch_auction"
	OrderTypeLimit        OrderType = "limit"
	OrderTypeMarket       OrderType = "market"
)

// OrderResult is returned after submitting an order.
type OrderResult struct {
	OrderID  string
	Status   OrderStatus
	Exchange string
}

// OrderStatus tracks the lifecycle of an order.
type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusFilled    OrderStatus = "filled"
	OrderStatusPartial   OrderStatus = "partial"
	OrderStatusCancelled OrderStatus = "cancelled"
	OrderStatusFailed    OrderStatus = "failed"
)

// OrderStatusResult is the full status of an order from an exchange.
type OrderStatusResult struct {
	OrderID      string
	Status       OrderStatus
	FilledAmount decimal.Decimal
	AvgPrice     decimal.Decimal
}

// Fill represents a completed trade fill.
type Fill struct {
	OrderID     string
	Exchange    string
	AmountUSD   decimal.Decimal
	AvgPrice    decimal.Decimal
	SlippageBps float64
	MEVSavedUSD decimal.Decimal
	FilledAt    time.Time
}

// RegimeScores maps each regime to a score from a scoring function.
type RegimeScores map[MarketRegime]float64

// ClassificationResult is the output of the regime classifier.
type ClassificationResult struct {
	Regime     MarketRegime
	Confidence float64                    // 0.0-1.0, spread between top two
	Breakdown  map[string]RegimeScores    // per-ScoreFunc contributions, keyed by Name()
}

// RouteAllocation describes how an order is split across exchanges.
type RouteAllocation struct {
	Exchange   string
	AmountUSD  decimal.Decimal
	Weight     float64
	OrderType  OrderType
	Reason     string
}

// CycleSummary aggregates the result of a single buyback execution cycle.
type CycleSummary struct {
	Regime          MarketRegime
	Confidence      float64
	DailyBudget     decimal.Decimal
	AdjustedBudget  decimal.Decimal
	VWAP30d         decimal.Decimal
	TargetPrice     decimal.Decimal
	CurrentPrice    decimal.Decimal
	BelowTarget     bool
	Allocations     []RouteAllocation
	Fills           []Fill
	TotalFilled     decimal.Decimal
	FillRate        decimal.Decimal
	AvgSlippageBps  float64
	DryRun          bool
}
