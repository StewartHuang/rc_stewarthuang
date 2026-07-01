package db

import (
	"fmt"
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
	tests := []struct {
		name    string
		preSeed bool
		wantErr bool
	}{
		{name: "valid", wantErr: false},
		{name: "duplicate name", preSeed: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			if tt.preSeed {
				s.CreateSupplier(&model.Supplier{Name: "test-supplier", URL: "https://a.com", Method: "POST", Headers: "{}", Enabled: true})
			}
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
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateSupplier failed: %v", err)
			}
			if sup.ID == 0 {
				t.Error("expected non-zero ID after creation")
			}
		})
	}
}

func TestGetSupplier(t *testing.T) {
	tests := []struct {
		name      string
		supName   string
		seed      bool
		wantErr   bool
		wantEqual string
	}{
		{name: "found", supName: "test-supplier", seed: true, wantErr: false, wantEqual: "https://example.com"},
		{name: "not found", supName: "nonexistent", seed: false, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			if tt.seed {
				seedSupplier(t, s)
			}
			sup, err := s.GetSupplier(tt.supName)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if sup.URL != tt.wantEqual {
				t.Errorf("expected URL %s, got %s", tt.wantEqual, sup.URL)
			}
		})
	}
}

func TestListSuppliers(t *testing.T) {
	tests := []struct {
		name     string
		seed     int
		wantLen  int
	}{
		{name: "multiple", seed: 2, wantLen: 2},
		{name: "empty", seed: 0, wantLen: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			for i := range tt.seed {
				name := fmt.Sprintf("s%d", i+1)
				s.CreateSupplier(&model.Supplier{Name: name, URL: "https://a.com", Method: "POST", Headers: "{}", Enabled: true})
			}
			sup, err := s.ListSuppliers()
			if err != nil {
				t.Fatal(err)
			}
			if len(sup) != tt.wantLen {
				t.Errorf("expected %d suppliers, got %d", tt.wantLen, len(sup))
			}
		})
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
	tests := []struct {
		name    string
		supName string
		seed    bool
		wantErr bool
	}{
		{name: "existing", supName: "del", seed: true, wantErr: false},
		{name: "not found", supName: "nonexistent", seed: false, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			if tt.seed {
				s.CreateSupplier(&model.Supplier{Name: tt.supName, URL: "https://del.com", Method: "POST", Headers: "{}", Enabled: true})
			}
			err := s.DeleteSupplier(tt.supName)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.GetSupplier(tt.supName)
			if err == nil {
				t.Error("expected error after deletion")
			}
		})
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
