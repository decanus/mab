package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/decanus/mab/pkg/types"
)

func TestLoadExampleYAML(t *testing.T) {
	// Resolve from the project root.
	path := filepath.Join("..", "..", "configs", "example.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q): %v", path, err)
	}

	if cfg.Token != "AAVE" {
		t.Errorf("Token = %q, want %q", cfg.Token, "AAVE")
	}
	if cfg.QuoteAsset != "USDC" {
		t.Errorf("QuoteAsset = %q, want %q", cfg.QuoteAsset, "USDC")
	}
	if !cfg.AnnualBudgetUSD.Equal(decimal.NewFromInt(50_000_000)) {
		t.Errorf("AnnualBudgetUSD = %s, want 50000000", cfg.AnnualBudgetUSD)
	}
	if !cfg.BaseDiscount.Equal(decimal.NewFromFloat(0.02)) {
		t.Errorf("BaseDiscount = %s, want 0.02", cfg.BaseDiscount)
	}
	if cfg.MaxSlippageBps != 200 {
		t.Errorf("MaxSlippageBps = %d, want 200", cfg.MaxSlippageBps)
	}
	if cfg.OrderSplitCount != 3 {
		t.Errorf("OrderSplitCount = %d, want 3", cfg.OrderSplitCount)
	}
	if !cfg.PreferBatchAuction {
		t.Error("PreferBatchAuction = false, want true")
	}

	// Check all regimes are present.
	for _, r := range types.AllRegimes() {
		if _, ok := cfg.RegimeMultipliers[r]; !ok {
			t.Errorf("missing regime multiplier for %q", r)
		}
	}

	accum := cfg.RegimeMultipliers[types.RegimeAccumulation]
	if !accum.Min.Equal(decimal.NewFromFloat(1.5)) || !accum.Max.Equal(decimal.NewFromFloat(2.0)) {
		t.Errorf("accumulation range = [%s, %s], want [1.5, 2.0]", accum.Min, accum.Max)
	}
}

func TestDefaultConfigValidates(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("DefaultConfig().Validate(): %v", err)
	}
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(c *BuybackConfig)
	}{
		{
			name:   "empty token",
			mutate: func(c *BuybackConfig) { c.Token = "" },
		},
		{
			name:   "empty coingecko_id",
			mutate: func(c *BuybackConfig) { c.CoingeckoID = "" },
		},
		{
			name:   "empty quote asset",
			mutate: func(c *BuybackConfig) { c.QuoteAsset = "" },
		},
		{
			name:   "zero budget",
			mutate: func(c *BuybackConfig) { c.AnnualBudgetUSD = decimal.Zero },
		},
		{
			name:   "negative budget",
			mutate: func(c *BuybackConfig) { c.AnnualBudgetUSD = decimal.NewFromInt(-1) },
		},
		{
			name: "missing regime",
			mutate: func(c *BuybackConfig) {
				delete(c.RegimeMultipliers, types.RegimeAccumulation)
			},
		},
		{
			name:   "base discount too high",
			mutate: func(c *BuybackConfig) { c.BaseDiscount = decimal.NewFromFloat(1.5) },
		},
		{
			name:   "negative base discount",
			mutate: func(c *BuybackConfig) { c.BaseDiscount = decimal.NewFromFloat(-0.1) },
		},
		{
			name:   "zero slippage",
			mutate: func(c *BuybackConfig) { c.MaxSlippageBps = 0 },
		},
		{
			name:   "zero split count",
			mutate: func(c *BuybackConfig) { c.OrderSplitCount = 0 },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.mutate(cfg)
			if err := cfg.Validate(); err == nil {
				t.Error("expected validation error, got nil")
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/tmp/nonexistent_config_file_12345.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(tmp, []byte("{{{{invalid yaml"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(tmp)
	if err == nil {
		t.Fatal("expected error for invalid yaml")
	}
}

func TestLoadValidationFailure(t *testing.T) {
	// Valid YAML but missing required fields.
	tmp := filepath.Join(t.TempDir(), "empty.yaml")
	if err := os.WriteFile(tmp, []byte("db_path: test.db\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(tmp)
	if err == nil {
		t.Fatal("expected validation error for incomplete config")
	}
}
