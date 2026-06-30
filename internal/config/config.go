package config

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gagliardetto/solana-go"
)

type Config struct {
	Port                string
	UpstreamURL         string
	BaseURL             string
	UpstreamToken       string
	PaymentChain        string // "evm" or "solana"
	PayToAddress        string
	GasPrivKeyHex       string
	ChainID             int64
	Network             string // CAIP-2, e.g. "eip155:84532"
	RPCURL              string
	USDCAddress         string
	USDCName            string
	USDCVersion         string
	SolanaCluster       string
	SolanaConfirmation  string
	DBPath              string
	AdminToken          string
	CacheTTLSecs        int
	SigTimeoutSecs      int
	UpstreamTimeoutSecs int
	ShutdownTimeoutSecs int
	MaxBodyBytes        int64
	AllowedOrigins      []string
	Markup              float64
	DryRun              bool
}

// Known USDC deployments per chain.
var usdcByChain = map[int64]string{
	8453:  "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
	84532: "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
}

// USDC EIP-712 domain names per chain (verified against deployed contracts).
var usdcDomainNameByChain = map[int64]string{
	8453:  "USD Coin",
	84532: "USDC",
}

// rpcByChain provides sensible defaults.
var rpcByChain = map[int64]string{
	8453:  "https://mainnet.base.org",
	84532: "https://sepolia.base.org",
}

var solanaRPCByCluster = map[string]string{
	"mainnet-beta": "https://api.mainnet-beta.solana.com",
	"devnet":       "https://api.devnet.solana.com",
}

