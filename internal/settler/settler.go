// Package settler verifies EIP-712 signatures and submits EIP-3009
// TransferWithAuthorization transactions on-chain.
package settler

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	gethmath "github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/payapi/x402-server/internal/config"
	"github.com/payapi/x402-server/internal/types"
)

const transferWithAuthorizationABI = `[{
  "inputs": [
    {"internalType":"address","name":"from","type":"address"},
    {"internalType":"address","name":"to","type":"address"},
    {"internalType":"uint256","name":"value","type":"uint256"},
    {"internalType":"uint256","name":"validAfter","type":"uint256"},
    {"internalType":"uint256","name":"validBefore","type":"uint256"},
    {"internalType":"bytes32","name":"nonce","type":"bytes32"},
    {"internalType":"uint8","name":"v","type":"uint8"},
    {"internalType":"bytes32","name":"r","type":"bytes32"},
    {"internalType":"bytes32","name":"s","type":"bytes32"}
  ],
  "name":"transferWithAuthorization",
  "outputs":[],
  "stateMutability":"nonpayable",
  "type":"function"
}]`

// Settler submits EIP-3009 authorizations on-chain.
type Settler struct {
	cfg      *config.Config
	client   *ethclient.Client
	contract *bind.BoundContract
	opts     *bind.TransactOpts
}

// New dials the RPC and prepares the gas-wallet transactor.
// In dry-run mode it skips RPC setup; Settle returns a synthetic hash.
func New(c *config.Config) (*Settler, error) {
	if c.DryRun {
		s := &Settler{cfg: c}
		if c.GasPrivKeyHex != "" {
			if key, err := crypto.HexToECDSA(strings.TrimPrefix(c.GasPrivKeyHex, "0x")); err == nil {
				s.opts = &bind.TransactOpts{From: crypto.PubkeyToAddress(key.PublicKey)}
			}
		}
		return s, nil
	}
	client, err := ethclient.Dial(c.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("dial rpc: %w", err)
	}
	key, err := crypto.HexToECDSA(strings.TrimPrefix(c.GasPrivKeyHex, "0x"))
	if err != nil {
		return nil, fmt.Errorf("gas key: %w", err)
	}
	opts, err := bind.NewKeyedTransactorWithChainID(key, big.NewInt(c.ChainID))
	if err != nil {
		return nil, fmt.Errorf("transactor: %w", err)
	}
	parsed, err := abi.JSON(strings.NewReader(transferWithAuthorizationABI))
	if err != nil {
		return nil, fmt.Errorf("parse abi: %w", err)
	}
	usdc := common.HexToAddress(c.USDCAddress)
	contract := bind.NewBoundContract(usdc, parsed, client, client, client)
	return &Settler{cfg: c, client: client, contract: contract, opts: opts}, nil
}

// Settle submits transferWithAuthorization and returns the transaction hash.
func (s *Settler) Settle(ctx context.Context, auth types.TransferAuthorization, signature []byte) (string, error) {
	r, ss, v, err := splitSignature(signature)
	if err != nil {
		return "", err
	}
	if s.cfg.DryRun {
		return "0xDRYRUN" + common.Bytes2Hex(auth.Nonce[:30]), nil
	}
	opts := *s.opts
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	opts.Context = cctx
	tx, err := s.contract.Transact(&opts, "transferWithAuthorization",
		auth.From, auth.To, auth.Value, auth.ValidAfter, auth.ValidBefore, auth.Nonce, v, r, ss)
	if err != nil {
		return "", fmt.Errorf("submit tx: %w", err)
	}
	return tx.Hash().Hex(), nil
}

// GasWalletAddress returns the address that pays gas.
func (s *Settler) GasWalletAddress() common.Address {
	if s.opts == nil {
		return common.Address{}
	}
	return s.opts.From
}

// VerifyEIP712 recovers the signer address from a 65-byte signature and asserts
// it equals auth.From.
func VerifyEIP712(auth types.TransferAuthorization, signature []byte, c *config.Config) (common.Address, error) {
	if len(signature) != 65 {
		return common.Address{}, fmt.Errorf("signature must be 65 bytes, got %d", len(signature))
	}
	digest, err := eip712Digest(auth, c)
	if err != nil {
		return common.Address{}, err
	}
	sig := make([]byte, 65)
	copy(sig, signature)
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	pub, err := crypto.SigToPub(digest, sig)
	if err != nil {
		return common.Address{}, fmt.Errorf("ecrecover: %w", err)
	}
	recovered := crypto.PubkeyToAddress(*pub)
	if recovered != auth.From {
		return common.Address{}, fmt.Errorf("signer %s does not match from %s", recovered.Hex(), auth.From.Hex())
	}
	return recovered, nil
}

