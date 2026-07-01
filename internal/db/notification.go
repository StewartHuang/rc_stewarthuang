package db

import (
	"time"

	"rc_stewarthuang/internal/model"

	"gorm.io/gorm"
)

func (s *Store) CreateNotification(n *model.Notification) error {
	return s.DB.Create(n).Error
}

func (s *Store) GetNotification(id string) (*model.Notification, error) {
	var n model.Notification
	err := s.DB.Where("id = ?", id).First(&n).Error
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func (s *Store) ListNotificationsByStatus(status string) ([]model.Notification, error) {
	var result []model.Notification
	err := s.DB.Where("status = ?", status).Order("created_at DESC").Find(&result).Error
	return result, err
}

func (s *Store) UpdateNotification(n *model.Notification) error {
	return s.DB.Save(n).Error
}

func (s *Store) FindPendingNotifications(limit int) ([]model.Notification, error) {
	now := time.Now().UTC()
	var result []model.Notification
	err := s.DB.Where("status IN ?", []string{"pending", "failed"}).
		Where(s.DB.Where("next_retry_at IS NULL").Or("next_retry_at <= ?", now)).
		Order("created_at ASC").
		Limit(limit).
		Find(&result).Error
	return result, err
}

func (s *Store) ReplayNotification(id string) error {
	result := s.DB.Model(&model.Notification{}).
		Where("id = ? AND status = ?", id, "dead").
		Updates(map[string]interface{}{
			"status":        "pending",
			"attempt_count": 0,
			"next_retry_at": nil,
			"dead_reason":   nil,
		})
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return result.Error
}

func (s *Store) FindByIdempotencyKey(key string) (*model.Notification, error) {
	var n model.Notification
	err := s.DB.Where("idempotency_key = ?", key).First(&n).Error
	if err != nil {
		return nil, err
	}
	return &n, nil
}
