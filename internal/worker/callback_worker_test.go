package worker

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rc_stewarthuang/internal/config"
	"rc_stewarthuang/internal/model"
)

func TestCallbackWorkerSuccess(t *testing.T) {
	s := newTestStore(t)
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
	defer server.Close()

	cb, _ := s.GetCallback(1)
	cb.CallbackURL = server.URL
	s.UpdateCallback(cb)

	cfg := &config.WorkerConfig{PollInterval: "100ms", HTTPTimeout: "5s"}
	cw := NewCallbackWorker(s, cfg)
	go cw.Start()
	time.Sleep(500 * time.Millisecond)
	cw.Stop()

	updated, _ := s.GetCallback(1)
	if updated.Status != "completed" {
		t.Fatalf("expected completed, got %s", updated.Status)
	}
}

func TestCallbackWorkerRetryThenFailed(t *testing.T) {
	s := newTestStore(t)

	s.CreateCallback(&model.Callback{
		NotificationID:     "n2-cb",
		NotificationStatus: "dead",
		CallbackURL:        "http://localhost:19995/cb",
		Status:             "pending",
		MaxAttempts:        2,
		RetryDelayMs:       50,
	})

	cfg := &config.WorkerConfig{PollInterval: "100ms", HTTPTimeout: "1s"}
	cw := NewCallbackWorker(s, cfg)
	go cw.Start()
	time.Sleep(1 * time.Second)
	cw.Stop()

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
}
