package store

import (
	"context"
	"database/sql"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/shopspring/decimal"

	"github.com/decanus/mab/pkg/types"
)

// SQLiteStore implements Store using a SQLite database.
type SQLiteStore struct {
	db *sql.DB
}

// Compile-time check that SQLiteStore implements Store.
var _ Store = (*SQLiteStore)(nil)

// NewSQLiteStore opens a SQLite database at the given path, runs migrations,
// and returns a ready-to-use store.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}

	if err := runMigrations(db); err != nil {
		db.Close()
		return nil, err
	}

	return &SQLiteStore{db: db}, nil
}

// SaveCycle inserts a new cycle record and returns the generated ID.
func (s *SQLiteStore) SaveCycle(ctx context.Context, cycle *Cycle) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO cycles (timestamp, token, quote_asset, regime, regime_confidence,
			daily_budget_usd, adjusted_budget_usd, vwap_30d, target_price, current_price,
			price_below_target, dry_run)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cycle.Timestamp, cycle.Token, cycle.QuoteAsset, string(cycle.Regime), cycle.RegimeConfidence,
		cycle.DailyBudgetUSD.String(), cycle.AdjustedBudgetUSD.String(),
		cycle.VWAP30d.String(), cycle.TargetPrice.String(), cycle.CurrentPrice.String(),
		cycle.PriceBelowTarget, cycle.DryRun,
	)
	if err != nil {
		return 0, err
	}

	return result.LastInsertId()
}

// GetCycles retrieves cycles for a token since a given time, ordered by timestamp descending.
func (s *SQLiteStore) GetCycles(ctx context.Context, token string, since time.Time) ([]Cycle, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, timestamp, token, quote_asset, regime, regime_confidence,
			daily_budget_usd, adjusted_budget_usd, vwap_30d, target_price, current_price,
			price_below_target, dry_run
		FROM cycles
		WHERE token = ? AND timestamp >= ?
		ORDER BY timestamp DESC`,
		token, since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cycles []Cycle
	for rows.Next() {
		var c Cycle
		var regime string
		var dailyBudget, adjustedBudget, vwap, target, current string

		err := rows.Scan(
			&c.ID, &c.Timestamp, &c.Token, &c.QuoteAsset, &regime, &c.RegimeConfidence,
			&dailyBudget, &adjustedBudget, &vwap, &target, &current,
			&c.PriceBelowTarget, &c.DryRun,
		)
		if err != nil {
			return nil, err
		}

		c.Regime = types.MarketRegime(regime)
		c.DailyBudgetUSD = decimal.RequireFromString(dailyBudget)
		c.AdjustedBudgetUSD = decimal.RequireFromString(adjustedBudget)
		c.VWAP30d = decimal.RequireFromString(vwap)
		c.TargetPrice = decimal.RequireFromString(target)
		c.CurrentPrice = decimal.RequireFromString(current)

		cycles = append(cycles, c)
	}

	return cycles, rows.Err()
}

// SaveOrder inserts a new order record and returns the generated ID.
func (s *SQLiteStore) SaveOrder(ctx context.Context, order *StoreOrder) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO orders (cycle_id, exchange, order_id_external, amount_usd, order_type,
			sub_order_count, jitter_pct, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		order.CycleID, order.Exchange, order.OrderIDExternal,
		order.AmountUSD.String(), order.OrderType,
		order.SubOrderCount, order.JitterPct, order.Status, order.CreatedAt,
	)
	if err != nil {
		return 0, err
	}

	return result.LastInsertId()
}

// UpdateOrderStatus sets the status of an order by its ID.
func (s *SQLiteStore) UpdateOrderStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE orders SET status = ? WHERE id = ?`,
		status, id,
	)
	return err
}

