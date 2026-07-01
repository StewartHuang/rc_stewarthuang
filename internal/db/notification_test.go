package db

import (
	"testing"
	"time"

	"rc_stewarthuang/internal/model"
)

func TestCreateNotification(t *testing.T) {
	s := newTestStore(t)
	seedSupplier(t, s)
	now := time.Now().UTC().Format(time.RFC3339)
	n := &model.Notification{
		ID: "notif-1", Supplier: "test-supplier",
		URL: "https://example.com/notify", Method: "POST",
		Headers: `{"Content-Type": "application/json"}`,
		Body:    `{"user_id": 123}`,
		Status:  "pending", AttemptCount: 0, MaxAttempts: 15,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateNotification(n); err != nil {
		t.Fatalf("CreateNotification failed: %v", err)
	}
}

func TestCreateNotificationIdempotency(t *testing.T) {
	s := newTestStore(t)
	seedSupplier(t, s)
	now := time.Now().UTC().Format(time.RFC3339)
	key := "idem-key-1"
	n1 := &model.Notification{
		ID: "n1", Supplier: "test-supplier",
		URL: "https://example.com/n", Method: "POST",
		Headers: "{}", Body: "{}",
		IdempotencyKey: key,
		Status: "pending", MaxAttempts: 15,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateNotification(n1); err != nil {
		t.Fatal(err)
	}
	n2 := &model.Notification{
		ID: "n2", Supplier: "test-supplier",
		URL: "https://example.com/n", Method: "POST",
		Headers: "{}", Body: "{}",
		IdempotencyKey: key,
		Status: "pending", MaxAttempts: 15,
		CreatedAt: now, UpdatedAt: now,
	}
	err := s.CreateNotification(n2)
	if err == nil {
		t.Fatal("expected error for duplicate idempotency_key")
	}
}

func TestGetNotification(t *testing.T) {
	s := newTestStore(t)
	seedSupplier(t, s)
	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateNotification(&model.Notification{
		ID: "get-test", Supplier: "test-supplier",
		URL: "https://example.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "pending", MaxAttempts: 15,
		CreatedAt: now, UpdatedAt: now,
	})
	got, err := s.GetNotification("get-test")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "pending" {
		t.Fatalf("expected status pending, got %s", got.Status)
	}
}

func TestGetNotificationNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetNotification("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent notification")
	}
}

func TestListNotificationsByStatus(t *testing.T) {
	s := newTestStore(t)
	seedSupplier(t, s)
	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateNotification(&model.Notification{
		ID: "n1", Supplier: "test-supplier",
		URL: "https://a.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "pending", MaxAttempts: 15,
		CreatedAt: now, UpdatedAt: now,
	})
	s.CreateNotification(&model.Notification{
		ID: "n2", Supplier: "test-supplier",
		URL: "https://b.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "delivered", MaxAttempts: 15,
		CreatedAt: now, UpdatedAt: now,
	})
	s.CreateNotification(&model.Notification{
		ID: "n3", Supplier: "test-supplier",
		URL: "https://c.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "dead", MaxAttempts: 15,
		DeadReason: strPtr("max retries exceeded"),
		CreatedAt: now, UpdatedAt: now,
	})

	pending, err := s.ListNotificationsByStatus("pending")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}

	dead, err := s.ListNotificationsByStatus("dead")
	if err != nil {
		t.Fatal(err)
	}
	if len(dead) != 1 {
		t.Fatalf("expected 1 dead, got %d", len(dead))
	}
}

