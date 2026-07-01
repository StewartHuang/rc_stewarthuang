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

func TestCallbackWorker(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(t *testing.T, s *db.Store) *httptest.Server
		assert func(t *testing.T, s *db.Store)
	}{
		{
			name: "success",
			setup: func(t *testing.T, s *db.Store) *httptest.Server {
				cbURL := "http://callback-test.local/cb"
				s.CreateCallback(&model.Callback{
					NotificationID:     "n1-cb",
					NotificationStatus: "delivered",
					CallbackURL:        cbURL,
					Status:             "pending",
					MaxAttempts:        3,
					RetryDelayMs:       100,
				})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(`{"received":true}`))
				}))
				cb, _ := s.GetCallback(1)
				cb.CallbackURL = server.URL
				s.UpdateCallback(cb)
				return server
			},
			assert: func(t *testing.T, s *db.Store) {
				updated, _ := s.GetCallback(1)
				if updated.Status != "completed" {
					t.Fatalf("expected completed, got %s", updated.Status)
				}
			},
		},
		{
			name: "retry then failed",
			setup: func(t *testing.T, s *db.Store) *httptest.Server {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte(`{"error":"server error"}`))
				}))
				s.CreateCallback(&model.Callback{
					NotificationID:     "n2-cb",
					NotificationStatus: "dead",
					CallbackURL:        server.URL,
					Status:             "pending",
					MaxAttempts:        2,
					RetryDelayMs:       50,
				})
				return server
			},
			assert: func(t *testing.T, s *db.Store) {
				updated, _ := s.GetCallback(1)
				if updated.Status != "failed" {
					t.Fatalf("expected failed, got %s", updated.Status)
				}
				if updated.LastError == nil {
					t.Fatal("expected non-nil last_error")
				}
				if updated.AttemptCount > updated.MaxAttempts {
					t.Fatalf("attempt count %d exceeds max %d — infinite retry", updated.AttemptCount, updated.MaxAttempts)
				}
				if updated.NextRetryAt != nil {
					t.Fatal("expected NextRetryAt to be nil after max attempts exhausted")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			server := tt.setup(t, s)
			defer server.Close()

			cfg := &config.WorkerConfig{PollInterval: "100ms", HTTPTimeout: "5s"}
			if tt.name == "retry then failed" {
				cfg.HTTPTimeout = "1s"
			}
			cw := NewCallbackWorker(s, cfg)
			go cw.Start()
			time.Sleep(500 * time.Millisecond)
			if tt.name == "retry then failed" {
				time.Sleep(500 * time.Millisecond)
			}
			cw.Stop()
			tt.assert(t, s)
		})
	}
}
