package db

import (
	"database/sql"
	"fmt"
	"time"

	"rc_stewarthuang/internal/model"
)

func (s *Store) CreateNotification(n *model.Notification) error {
	_, err := s.DB.Exec(
		`INSERT INTO notifications (id, supplier, url, method, headers, body, idempotency_key, status, attempt_count, max_attempts, next_retry_at, dead_reason, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.Supplier, n.URL, n.Method, n.Headers, n.Body,
		nullString(n.IdempotencyKey), n.Status, n.AttemptCount, n.MaxAttempts,
		nullStringPtr(n.NextRetryAt), nullStringPtr(n.DeadReason),
		n.CreatedAt, n.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create notification: %w", err)
	}
	return nil
}

func (s *Store) GetNotification(id string) (*model.Notification, error) {
	row := s.DB.QueryRow(
		`SELECT id, supplier, url, method, headers, body, idempotency_key, status, attempt_count, max_attempts, next_retry_at, dead_reason, created_at, updated_at
		 FROM notifications WHERE id = ?`, id)
	n := &model.Notification{}
	var idemKey, nextRetry, deadReason sql.NullString
	err := row.Scan(&n.ID, &n.Supplier, &n.URL, &n.Method, &n.Headers, &n.Body,
		&idemKey, &n.Status, &n.AttemptCount, &n.MaxAttempts,
		&nextRetry, &deadReason, &n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("notification %q not found", id)
		}
		return nil, fmt.Errorf("get notification: %w", err)
	}
	n.IdempotencyKey = idemKey.String
	if nextRetry.Valid {
		n.NextRetryAt = &nextRetry.String
	}
	if deadReason.Valid {
		n.DeadReason = &deadReason.String
	}
	return n, nil
}

func (s *Store) ListNotificationsByStatus(status string) ([]model.Notification, error) {
	rows, err := s.DB.Query(
		`SELECT id, supplier, url, method, headers, body, idempotency_key, status, attempt_count, max_attempts, next_retry_at, dead_reason, created_at, updated_at
		 FROM notifications WHERE status = ? ORDER BY created_at DESC`, status)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()
	return scanNotifications(rows)
}

func (s *Store) UpdateNotification(n *model.Notification) error {
	res, err := s.DB.Exec(
		`UPDATE notifications SET supplier=?, url=?, method=?, headers=?, body=?, status=?, attempt_count=?, max_attempts=?, next_retry_at=?, dead_reason=?, updated_at=?
		 WHERE id=?`,
		n.Supplier, n.URL, n.Method, n.Headers, n.Body,
		n.Status, n.AttemptCount, n.MaxAttempts,
		nullStringPtr(n.NextRetryAt), nullStringPtr(n.DeadReason),
		n.UpdatedAt, n.ID)
	if err != nil {
		return fmt.Errorf("update notification: %w", err)
	}
	nr, _ := res.RowsAffected()
	if nr == 0 {
		return fmt.Errorf("notification %q not found", n.ID)
	}
	return nil
}

func (s *Store) FindPendingNotifications(limit int) ([]model.Notification, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := s.DB.Query(
		`SELECT id, supplier, url, method, headers, body, idempotency_key, status, attempt_count, max_attempts, next_retry_at, dead_reason, created_at, updated_at
		 FROM notifications
		 WHERE status IN ('pending', 'failed')
		   AND (next_retry_at IS NULL OR next_retry_at <= ?)
		 ORDER BY created_at ASC
		 LIMIT ?`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("find pending notifications: %w", err)
	}
	defer rows.Close()
	return scanNotifications(rows)
}

func (s *Store) ReplayNotification(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(
		`UPDATE notifications SET status='pending', attempt_count=0, next_retry_at=NULL, dead_reason=NULL, updated_at=?
		 WHERE id=? AND status='dead'`, now, id)
	if err != nil {
		return fmt.Errorf("replay notification: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("notification %q not found or not in dead status", id)
	}
	return nil
}

func (s *Store) FindByIdempotencyKey(key string) (*model.Notification, error) {
	row := s.DB.QueryRow(
		`SELECT id, supplier, url, method, headers, body, idempotency_key, status, attempt_count, max_attempts, next_retry_at, dead_reason, created_at, updated_at
		 FROM notifications WHERE idempotency_key = ?`, key)
	n := &model.Notification{}
	var idemKey, nextRetry, deadReason sql.NullString
	err := row.Scan(&n.ID, &n.Supplier, &n.URL, &n.Method, &n.Headers, &n.Body,
		&idemKey, &n.Status, &n.AttemptCount, &n.MaxAttempts,
		&nextRetry, &deadReason, &n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("not found")
		}
		return nil, fmt.Errorf("find by idempotency key: %w", err)
	}
	n.IdempotencyKey = idemKey.String
	if nextRetry.Valid {
		n.NextRetryAt = &nextRetry.String
	}
	if deadReason.Valid {
		n.DeadReason = &deadReason.String
	}
	return n, nil
}

func scanNotifications(rows *sql.Rows) ([]model.Notification, error) {
	var result []model.Notification
	for rows.Next() {
		var n model.Notification
		var idemKey, nextRetry, deadReason sql.NullString
		err := rows.Scan(&n.ID, &n.Supplier, &n.URL, &n.Method, &n.Headers, &n.Body,
			&idemKey, &n.Status, &n.AttemptCount, &n.MaxAttempts,
			&nextRetry, &deadReason, &n.CreatedAt, &n.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		n.IdempotencyKey = idemKey.String
		if nextRetry.Valid {
			n.NextRetryAt = &nextRetry.String
		}
		if deadReason.Valid {
			n.DeadReason = &deadReason.String
		}
		result = append(result, n)
	}
	return result, rows.Err()
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullStringPtr(s *string) interface{} {
	if s == nil {
		return nil
	}
	return *s
}
