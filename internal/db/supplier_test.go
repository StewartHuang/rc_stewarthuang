package db

import (
	"os"
	"testing"
	"time"

	"rc_stewarthuang/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	f, err := os.CreateTemp("", "delivery-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	s, err := NewStore(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Close()
		os.Remove(f.Name())
	})
	return s
}

func TestCreateSupplier(t *testing.T) {
	s := newTestStore(t)
	sup := &model.Supplier{
		Name:             "test-supplier",
		URL:              "https://example.com/api",
		Method:           "POST",
		Headers:          `{"Content-Type": "application/json"}`,
		RetryMaxAttempts: 10,
		RetryBaseDelayMs: 1000,
		RetryMaxDelayMs:  60000,
		Enabled:          true,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
		UpdatedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	err := s.CreateSupplier(sup)
	if err != nil {
		t.Fatalf("CreateSupplier failed: %v", err)
	}
	if sup.ID == 0 {
		t.Fatal("expected non-zero ID after creation")
	}
}

func TestGetSupplierNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetSupplier("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent supplier")
	}
}

func TestCreateDuplicateSupplier(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Format(time.RFC3339)
	sup := &model.Supplier{
		Name: "dup", URL: "https://a.com", Method: "POST",
		Headers: "{}", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateSupplier(sup); err != nil {
		t.Fatal(err)
	}
	sup2 := &model.Supplier{
		Name: "dup", URL: "https://b.com", Method: "POST",
		Headers: "{}", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateSupplier(sup2); err == nil {
		t.Fatal("expected error for duplicate supplier name")
	}
}

func TestListSuppliers(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateSupplier(&model.Supplier{
		Name: "s1", URL: "https://a.com", Method: "POST",
		Headers: "{}", Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	s.CreateSupplier(&model.Supplier{
		Name: "s2", URL: "https://b.com", Method: "POST",
		Headers: "{}", Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	suppliers, err := s.ListSuppliers()
	if err != nil {
		t.Fatal(err)
	}
	if len(suppliers) != 2 {
		t.Fatalf("expected 2 suppliers, got %d", len(suppliers))
	}
}

func TestUpdateSupplier(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Format(time.RFC3339)
	sup := &model.Supplier{
		Name: "upd", URL: "https://old.com", Method: "POST",
		Headers: "{}", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	s.CreateSupplier(sup)
	sup.URL = "https://new.com"
	sup.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.UpdateSupplier(sup); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSupplier("upd")
	if got.URL != "https://new.com" {
		t.Fatalf("expected URL https://new.com, got %s", got.URL)
	}
}

func TestDeleteSupplier(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateSupplier(&model.Supplier{
		Name: "del", URL: "https://del.com", Method: "POST",
		Headers: "{}", Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	if err := s.DeleteSupplier("del"); err != nil {
		t.Fatal(err)
	}
	_, err := s.GetSupplier("del")
	if err == nil {
		t.Fatal("expected error after deletion")
	}
}

func TestSyncSuppliersFromConfig(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Format(time.RFC3339)
	entries := []model.Supplier{
		{Name: "a", URL: "https://a.com", Method: "POST", Headers: "{}",
			RetryMaxAttempts: 10, RetryBaseDelayMs: 1000, RetryMaxDelayMs: 60000,
			Enabled: true, CreatedAt: now, UpdatedAt: now},
		{Name: "b", URL: "https://b.com", Method: "POST", Headers: "{}",
			RetryMaxAttempts: 5, RetryBaseDelayMs: 2000, RetryMaxDelayMs: 120000,
			Enabled: true, CreatedAt: now, UpdatedAt: now},
	}
	if err := s.SyncSuppliersFromConfig(entries); err != nil {
		t.Fatal(err)
	}
	all, _ := s.ListSuppliers()
	if len(all) != 2 {
		t.Fatalf("expected 2 suppliers, got %d", len(all))
	}
	// Sync again with same data should be idempotent
	if err := s.SyncSuppliersFromConfig(entries); err != nil {
		t.Fatal(err)
	}
}
