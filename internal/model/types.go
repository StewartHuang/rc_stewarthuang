package model

type Supplier struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	URL              string `json:"url"`
	Method           string `json:"method"`
	Headers          string `json:"headers"`
	RetryMaxAttempts int    `json:"retry_max_attempts"`
	RetryBaseDelayMs int    `json:"retry_base_delay_ms"`
	RetryMaxDelayMs  int    `json:"retry_max_delay_ms"`
	Enabled          bool   `json:"enabled"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

type Notification struct {
	ID             string  `json:"id"`
	Supplier       string  `json:"supplier"`
	URL            string  `json:"url"`
	Method         string  `json:"method"`
	Headers        string  `json:"headers"`
	Body           string  `json:"body"`
	IdempotencyKey string  `json:"idempotency_key"`
	Status         string  `json:"status"`
	AttemptCount   int     `json:"attempt_count"`
	MaxAttempts    int     `json:"max_attempts"`
	NextRetryAt    *string `json:"next_retry_at"`
	DeadReason     *string `json:"dead_reason"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

type DeliveryAttempt struct {
	ID               int64   `json:"id"`
	NotificationID   string  `json:"notification_id"`
	AttemptNumber    int     `json:"attempt_number"`
	Status           string  `json:"status"`
	ResponseStatus   *int    `json:"response_status"`
	ResponseBody     *string `json:"response_body"`
	ErrorMessage     *string `json:"error_message"`
	AttemptedAt      string  `json:"attempted_at"`
}
