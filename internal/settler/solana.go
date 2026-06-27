package settler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"time"

	solanago "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/memo"
	"github.com/gagliardetto/solana-go/programs/token"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/payapi/x402-server/internal/config"
	"github.com/payapi/x402-server/internal/types"
)

type SolanaSettler struct {
	cfg           *config.Config
	client        *rpc.Client
	mint          solanago.PublicKey
	payToWallet   solanago.PublicKey
	payToTokenATA solanago.PublicKey
	commitment    rpc.CommitmentType
}

func NewSolana(c *config.Config) (*SolanaSettler, error) {
	mint, err := solanago.PublicKeyFromBase58(c.USDCAddress)
	if err != nil {
		return nil, fmt.Errorf("solana mint: %w", err)
	}
	payTo, err := solanago.PublicKeyFromBase58(c.PayToAddress)
	if err != nil {
		return nil, fmt.Errorf("solana payTo: %w", err)
	}
	ata, _, err := solanago.FindAssociatedTokenAddress(payTo, mint)
	if err != nil {
		return nil, fmt.Errorf("derive payTo token account: %w", err)
	}
	commitment := rpc.CommitmentConfirmed
	if c.SolanaConfirmation == "finalized" {
		commitment = rpc.CommitmentFinalized
	}
	return &SolanaSettler{
		cfg:           c,
		client:        rpc.New(c.RPCURL),
		mint:          mint,
		payToWallet:   payTo,
		payToTokenATA: ata,
		commitment:    commitment,
	}, nil
}

func (s *SolanaSettler) BuildRequirements(resource, description, reqID string, cost *big.Int, maxTimeoutSeconds int) types.PaymentRequirements {
	return types.PaymentRequirements{
		Scheme:            types.SchemeExact,
		Network:           s.cfg.Network,
		MaxAmountRequired: cost.String(),
		Resource:          resource,
		Description:       description,
		PayTo:             s.payToWallet.String(),
		MaxTimeoutSeconds: maxTimeoutSeconds,
		Asset:             s.mint.String(),
		Extra: map[string]string{
			"requestId":             reqID,
			"decimals":              "6",
			"tokenProgram":          solanago.TokenProgramID.String(),
			"payToTokenAccount":     s.payToTokenATA.String(),
			"settlement":            types.SolanaPayType,
			"memo":                  solanaMemo(reqID),
			"confirmation":          string(s.commitment),
			"addressLookupTables":   "unsupported",
			"clientSubmitSupported": "false",
		},
	}
}

func (s *SolanaSettler) Settle(ctx context.Context, payment types.PaymentPayload, cost *big.Int, reqID string) (Settlement, error) {
	var payload types.ExactSolanaPayload
	if err := json.Unmarshal(payment.Payload, &payload); err != nil {
		return Settlement{}, fmt.Errorf("bad Solana payload: %w", err)
	}
	if payload.Type != types.SolanaPayType {
		return Settlement{}, fmt.Errorf("unsupported Solana payment type %q", payload.Type)
	}
	if payload.RequestID != reqID {
		return Settlement{}, fmt.Errorf("requestId mismatch")
	}
	tx, err := solanago.TransactionFromBase64(payload.Transaction)
	if err != nil {
		return Settlement{}, fmt.Errorf("decode Solana transaction: %w", err)
	}
	payer, err := s.validateTransaction(tx, cost, reqID)
	if err != nil {
		return Settlement{}, err
	}
	if s.cfg.DryRun {
		return Settlement{TxHash: "DRYRUN-" + tx.Signatures[0].String(), Payer: payer.String()}, nil
	}
	sig, err := s.client.SendTransaction(ctx, tx)
	if err != nil {
		return Settlement{}, fmt.Errorf("submit tx: %w", err)
	}
	if err := s.waitConfirmed(ctx, sig); err != nil {
		return Settlement{}, err
	}
	return Settlement{TxHash: sig.String(), Payer: payer.String()}, nil
}

