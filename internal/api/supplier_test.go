package api

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"rc_stewarthuang/internal/db"
	"rc_stewarthuang/internal/model"
)

func newTestApp(t *testing.T) (*App, *db.Store) {
	t.Helper()
	f, err := os.CreateTemp("", "delivery-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	store, err := db.NewStore(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		os.Remove(f.Name())
	})
	return NewApp(store), store
}

func TestListSuppliers(t *testing.T) {
	app, s := newTestApp(t)
	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateSupplier(&model.Supplier{
		Name: "s1", URL: "https://a.com", Method: "POST",
		Headers: "{}", Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	s.CreateSupplier(&model.Supplier{
		Name: "s2", URL: "https://b.com", Method: "POST",
		Headers: "{}", Enabled: true, CreatedAt: now, UpdatedAt: now,
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/suppliers", nil)
	app.Router.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result []model.Supplier
	json.Unmarshal(w.Body.Bytes(), &result)
	if len(result) != 2 {
		t.Fatalf("expected 2 suppliers, got %d", len(result))
	}
}

func TestGetSupplier(t *testing.T) {
	app, s := newTestApp(t)
	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateSupplier(&model.Supplier{
		Name: "test-me", URL: "https://test.com", Method: "POST",
		Headers: `{"X-Key":"val"}`, Enabled: true,
		RetryMaxAttempts: 10, RetryBaseDelayMs: 1000, RetryMaxDelayMs: 60000,
		CreatedAt: now, UpdatedAt: now,
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/suppliers/test-me", nil)
	app.Router.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var sup model.Supplier
	json.Unmarshal(w.Body.Bytes(), &sup)
	if sup.Name != "test-me" {
		t.Fatalf("expected test-me, got %s", sup.Name)
	}
}

func TestGetSupplierNotFound(t *testing.T) {
	app, _ := newTestApp(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/suppliers/nonexistent", nil)
	app.Router.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestCreateSupplier(t *testing.T) {
	app, _ := newTestApp(t)
	body := `{"name":"new-sup","url":"https://new.com","method":"PUT","headers":{"X-Api-Key":"secret"},"retry":{"max_attempts":5,"base_delay":"2s","max_delay":"60s"}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/suppliers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	app.Router.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateSupplierDuplicate(t *testing.T) {
	app, s := newTestApp(t)
	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateSupplier(&model.Supplier{
		Name: "dup", URL: "https://dup.com", Method: "POST",
		Headers: "{}", Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	body := `{"name":"dup","url":"https://dup.com","method":"POST"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/suppliers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	app.Router.ServeHTTP(w, req)
	if w.Code != 409 {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestUpdateSupplier(t *testing.T) {
	app, s := newTestApp(t)
	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateSupplier(&model.Supplier{
		Name: "upd", URL: "https://old.com", Method: "POST",
		Headers: "{}", Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	body := `{"name":"upd","url":"https://new.com","method":"PUT"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/suppliers/upd", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	app.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeleteSupplier(t *testing.T) {
	app, s := newTestApp(t)
	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateSupplier(&model.Supplier{
		Name: "del", URL: "https://del.com", Method: "POST",
		Headers: "{}", Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/suppliers/del", nil)
	app.Router.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

func TestDeleteSupplierNotFound(t *testing.T) {
	app, _ := newTestApp(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/suppliers/nonexistent", nil)
	app.Router.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
