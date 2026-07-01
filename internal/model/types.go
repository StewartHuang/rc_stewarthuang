package model

import "time"

type Supplier struct {
	ID               uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Name             string    `gorm:"uniqueIndex;size:255;not null" json:"name"`
	URL              string    `gorm:"size:2048;not null" json:"url"`
	Method           string    `gorm:"size:10;not null;default:POST" json:"method"`
	Headers          string    `gorm:"type:text;not null;default:{}" json:"headers"`
	RetryMaxAttempts int       `gorm:"not null;default:15" json:"retry_max_attempts"`
	RetryBaseDelayMs int       `gorm:"not null;default:1000" json:"retry_base_delay_ms"`
	RetryMaxDelayMs  int       `gorm:"not null;default:240000" json:"retry_max_delay_ms"`
	Enabled          bool      `gorm:"not null;default:true" json:"enabled"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type Notification struct {
	ID             string     `gorm:"primaryKey;size:36" json:"id"`
	Supplier       string     `gorm:"size:255;not null" json:"supplier"`
	URL            string     `gorm:"size:2048;not null" json:"url"`
	Method         string     `gorm:"size:10;not null;default:POST" json:"method"`
	Headers        string     `gorm:"type:text;not null;default:{}" json:"headers"`
	Body           string     `gorm:"type:text;not null;default:{}" json:"body"`
	IdempotencyKey *string    `gorm:"uniqueIndex;size:255" json:"idempotency_key"`
	Status         string     `gorm:"size:20;not null;default:pending;index:idx_status_next_retry" json:"status"`
	AttemptCount   int        `gorm:"not null;default:0" json:"attempt_count"`
	MaxAttempts    int        `gorm:"not null;default:15" json:"max_attempts"`
	NextRetryAt    *time.Time `gorm:"index:idx_status_next_retry" json:"next_retry_at"`
	DeadReason     *string    `gorm:"type:text" json:"dead_reason"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type DeliveryAttempt struct {
	ID             uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	NotificationID string    `gorm:"size:36;not null;index" json:"notification_id"`
	AttemptNumber  int       `gorm:"not null" json:"attempt_number"`
	Status         string    `gorm:"size:20;not null" json:"status"`
	ResponseStatus *int      `json:"response_status"`
	ResponseBody   *string   `gorm:"type:text" json:"response_body"`
	ErrorMessage   *string   `gorm:"type:text" json:"error_message"`
	AttemptedAt    time.Time `json:"attempted_at"`
}
