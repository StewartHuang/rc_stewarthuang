package db

import (
	"testing"

	"rc_stewarthuang/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func seedSupplier(t *testing.T, s *Store) {
	t.Helper()
	s.CreateSupplier(&model.Supplier{
		Name:             "test-supplier",
		URL:              "https://example.com",
		Method:           "POST",
		Headers:          "{}",
		Enabled:          true,
		RetryMaxAttempts: 15,
		RetryBaseDelayMs: 1000,
		RetryMaxDelayMs:  240000,
	})
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
	sup := &model.Supplier{Name: "dup", URL: "https://a.com", Method: "POST", Headers: "{}", Enabled: true}
	if err := s.CreateSupplier(sup); err != nil {
		t.Fatal(err)
	}
	sup2 := &model.Supplier{Name: "dup", URL: "https://b.com", Method: "POST", Headers: "{}", Enabled: true}
	if err := s.CreateSupplier(sup2); err == nil {
		t.Fatal("expected error for duplicate supplier name")
	}
}

func TestListSuppliers(t *testing.T) {
	s := newTestStore(t)
	s.CreateSupplier(&model.Supplier{Name: "s1", URL: "https://a.com", Method: "POST", Headers: "{}", Enabled: true})
	s.CreateSupplier(&model.Supplier{Name: "s2", URL: "https://b.com", Method: "POST", Headers: "{}", Enabled: true})
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
	sup := &model.Supplier{Name: "upd", URL: "https://old.com", Method: "POST", Headers: "{}", Enabled: true}
	s.CreateSupplier(sup)
	sup.URL = "https://new.com"
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
	s.CreateSupplier(&model.Supplier{Name: "del", URL: "https://del.com", Method: "POST", Headers: "{}", Enabled: true})
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
	entries := []model.Supplier{
		{Name: "a", URL: "https://a.com", Method: "POST", Headers: "{}",
			RetryMaxAttempts: 10, RetryBaseDelayMs: 1000, RetryMaxDelayMs: 60000, Enabled: true},
		{Name: "b", URL: "https://b.com", Method: "POST", Headers: "{}",
			RetryMaxAttempts: 5, RetryBaseDelayMs: 2000, RetryMaxDelayMs: 120000, Enabled: true},
	}
	if err := s.SyncSuppliersFromConfig(entries); err != nil {
		t.Fatal(err)
	}
	all, _ := s.ListSuppliers()
	if len(all) != 2 {
		t.Fatalf("expected 2 suppliers, got %d", len(all))
	}
	if err := s.SyncSuppliersFromConfig(entries); err != nil {
		t.Fatal(err)
	}
}

func intPtr(i int) *int       { return &i }
func strPtr(s string) *string { return &s }
