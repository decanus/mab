package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"github.com/decanus/mab/internal/config"
	"github.com/decanus/mab/internal/engine"
	"github.com/decanus/mab/internal/exchange"
	"github.com/decanus/mab/internal/exchange/cow"
	"github.com/decanus/mab/internal/market/coingecko"
	"github.com/decanus/mab/internal/regime"
	"github.com/decanus/mab/internal/store"
	"github.com/decanus/mab/pkg/types"
)

func main() {
	root := &cobra.Command{
		Use:   "buyback",
		Short: "Market-aware buyback execution engine",
	}

	root.AddCommand(
		newRunCmd(),
		newRegimeCmd(),
		newPlanCmd(),
		newHistoryCmd(),
		newRegimesCmd(),
		newStatsCmd(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// --- run command ---

func newRunCmd() *cobra.Command {
	var (
		cfgPath  string
		dryRun   bool
		interval time.Duration
		dbPath   string
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run buyback execution cycle(s)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cfgPath)
			if err != nil {
				return err
			}
			if dbPath != "" {
				cfg.DBPath = dbPath
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
			defer cancel()

			logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
			eng, cleanup, err := buildEngine(cfg, dryRun, logger)
			if err != nil {
				return err
			}
			defer cleanup()

			if interval > 0 {
				return runContinuous(ctx, eng, interval, logger)
			}

			summary, err := eng.RunCycle(ctx)
			if err != nil {
				return fmt.Errorf("run cycle: %w", err)
			}
			printSummary(summary)
			return nil
		},
	}

	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "path to config file")
	cmd.Flags().BoolVar(&dryRun, "dry-run", true, "log decisions without submitting orders")
	cmd.Flags().DurationVar(&interval, "interval", 0, "continuous mode interval (e.g. 15m)")
	cmd.Flags().StringVar(&dbPath, "db", "", "database path (overrides config)")

	return cmd
}

func runContinuous(ctx context.Context, eng *engine.Engine, interval time.Duration, logger *slog.Logger) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	logger.Info("starting continuous mode", "interval", interval)

	// Run once immediately.
	if summary, err := eng.RunCycle(ctx); err != nil {
		logger.Error("cycle failed", "error", err)
	} else {
		printSummary(summary)
	}

	for {
		select {
		case <-ctx.Done():
			logger.Info("shutting down")
			return nil
		case <-ticker.C:
			summary, err := eng.RunCycle(ctx)
			if err != nil {
				logger.Error("cycle failed", "error", err)
				continue
			}
			printSummary(summary)
		}
	}
}

// --- regime command ---

func newRegimeCmd() *cobra.Command {
	var (
		token       string
		quote       string
		coingeckoID string
	)

	cmd := &cobra.Command{
		Use:   "regime",
		Short: "Classify current market regime for a token",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

			provider := coingecko.NewClient("", coingeckoID)
			pair := types.TradingPair{Base: strings.ToUpper(token), Quote: strings.ToUpper(quote)}

			ohlcv, err := provider.GetOHLCV(ctx, pair, "1d", 30)
			if err != nil {
				return fmt.Errorf("get ohlcv: %w", err)
			}

			classifier := regime.NewClassifier(
				regime.NewPriceTrendScore(1.0),
				regime.NewVolumeTrendScore(0.8),
				regime.NewVolatilityScore(0.7),
				regime.NewDivergenceScore(0.6),
			)

			result, err := classifier.Classify(ctx, ohlcv)
			if err != nil {
				return fmt.Errorf("classify: %w", err)
			}

			logger.Info("[REGIME]",
				"token", token,
				"regime", result.Regime,
				"confidence", fmt.Sprintf("%.2f", result.Confidence),
			)

			fmt.Printf("\nRegime: %s (confidence: %.2f)\n\n", result.Regime, result.Confidence)
			fmt.Println("Signal breakdown:")
			for name, scores := range result.Breakdown {
				fmt.Printf("  %s:\n", name)
				for _, r := range types.AllRegimes() {
					fmt.Printf("    %-15s %.3f\n", r, scores[r])
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&token, "token", "AAVE", "token to classify")
	cmd.Flags().StringVar(&quote, "quote", "USDC", "quote asset")
	cmd.Flags().StringVar(&coingeckoID, "coingecko-id", "", "CoinGecko coin ID (overrides built-in mapping)")

	return cmd
}

// --- plan command ---

func newPlanCmd() *cobra.Command {
	var cfgPath string

	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Show execution plan without executing",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cfgPath)
			if err != nil {
				return err
			}

			ctx := context.Background()
			logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

			// Always dry-run for plan.
			eng, cleanup, err := buildEngine(cfg, true, logger)
			if err != nil {
				return err
			}
			defer cleanup()

			summary, err := eng.RunCycle(ctx)
			if err != nil {
				return fmt.Errorf("plan: %w", err)
			}

			fmt.Println("\n--- Execution Plan ---")
			printSummary(summary)
			return nil
		},
	}

	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "path to config file")

	return cmd
}

