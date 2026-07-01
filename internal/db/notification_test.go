package db

import (
	"sort"
	"testing"
	"time"

	"rc_stewarthuang/internal/model"

	"gorm.io/gorm"
)

func TestCreateNotification(t *testing.T) {
	tests := []struct {
		name       string
		id         string
		idemKey    *string
		wantErr    bool
		wantErrMsg string
	}{
		{name: "valid", id: "notif-1", wantErr: false},
		{name: "duplicate idempotency key", id: "notif-2", idemKey: strPtr("dup-key"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			seedSupplier(t, s)
			if tt.idemKey != nil {
				s.CreateNotification(&model.Notification{
					ID: "existing", Supplier: "test-supplier",
					URL: "https://example.com/n", Method: "POST",
					Headers: "{}", Body: "{}",
					IdempotencyKey: tt.idemKey,
					Status:         "pending", MaxAttempts: 15,
				})
			}
			n := &model.Notification{
				ID: tt.id, Supplier: "test-supplier",
				URL: "https://example.com/notify", Method: "POST",
				Headers: `{"Content-Type": "application/json"}`,
				Body:    `{"user_id": 123}`,
				Status:  "pending", AttemptCount: 0, MaxAttempts: 15,
			}
			if tt.idemKey != nil {
				n.IdempotencyKey = tt.idemKey
			}
			err := s.CreateNotification(n)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateNotification failed: %v", err)
			}
		})
	}
}

func TestGetNotification(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{name: "found", id: "get-test", wantErr: false},
		{name: "not found", id: "nonexistent", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			seedSupplier(t, s)
			if tt.id == "get-test" {
				s.CreateNotification(&model.Notification{
					ID: "get-test", Supplier: "test-supplier",
					URL: "https://example.com", Method: "POST",
					Headers: "{}", Body: "{}",
					Status: "pending", MaxAttempts: 15,
				})
			}
			got, err := s.GetNotification(tt.id)
			if tt.wantErr {
				if err != gorm.ErrRecordNotFound {
					t.Errorf("expected ErrRecordNotFound, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "pending" {
				t.Errorf("expected status pending, got %s", got.Status)
			}
		})
	}
}

func TestListNotificationsByStatus(t *testing.T) {
	tests := []struct {
		name   string
		status string
		count  int
	}{
		{name: "pending", status: "pending", count: 1},
		{name: "delivered", status: "delivered", count: 1},
		{name: "dead", status: "dead", count: 1},
		{name: "no match", status: "failed", count: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			seedSupplier(t, s)
			s.CreateNotification(&model.Notification{
				ID: "n1", Supplier: "test-supplier", URL: "https://a.com",
				Method: "POST", Headers: "{}", Body: "{}", Status: "pending", MaxAttempts: 15,
			})
			s.CreateNotification(&model.Notification{
				ID: "n2", Supplier: "test-supplier", URL: "https://b.com",
				Method: "POST", Headers: "{}", Body: "{}", Status: "delivered", MaxAttempts: 15,
			})
			s.CreateNotification(&model.Notification{
				ID: "n3", Supplier: "test-supplier", URL: "https://c.com",
				Method: "POST", Headers: "{}", Body: "{}", Status: "dead", MaxAttempts: 15,
				DeadReason: strPtr("max retries exceeded"),
			})
			results, err := s.ListNotificationsByStatus(tt.status)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != tt.count {
				t.Errorf("expected %d notifications, got %d", tt.count, len(results))
			}
		})
	}
}

func TestUpdateNotification(t *testing.T) {
	s := newTestStore(t)
	seedSupplier(t, s)
	s.CreateNotification(&model.Notification{
		ID: "upd-test", Supplier: "test-supplier",
		URL: "https://example.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "pending", MaxAttempts: 15,
	})
	nt := &model.Notification{ID: "upd-test", Status: "delivered"}
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
	past := time.Now().UTC().Add(-time.Minute)
	future := time.Now().UTC().Add(time.Hour)

	// pending with no next_retry_at (immediately eligible)
	s.CreateNotification(&model.Notification{
		ID: "n1", Supplier: "test-supplier",
		URL: "https://a.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "pending", MaxAttempts: 15,
	})
	// failed with past next_retry_at (eligible)
	s.CreateNotification(&model.Notification{
		ID: "n2", Supplier: "test-supplier",
		URL: "https://b.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "failed", MaxAttempts: 15,
		AttemptCount: 1, NextRetryAt: &past,
	})
	// failed with future next_retry_at (not eligible)
	s.CreateNotification(&model.Notification{
		ID: "n4", Supplier: "test-supplier",
		URL: "https://d.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "failed", MaxAttempts: 15,
		AttemptCount: 1, NextRetryAt: &future,
	})
	// delivered (not eligible)
	s.CreateNotification(&model.Notification{
		ID: "n3", Supplier: "test-supplier",
		URL: "https://c.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "delivered", MaxAttempts: 15,
	})
	// dead (not eligible)
	s.CreateNotification(&model.Notification{
		ID: "n5", Supplier: "test-supplier",
		URL: "https://e.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "dead", MaxAttempts: 15,
		AttemptCount: 15, DeadReason: strPtr("max retries"),
	})

	results, err := s.FindPendingNotifications(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 pending notifications, got %d", len(results))
	}
	ids := []string{results[0].ID, results[1].ID}
	sort.Strings(ids)
	if ids[0] != "n1" || ids[1] != "n2" {
		t.Errorf("expected [n1 n2], got %v", ids)
	}
}

func TestReplayNotification(t *testing.T) {
	s := newTestStore(t)
	seedSupplier(t, s)
	reason := "max retries exceeded"
	s.CreateNotification(&model.Notification{
		ID: "replay-test", Supplier: "test-supplier",
		URL: "https://example.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "dead", MaxAttempts: 15,
		AttemptCount: 15, DeadReason: &reason,
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
	s.CreateNotification(&model.Notification{
		ID: "da-test", Supplier: "test-supplier",
		URL: "https://example.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "pending", MaxAttempts: 15,
	})
	da := &model.DeliveryAttempt{
		NotificationID: "da-test",
		AttemptNumber:  1,
		Status:         "success",
		ResponseStatus: intPtr(200),
		AttemptedAt:    time.Now().UTC(),
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
	s.CreateNotification(&model.Notification{
		ID: "list-da", Supplier: "test-supplier",
		URL: "https://example.com", Method: "POST",
		Headers: "{}", Body: "{}",
		Status: "pending", MaxAttempts: 15,
	})
	for i := 1; i <= 3; i++ {
		s.CreateDeliveryAttempt(&model.DeliveryAttempt{
			NotificationID: "list-da",
			AttemptNumber:  i,
			Status:         "failed",
			AttemptedAt:    time.Now().UTC(),
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
