// Package types defines the x402 wire format and EIP-3009 structs.
// Wire format follows Coinbase x402 v1 "exact" EVM scheme.
package types

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// TransferAuthorization mirrors the EIP-3009 TransferWithAuthorization message.
// Amounts are in USDC base units (6 decimals).
type TransferAuthorization struct {
	From        common.Address
	To          common.Address
	Value       *big.Int
	ValidAfter  *big.Int
	ValidBefore *big.Int
	Nonce       [32]byte
}

// ---- x402 wire format ----

// PaymentRequirements is one entry in the 402 challenge's `accepts` array.
type PaymentRequirements struct {
	Scheme            string            `json:"scheme"`
	Network           string            `json:"network"`
	MaxAmountRequired string            `json:"maxAmountRequired"`
	Resource          string            `json:"resource"`
	Description       string            `json:"description,omitempty"`
	MimeType          string            `json:"mimeType,omitempty"`
	PayTo             string            `json:"payTo"`
	MaxTimeoutSeconds int               `json:"maxTimeoutSeconds"`
	Asset             string            `json:"asset"`
	Extra             map[string]string `json:"extra,omitempty"`
}

// PaymentRequiredResponse is the body of the HTTP 402 challenge.
type PaymentRequiredResponse struct {
	X402Version int                   `json:"x402Version"`
	Accepts     []PaymentRequirements `json:"accepts"`
	Error       string                `json:"error,omitempty"`
}

// AuthorizationPayload is the EIP-3009 authorization carried in X-PAYMENT.
// All numeric fields are decimal strings; addresses and nonce are 0x-hex.
type AuthorizationPayload struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Value       string `json:"value"`
	ValidAfter  string `json:"validAfter"`
	ValidBefore string `json:"validBefore"`
	Nonce       string `json:"nonce"` // 0x + 64 hex chars
}

// ExactEvmPayload is the inner payload for the exact/EVM scheme.
type ExactEvmPayload struct {
	Signature     string               `json:"signature"` // 0x + 130 hex chars
	Authorization AuthorizationPayload `json:"authorization"`
}

// PaymentPayload is the base64-decoded content of the X-PAYMENT request header.
type PaymentPayload struct {
	X402Version int             `json:"x402Version"`
	Scheme      string          `json:"scheme"`
	Network     string          `json:"network"`
	Payload     ExactEvmPayload `json:"payload"`
}

// SettlementResponse is the base64-encoded content of X-PAYMENT-RESPONSE header.
type SettlementResponse struct {
	Success     bool   `json:"success"`
	Transaction string `json:"transaction"`
	Network     string `json:"network"`
	Payer       string `json:"payer"`
}

const (
	X402Version    = 1
	SchemeExact    = "exact"
	HeaderPayment  = "X-PAYMENT"
	HeaderRequest  = "X-PAYMENT-REQUEST-ID"
	HeaderResponse = "X-PAYMENT-RESPONSE"
	HeaderCost     = "X-Cost"
	HeaderTx       = "X-Tx"
)
