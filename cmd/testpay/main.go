// 端到端测试工具：模拟 x402 客户端完成两阶段支付流程
package main

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	serverURL = "http://localhost:3402"
	testModel = "claude-sonnet-4-6"
)

func main() {
	// 生成测试钱包（固定私钥，方便调试）
	privKey, err := crypto.HexToECDSA("4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318")
	must(err, "parse private key")
	signer := crypto.PubkeyToAddress(privKey.PublicKey)
	fmt.Printf("Test wallet: %s\n\n", signer.Hex())

	body := []byte(`{"model":"` + testModel + `","messages":[{"role":"user","content":"Hello, one sentence please."}],"stream":false,"max_tokens":50}`)

	// ── Phase 1: initial request → expect 402 ──────────────────────────────
	fmt.Println("=== Phase 1: Initial request ===")
	resp1, err := http.Post(serverURL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	must(err, "phase1 request")
	defer resp1.Body.Close()

	if resp1.StatusCode != http.StatusPaymentRequired {
		respBody, _ := io.ReadAll(resp1.Body)
		fmt.Printf("Expected 402, got %d: %s\n", resp1.StatusCode, respBody)
		os.Exit(1)
	}

	requestID := resp1.Header.Get("X-PAYMENT-REQUEST-ID")
	fmt.Printf("Status: 402 ✓\nRequestID: %s\n", requestID)

	var challenge struct {
		X402Version int `json:"x402Version"`
		Accepts     []struct {
			Scheme            string            `json:"scheme"`
			Network           string            `json:"network"`
			MaxAmountRequired string            `json:"maxAmountRequired"`
			PayTo             string            `json:"payTo"`
			Asset             string            `json:"asset"`
			MaxTimeoutSeconds int               `json:"maxTimeoutSeconds"`
			Extra             map[string]string `json:"extra"`
		} `json:"accepts"`
	}
	must(json.NewDecoder(resp1.Body).Decode(&challenge), "decode 402")
	req := challenge.Accepts[0]
	fmt.Printf("Amount: %s USDC units (~$%s)\nPayTo: %s\n\n", req.MaxAmountRequired, formatUSDC(req.MaxAmountRequired), req.PayTo)

	// ── Build EIP-712 signed payment ───────────────────────────────────────
	chainID := networkToChainID(req.Network)
	usdc := common.HexToAddress(req.Asset)
	payTo := common.HexToAddress(req.PayTo)
	amount, _ := new(big.Int).SetString(req.MaxAmountRequired, 10)
	validBefore := big.NewInt(time.Now().Unix() + int64(req.MaxTimeoutSeconds))
	nonce := make([]byte, 32)
	copy(nonce, crypto.Keccak256([]byte(fmt.Sprintf("test-nonce-%d", time.Now().UnixNano()))))

	domainName := req.Extra["name"]
	domainVersion := req.Extra["version"]
	if domainName == "" { domainName = "USDC" }
	if domainVersion == "" { domainVersion = "2" }

	sig, err := signEIP712(privKey, signer, payTo, amount, validBefore, nonce, domainName, domainVersion, chainID, usdc)
	must(err, "sign")

	// ── Build X-PAYMENT header ─────────────────────────────────────────────
	type authPayload struct {
		From        string `json:"from"`
		To          string `json:"to"`
		Value       string `json:"value"`
		ValidAfter  string `json:"validAfter"`
		ValidBefore string `json:"validBefore"`
		Nonce       string `json:"nonce"`
	}
	type exactPayload struct {
		Signature     string      `json:"signature"`
		Authorization authPayload `json:"authorization"`
	}
	type payment struct {
		X402Version int          `json:"x402Version"`
		Scheme      string       `json:"scheme"`
		Network     string       `json:"network"`
		Payload     exactPayload `json:"payload"`
	}

	p := payment{
		X402Version: 1,
		Scheme:      "exact",
		Network:     req.Network,
		Payload: exactPayload{
			Signature: "0x" + fmt.Sprintf("%x", sig),
			Authorization: authPayload{
				From:        signer.Hex(),
				To:          req.PayTo,
				Value:       req.MaxAmountRequired,
				ValidAfter:  "0",
				ValidBefore: validBefore.String(),
				Nonce:       "0x" + fmt.Sprintf("%x", nonce),
			},
		},
	}
	payJSON, _ := json.Marshal(p)
	xPayment := base64.StdEncoding.EncodeToString(payJSON)

	// ── Phase 2: retry with payment ───────────────────────────────────────
	fmt.Println("=== Phase 2: Retry with X-PAYMENT ===")
	req2, _ := http.NewRequest("POST", serverURL+"/v1/chat/completions", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-PAYMENT", xPayment)
	req2.Header.Set("X-PAYMENT-REQUEST-ID", requestID)

	resp2, err := http.DefaultClient.Do(req2)
	must(err, "phase2 request")
	defer resp2.Body.Close()
	resp2Body, _ := io.ReadAll(resp2.Body)

	fmt.Printf("Status: %d\n", resp2.StatusCode)
	fmt.Printf("X-Cost: %s\n", resp2.Header.Get("X-Cost"))
	fmt.Printf("X-Tx:   %s\n", resp2.Header.Get("X-Tx"))

	if resp2.StatusCode == 200 {
		var result struct {
			Choices []struct {
				Message struct{ Content string `json:"content"` } `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(resp2Body, &result); err == nil && len(result.Choices) > 0 {
			fmt.Printf("\n✅ LLM Response: %q\n", result.Choices[0].Message.Content)
		} else {
			fmt.Printf("\nBody: %s\n", resp2Body[:min(len(resp2Body), 400)])
		}
	} else {
		fmt.Printf("\n❌ Error: %s\n", resp2Body)
	}
}

func signEIP712(key *ecdsa.PrivateKey, from, to common.Address, value, validBefore *big.Int, nonce []byte, name, version string, chainID int64, usdc common.Address) ([]byte, error) {
	// TypeHash for TransferWithAuthorization
	typeHash := crypto.Keccak256([]byte(
		"TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)",
	))

	// Domain separator
	domainTypeHash := crypto.Keccak256([]byte(
		"EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)",
	))
	domainSep := crypto.Keccak256(encodeABI(
		domainTypeHash,
		crypto.Keccak256([]byte(name)),
		crypto.Keccak256([]byte(version)),
		paddedBig(big.NewInt(chainID)),
		paddedAddr(usdc),
	))

	// Message hash
	var nonce32 [32]byte
	copy(nonce32[:], nonce)
	msgHash := crypto.Keccak256(encodeABI(
		typeHash,
		paddedAddr(from),
		paddedAddr(to),
		paddedBig(value),
		paddedBig(big.NewInt(0)), // validAfter
		paddedBig(validBefore),
		nonce32[:],
	))

	// Final digest: "\x19\x01" || domainSep || msgHash
	digest := crypto.Keccak256(append([]byte{0x19, 0x01}, append(domainSep, msgHash...)...))
	sig, err := crypto.Sign(digest, key)
	if err != nil {
		return nil, err
	}
	// Adjust v (add 27 for Ethereum legacy)
	sig[64] += 27
	return sig, nil
}

func encodeABI(parts ...[]byte) []byte {
	var buf []byte
	for _, p := range parts {
		padded := make([]byte, 32)
		copy(padded[32-len(p):], p)
		buf = append(buf, padded...)
	}
	return buf
}

func paddedBig(n *big.Int) []byte {
	b := n.Bytes()
	padded := make([]byte, 32)
	copy(padded[32-len(b):], b)
	return padded
}

func paddedAddr(a common.Address) []byte {
	padded := make([]byte, 32)
	copy(padded[12:], a.Bytes())
	return padded
}

func networkToChainID(network string) int64 {
	network = strings.TrimPrefix(network, "eip155:")
	var id int64
	fmt.Sscanf(network, "%d", &id)
	return id
}

func formatUSDC(units string) string {
	n, _ := new(big.Float).SetString(units)
	divisor := big.NewFloat(1_000_000)
	result := new(big.Float).Quo(n, divisor)
	f, _ := result.Float64()
	return fmt.Sprintf("%.6f", f)
}

func must(err error, msg string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error %s: %v\n", msg, err)
		os.Exit(1)
	}
}

func min(a, b int) int {
	if a < b { return a }
	return b
}

// 引用 abi 包避免 unused import
var _ = abi.Arguments{}