// DecodeAuthorization converts a wire AuthorizationPayload into TransferAuthorization.
func DecodeAuthorization(a types.AuthorizationPayload) (types.TransferAuthorization, error) {
	var auth types.TransferAuthorization
	if !common.IsHexAddress(a.From) || !common.IsHexAddress(a.To) {
		return auth, fmt.Errorf("invalid from/to address")
	}
	auth.From = common.HexToAddress(a.From)
	auth.To = common.HexToAddress(a.To)
	var ok bool
	if auth.Value, ok = new(big.Int).SetString(a.Value, 10); !ok {
		return auth, fmt.Errorf("invalid value %q", a.Value)
	}
	if auth.Value.Sign() <= 0 {
		return auth, fmt.Errorf("value must be > 0")
	}
	if auth.ValidAfter, ok = new(big.Int).SetString(a.ValidAfter, 10); !ok {
		return auth, fmt.Errorf("invalid validAfter %q", a.ValidAfter)
	}
	if auth.ValidAfter.Sign() < 0 {
		return auth, fmt.Errorf("validAfter must be >= 0")
	}
	if auth.ValidBefore, ok = new(big.Int).SetString(a.ValidBefore, 10); !ok {
		return auth, fmt.Errorf("invalid validBefore %q", a.ValidBefore)
	}
	if auth.ValidBefore.Sign() <= 0 {
		return auth, fmt.Errorf("validBefore must be > 0")
	}
	if auth.ValidBefore.Cmp(auth.ValidAfter) <= 0 {
		return auth, fmt.Errorf("validBefore must be greater than validAfter")
	}
	nonceBytes, err := hexutil.Decode(a.Nonce)
	if err != nil || len(nonceBytes) != 32 {
		return auth, fmt.Errorf("invalid nonce %q", a.Nonce)
	}
	copy(auth.Nonce[:], nonceBytes)
	return auth, nil
}

func eip712Digest(auth types.TransferAuthorization, c *config.Config) ([]byte, error) {
	usdc := common.HexToAddress(c.USDCAddress)
	td := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": []apitypes.Type{
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			"TransferWithAuthorization": []apitypes.Type{
				{Name: "from", Type: "address"},
				{Name: "to", Type: "address"},
				{Name: "value", Type: "uint256"},
				{Name: "validAfter", Type: "uint256"},
				{Name: "validBefore", Type: "uint256"},
				{Name: "nonce", Type: "bytes32"},
			},
		},
		PrimaryType: "TransferWithAuthorization",
		Domain: apitypes.TypedDataDomain{
			Name:              c.USDCName,
			Version:           c.USDCVersion,
			ChainId:           gethmath.NewHexOrDecimal256(c.ChainID),
			VerifyingContract: usdc.Hex(),
		},
		Message: apitypes.TypedDataMessage{
			"from":        auth.From.Hex(),
			"to":          auth.To.Hex(),
			"value":       (*gethmath.HexOrDecimal256)(auth.Value),
			"validAfter":  (*gethmath.HexOrDecimal256)(auth.ValidAfter),
			"validBefore": (*gethmath.HexOrDecimal256)(auth.ValidBefore),
			"nonce":       hexutil.Encode(auth.Nonce[:]),
		},
	}
	domainSep, err := td.HashStruct("EIP712Domain", td.Domain.Map())
	if err != nil {
		return nil, fmt.Errorf("hash domain: %w", err)
	}
	msgHash, err := td.HashStruct(td.PrimaryType, td.Message)
	if err != nil {
		return nil, fmt.Errorf("hash message: %w", err)
	}
	raw := append([]byte{0x19, 0x01}, domainSep...)
	raw = append(raw, msgHash...)
	return crypto.Keccak256(raw), nil
}

func splitSignature(signature []byte) (r [32]byte, s [32]byte, v uint8, err error) {
	if len(signature) != 65 {
		err = fmt.Errorf("signature must be 65 bytes, got %d", len(signature))
		return
	}
	copy(r[:], signature[0:32])
	copy(s[:], signature[32:64])
	v = signature[64]
	recoveryID := v
	if v < 27 {
		recoveryID = v
		v += 27
	} else {
		recoveryID = v - 27
	}
	if recoveryID > 1 {
		err = fmt.Errorf("invalid signature recovery id %d", recoveryID)
		return
	}
	if !crypto.ValidateSignatureValues(recoveryID, new(big.Int).SetBytes(r[:]), new(big.Int).SetBytes(s[:]), true) {
		err = fmt.Errorf("invalid signature values")
		return
	}
	return
}