func (s *SolanaSettler) validateTransaction(tx *solanago.Transaction, cost *big.Int, reqID string) (solanago.PublicKey, error) {
	if len(tx.Signatures) == 0 {
		return solanago.PublicKey{}, fmt.Errorf("missing transaction signature")
	}
	if len(tx.Message.AddressTableLookups) != 0 {
		return solanago.PublicKey{}, fmt.Errorf("address lookup tables are not supported")
	}
	if err := tx.VerifySignatures(); err != nil {
		return solanago.PublicKey{}, fmt.Errorf("signature verification failed: %w", err)
	}
	if cost.Sign() <= 0 || !cost.IsUint64() {
		return solanago.PublicKey{}, fmt.Errorf("invalid payment amount")
	}
	if cost.Uint64() > math.MaxInt64 {
		return solanago.PublicKey{}, fmt.Errorf("payment amount exceeds supported range")
	}

	expectedMemo := solanaMemo(reqID)
	var transfer *token.TransferChecked
	var payer solanago.PublicKey
	memoOK := false
	transferCount := 0

	for i := range tx.Message.Instructions {
		ix := &tx.Message.Instructions[i]
		programID, err := tx.ResolveProgramIDIndex(ix.ProgramIDIndex)
		if err != nil {
			return solanago.PublicKey{}, fmt.Errorf("resolve instruction %d program: %w", i, err)
		}
		accounts, err := ix.ResolveInstructionAccounts(&tx.Message)
		if err != nil {
			return solanago.PublicKey{}, fmt.Errorf("resolve instruction %d accounts: %w", i, err)
		}

		switch programID {
		case solanago.ComputeBudget:
			continue
		case solanago.MemoProgramID:
			decoded, err := memo.DecodeInstruction(accounts, ix.Data)
			if err != nil {
				return solanago.PublicKey{}, fmt.Errorf("decode memo instruction: %w", err)
			}
			create, ok := decoded.Impl.(*memo.Create)
			if !ok {
				return solanago.PublicKey{}, fmt.Errorf("unsupported memo instruction")
			}
			if string(create.Message) == expectedMemo {
				memoOK = true
			}
		case solanago.TokenProgramID:
			decoded, err := token.DecodeInstruction(accounts, ix.Data)
			if err != nil {
				return solanago.PublicKey{}, fmt.Errorf("decode token instruction: %w", err)
			}
			tc, ok := decoded.Impl.(*token.TransferChecked)
			if !ok {
				return solanago.PublicKey{}, fmt.Errorf("unsupported token instruction")
			}
			transferCount++
			transfer = tc
			payer = tc.GetOwnerAccount().PublicKey
		default:
			return solanago.PublicKey{}, fmt.Errorf("unsupported Solana program %s", programID.String())
		}
	}

	if !memoOK {
		return solanago.PublicKey{}, fmt.Errorf("missing required memo %q", expectedMemo)
	}
	if transferCount != 1 || transfer == nil {
		return solanago.PublicKey{}, fmt.Errorf("expected exactly one token transferChecked instruction")
	}
	if transfer.Amount == nil || *transfer.Amount != cost.Uint64() {
		return solanago.PublicKey{}, fmt.Errorf("amount mismatch")
	}
	if transfer.Decimals == nil || *transfer.Decimals != 6 {
		return solanago.PublicKey{}, fmt.Errorf("USDC decimals mismatch")
	}
	if transfer.GetMintAccount().PublicKey != s.mint {
		return solanago.PublicKey{}, fmt.Errorf("mint mismatch")
	}
	if transfer.GetDestinationAccount().PublicKey != s.payToTokenATA {
		return solanago.PublicKey{}, fmt.Errorf("destination token account mismatch")
	}
	if payer.IsZero() || !tx.IsSigner(payer) {
		return solanago.PublicKey{}, fmt.Errorf("payer is not a signer")
	}
	return payer, nil
}

func (s *SolanaSettler) waitConfirmed(ctx context.Context, sig solanago.Signature) error {
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()
	for {
		statuses, err := s.client.GetSignatureStatuses(ctx, true, sig)
		if err == nil && len(statuses.Value) > 0 && statuses.Value[0] != nil {
			status := statuses.Value[0]
			if status.Err != nil {
				return fmt.Errorf("transaction failed: %v", status.Err)
			}
			if status.ConfirmationStatus == rpc.ConfirmationStatusFinalized ||
				(s.commitment == rpc.CommitmentConfirmed && status.ConfirmationStatus == rpc.ConfirmationStatusConfirmed) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for confirmation: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func solanaMemo(reqID string) string {
	return "x402:" + reqID
}