func Load() (*Config, error) {
	paymentChain := strings.ToLower(envStr("PAYMENT_CHAIN", "evm"))
	chainID, err := envInt64("CHAIN_ID", 84532)
	if err != nil {
		return nil, err
	}
	dryRun, err := envBool("DRY_RUN", false)
	if err != nil {
		return nil, err
	}
	gasKey := envStr("GAS_PRIVATE_KEY", "")

	usdcAddr := envStr("USDC_ADDRESS", "")
	if paymentChain == "evm" && usdcAddr == "" {
		if addr, ok := usdcByChain[chainID]; ok {
			usdcAddr = addr
		}
	}

	usdcName := usdcDomainNameByChain[chainID]
	if usdcName == "" {
		usdcName = "USD Coin"
	}
	usdcName = envStr("USDC_NAME", usdcName)

	rpcDefault := rpcByChain[chainID]
	if rpcDefault == "" {
		rpcDefault = "https://sepolia.base.org"
	}
	solanaCluster := strings.ToLower(envStr("SOLANA_CLUSTER", "devnet"))
	if paymentChain == "solana" {
		rpcDefault = solanaRPCByCluster[solanaCluster]
	}

	cacheTTL, err := envInt("CACHE_TTL_SECS", 300)
	if err != nil {
		return nil, err
	}
	sigTimeout, err := envInt("SIG_TIMEOUT_SECS", 120)
	if err != nil {
		return nil, err
	}
	upstreamTimeout, err := envInt("UPSTREAM_TIMEOUT_SECS", 300)
	if err != nil {
		return nil, err
	}
	shutdownTimeout, err := envInt("SHUTDOWN_TIMEOUT_SECS", 10)
	if err != nil {
		return nil, err
	}
	maxBodyBytes, err := envInt64("MAX_BODY_BYTES", 4<<20)
	if err != nil {
		return nil, err
	}
	markup, err := envFloat("MARKUP", 1.0)
	if err != nil {
		return nil, err
	}

	c := &Config{
		Port:                envStr("PORT", "3402"),
		UpstreamURL:         strings.TrimRight(envStr("UPSTREAM_URL", ""), "/"),
		BaseURL:             normalizeBaseURL(envStr("BASE_URL", "")),
		UpstreamToken:       envStr("UPSTREAM_TOKEN", ""),
		PaymentChain:        paymentChain,
		PayToAddress:        envStr("PAY_TO_ADDRESS", ""),
		GasPrivKeyHex:       gasKey,
		ChainID:             chainID,
		Network:             paymentNetwork(paymentChain, chainID, solanaCluster),
		RPCURL:              envStr("RPC_URL", rpcDefault),
		USDCAddress:         usdcAddr,
		USDCName:            usdcName,
		USDCVersion:         envStr("USDC_VERSION", "2"),
		SolanaCluster:       solanaCluster,
		SolanaConfirmation:  envStr("SOLANA_CONFIRMATION", "confirmed"),
		DBPath:              envStr("DB_PATH", "./xpay.db"),
		AdminToken:          envStr("ADMIN_TOKEN", ""),
		CacheTTLSecs:        cacheTTL,
		SigTimeoutSecs:      sigTimeout,
		UpstreamTimeoutSecs: upstreamTimeout,
		ShutdownTimeoutSecs: shutdownTimeout,
		MaxBodyBytes:        maxBodyBytes,
		AllowedOrigins:      envCSV("ALLOWED_ORIGINS", "*"),
		Markup:              markup,
		DryRun:              dryRun,
	}

	if c.UpstreamURL == "" {
		return nil, fmt.Errorf("UPSTREAM_URL is required")
	}
	if c.UpstreamToken == "" {
		return nil, fmt.Errorf("UPSTREAM_TOKEN is required")
	}
	if c.PaymentChain != "evm" && c.PaymentChain != "solana" {
		return nil, fmt.Errorf("PAYMENT_CHAIN must be evm or solana")
	}
	if c.PayToAddress == "" {
		return nil, fmt.Errorf("PAY_TO_ADDRESS is required")
	}
	if c.PaymentChain == "evm" {
		if !common.IsHexAddress(c.PayToAddress) {
			return nil, fmt.Errorf("invalid PAY_TO_ADDRESS")
		}
		if usdcAddr == "" {
			return nil, fmt.Errorf("USDC_ADDRESS is required (no default for chain %d)", chainID)
		}
		if !common.IsHexAddress(usdcAddr) {
			return nil, fmt.Errorf("invalid USDC_ADDRESS")
		}
		if _, err := hexutil.Decode(ensure0x(usdcAddr)); err != nil {
			return nil, fmt.Errorf("invalid USDC_ADDRESS: %w", err)
		}
	} else {
		if _, err := solana.PublicKeyFromBase58(c.PayToAddress); err != nil {
			return nil, fmt.Errorf("invalid PAY_TO_ADDRESS")
		}
		if usdcAddr == "" {
			return nil, fmt.Errorf("USDC_ADDRESS is required for Solana")
		}
		if _, err := solana.PublicKeyFromBase58(usdcAddr); err != nil {
			return nil, fmt.Errorf("invalid USDC_ADDRESS")
		}
		if c.RPCURL == "" {
			return nil, fmt.Errorf("RPC_URL is required for Solana cluster %q", solanaCluster)
		}
		if c.SolanaConfirmation != "confirmed" && c.SolanaConfirmation != "finalized" {
			return nil, fmt.Errorf("SOLANA_CONFIRMATION must be confirmed or finalized")
		}
	}
	if cacheTTL <= 0 {
		return nil, fmt.Errorf("CACHE_TTL_SECS must be > 0")
	}
	if sigTimeout <= 0 {
		return nil, fmt.Errorf("SIG_TIMEOUT_SECS must be > 0")
	}
	if upstreamTimeout <= 0 {
		return nil, fmt.Errorf("UPSTREAM_TIMEOUT_SECS must be > 0")
	}
	if shutdownTimeout <= 0 {
		return nil, fmt.Errorf("SHUTDOWN_TIMEOUT_SECS must be > 0")
	}
	if maxBodyBytes <= 0 {
		return nil, fmt.Errorf("MAX_BODY_BYTES must be > 0")
	}
	if markup <= 0 {
		return nil, fmt.Errorf("MARKUP must be > 0")
	}
	if math.IsNaN(markup) || math.IsInf(markup, 0) {
		return nil, fmt.Errorf("MARKUP must be finite")
	}
	if !dryRun && c.PaymentChain == "evm" {
		if gasKey == "" {
			return nil, fmt.Errorf("GAS_PRIVATE_KEY is required (or set DRY_RUN=true)")
		}
		if _, err := crypto.HexToECDSA(strings.TrimPrefix(gasKey, "0x")); err != nil {
			return nil, fmt.Errorf("invalid GAS_PRIVATE_KEY: %w", err)
		}
	}
	return c, nil
}

func normalizeBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "/" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.IsAbs() {
		raw = u.EscapedPath()
	}
	raw = "/" + strings.Trim(raw, "/")
	return strings.TrimRight(raw, "/")
}

func paymentNetwork(paymentChain string, chainID int64, solanaCluster string) string {
	if paymentChain == "solana" {
		return "solana:" + solanaCluster
	}
	return fmt.Sprintf("eip155:%d", chainID)
}

func ensure0x(s string) string {
	if !strings.HasPrefix(s, "0x") {
		return "0x" + s
	}
	return s
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) (int, error) {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer: %w", key, err)
		}
		return n, nil
	}
	return def, nil
}

func envInt64(key string, def int64) (int64, error) {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer: %w", key, err)
		}
		return n, nil
	}
	return def, nil
}

func envBool(key string, def bool) (bool, error) {
	if v := os.Getenv(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return false, fmt.Errorf("%s must be a boolean: %w", key, err)
		}
		return b, nil
	}
	return def, nil
}

func envFloat(key string, def float64) (float64, error) {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, fmt.Errorf("%s must be a number: %w", key, err)
		}
		return n, nil
	}
	return def, nil
}

func envCSV(key, def string) []string {
	raw := envStr(key, def)
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
