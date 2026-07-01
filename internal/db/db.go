package db

import (
	"rc_stewarthuang/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type Store struct {
	DB *gorm.DB
}

func NewStore(dbPath string) (*Store, error) {
	dsn := dbPath
	if dbPath == ":memory:" {
		dsn = "file::memory:?cache=shared"
	} else {
		dsn += "?_pragma=journal_mode(WAL)"
	}
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(&model.Supplier{}, &model.Notification{}, &model.DeliveryAttempt{}); err != nil {
		return nil, err
	}
	return &Store{DB: db}, nil
}

func (s *Store) Close() error {
	sqlDB, err := s.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
