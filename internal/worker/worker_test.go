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

func TestWorker(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, s *db.Store) (*httptest.Server, string)
		assert     func(t *testing.T, s *db.Store, notificationID string)
	}{
		{
			name: "deliver success",
			setup: func(t *testing.T, s *db.Store) (*httptest.Server, string) {
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
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(`{"ok":true}`))
				}))
				n, _ := s.GetNotification("n1")
				n.URL = server.URL
				s.UpdateNotification(n)
				return server, "n1"
			},
			assert: func(t *testing.T, s *db.Store, _ string) {
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
			},
		},
		{
			name: "fail then dead",
			setup: func(t *testing.T, s *db.Store) (*httptest.Server, string) {
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
				return nil, "n2"
			},
			assert: func(t *testing.T, s *db.Store, _ string) {
				updated, _ := s.GetNotification("n2")
				if updated.Status != "dead" {
					t.Fatalf("expected dead after max attempts, got %s", updated.Status)
				}
				if updated.DeadReason == nil {
					t.Fatal("expected non-nil dead_reason")
				}
			},
		},
		{
			name: "accepted statuses [201]",
			setup: func(t *testing.T, s *db.Store) (*httptest.Server, string) {
				s.CreateSupplier(&model.Supplier{
					Name: "custom-status", URL: "http://localhost:19997/notify", Method: "POST",
					Headers: "{}", Enabled: true,
					RetryMaxAttempts: 3, RetryBaseDelayMs: 100, RetryMaxDelayMs: 1000,
					AcceptedStatuses: "[201]",
				})
				s.CreateNotification(&model.Notification{
					ID: "cs1", Supplier: "custom-status",
					URL: "http://localhost:19997/notify", Method: "POST",
					Headers: "{}", Body: `{}`,
					Status: "pending", MaxAttempts: 3,
				})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
					w.Write([]byte(`{"ok":true}`))
				}))
				n, _ := s.GetNotification("cs1")
				n.URL = server.URL
				s.UpdateNotification(n)
				return server, "cs1"
			},
			assert: func(t *testing.T, s *db.Store, _ string) {
				updated, _ := s.GetNotification("cs1")
				if updated.Status != "delivered" {
					t.Fatalf("expected delivered, got %s", updated.Status)
				}
			},
		},
		{
			name: "callback inserted on delivered",
			setup: func(t *testing.T, s *db.Store) (*httptest.Server, string) {
				cbURL := "https://biz.company.com/callback"
				s.CreateSupplier(&model.Supplier{
					Name: "cb-sup", URL: "http://localhost:19996/notify", Method: "POST",
					Headers: "{}", Enabled: true,
					RetryMaxAttempts: 3, RetryBaseDelayMs: 100, RetryMaxDelayMs: 1000,
				})
				s.CreateNotification(&model.Notification{
					ID: "cb1", Supplier: "cb-sup",
					URL: "http://localhost:19996/notify", Method: "POST",
					Headers: "{}", Body: `{}`,
					Status: "pending", MaxAttempts: 3,
					CallbackURL: &cbURL,
				})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(`{"ok":true}`))
				}))
				n, _ := s.GetNotification("cb1")
				n.URL = server.URL
				s.UpdateNotification(n)
				return server, "cb1"
			},
			assert: func(t *testing.T, s *db.Store, _ string) {
				callbacks, _ := s.FindPendingCallbacks(10)
				if len(callbacks) != 1 {
					t.Fatalf("expected 1 callback, got %d", len(callbacks))
				}
				if callbacks[0].NotificationID != "cb1" {
					t.Fatalf("expected cb1, got %s", callbacks[0].NotificationID)
				}
			},
		},
		{
			name: "callback inserted on dead",
			setup: func(t *testing.T, s *db.Store) (*httptest.Server, string) {
				cbURL := "https://biz.company.com/callback"
				s.CreateSupplier(&model.Supplier{
					Name: "dead-cb-sup", URL: "http://localhost:19995/notify", Method: "POST",
					Headers: "{}", Enabled: true,
					RetryMaxAttempts: 1, RetryBaseDelayMs: 50, RetryMaxDelayMs: 200,
				})
				s.CreateNotification(&model.Notification{
					ID: "cb-dead", Supplier: "dead-cb-sup",
					URL: "http://localhost:19995/notify", Method: "POST",
					Headers: "{}", Body: `{}`,
					Status: "pending", MaxAttempts: 1,
					CallbackURL: &cbURL,
				})
				return nil, "cb-dead"
			},
			assert: func(t *testing.T, s *db.Store, _ string) {
				updated, _ := s.GetNotification("cb-dead")
				if updated.Status != "dead" {
					t.Fatalf("expected dead, got %s", updated.Status)
				}
				callbacks, _ := s.FindPendingCallbacks(10)
				if len(callbacks) != 1 {
					t.Fatalf("expected 1 callback, got %d", len(callbacks))
				}
				if callbacks[0].NotificationID != "cb-dead" {
					t.Fatalf("expected cb-dead, got %s", callbacks[0].NotificationID)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			server, _ := tt.setup(t, s)
			if server != nil {
				defer server.Close()
			}
			cfg := &config.WorkerConfig{
				PollInterval: "100ms", MaxConcurrency: 5, HTTPTimeout: "5s",
			}
			if tt.name == "fail then dead" || tt.name == "callback inserted on dead" {
				cfg.HTTPTimeout = "1s"
			}
			w := NewWorker(s, cfg)
			go w.Start()
			time.Sleep(500 * time.Millisecond)
			if tt.name == "callback inserted on dead" {
				time.Sleep(500 * time.Millisecond)
			}
			w.Stop()
			tt.assert(t, s, "")
		})
	}
}

func TestCalculateNextRetry(t *testing.T) {
	tests := []struct {
		name     string
		attempt  int
		baseMs   int
		maxMs    int
		minDelay time.Duration
		maxDelay time.Duration
	}{
		{name: "attempt 0", attempt: 0, baseMs: 1000, maxMs: 240000, minDelay: 1000 * time.Millisecond, maxDelay: 1500 * time.Millisecond},
		{name: "attempt 1", attempt: 1, baseMs: 1000, maxMs: 240000, minDelay: 2000 * time.Millisecond, maxDelay: 3000 * time.Millisecond},
		{name: "attempt 2", attempt: 2, baseMs: 1000, maxMs: 240000, minDelay: 4000 * time.Millisecond, maxDelay: 6000 * time.Millisecond},
		{name: "capped at max", attempt: 10, baseMs: 1000, maxMs: 240000, minDelay: 240000 * time.Millisecond, maxDelay: 360000 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delay := calculateNextRetry(tt.attempt, tt.baseMs, tt.maxMs)
			if delay < tt.minDelay || delay > tt.maxDelay {
				t.Errorf("expected between %v and %v, got %v", tt.minDelay, tt.maxDelay, delay)
			}
		})
	}
}
