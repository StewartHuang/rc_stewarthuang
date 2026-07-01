package db

import (
	"fmt"

	"rc_stewarthuang/internal/model"
)

func (s *Store) CreateDeliveryAttempt(da *model.DeliveryAttempt) error {
	res, err := s.DB.Exec(
		`INSERT INTO delivery_attempts (notification_id, attempt_number, status, response_status, response_body, error_message, attempted_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		da.NotificationID, da.AttemptNumber, da.Status,
		da.ResponseStatus, da.ResponseBody, da.ErrorMessage, da.AttemptedAt)
	if err != nil {
		return fmt.Errorf("create delivery attempt: %w", err)
	}
	id, _ := res.LastInsertId()
	da.ID = id
	return nil
}

func (s *Store) ListDeliveryAttempts(notificationID string) ([]model.DeliveryAttempt, error) {
	rows, err := s.DB.Query(
		`SELECT id, notification_id, attempt_number, status, response_status, response_body, error_message, attempted_at
		 FROM delivery_attempts WHERE notification_id = ? ORDER BY attempt_number`, notificationID)
	if err != nil {
		return nil, fmt.Errorf("list delivery attempts: %w", err)
	}
	defer rows.Close()
	var result []model.DeliveryAttempt
	for rows.Next() {
		var da model.DeliveryAttempt
		if err := rows.Scan(&da.ID, &da.NotificationID, &da.AttemptNumber, &da.Status,
			&da.ResponseStatus, &da.ResponseBody, &da.ErrorMessage, &da.AttemptedAt); err != nil {
			return nil, fmt.Errorf("scan delivery attempt: %w", err)
		}
		result = append(result, da)
	}
	return result, rows.Err()
}
