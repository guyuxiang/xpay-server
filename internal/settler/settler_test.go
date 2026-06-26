package settler

import (
	"testing"

	"github.com/payapi/x402-server/internal/types"
)

func validAuthorizationPayload() types.AuthorizationPayload {
	return types.AuthorizationPayload{
		From:        "0x0000000000000000000000000000000000000001",
		To:          "0x0000000000000000000000000000000000000002",
		Value:       "100",
		ValidAfter:  "0",
		ValidBefore: "9999999999",
		Nonce:       "0x0000000000000000000000000000000000000000000000000000000000000001",
	}
}

func TestDecodeAuthorizationValidatesPositiveValue(t *testing.T) {
	payload := validAuthorizationPayload()
	payload.Value = "0"

	if _, err := DecodeAuthorization(payload); err == nil {
		t.Fatal("DecodeAuthorization() error = nil, want value validation error")
	}
}

func TestDecodeAuthorizationValidatesTimeWindow(t *testing.T) {
	payload := validAuthorizationPayload()
	payload.ValidAfter = "10"
	payload.ValidBefore = "10"

	if _, err := DecodeAuthorization(payload); err == nil {
		t.Fatal("DecodeAuthorization() error = nil, want time window validation error")
	}
}

func TestDecodeAuthorizationValid(t *testing.T) {
	if _, err := DecodeAuthorization(validAuthorizationPayload()); err != nil {
		t.Fatalf("DecodeAuthorization() error = %v", err)
	}
}
