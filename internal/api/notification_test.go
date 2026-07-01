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

func seedTestSupplier(t *testing.T, s *db.Store) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateSupplier(&model.Supplier{
		Name: "test-supplier", URL: "https://example.com/api", Method: "POST",
		Headers: `{"Content-Type":"application/json"}`, Enabled: true,
		RetryMaxAttempts: 15, RetryBaseDelayMs: 1000, RetryMaxDelayMs: 240000,
		CreatedAt: now, UpdatedAt: now,
	})
}

func (a *App) submitTestNotification(t *testing.T, s *db.Store, id string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateNotification(&model.Notification{
		ID: id, Supplier: "test-supplier",
		URL: "https://example.com/api", Method: "POST",
		Headers: `{"Content-Type":"application/json"}`,
		Body:    `{"user_id":1}`,
		Status:  "pending", MaxAttempts: 15,
		CreatedAt: now, UpdatedAt: now,
	})
}

func TestSubmitNotification(t *testing.T) {
	app, s := newTestApp(t)
	seedTestSupplier(t, s)

	body := `{"supplier":"test-supplier","body":{"user_id":1}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/notifications", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	app.Router.ServeHTTP(w, req)

	if w.Code != 202 {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["id"] == "" {
		t.Fatal("expected non-empty notification id")
	}
	if resp["status"] != "accepted" {
		t.Fatalf("expected accepted, got %s", resp["status"])
	}
}

func TestSubmitNotificationWithIdempotencyKey(t *testing.T) {
	app, s := newTestApp(t)
	seedTestSupplier(t, s)

	body := `{"supplier":"test-supplier","body":{"user_id":1},"idempotency_key":"key-123"}`
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("POST", "/api/v1/notifications", strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	app.Router.ServeHTTP(w1, req1)
	if w1.Code != 202 {
		t.Fatalf("first request failed: %d", w1.Code)
	}
	var r1 map[string]string
	json.Unmarshal(w1.Body.Bytes(), &r1)

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/v1/notifications", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	app.Router.ServeHTTP(w2, req2)
	if w2.Code != 200 {
		t.Fatalf("expected 200 for duplicate, got %d", w2.Code)
	}
	var r2 map[string]string
	json.Unmarshal(w2.Body.Bytes(), &r2)
	if r2["id"] != r1["id"] {
		t.Fatalf("expected same id %s, got %s", r1["id"], r2["id"])
	}
}

func TestSubmitNotificationMissingSupplier(t *testing.T) {
	app, _ := newTestApp(t)
	body := `{"body":{"user_id":1}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/notifications", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	app.Router.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestSubmitNotificationSupplierNotFound(t *testing.T) {
	app, _ := newTestApp(t)
	body := `{"supplier":"nonexistent","body":{}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/notifications", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	app.Router.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGetNotification(t *testing.T) {
	app, s := newTestApp(t)
	seedTestSupplier(t, s)
	app.submitTestNotification(t, s, "n1")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/notifications/n1", nil)
	app.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var n model.Notification
	json.Unmarshal(w.Body.Bytes(), &n)
	if n.ID != "n1" {
		t.Fatalf("expected n1, got %s", n.ID)
	}
}

func TestListNotificationsByStatus(t *testing.T) {
	app, s := newTestApp(t)
	seedTestSupplier(t, s)
	app.submitTestNotification(t, s, "n1")
	app.submitTestNotification(t, s, "n2")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/notifications?status=pending", nil)
	app.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result []model.Notification
	json.Unmarshal(w.Body.Bytes(), &result)
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
}

func TestReplayDeadLetter(t *testing.T) {
	app, s := newTestApp(t)
	seedTestSupplier(t, s)
	now := time.Now().UTC().Format(time.RFC3339)
	reason := "max retries"
	s.CreateNotification(&model.Notification{
		ID: "dead-1", Supplier: "test-supplier",
		URL: "https://example.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "dead", MaxAttempts: 15,
		AttemptCount: 15, DeadReason: &reason,
		CreatedAt: now, UpdatedAt: now,
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/notifications/dead-1/replay", nil)
	app.Router.ServeHTTP(w, req)
	if w.Code != 202 {
		t.Fatalf("expected 202, got %d", w.Code)
	}
}
