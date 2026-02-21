package store

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/decanus/mab/pkg/types"
)

// Cycle represents a single buyback execution cycle stored in the database.
type Cycle struct {
	ID                int64
	Timestamp         time.Time
	Token             string
	QuoteAsset        string
	Regime            types.MarketRegime
	RegimeConfidence  float64
	DailyBudgetUSD    decimal.Decimal
	AdjustedBudgetUSD decimal.Decimal
	VWAP30d           decimal.Decimal
	TargetPrice       decimal.Decimal
	CurrentPrice      decimal.Decimal
	PriceBelowTarget  bool
	DryRun            bool
}

// StoreOrder represents an order persisted in the database.
type StoreOrder struct {
	ID              int64
	CycleID         int64
	Exchange        string
	OrderIDExternal string
	AmountUSD       decimal.Decimal
	OrderType       string
	SubOrderCount   int
	JitterPct       float64
	Status          string
	CreatedAt       time.Time
}

// StoreFill represents a completed fill persisted in the database.
type StoreFill struct {
	ID              int64
	OrderID         int64
	FilledAmountUSD decimal.Decimal
	AvgPrice        decimal.Decimal
	SlippageBps     float64
	MEVSavedUSD     decimal.Decimal
	FilledAt        time.Time
}

// RegimeEntry represents a historical regime classification entry.
type RegimeEntry struct {
	ID         int64
	Timestamp  time.Time
	Token      string
	Regime     types.MarketRegime
	Confidence float64
	PriceTrend float64
	VolTrend   float64
	Volatility float64
	Divergence float64
}

// FillStats aggregates fill statistics over a time window.
type FillStats struct {
	TotalFills     int
	TotalFilledUSD decimal.Decimal
	AvgSlippageBps float64
	TotalMEVSaved  decimal.Decimal
	AvgFillRate    float64
}

// Store defines the persistence interface for the buyback system.
type Store interface {
	SaveCycle(ctx context.Context, cycle *Cycle) (int64, error)
	GetCycles(ctx context.Context, token string, since time.Time) ([]Cycle, error)

	SaveOrder(ctx context.Context, order *StoreOrder) (int64, error)
	UpdateOrderStatus(ctx context.Context, id int64, status string) error
	GetOrdersByCycle(ctx context.Context, cycleID int64) ([]StoreOrder, error)

	SaveFill(ctx context.Context, fill *StoreFill) (int64, error)
	GetFillStats(ctx context.Context, token string, since time.Time) (*FillStats, error)

	SaveRegime(ctx context.Context, entry *RegimeEntry) error
	GetRegimeHistory(ctx context.Context, token string, limit int) ([]RegimeEntry, error)

	Close() error
}
