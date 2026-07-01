package db

import (
	"rc_stewarthuang/internal/model"

	"gorm.io/gorm"
)

func (s *Store) CreateSupplier(sup *model.Supplier) error {
	return s.DB.Create(sup).Error
}

func (s *Store) GetSupplier(name string) (*model.Supplier, error) {
	var sup model.Supplier
	err := s.DB.Where("name = ?", name).First(&sup).Error
	if err != nil {
		return nil, err
	}
	return &sup, nil
}

func (s *Store) ListSuppliers() ([]model.Supplier, error) {
	var result []model.Supplier
	err := s.DB.Order("name").Find(&result).Error
	return result, err
}

func (s *Store) UpdateSupplier(sup *model.Supplier) error {
	return s.DB.Save(sup).Error
}

func (s *Store) DeleteSupplier(name string) error {
	result := s.DB.Where("name = ?", name).Delete(&model.Supplier{})
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return result.Error
}

func (s *Store) SyncSuppliersFromConfig(entries []model.Supplier) error {
	for _, e := range entries {
		var existing model.Supplier
		err := s.DB.Where("name = ?", e.Name).First(&existing).Error
		if err == gorm.ErrRecordNotFound {
			if err := s.DB.Create(&e).Error; err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		e.ID = existing.ID
		// Preserve accepted_statuses if not set in config (empty list)
		if e.AcceptedStatuses == "[200]" && existing.AcceptedStatuses != "[200]" {
			e.AcceptedStatuses = existing.AcceptedStatuses
		}
		if err := s.DB.Save(&e).Error; err != nil {
			return err
		}
	}
	return nil
}