// GetOrdersByCycle retrieves all orders for a given cycle, ordered by creation time.
func (s *SQLiteStore) GetOrdersByCycle(ctx context.Context, cycleID int64) ([]StoreOrder, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, cycle_id, exchange, order_id_external, amount_usd, order_type,
			sub_order_count, jitter_pct, status, created_at
		FROM orders
		WHERE cycle_id = ?
		ORDER BY created_at`,
		cycleID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []StoreOrder
	for rows.Next() {
		var o StoreOrder
		var amountUSD string
		var orderIDExternal sql.NullString

		err := rows.Scan(
			&o.ID, &o.CycleID, &o.Exchange, &orderIDExternal, &amountUSD, &o.OrderType,
			&o.SubOrderCount, &o.JitterPct, &o.Status, &o.CreatedAt,
		)
		if err != nil {
			return nil, err
		}

		if orderIDExternal.Valid {
			o.OrderIDExternal = orderIDExternal.String
		}
		o.AmountUSD = decimal.RequireFromString(amountUSD)

		orders = append(orders, o)
	}

	return orders, rows.Err()
}

// SaveFill inserts a new fill record and returns the generated ID.
func (s *SQLiteStore) SaveFill(ctx context.Context, fill *StoreFill) (int64, error) {
	var mevSaved *string
	if !fill.MEVSavedUSD.IsZero() {
		v := fill.MEVSavedUSD.String()
		mevSaved = &v
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO fills (order_id, filled_amount_usd, avg_price, slippage_bps, mev_saved_usd, filled_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		fill.OrderID, fill.FilledAmountUSD.String(), fill.AvgPrice.String(),
		fill.SlippageBps, mevSaved, fill.FilledAt,
	)
	if err != nil {
		return 0, err
	}

	return result.LastInsertId()
}

// GetFillStats computes aggregate fill statistics for a token since a given time.
func (s *SQLiteStore) GetFillStats(ctx context.Context, token string, since time.Time) (*FillStats, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT
			COUNT(*),
			COALESCE(SUM(CAST(f.filled_amount_usd AS REAL)), 0),
			COALESCE(AVG(f.slippage_bps), 0),
			COALESCE(SUM(CAST(COALESCE(f.mev_saved_usd, '0') AS REAL)), 0),
			COALESCE(SUM(CAST(f.filled_amount_usd AS REAL)), 0),
			COALESCE(SUM(CAST(o.amount_usd AS REAL)), 0)
		FROM fills f
		JOIN orders o ON f.order_id = o.id
		JOIN cycles c ON o.cycle_id = c.id
		WHERE c.token = ? AND f.filled_at >= ?`,
		token, since,
	)

	var totalFills int
	var totalFilledRaw, avgSlippage, totalMEVRaw float64
	var totalFilledSum, totalOrderedSum float64

	err := row.Scan(&totalFills, &totalFilledRaw, &avgSlippage, &totalMEVRaw, &totalFilledSum, &totalOrderedSum)
	if err != nil {
		return nil, err
	}

	var avgFillRate float64
	if totalOrderedSum > 0 {
		avgFillRate = totalFilledSum / totalOrderedSum
	}

	// Re-read precise decimal values with a separate query when there are fills.
	stats := &FillStats{
		TotalFills:     totalFills,
		TotalFilledUSD: decimal.Zero,
		AvgSlippageBps: avgSlippage,
		TotalMEVSaved:  decimal.Zero,
		AvgFillRate:    avgFillRate,
	}

	if totalFills == 0 {
		return stats, nil
	}

	// Compute precise decimal sums by iterating rows.
	rows, err := s.db.QueryContext(ctx,
		`SELECT f.filled_amount_usd, COALESCE(f.mev_saved_usd, '0')
		FROM fills f
		JOIN orders o ON f.order_id = o.id
		JOIN cycles c ON o.cycle_id = c.id
		WHERE c.token = ? AND f.filled_at >= ?`,
		token, since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	totalFilled := decimal.Zero
	totalMEV := decimal.Zero

	for rows.Next() {
		var filledStr, mevStr string
		if err := rows.Scan(&filledStr, &mevStr); err != nil {
			return nil, err
		}
		totalFilled = totalFilled.Add(decimal.RequireFromString(filledStr))
		totalMEV = totalMEV.Add(decimal.RequireFromString(mevStr))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	stats.TotalFilledUSD = totalFilled
	stats.TotalMEVSaved = totalMEV

	return stats, nil
}

// SaveRegime inserts a regime classification entry.
func (s *SQLiteStore) SaveRegime(ctx context.Context, entry *RegimeEntry) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO regime_history (timestamp, token, regime, confidence, price_trend, vol_trend, volatility, divergence)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.Timestamp, entry.Token, string(entry.Regime), entry.Confidence,
		entry.PriceTrend, entry.VolTrend, entry.Volatility, entry.Divergence,
	)
	return err
}

// GetRegimeHistory retrieves the most recent regime entries for a token.
func (s *SQLiteStore) GetRegimeHistory(ctx context.Context, token string, limit int) ([]RegimeEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, timestamp, token, regime, confidence, price_trend, vol_trend, volatility, divergence
		FROM regime_history
		WHERE token = ?
		ORDER BY timestamp DESC
		LIMIT ?`,
		token, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []RegimeEntry
	for rows.Next() {
		var e RegimeEntry
		var regime string

		err := rows.Scan(
			&e.ID, &e.Timestamp, &e.Token, &regime, &e.Confidence,
			&e.PriceTrend, &e.VolTrend, &e.Volatility, &e.Divergence,
		)
		if err != nil {
			return nil, err
		}

		e.Regime = types.MarketRegime(regime)
		entries = append(entries, e)
	}

	return entries, rows.Err()
}

// Close closes the underlying database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
