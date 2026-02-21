package store

import "database/sql"

func runMigrations(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS cycles (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			token TEXT NOT NULL,
			quote_asset TEXT NOT NULL,
			regime TEXT NOT NULL,
			regime_confidence REAL NOT NULL,
			daily_budget_usd TEXT NOT NULL,
			adjusted_budget_usd TEXT NOT NULL,
			vwap_30d TEXT NOT NULL,
			target_price TEXT NOT NULL,
			current_price TEXT NOT NULL,
			price_below_target BOOLEAN NOT NULL,
			dry_run BOOLEAN NOT NULL DEFAULT 1
		)`,
		`CREATE TABLE IF NOT EXISTS orders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			cycle_id INTEGER NOT NULL REFERENCES cycles(id),
			exchange TEXT NOT NULL,
			order_id_external TEXT,
			amount_usd TEXT NOT NULL,
			order_type TEXT NOT NULL,
			sub_order_count INTEGER NOT NULL,
			jitter_pct REAL NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS fills (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			order_id INTEGER NOT NULL REFERENCES orders(id),
			filled_amount_usd TEXT NOT NULL,
			avg_price TEXT NOT NULL,
			slippage_bps REAL NOT NULL,
			mev_saved_usd TEXT,
			filled_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS regime_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			token TEXT NOT NULL,
			regime TEXT NOT NULL,
			confidence REAL NOT NULL,
			price_trend REAL NOT NULL,
			vol_trend REAL NOT NULL,
			volatility REAL NOT NULL,
			divergence REAL NOT NULL
		)`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}

	return nil
}
