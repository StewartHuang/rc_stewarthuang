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
	AcceptedStatuses string    `gorm:"type:text;not null;default:'[200]'" json:"accepted_statuses"`
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
	CallbackURL    *string    `gorm:"size:2048" json:"callback_url"`
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

type Callback struct {
	ID                 uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	NotificationID     string     `gorm:"size:36;not null" json:"notification_id"`
	NotificationStatus string     `gorm:"size:20;not null" json:"notification_status"`
	CallbackURL        string     `gorm:"size:2048;not null" json:"callback_url"`
	Status             string     `gorm:"size:20;not null;default:pending;index:idx_callbacks_status_next_retry" json:"status"`
	AttemptCount       int        `gorm:"not null;default:0" json:"attempt_count"`
	MaxAttempts        int        `gorm:"not null;default:3" json:"max_attempts"`
	RetryDelayMs       int        `gorm:"not null;default:10000" json:"retry_delay_ms"`
	LastError          *string    `gorm:"type:text" json:"last_error"`
	NextRetryAt        *time.Time `gorm:"index:idx_callbacks_status_next_retry" json:"next_retry_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type CallbackAttempt struct {
	ID             uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	CallbackID     uint      `gorm:"not null;index" json:"callback_id"`
	AttemptNumber  int       `gorm:"not null" json:"attempt_number"`
	Status         string    `gorm:"size:20;not null" json:"status"`
	ResponseStatus *int      `json:"response_status"`
	ResponseBody   *string   `gorm:"type:text" json:"response_body"`
	ErrorMessage   *string   `gorm:"type:text" json:"error_message"`
	AttemptedAt    time.Time `json:"attempted_at"`
}
