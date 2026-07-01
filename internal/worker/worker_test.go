package worker

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rc_stewarthuang/internal/config"
	"rc_stewarthuang/internal/db"
	"rc_stewarthuang/internal/model"
)

func newTestStore(t *testing.T) *db.Store {
	t.Helper()
	s, err := db.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func seedTestData(t *testing.T, s *db.Store) {
	t.Helper()
	s.CreateSupplier(&model.Supplier{
		Name: "test-sup", URL: "http://localhost:19999/notify", Method: "POST",
		Headers: `{"Content-Type":"application/json"}`, Enabled: true,
		RetryMaxAttempts: 3, RetryBaseDelayMs: 100, RetryMaxDelayMs: 1000,
	})
	s.CreateNotification(&model.Notification{
		ID: "n1", Supplier: "test-sup",
		URL: "http://localhost:19999/notify", Method: "POST",
		Headers: `{"Content-Type":"application/json"}`,
		Body:    `{"user_id":1}`,
		Status:  "pending", MaxAttempts: 3,
	})
}

func TestWorkerDeliverSuccess(t *testing.T) {
	s := newTestStore(t)
	seedTestData(t, s)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	n, _ := s.GetNotification("n1")
	n.URL = server.URL
	s.UpdateNotification(n)

	cfg := &config.WorkerConfig{
		PollInterval:   "100ms",
		MaxConcurrency: 5,
		HTTPTimeout:    "5s",
	}
	w := NewWorker(s, cfg)
	go w.Start()
	time.Sleep(500 * time.Millisecond)
	w.Stop()

	updated, _ := s.GetNotification("n1")
	if updated.Status != "delivered" {
		t.Fatalf("expected delivered, got %s", updated.Status)
	}
	attempts, _ := s.ListDeliveryAttempts("n1")
	if len(attempts) != 1 {
		t.Fatalf("expected 1 delivery attempt, got %d", len(attempts))
	}
	if attempts[0].Status != "success" {
		t.Fatalf("expected success, got %s", attempts[0].Status)
	}
}

func TestWorkerDeliverFailureThenDead(t *testing.T) {
	s := newTestStore(t)
	s.CreateSupplier(&model.Supplier{
		Name: "fail-sup", URL: "http://localhost:19998/notify", Method: "POST",
		Headers: "{}", Enabled: true,
		RetryMaxAttempts: 2, RetryBaseDelayMs: 50, RetryMaxDelayMs: 200,
	})
	s.CreateNotification(&model.Notification{
		ID: "n2", Supplier: "fail-sup",
		URL: "http://localhost:19998/notify", Method: "POST",
		Headers: "{}", Body: `{}`,
		Status: "pending", MaxAttempts: 2,
	})

	cfg := &config.WorkerConfig{
		PollInterval:   "100ms",
		MaxConcurrency: 5,
		HTTPTimeout:    "1s",
	}
	w := NewWorker(s, cfg)
	go w.Start()
	time.Sleep(1 * time.Second)
	w.Stop()

	updated, _ := s.GetNotification("n2")
	if updated.Status != "dead" {
		t.Fatalf("expected dead after max attempts, got %s", updated.Status)
	}
	if updated.DeadReason == nil {
		t.Fatal("expected non-nil dead_reason")
	}
}

func TestCalculateNextRetry(t *testing.T) {
	tests := []struct {
		attempt  int
		baseMs   int
		maxMs    int
		minDelay time.Duration
		maxDelay time.Duration
	}{
		{0, 1000, 240000, 1000 * time.Millisecond, 1500 * time.Millisecond},
		{1, 1000, 240000, 2000 * time.Millisecond, 3000 * time.Millisecond},
		{2, 1000, 240000, 4000 * time.Millisecond, 6000 * time.Millisecond},
		{10, 1000, 240000, 240000 * time.Millisecond, 360000 * time.Millisecond},
	}
	for _, tc := range tests {
		delay := calculateNextRetry(tc.attempt, tc.baseMs, tc.maxMs)
		if delay < tc.minDelay || delay > tc.maxDelay {
			t.Errorf("attempt %d: expected between %v and %v, got %v",
				tc.attempt, tc.minDelay, tc.maxDelay, delay)
		}
	}
}
