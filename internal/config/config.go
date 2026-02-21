package config

import (
	"fmt"
	"os"

	"github.com/shopspring/decimal"
	"gopkg.in/yaml.v3"

	"github.com/decanus/mab/pkg/types"
)

// BuybackConfig holds all configuration for the market-aware buyback engine.
type BuybackConfig struct {
	// Token is the asset to buy back (e.g., "AAVE").
	Token string `yaml:"token"`
	// CoingeckoID is the CoinGecko coin ID used to fetch market data (e.g. "aave", "ethereum", "celestia").
	// Find your token's ID at https://www.coingecko.com/en/api/documentation
	CoingeckoID string `yaml:"coingecko_id"`
	// QuoteAsset is the asset used to pay (e.g., "USDC").
	QuoteAsset string `yaml:"quote_asset"`
	// AnnualBudgetUSD is the total annual budget in USD for buybacks.
	AnnualBudgetUSD decimal.Decimal `yaml:"annual_budget_usd"`

	// RegimeMultipliers maps each market regime to a spending multiplier range.
	// Values > 1 mean spend more than average; < 1 means spend less.
	RegimeMultipliers map[types.MarketRegime]types.Range `yaml:"regime_multipliers"`

	// BaseDiscount is the target VWAP discount (0.0–1.0). For example, 0.02 means
	// the engine targets buying at 2% below the 30-day VWAP.
	BaseDiscount decimal.Decimal `yaml:"base_discount"`
	// VolScalingFactor controls how much realized volatility widens the discount.
	VolScalingFactor decimal.Decimal `yaml:"vol_scaling_factor"`

	// MaxSlippageBps is the maximum acceptable slippage in basis points.
	MaxSlippageBps int `yaml:"max_slippage_bps"`
	// OrderSplitCount is how many sub-orders each daily execution is split into.
	OrderSplitCount int `yaml:"order_split_count"`
	// ExecutionJitter adds randomness (0.0–1.0) to sub-order timing to reduce
	// predictability and front-running risk.
	ExecutionJitter float64 `yaml:"execution_jitter"`
	// MinExecutionSize is the smallest order size in USD that will be submitted.
	MinExecutionSize decimal.Decimal `yaml:"min_execution_size"`

	// PreferBatchAuction causes the router to prefer batch-auction venues
	// (e.g., CoW Protocol) for MEV protection.
	PreferBatchAuction bool `yaml:"prefer_batch_auction"`
	// ExchangeWeightOverrides lets operators manually bias routing weights
	// toward or away from specific exchanges.
	ExchangeWeightOverrides map[string]float64 `yaml:"exchange_weight_overrides"`

	// DBPath is the file path for the SQLite database used to persist state.
	DBPath string `yaml:"db_path"`
}

// Load reads a YAML configuration file from the given path and returns a
// validated BuybackConfig.
func Load(path string) (*BuybackConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read file: %w", err)
	}

	var cfg BuybackConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal yaml: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	return &cfg, nil
}

// Validate checks that all required fields are set and within acceptable bounds.
func (c *BuybackConfig) Validate() error {
	if c.Token == "" {
		return fmt.Errorf("token must be non-empty")
	}
	if c.CoingeckoID == "" {
		return fmt.Errorf("coingecko_id must be non-empty")
	}
	if c.QuoteAsset == "" {
		return fmt.Errorf("quote_asset must be non-empty")
	}
	if !c.AnnualBudgetUSD.IsPositive() {
		return fmt.Errorf("annual_budget_usd must be positive")
	}

	allRegimes := types.AllRegimes()
	if len(c.RegimeMultipliers) < len(allRegimes) {
		return fmt.Errorf("regime_multipliers must contain all %d regimes", len(allRegimes))
	}
	for _, r := range allRegimes {
		if _, ok := c.RegimeMultipliers[r]; !ok {
			return fmt.Errorf("regime_multipliers missing regime %q", r)
		}
	}

	if c.BaseDiscount.LessThan(decimal.Zero) || c.BaseDiscount.GreaterThan(decimal.NewFromInt(1)) {
		return fmt.Errorf("base_discount must be between 0 and 1")
	}

	if c.MaxSlippageBps <= 0 {
		return fmt.Errorf("max_slippage_bps must be positive")
	}

	if c.OrderSplitCount < 1 {
		return fmt.Errorf("order_split_count must be >= 1")
	}

	return nil
}

// DefaultConfig returns a BuybackConfig populated with sensible default values.
func DefaultConfig() *BuybackConfig {
	return &BuybackConfig{
		Token:           "AAVE",
		CoingeckoID:     "aave",
		QuoteAsset:      "USDC",
		AnnualBudgetUSD: decimal.NewFromInt(50_000_000),

		RegimeMultipliers: map[types.MarketRegime]types.Range{
			types.RegimeAccumulation: {
				Min: decimal.NewFromFloat(1.5),
				Max: decimal.NewFromFloat(2.0),
			},
			types.RegimeMarkup: {
				Min: decimal.NewFromFloat(0.3),
				Max: decimal.NewFromFloat(0.5),
			},
			types.RegimeDistribution: {
				Min: decimal.NewFromFloat(0.0),
				Max: decimal.NewFromFloat(0.2),
			},
			types.RegimeMarkdown: {
				Min: decimal.NewFromFloat(0.8),
				Max: decimal.NewFromFloat(1.0),
			},
		},

		BaseDiscount:     decimal.NewFromFloat(0.02),
		VolScalingFactor: decimal.NewFromFloat(0.1),

		MaxSlippageBps:   200,
		OrderSplitCount:  3,
		ExecutionJitter:  0.3,
		MinExecutionSize: decimal.NewFromInt(1000),

		PreferBatchAuction:      true,
		ExchangeWeightOverrides: map[string]float64{},

		DBPath: "./buyback.db",
	}
}