// --- history command ---

func newHistoryCmd() *cobra.Command {
	var (
		token string
		since string
		dbP   string
	)

	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show execution history",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			db := dbP
			if db == "" {
				db = "./buyback.db"
			}

			st, err := store.NewSQLiteStore(db)
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer st.Close()

			sinceTime, err := parseDuration(since)
			if err != nil {
				return err
			}

			cycles, err := st.GetCycles(ctx, strings.ToUpper(token), sinceTime)
			if err != nil {
				return fmt.Errorf("get cycles: %w", err)
			}

			if len(cycles) == 0 {
				fmt.Println("No cycles found.")
				return nil
			}

			fmt.Printf("%-20s %-14s %-8s %-14s %-14s %-14s %-8s\n",
				"Timestamp", "Regime", "Conf", "Daily Budget", "Adjusted", "Price", "DryRun")
			fmt.Println(strings.Repeat("-", 100))

			for _, c := range cycles {
				fmt.Printf("%-20s %-14s %-8.2f %-14s %-14s %-14s %-8v\n",
					c.Timestamp.Format("2006-01-02 15:04"),
					c.Regime,
					c.RegimeConfidence,
					c.DailyBudgetUSD.StringFixed(2),
					c.AdjustedBudgetUSD.StringFixed(2),
					c.CurrentPrice.StringFixed(2),
					c.DryRun,
				)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&token, "token", "AAVE", "token to query")
	cmd.Flags().StringVar(&since, "since", "7d", "time window (e.g. 7d, 30d)")
	cmd.Flags().StringVar(&dbP, "db", "", "database path")

	return cmd
}

// --- regimes command ---

func newRegimesCmd() *cobra.Command {
	var (
		token string
		last  int
		dbP   string
	)

	cmd := &cobra.Command{
		Use:   "regimes",
		Short: "Show regime classification history",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			db := dbP
			if db == "" {
				db = "./buyback.db"
			}

			st, err := store.NewSQLiteStore(db)
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer st.Close()

			entries, err := st.GetRegimeHistory(ctx, strings.ToUpper(token), last)
			if err != nil {
				return fmt.Errorf("get regime history: %w", err)
			}

			if len(entries) == 0 {
				fmt.Println("No regime entries found.")
				return nil
			}

			fmt.Printf("%-20s %-14s %-8s %-10s %-10s %-10s %-10s\n",
				"Timestamp", "Regime", "Conf", "PriceTrend", "VolTrend", "Volatility", "Divergence")
			fmt.Println(strings.Repeat("-", 90))

			for _, e := range entries {
				fmt.Printf("%-20s %-14s %-8.2f %-10.3f %-10.3f %-10.3f %-10.3f\n",
					e.Timestamp.Format("2006-01-02 15:04"),
					e.Regime,
					e.Confidence,
					e.PriceTrend,
					e.VolTrend,
					e.Volatility,
					e.Divergence,
				)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&token, "token", "AAVE", "token to query")
	cmd.Flags().IntVar(&last, "last", 30, "number of entries to show")
	cmd.Flags().StringVar(&dbP, "db", "", "database path")

	return cmd
}

// --- stats command ---

func newStatsCmd() *cobra.Command {
	var (
		token string
		since string
		dbP   string
	)

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show fill quality statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			db := dbP
			if db == "" {
				db = "./buyback.db"
			}

			st, err := store.NewSQLiteStore(db)
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer st.Close()

			sinceTime, err := parseDuration(since)
			if err != nil {
				return err
			}

			stats, err := st.GetFillStats(ctx, strings.ToUpper(token), sinceTime)
			if err != nil {
				return fmt.Errorf("get stats: %w", err)
			}

			fmt.Println("Fill Quality Statistics")
			fmt.Println(strings.Repeat("-", 40))
			fmt.Printf("Total Fills:       %d\n", stats.TotalFills)
			fmt.Printf("Total Filled USD:  %s\n", stats.TotalFilledUSD.StringFixed(2))
			fmt.Printf("Avg Slippage (bps): %.2f\n", stats.AvgSlippageBps)
			fmt.Printf("Total MEV Saved:   %s\n", stats.TotalMEVSaved.StringFixed(2))
			fmt.Printf("Avg Fill Rate:     %.2f%%\n", stats.AvgFillRate*100)

			return nil
		},
	}

	cmd.Flags().StringVar(&token, "token", "AAVE", "token to query")
	cmd.Flags().StringVar(&since, "since", "30d", "time window (e.g. 7d, 30d)")
	cmd.Flags().StringVar(&dbP, "db", "", "database path")

	return cmd
}