func TestUpdateNotification(t *testing.T) {
	s := newTestStore(t)
	seedSupplier(t, s)
	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateNotification(&model.Notification{
		ID: "upd-test", Supplier: "test-supplier",
		URL: "https://example.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "pending", MaxAttempts: 15,
		CreatedAt: now, UpdatedAt: now,
	})
	nt := &model.Notification{
		ID: "upd-test", Status: "delivered",
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.UpdateNotification(nt); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetNotification("upd-test")
	if got.Status != "delivered" {
		t.Fatalf("expected delivered, got %s", got.Status)
	}
}

func TestFindPendingNotifications(t *testing.T) {
	s := newTestStore(t)
	seedSupplier(t, s)
	now := time.Now().UTC().Format(time.RFC3339)

	// pending with no next_retry_at (immediately eligible)
	s.CreateNotification(&model.Notification{
		ID: "n1", Supplier: "test-supplier",
		URL: "https://a.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "pending", MaxAttempts: 15,
		CreatedAt: now, UpdatedAt: now,
	})
	// failed with past next_retry_at (eligible)
	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	s.CreateNotification(&model.Notification{
		ID: "n2", Supplier: "test-supplier",
		URL: "https://b.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "failed", MaxAttempts: 15,
		AttemptCount: 1,
		NextRetryAt: &past,
		CreatedAt: now, UpdatedAt: now,
	})
	// delivered (not eligible)
	s.CreateNotification(&model.Notification{
		ID: "n3", Supplier: "test-supplier",
		URL: "https://c.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "delivered", MaxAttempts: 15,
		CreatedAt: now, UpdatedAt: now,
	})

	results, err := s.FindPendingNotifications(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 pending notifications, got %d", len(results))
	}
}

func TestReplayNotification(t *testing.T) {
	s := newTestStore(t)
	seedSupplier(t, s)
	now := time.Now().UTC().Format(time.RFC3339)
	reason := "max retries exceeded"
	s.CreateNotification(&model.Notification{
		ID: "replay-test", Supplier: "test-supplier",
		URL: "https://example.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "dead", MaxAttempts: 15,
		AttemptCount: 15, DeadReason: &reason,
		CreatedAt: now, UpdatedAt: now,
	})
	if err := s.ReplayNotification("replay-test"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetNotification("replay-test")
	if got.Status != "pending" {
		t.Fatalf("expected pending after replay, got %s", got.Status)
	}
	if got.AttemptCount != 0 {
		t.Fatalf("expected 0 attempt count after replay, got %d", got.AttemptCount)
	}
	if got.DeadReason != nil {
		t.Fatal("expected nil dead_reason after replay")
	}
}

func TestCreateDeliveryAttempt(t *testing.T) {
	s := newTestStore(t)
	seedSupplier(t, s)
	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateNotification(&model.Notification{
		ID: "da-test", Supplier: "test-supplier",
		URL: "https://example.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "pending", MaxAttempts: 15,
		CreatedAt: now, UpdatedAt: now,
	})
	da := &model.DeliveryAttempt{
		NotificationID: "da-test",
		AttemptNumber:  1,
		Status:         "success",
		ResponseStatus: intPtr(200),
		AttemptedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.CreateDeliveryAttempt(da); err != nil {
		t.Fatal(err)
	}
	if da.ID == 0 {
		t.Fatal("expected non-zero ID")
	}
}

func TestListDeliveryAttempts(t *testing.T) {
	s := newTestStore(t)
	seedSupplier(t, s)
	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateNotification(&model.Notification{
		ID: "list-da", Supplier: "test-supplier",
		URL: "https://example.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "pending", MaxAttempts: 15,
		CreatedAt: now, UpdatedAt: now,
	})
	for i := 1; i <= 3; i++ {
		s.CreateDeliveryAttempt(&model.DeliveryAttempt{
			NotificationID: "list-da",
			AttemptNumber:  i,
			Status:         "failed",
			AttemptedAt:    now,
		})
	}
	attempts, err := s.ListDeliveryAttempts("list-da")
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 3 {
		t.Fatalf("expected 3 attempts, got %d", len(attempts))
	}
}

func intPtr(i int) *int              { return &i }
func strPtr(s string) *string        { return &s }

func seedSupplier(t *testing.T, s *Store) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateSupplier(&model.Supplier{
		Name: "test-supplier", URL: "https://example.com", Method: "POST",
		Headers: "{}", Enabled: true,
		RetryMaxAttempts: 15, RetryBaseDelayMs: 1000, RetryMaxDelayMs: 240000,
		CreatedAt: now, UpdatedAt: now,
	})
}
