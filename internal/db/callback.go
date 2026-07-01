package db

import (
	"time"

	"rc_stewarthuang/internal/model"
)

func (s *Store) CreateCallback(c *model.Callback) error {
	return s.DB.Create(c).Error
}

func (s *Store) GetCallback(id uint) (*model.Callback, error) {
	var c model.Callback
	err := s.DB.Where("id = ?", id).First(&c).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) FindPendingCallbacks(limit int) ([]model.Callback, error) {
	now := time.Now().UTC()
	var result []model.Callback
	err := s.DB.Where("status IN ?", []string{"pending", "failed"}).
		Where(s.DB.Where("next_retry_at IS NULL").Or("next_retry_at <= ?", now)).
		Order("created_at ASC").
		Limit(limit).
		Find(&result).Error
	return result, err
}

func (s *Store) UpdateCallback(c *model.Callback) error {
	return s.DB.Save(c).Error
}

func (s *Store) CreateCallbackAttempt(ca *model.CallbackAttempt) error {
	return s.DB.Create(ca).Error
}

func (s *Store) ListCallbackAttempts(callbackID uint) ([]model.CallbackAttempt, error) {
	var result []model.CallbackAttempt
	err := s.DB.Where("callback_id = ?", callbackID).
		Order("attempt_number").
		Find(&result).Error
	return result, err
}