// --- helpers ---

func loadConfig(path string) (*config.BuybackConfig, error) {
	if path != "" {
		return config.Load(path)
	}
	return config.DefaultConfig(), nil
}

func buildEngine(cfg *config.BuybackConfig, dryRun bool, logger *slog.Logger) (*engine.Engine, func(), error) {
	classifier := regime.NewClassifier(
		regime.NewPriceTrendScore(1.0),
		regime.NewVolumeTrendScore(0.8),
		regime.NewVolatilityScore(0.7),
		regime.NewDivergenceScore(0.6),
	)

	cowClient := cow.NewClient(cfg.CowAPIURL, "buyback-engine", cfg.WalletAddress)
	exchanges := []exchange.Exchange{cowClient}
	provider := coingecko.NewClient("", cfg.CoingeckoID)

	router := exchange.NewRouter(exchanges, cfg.PreferBatchAuction, cfg.ExchangeWeightOverrides)

	var st store.Store
	cleanup := func() {}

	if cfg.DBPath != "" {
		sqliteStore, err := store.NewSQLiteStore(cfg.DBPath)
		if err != nil {
			logger.Warn("failed to open database, continuing without persistence", "error", err, "path", cfg.DBPath)
		} else {
			st = sqliteStore
			cleanup = func() { sqliteStore.Close() }
		}
	}

	eng := engine.NewEngine(cfg, classifier, router, exchanges, provider, st, logger, dryRun)
	return eng, cleanup, nil
}

func printSummary(s *types.CycleSummary) {
	fmt.Println()
	fmt.Printf("Regime:          %s (confidence: %.2f)\n", s.Regime, s.Confidence)
	fmt.Printf("Daily Budget:    %s USD\n", s.DailyBudget.StringFixed(2))
	fmt.Printf("Adjusted Budget: %s USD\n", s.AdjustedBudget.StringFixed(2))
	fmt.Printf("30d VWAP:        %s\n", s.VWAP30d.StringFixed(4))
	fmt.Printf("Target Price:    %s\n", s.TargetPrice.StringFixed(4))
	fmt.Printf("Current Price:   %s\n", s.CurrentPrice.StringFixed(4))
	fmt.Printf("Below Target:    %v\n", s.BelowTarget)

	if len(s.Allocations) > 0 {
		fmt.Println("\nAllocations:")
		for _, a := range s.Allocations {
			fmt.Printf("  %-15s %s USD (%.1f%%) [%s]\n",
				a.Exchange, a.AmountUSD.StringFixed(2), a.Weight*100, a.OrderType)
		}
	}

	if len(s.Fills) > 0 {
		fmt.Println("\nFills:")
		for _, f := range s.Fills {
			fmt.Printf("  %-15s %s USD @ %s (slip: %.1f bps)\n",
				f.Exchange, f.AmountUSD.StringFixed(2), f.AvgPrice.StringFixed(4), f.SlippageBps)
		}
	}

	if s.TotalFilled.IsPositive() {
		fmt.Printf("\nTotal Filled:    %s USD\n", s.TotalFilled.StringFixed(2))
		fmt.Printf("Fill Rate:       %s%%\n", s.FillRate.Mul(decimal.NewFromInt(100)).StringFixed(2))
		fmt.Printf("Avg Slippage:    %.1f bps\n", s.AvgSlippageBps)
	}

	if s.DryRun {
		fmt.Println("\n[DRY-RUN MODE - no orders were submitted]")
	}
	fmt.Println()
}

func parseDuration(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return time.Time{}, fmt.Errorf("invalid duration: %s", s)
	}

	unit := s[len(s)-1]
	numStr := s[:len(s)-1]

	var num int
	if _, err := fmt.Sscanf(numStr, "%d", &num); err != nil {
		return time.Time{}, fmt.Errorf("invalid duration: %s", s)
	}

	var d time.Duration
	switch unit {
	case 'd':
		d = time.Duration(num) * 24 * time.Hour
	case 'h':
		d = time.Duration(num) * time.Hour
	case 'm':
		d = time.Duration(num) * time.Minute
	default:
		return time.Time{}, fmt.Errorf("invalid duration unit: %c (use d, h, or m)", unit)
	}

	return time.Now().Add(-d), nil
}
