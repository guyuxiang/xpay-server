package settler

import (
	"math/big"
	"testing"

	solanago "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/memo"
	"github.com/gagliardetto/solana-go/programs/token"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/payapi/x402-server/internal/config"
)

func TestSolanaValidateTransaction(t *testing.T) {
	payer := solanago.NewWallet()
	payTo := solanago.NewWallet()
	mint := solanago.NewWallet().PublicKey()
	payerATA, _, err := solanago.FindAssociatedTokenAddress(payer.PublicKey(), mint)
	if err != nil {
		t.Fatal(err)
	}
	payToATA, _, err := solanago.FindAssociatedTokenAddress(payTo.PublicKey(), mint)
	if err != nil {
		t.Fatal(err)
	}
	amount := uint64(12345)
	reqID := "req_test"

	tx, err := solanago.NewTransaction(
		[]solanago.Instruction{
			memo.NewMemoInstruction([]byte(solanaMemo(reqID))).Build(),
			token.NewTransferCheckedInstruction(
				amount,
				6,
				payerATA,
				mint,
				payToATA,
				payer.PublicKey(),
				nil,
			).Build(),
		},
		solanago.HashFromBytes([]byte("12345678901234567890123456789012")),
		solanago.TransactionPayer(payer.PublicKey()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Sign(func(key solanago.PublicKey) *solanago.PrivateKey {
		if key.Equals(payer.PublicKey()) {
			return &payer.PrivateKey
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	s := &SolanaSettler{
		cfg:           &config.Config{},
		mint:          mint,
		payToWallet:   payTo.PublicKey(),
		payToTokenATA: payToATA,
		commitment:    rpc.CommitmentConfirmed,
	}
	gotPayer, err := s.validateTransaction(tx, new(big.Int).SetUint64(amount), reqID)
	if err != nil {
		t.Fatalf("validateTransaction() error = %v", err)
	}
	if gotPayer != payer.PublicKey() {
		t.Fatalf("payer = %s, want %s", gotPayer, payer.PublicKey())
	}
}

func TestSolanaValidateTransactionRejectsWrongMemo(t *testing.T) {
	payer := solanago.NewWallet()
	payTo := solanago.NewWallet()
	mint := solanago.NewWallet().PublicKey()
	payerATA, _, err := solanago.FindAssociatedTokenAddress(payer.PublicKey(), mint)
	if err != nil {
		t.Fatal(err)
	}
	payToATA, _, err := solanago.FindAssociatedTokenAddress(payTo.PublicKey(), mint)
	if err != nil {
		t.Fatal(err)
	}

	tx, err := solanago.NewTransaction(
		[]solanago.Instruction{
			memo.NewMemoInstruction([]byte("x402:other")).Build(),
			token.NewTransferCheckedInstruction(
				1,
				6,
				payerATA,
				mint,
				payToATA,
				payer.PublicKey(),
				nil,
			).Build(),
		},
		solanago.HashFromBytes([]byte("12345678901234567890123456789012")),
		solanago.TransactionPayer(payer.PublicKey()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Sign(func(key solanago.PublicKey) *solanago.PrivateKey {
		if key.Equals(payer.PublicKey()) {
			return &payer.PrivateKey
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	s := &SolanaSettler{
		cfg:           &config.Config{},
		mint:          mint,
		payToWallet:   payTo.PublicKey(),
		payToTokenATA: payToATA,
		commitment:    rpc.CommitmentConfirmed,
	}
	if _, err := s.validateTransaction(tx, big.NewInt(1), "req_test"); err == nil {
		t.Fatal("validateTransaction() error = nil, want memo mismatch")
	}
}
