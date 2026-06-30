package config

import "testing"

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("UPSTREAM_URL", "http://localhost:3001")
	t.Setenv("UPSTREAM_TOKEN", "test-token")
	t.Setenv("PAY_TO_ADDRESS", "0x0000000000000000000000000000000000000001")
	t.Setenv("USDC_ADDRESS", "0x0000000000000000000000000000000000000002")
	t.Setenv("CHAIN_ID", "84532")
	t.Setenv("CACHE_TTL_SECS", "300")
	t.Setenv("SIG_TIMEOUT_SECS", "120")
	t.Setenv("UPSTREAM_TIMEOUT_SECS", "300")
	t.Setenv("SHUTDOWN_TIMEOUT_SECS", "10")
	t.Setenv("MAX_BODY_BYTES", "4194304")
	t.Setenv("MARKUP", "1.0")
	t.Setenv("ALLOWED_ORIGINS", "*")
	t.Setenv("DRY_RUN", "true")
}

func TestLoadValidDryRunConfig(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("CHAIN_ID", "84532")
	t.Setenv("CACHE_TTL_SECS", "60")
	t.Setenv("SIG_TIMEOUT_SECS", "90")
	t.Setenv("UPSTREAM_TIMEOUT_SECS", "10")
	t.Setenv("SHUTDOWN_TIMEOUT_SECS", "5")
	t.Setenv("MAX_BODY_BYTES", "1024")
	t.Setenv("MARKUP", "1.25")
	t.Setenv("ALLOWED_ORIGINS", "https://app.example, https://admin.example")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Network != "eip155:84532" {
		t.Fatalf("Network = %q", cfg.Network)
	}
	if cfg.MaxBodyBytes != 1024 {
		t.Fatalf("MaxBodyBytes = %d", cfg.MaxBodyBytes)
	}
	if len(cfg.AllowedOrigins) != 2 {
		t.Fatalf("AllowedOrigins = %#v", cfg.AllowedOrigins)
	}
}

func TestLoadRejectsInvalidIntegerEnv(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("CACHE_TTL_SECS", "not-an-int")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid integer error")
	}
}

func TestLoadRejectsInvalidPayToAddress(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("PAY_TO_ADDRESS", "0x1234")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid address error")
	}
}

func TestLoadRejectsInfiniteMarkup(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("MARKUP", "+Inf")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want finite markup error")
	}
}

func TestLoadValidSolanaConfig(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("PAYMENT_CHAIN", "solana")
	t.Setenv("PAY_TO_ADDRESS", "11111111111111111111111111111111")
	t.Setenv("USDC_ADDRESS", "So11111111111111111111111111111111111111112")
	t.Setenv("SOLANA_CLUSTER", "devnet")
	t.Setenv("SOLANA_CONFIRMATION", "finalized")
	t.Setenv("GAS_PRIVATE_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.PaymentChain != "solana" {
		t.Fatalf("PaymentChain = %q", cfg.PaymentChain)
	}
	if cfg.Network != "solana:devnet" {
		t.Fatalf("Network = %q", cfg.Network)
	}
	if cfg.RPCURL != "https://api.devnet.solana.com" {
		t.Fatalf("RPCURL = %q", cfg.RPCURL)
	}
}

func TestLoadNormalizesBaseURL(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("BASE_URL", "https://www.openshort.cloud/payapi/")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.BaseURL != "/payapi" {
		t.Fatalf("BaseURL = %q, want /payapi", cfg.BaseURL)
	}
}
