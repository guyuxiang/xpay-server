package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xpay/xpay-server/internal/pricing"
)

func TestRecordEnforcesUniqueRequestID(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "xpay.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	p := &Payment{
		FromAddress:      "0x0000000000000000000000000000000000000001",
		ToAddress:        "0x0000000000000000000000000000000000000002",
		Amount:           1,
		TxHash:           "0xabc",
		Model:            "test-model",
		PromptTokens:     1,
		CompletionTokens: 1,
		RequestID:        "req_test",
		Network:          "eip155:84532",
	}
	if err := db.Record(p); err != nil {
		t.Fatalf("Record() first error = %v", err)
	}
	p.TxHash = "0xdef"
	if err := db.Record(p); err == nil {
		t.Fatal("Record() duplicate error = nil")
	}
}

func TestRecentByAddressDefaultLimit(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "xpay.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	recent, err := db.RecentByAddress("0x0000000000000000000000000000000000000001", 0)
	if err != nil {
		t.Fatalf("RecentByAddress() error = %v", err)
	}
	if len(recent) != 0 {
		t.Fatalf("RecentByAddress() len = %d, want 0", len(recent))
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "xpay.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	if _, ok, err := db.GetSetting("markup"); err != nil || ok {
		t.Fatalf("GetSetting() = ok %v err %v, want missing", ok, err)
	}
	if err := db.SetSetting("markup", "1.25"); err != nil {
		t.Fatalf("SetSetting() error = %v", err)
	}
	value, ok, err := db.GetSetting("markup")
	if err != nil {
		t.Fatalf("GetSetting() error = %v", err)
	}
	if !ok || value != "1.25" {
		t.Fatalf("GetSetting() = %q %v, want 1.25 true", value, ok)
	}
}

func TestModelPricesRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "xpay.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	defaults := pricing.DefaultEntries()
	if err := db.BootstrapDefaultPrices(defaults); err != nil {
		t.Fatalf("BootstrapDefaultPrices() error = %v", err)
	}
	if err := db.BootstrapDefaultPrices([]pricing.ModelPriceEntry{{Model: "new-default", Input: "1", Output: "1"}}); err != nil {
		t.Fatalf("BootstrapDefaultPrices() second error = %v", err)
	}
	list, err := db.ListModelPrices()
	if err != nil {
		t.Fatalf("ListModelPrices() error = %v", err)
	}
	if len(list) != len(defaults)+1 {
		t.Fatalf("ListModelPrices() len = %d, want %d", len(list), len(defaults)+1)
	}

	custom := pricing.ModelPriceEntry{Model: "custom", Input: "0.10", CachedInput: "0.01", Output: "0.20"}
	if err := db.UpsertModelPrice(custom); err != nil {
		t.Fatalf("UpsertModelPrice() error = %v", err)
	}
	list, err = db.ListModelPrices()
	if err != nil {
		t.Fatalf("ListModelPrices() after custom error = %v", err)
	}
	var found bool
	for _, entry := range list {
		if entry.Model == "custom" {
			found = true
			if entry.CachedInput != "0.01" {
				t.Fatalf("CachedInput = %q, want 0.01", entry.CachedInput)
			}
		}
	}
	if !found {
		t.Fatal("custom model not found")
	}
	if err := db.DeleteModelPrice("custom"); err != nil {
		t.Fatalf("DeleteModelPrice() error = %v", err)
	}
}

func TestOpenCreatesParentDirectory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "nested", "data", "xpay.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database file was not created: %v", err)
	}
}
