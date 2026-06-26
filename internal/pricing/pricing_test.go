package pricing

import (
	"math/big"
	"testing"
)

func TestCostMicroUSDCRoundsUp(t *testing.T) {
	table, err := NewTable(
		ModelPrice{InputPerM: "5", OutputPerM: "15"},
		map[string]ModelPrice{
			"gpt-4o-mini": {InputPerM: "0.15", OutputPerM: "0.60"},
		},
	)
	if err != nil {
		t.Fatalf("NewTable() error = %v", err)
	}

	got := table.CostMicroUSDC("gpt-4o-mini", Usage{PromptTokens: 1, CompletionTokens: 1}, 1)
	if got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("CostMicroUSDC() = %s, want 1", got)
	}
}

func TestCostMicroUSDCUsesLongestPrefix(t *testing.T) {
	table, err := NewTable(
		ModelPrice{InputPerM: "5", OutputPerM: "15"},
		map[string]ModelPrice{
			"claude":          {InputPerM: "10", OutputPerM: "10"},
			"claude-sonnet-4": {InputPerM: "3", OutputPerM: "15"},
		},
	)
	if err != nil {
		t.Fatalf("NewTable() error = %v", err)
	}

	got := table.CostMicroUSDC("claude-sonnet-4-5-20250929", Usage{PromptTokens: 1_000_000}, 1)
	if got.Cmp(big.NewInt(3_000_000)) != 0 {
		t.Fatalf("CostMicroUSDC() = %s, want 3000000", got)
	}
}

func TestCostMicroUSDCFallbackDefault(t *testing.T) {
	table, err := NewTable(ModelPrice{InputPerM: "5", OutputPerM: "15"}, nil)
	if err != nil {
		t.Fatalf("NewTable() error = %v", err)
	}

	got := table.CostMicroUSDC("unknown-model", Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000}, 1)
	if got.Cmp(big.NewInt(20_000_000)) != 0 {
		t.Fatalf("CostMicroUSDC() = %s, want 20000000", got)
	}
}

func TestCostMicroUSDCMarkupRoundsUp(t *testing.T) {
	table, err := NewTable(ModelPrice{InputPerM: "1", OutputPerM: "1"}, nil)
	if err != nil {
		t.Fatalf("NewTable() error = %v", err)
	}

	got := table.CostMicroUSDC("any", Usage{PromptTokens: 1}, 1.5)
	if got.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("CostMicroUSDC() = %s, want 2", got)
	}
}

func TestNewTableFromEntries(t *testing.T) {
	table, err := NewTableFromEntries(DefaultPrice(), []ModelPriceEntry{{
		Model:  "custom-model",
		Input:  "0.25",
		Output: "1.25",
	}})
	if err != nil {
		t.Fatalf("NewTableFromEntries() error = %v", err)
	}
	got := table.CostMicroUSDC("custom-model", Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000}, 1)
	if got.Cmp(big.NewInt(1_500_000)) != 0 {
		t.Fatalf("CostMicroUSDC() = %s, want 1500000", got)
	}
}

func TestNewTableRejectsTooPrecisePrice(t *testing.T) {
	_, err := NewTable(ModelPrice{InputPerM: "0.0000001", OutputPerM: "1"}, nil)
	if err == nil {
		t.Fatal("NewTable() error = nil, want precision error")
	}
}

func TestUSDCUnitsToUSD(t *testing.T) {
	got := USDCUnitsToUSD(big.NewInt(1234567))
	if got != "1.234567" {
		t.Fatalf("USDCUnitsToUSD() = %q", got)
	}
}
