package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rc_stewarthuang/internal/db"
	"rc_stewarthuang/internal/model"
)

func newTestApp(t *testing.T) (*App, *db.Store) {
	t.Helper()
	store, err := db.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return NewApp(store), store
}

func seedTestSupplier(t *testing.T, s *db.Store) {
	t.Helper()
	s.CreateSupplier(&model.Supplier{
		Name: "test-supplier", URL: "https://example.com/api", Method: "POST",
		Headers: `{"Content-Type":"application/json"}`, Enabled: true,
		RetryMaxAttempts: 15, RetryBaseDelayMs: 1000, RetryMaxDelayMs: 240000,
	})
}

func submitTestNotification(t *testing.T, s *db.Store, id string) {
	t.Helper()
	s.CreateNotification(&model.Notification{
		ID: id, Supplier: "test-supplier",
		URL: "https://example.com/api", Method: "POST",
		Headers: `{"Content-Type":"application/json"}`,
		Body:    `{"user_id":1}`,
		Status:  "pending", MaxAttempts: 15,
	})
}

func timePtr(t time.Time) *time.Time { return &t }

func TestListSuppliers(t *testing.T) {
	tests := []struct {
		name    string
		count   int
	}{
		{name: "multiple", count: 2},
		{name: "empty", count: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, s := newTestApp(t)
			for i := range tt.count {
				name := "s" + string(rune('a'+i))
				s.CreateSupplier(&model.Supplier{Name: name, URL: "https://a.com", Method: "POST", Headers: "{}", Enabled: true})
			}
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/api/v1/suppliers", nil)
			app.Router.ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatalf("expected 200, got %d", w.Code)
			}
			var result []model.Supplier
			json.Unmarshal(w.Body.Bytes(), &result)
			if len(result) != tt.count {
				t.Errorf("expected %d suppliers, got %d", tt.count, len(result))
			}
		})
	}
}

func TestGetSupplier(t *testing.T) {
	tests := []struct {
		name     string
		supName  string
		wantCode int
	}{
		{name: "found", supName: "test-me", wantCode: 200},
		{name: "not found", supName: "nonexistent", wantCode: 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, s := newTestApp(t)
			if tt.supName == "test-me" {
				s.CreateSupplier(&model.Supplier{
					Name: "test-me", URL: "https://test.com", Method: "POST",
					Headers: `{"X-Key":"val"}`, Enabled: true,
					RetryMaxAttempts: 10, RetryBaseDelayMs: 1000, RetryMaxDelayMs: 60000,
				})
			}
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/api/v1/suppliers/"+tt.supName, nil)
			app.Router.ServeHTTP(w, req)
			if w.Code != tt.wantCode {
				t.Fatalf("expected %d, got %d: %s", tt.wantCode, w.Code, w.Body.String())
			}
		})
	}
}

func TestCreateSupplier(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		preSeed    bool
		wantCode   int
		expectCB   bool
		wantStatus string
	}{
		{
			name:     "valid",
			body:     `{"name":"new-sup","url":"https://new.com","method":"PUT","headers":{"X-Api-Key":"secret"},"retry":{"max_attempts":5,"base_delay":"2s","max_delay":"60s"}}`,
			wantCode: 201,
		},
		{
			name:     "duplicate",
			body:     `{"name":"dup","url":"https://dup.com","method":"POST"}`,
			preSeed:  true,
			wantCode: 409,
		},
		{
			name:       "with accepted_statuses",
			body:       `{"name":"status-sup","url":"https://status.com","method":"POST","accepted_statuses":[200,201,204]}`,
			wantCode:   201,
			expectCB:   true,
			wantStatus: `[200,201,204]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, s := newTestApp(t)
			if tt.preSeed {
				s.CreateSupplier(&model.Supplier{Name: "dup", URL: "https://dup.com", Method: "POST", Headers: "{}", Enabled: true})
			}
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/v1/suppliers", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			app.Router.ServeHTTP(w, req)
			if w.Code != tt.wantCode {
				t.Fatalf("expected %d, got %d: %s", tt.wantCode, w.Code, w.Body.String())
			}
			if tt.expectCB {
				var sup model.Supplier
				json.Unmarshal(w.Body.Bytes(), &sup)
				if sup.AcceptedStatuses != tt.wantStatus {
					t.Errorf("expected accepted_statuses %s, got %s", tt.wantStatus, sup.AcceptedStatuses)
				}
			}
		})
	}
}

func TestUpdateSupplier(t *testing.T) {
	tests := []struct {
		name       string
		setupURL   string
		body       string
		wantCode   int
		checkAccep bool
		wantStatus string
	}{
		{
			name:     "url and method",
			setupURL: "https://old.com",
			body:     `{"name":"upd","url":"https://new.com","method":"PUT"}`,
			wantCode: 200,
		},
		{
			name:       "accepted_statuses",
			setupURL:   "https://old.com",
			body:       `{"accepted_statuses":[200,202]}`,
			wantCode:   200,
			checkAccep: true,
			wantStatus: `[200,202]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, s := newTestApp(t)
			s.CreateSupplier(&model.Supplier{
				Name: "upd", URL: tt.setupURL, Method: "POST",
				Headers: "{}", Enabled: true, AcceptedStatuses: "[200]",
			})
			w := httptest.NewRecorder()
			req := httptest.NewRequest("PUT", "/api/v1/suppliers/upd", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			app.Router.ServeHTTP(w, req)
			if w.Code != tt.wantCode {
				t.Fatalf("expected %d, got %d: %s", tt.wantCode, w.Code, w.Body.String())
			}
			if tt.checkAccep {
				updated, _ := s.GetSupplier("upd")
				if updated.AcceptedStatuses != tt.wantStatus {
					t.Errorf("expected accepted_statuses %s, got %s", tt.wantStatus, updated.AcceptedStatuses)
				}
			}
		})
	}
}

func TestDeleteSupplier(t *testing.T) {
	tests := []struct {
		name     string
		supName  string
		seed     bool
		wantCode int
	}{
		{name: "existing", supName: "del", seed: true, wantCode: 204},
		{name: "not found", supName: "nonexistent", seed: false, wantCode: 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, s := newTestApp(t)
			if tt.seed {
				s.CreateSupplier(&model.Supplier{Name: tt.supName, URL: "https://del.com", Method: "POST", Headers: "{}", Enabled: true})
			}
			w := httptest.NewRecorder()
			req := httptest.NewRequest("DELETE", "/api/v1/suppliers/"+tt.supName, nil)
			app.Router.ServeHTTP(w, req)
			if w.Code != tt.wantCode {
				t.Fatalf("expected %d, got %d", tt.wantCode, w.Code)
			}
		})
	}
}
