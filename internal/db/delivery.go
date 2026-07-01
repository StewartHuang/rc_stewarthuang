package db

import "rc_stewarthuang/internal/model"

func (s *Store) CreateDeliveryAttempt(da *model.DeliveryAttempt) error {
	return s.DB.Create(da).Error
}

func (s *Store) ListDeliveryAttempts(notificationID string) ([]model.DeliveryAttempt, error) {
	var result []model.DeliveryAttempt
	err := s.DB.Where("notification_id = ?", notificationID).
		Order("attempt_number").
		Find(&result).Error
	return result, err
}
