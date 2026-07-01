# 回调通知 & 自定义 Status Code 接受范围 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add callback notification when delivery reaches terminal state, and allow suppliers to configure which HTTP status codes count as success.

**Architecture:** Callback uses independent `callbacks` table + separate CallbackWorker polling (2s interval). Status code acceptance uses `accepted_statuses` JSON list column on `suppliers` table, replacing hardcoded 2xx check in `deliver()`.

**Tech Stack:** Go, GORM, modernc.org/sqlite, Gin

## Global Constraints

- All validation in application layer, no DB constraints (CHECK/foreign keys)
- Use `json.RawMessage` for body pass-through
- GORM `AutoMigrate` for schema management
- Tests use SQLite `:memory:` mode
- Retry delay formula preserved: `min(base_delay * 2^attempt, max_delay) * (1 + random(0, 50%))`

---

### Task 1: Data model changes + DB migration

**Files:**
- Modify: `internal/model/types.go` — Supplier add AcceptedStatuses, Notification add CallbackURL, add Callback/CallbackAttempt structs
- Modify: `internal/db/db.go` — AutoMigrate new models

**Interfaces:**
- Produces: `model.Supplier.AcceptedStatuses string`, `model.Notification.CallbackURL *string`, `model.Callback`, `model.CallbackAttempt`

- [ ] **Step 1: Add AcceptedStatuses to Supplier, CallbackURL to Notification, and new models in types.go**

```go
// Add to Supplier struct (after RetryMaxDelayMs):
AcceptedStatuses string `gorm:"type:text;not null;default:'[200]'" json:"accepted_statuses"`

// Add to Notification struct (after IdempotencyKey):
CallbackURL *string `gorm:"size:2048" json:"callback_url"`

// New structs after DeliveryAttempt:
type Callback struct {
	ID                 uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	NotificationID     string     `gorm:"size:36;not null" json:"notification_id"`
	NotificationStatus string     `gorm:"size:20;not null" json:"notification_status"`
	CallbackURL        string     `gorm:"size:2048;not null" json:"callback_url"`
	Status             string     `gorm:"size:20;not null;default:pending;index:idx_callbacks_status_next_retry" json:"status"`
	AttemptCount   int        `gorm:"not null;default:0" json:"attempt_count"`
	MaxAttempts    int        `gorm:"not null;default:3" json:"max_attempts"`
	RetryDelayMs   int        `gorm:"not null;default:10000" json:"retry_delay_ms"`
	LastError      *string    `gorm:"type:text" json:"last_error"`
	NextRetryAt    *time.Time `gorm:"index:idx_callbacks_status_next_retry" json:"next_retry_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
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
```

- [ ] **Step 2: Update AutoMigrate in db.go**

```go
// Replace the AutoMigrate line:
if err := db.AutoMigrate(&model.Supplier{}, &model.Notification{}, &model.DeliveryAttempt{}, &model.Callback{}, &model.CallbackAttempt{}); err != nil {
```

- [ ] **Step 3: Run tests to verify migration works**

Run: `go test ./internal/db/ -v`
Expected: PASS (no existing tests break)

- [ ] **Step 4: Commit**

```bash
git add internal/model/types.go internal/db/db.go
git commit -m "feat: add callback models and accepted_statuses to supplier"
```

---

### Task 2: Callback DB CRUD

**Files:**
- Create: `internal/db/callback.go`

**Interfaces:**
- Produces: `CreateCallback`, `FindPendingCallbacks`, `GetCallback`, `UpdateCallback`, `CreateCallbackAttempt`, `ListCallbackAttempts`
- Consumes: `model.Callback`, `model.CallbackAttempt`

- [ ] **Step 1: Write callback.go**

```go
package db

import (
	"time"
	"rc_stewarthuang/internal/model"
	"gorm.io/gorm"
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
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/db/ -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/db/callback.go
git commit -m "feat: add callback DB CRUD operations"
```

---

### Task 3: Supplier API + config for accepted_statuses

**Files:**
- Modify: `internal/config/config.go` — SupplierEntry add AcceptedStatuses
- Modify: `internal/db/supplier.go` — SyncSuppliersFromConfig write accepted_statuses
- Modify: `internal/api/supplier.go` — CreateSupplier/UpdateSupplier accept accepted_statuses
- Modify: `cmd/server/main.go` — build supplier with accepted_statuses
- Modify: `config.yaml` — example accepted_statuses
- Test: `internal/api/supplier_test.go` — test accepted_statuses

**Interfaces:**
- Produces: API endpoints accept `accepted_statuses: [200, 201]`; config parser reads `accepted_statuses: [200, 201]`

- [ ] **Step 1: Add AcceptedStatuses to config SupplierEntry**

```go
// internal/config/config.go - add to SupplierEntry
type SupplierEntry struct {
	Name             string            `yaml:"name"`
	URL              string            `yaml:"url"`
	Method           string            `yaml:"method"`
	Headers          map[string]string `yaml:"headers"`
	Retry            RetryConfig       `yaml:"retry"`
	AcceptedStatuses []int             `yaml:"accepted_statuses"`
}
```

- [ ] **Step 2: Update SyncSuppliersFromConfig to write accepted_statuses**

```go
// internal/db/supplier.go - add a helper to the file:
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
```

Note: The `SyncSuppliersFromConfig` takes `[]model.Supplier` where `AcceptedStatuses` is a JSON string. The config parsing step below will set it properly. We need to make sure the config-to-model mapping serializes the `[]int` to a JSON string.

- [ ] **Step 3: Update main.go to parse accepted_statuses from config**

```go
// In cmd/server/main.go, after the maxAttempts check:
		acceptedStatuses := "[200]"
		if len(sc.AcceptedStatuses) > 0 {
			b, _ := json.Marshal(sc.AcceptedStatuses)
			acceptedStatuses = string(b)
		}
		suppliers = append(suppliers, model.Supplier{
			Name:             sc.Name,
			URL:              sc.URL,
			Method:           sc.Method,
			Headers:          headersJSON,
			RetryMaxAttempts: maxAttempts,
			RetryBaseDelayMs: baseDelay,
			RetryMaxDelayMs:  maxDelay,
			AcceptedStatuses: acceptedStatuses,
			Enabled:          true,
		})
```

- [ ] **Step 4: Update CreateSupplier/UpdateSupplier API handlers**

`internal/api/supplier.go` — add to `supplierRequest` struct:
```go
type supplierRequest struct {
	Name             string            `json:"name"`
	URL              string            `json:"url"`
	Method           string            `json:"method"`
	Headers          map[string]string `json:"headers"`
	Retry            *retryRequest     `json:"retry"`
	AcceptedStatuses []int             `json:"accepted_statuses"`
}
```

In `CreateSupplier`, after the retry block:
```go
	if len(req.AcceptedStatuses) > 0 {
		b, _ := json.Marshal(req.AcceptedStatuses)
		sup.AcceptedStatuses = string(b)
	}
```

In `UpdateSupplier`, after the retry block:
```go
	if len(req.AcceptedStatuses) > 0 {
		b, _ := json.Marshal(req.AcceptedStatuses)
		existing.AcceptedStatuses = string(b)
	}
```

- [ ] **Step 5: Update config.yaml with accepted_statuses example**

```yaml
  - name: ad-system
    url: https://api.adsystem.com/notify
    method: POST
    accepted_statuses: [200, 201]
    headers:
      Content-Type: application/json
      Authorization: "Bearer ${AD_SYSTEM_TOKEN}"
    retry:
      max_attempts: 15
      base_delay: 1s
      max_delay: 240s
```

- [ ] **Step 6: Write tests for accepted_statuses in supplier API**

Add to `internal/api/supplier_test.go`:

```go
func TestCreateSupplierWithAcceptedStatuses(t *testing.T) {
	app, _ := newTestApp(t)
	body := `{"name":"status-sup","url":"https://status.com","method":"POST","accepted_statuses":[200,201,204]}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/suppliers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	app.Router.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var sup model.Supplier
	json.Unmarshal(w.Body.Bytes(), &sup)
	if sup.AcceptedStatuses != `[200,201,204]` {
		t.Fatalf("expected [200,201,204], got %s", sup.AcceptedStatuses)
	}
}

func TestUpdateSupplierAcceptedStatuses(t *testing.T) {
	app, s := newTestApp(t)
	s.CreateSupplier(&model.Supplier{
		Name: "upd-status", URL: "https://old.com", Method: "POST",
		Headers: "{}", Enabled: true, AcceptedStatuses: "[200]",
	})
	body := `{"accepted_statuses":[200,202]}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/suppliers/upd-status", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	app.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	updated, _ := s.GetSupplier("upd-status")
	if updated.AcceptedStatuses != `[200,202]` {
		t.Fatalf("expected [200,202], got %s", updated.AcceptedStatuses)
	}
}
```

- [ ] **Step 7: Run tests**

Run: `go test ./internal/api/ -v`
Expected: all tests PASS including new ones
Run: `go build ./cmd/server`
Expected: BUILD OK

- [ ] **Step 8: Commit**

```bash
git add internal/config/config.go internal/db/supplier.go internal/api/supplier.go cmd/server/main.go config.yaml internal/api/supplier_test.go
git commit -m "feat: add accepted_statuses to supplier config and API"
```

---

### Task 4: Notification API for callback_url

**Files:**
- Modify: `internal/api/notification.go` — SubmitNotification accept callback_url
- Test: `internal/api/notification_test.go` — test callback_url

- [ ] **Step 1: Update notificationRequest struct and SubmitNotification handler**

```go
type notificationRequest struct {
	Supplier       string            `json:"supplier"`
	URL            string            `json:"url"`
	Method         string            `json:"method"`
	Headers        map[string]string `json:"headers"`
	Body           json.RawMessage   `json:"body"`
	IdempotencyKey string            `json:"idempotency_key"`
	CallbackURL    string            `json:"callback_url"`
}
```

In `SubmitNotification` handler, after the `MaxAttempts` line in the Notification literal:
```go
	n := model.Notification{
		...
		MaxAttempts: sup.RetryMaxAttempts,
	}
```

Add after `if req.IdempotencyKey != "" {` block:
```go
	if req.CallbackURL != "" {
		n.CallbackURL = &req.CallbackURL
	}
```

- [ ] **Step 2: Write test for callback_url**

Add to `internal/api/notification_test.go`:

```go
func TestSubmitNotificationWithCallbackURL(t *testing.T) {
	app, s := newTestApp(t)
	seedTestSupplier(t, s)

	body := `{"supplier":"test-supplier","body":{"user_id":1},"callback_url":"https://biz.company.com/callback"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/notifications", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	app.Router.ServeHTTP(w, req)
	if w.Code != 202 {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	n, err := s.GetNotification(resp["id"])
	if err != nil {
		t.Fatal(err)
	}
	if n.CallbackURL == nil || *n.CallbackURL != "https://biz.company.com/callback" {
		t.Fatalf("expected callback_url, got %v", n.CallbackURL)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/api/ -v`
Expected: all tests PASS

- [ ] **Step 4: Commit**

```bash
git add internal/api/notification.go internal/api/notification_test.go
git commit -m "feat: add callback_url support to notification submission"
```

---

### Task 5: Worker logic changes

**Files:**
- Modify: `internal/worker/worker.go` — deliver checks accepted_statuses, recordSuccess/recordFailure insert callback at terminal states
- Test: `internal/worker/worker_test.go` — test accepted_statuses and callback insertion

**Interfaces:**
- Consumes: `model.Supplier.AcceptedStatuses`, `model.Notification.CallbackURL`, `store.CreateCallback`

- [ ] **Step 1: Update deliver() to check accepted_statuses**

Current `deliver()`:
```go
func (w *Worker) deliver(n model.Notification) {
	bodyReader := bytes.NewReader([]byte(n.Body))
	req, err := http.NewRequestWithContext(
		w.ctx, n.Method, n.URL, bodyReader,
	)
	if err != nil {
		w.recordFailure(&n, nil, err.Error())
		return
	}
	var headers map[string]string
	json.Unmarshal([]byte(n.Headers), &headers)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		w.recordFailure(&n, nil, err.Error())
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	respStatus := resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		w.recordSuccess(&n, respStatus, string(respBody))
	} else {
		w.recordFailure(&n, &respStatus, string(respBody))
	}
}
```

Replace with:
```go
func (w *Worker) deliver(n model.Notification) {
	bodyReader := bytes.NewReader([]byte(n.Body))
	req, err := http.NewRequestWithContext(
		w.ctx, n.Method, n.URL, bodyReader,
	)
	if err != nil {
		w.recordFailure(&n, nil, err.Error())
		return
	}
	var headers map[string]string
	json.Unmarshal([]byte(n.Headers), &headers)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		w.recordFailure(&n, nil, err.Error())
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	respStatus := resp.StatusCode

	sup, supErr := w.store.GetSupplier(n.Supplier)
	if supErr != nil {
		log.Printf("worker: failed to get supplier %s: %v", n.Supplier, supErr)
		w.recordFailure(&n, &respStatus, string(respBody))
		return
	}

	var acceptedStatuses []int
	json.Unmarshal([]byte(sup.AcceptedStatuses), &acceptedStatuses)
	success := false
	for _, s := range acceptedStatuses {
		if respStatus == s {
			success = true
			break
		}
	}
	if success {
		w.recordSuccess(&n, respStatus, string(respBody))
	} else {
		w.recordFailure(&n, &respStatus, string(respBody))
	}
}
```

Add import for `"encoding/json"` if not present (should already be imported).

- [ ] **Step 2: Update recordSuccess to insert callback at terminal state**

```go
func (w *Worker) recordSuccess(n *model.Notification, status int, body string) {
	attemptNumber := n.AttemptCount + 1
	n.AttemptCount = attemptNumber
	n.Status = "delivered"
	if err := w.store.UpdateNotification(n); err != nil {
		log.Printf("worker: failed to update notification %s: %v", n.ID, err)
	}

	if err := w.store.CreateDeliveryAttempt(&model.DeliveryAttempt{
		NotificationID: n.ID,
		AttemptNumber:  attemptNumber,
		Status:         "success",
		ResponseStatus: &status,
		ResponseBody:   &body,
		AttemptedAt:    time.Now().UTC(),
	}); err != nil {
		log.Printf("worker: failed to record delivery attempt for %s: %v", n.ID, err)
	}

	// Insert callback record if notification has a callback_url
	if n.CallbackURL != nil && *n.CallbackURL != "" {
		w.insertCallback(n.ID, *n.CallbackURL, n.Status)
	}
}
```

- [ ] **Step 3: Update recordFailure to insert callback at dead state**

In `recordFailure`, after the `if n.AttemptCount >= n.MaxAttempts` block, add:
```go
	// Insert callback record if entering dead state and has a callback_url
	if n.Status == "dead" && n.CallbackURL != nil && *n.CallbackURL != "" {
		w.insertCallback(n.ID, *n.CallbackURL, n.Status)
	}
```

- [ ] **Step 4: Add insertCallback helper method**

```go
func (w *Worker) insertCallback(notificationID, callbackURL, notifStatus string) {
	now := time.Now().UTC()
	cb := &model.Callback{
		NotificationID:     notificationID,
		NotificationStatus: notifStatus,
		CallbackURL:        callbackURL,
		Status:             "pending",
		MaxAttempts:        3,
		RetryDelayMs:       10000,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := w.store.CreateCallback(cb); err != nil {
		log.Printf("worker: failed to create callback record for %s: %v", notificationID, err)
	}
}
```

- [ ] **Step 5: Write tests**

Add to `internal/worker/worker_test.go`:

```go
func TestAcceptedStatuses(t *testing.T) {
	s := newTestStore(t)
	s.CreateSupplier(&model.Supplier{
		Name: "custom-status", URL: "http://localhost:19997/notify", Method: "POST",
		Headers: "{}", Enabled: true,
		RetryMaxAttempts: 3, RetryBaseDelayMs: 100, RetryMaxDelayMs: 1000,
		AcceptedStatuses: "[201]", // only accept 201
	})
	s.CreateNotification(&model.Notification{
		ID: "cs1", Supplier: "custom-status",
		URL: "http://localhost:19997/notify", Method: "POST",
		Headers: "{}", Body: `{}`,
		Status: "pending", MaxAttempts: 3,
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated) // 201
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	n, _ := s.GetNotification("cs1")
	n.URL = server.URL
	s.UpdateNotification(n)

	cfg := &config.WorkerConfig{
		PollInterval: "100ms", MaxConcurrency: 5, HTTPTimeout: "5s",
	}
	w := NewWorker(s, cfg)
	go w.Start()
	time.Sleep(500 * time.Millisecond)
	w.Stop()

	updated, _ := s.GetNotification("cs1")
	if updated.Status != "delivered" {
		t.Fatalf("expected delivered, got %s", updated.Status)
	}
}

func TestCallbackInsertedOnDelivered(t *testing.T) {
	s := newTestStore(t)
	cbURL := "https://biz.company.com/callback"
	s.CreateSupplier(&model.Supplier{
		Name: "cb-sup", URL: "http://localhost:19996/notify", Method: "POST",
		Headers: "{}", Enabled: true,
		RetryMaxAttempts: 3, RetryBaseDelayMs: 100, RetryMaxDelayMs: 1000,
	})
	s.CreateNotification(&model.Notification{
		ID: "cb1", Supplier: "cb-sup",
		URL: "http://localhost:19996/notify", Method: "POST",
		Headers: "{}", Body: `{}`,
		Status: "pending", MaxAttempts: 3,
		CallbackURL: &cbURL,
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	n, _ := s.GetNotification("cb1")
	n.URL = server.URL
	s.UpdateNotification(n)

	cfg := &config.WorkerConfig{
		PollInterval: "100ms", MaxConcurrency: 5, HTTPTimeout: "5s",
	}
	w := NewWorker(s, cfg)
	go w.Start()
	time.Sleep(500 * time.Millisecond)
	w.Stop()

	// Verify callback record was created
	callbacks, _ := s.FindPendingCallbacks(10)
	if len(callbacks) != 1 {
		t.Fatalf("expected 1 callback, got %d", len(callbacks))
	}
	if callbacks[0].NotificationID != "cb1" {
		t.Fatalf("expected cb1, got %s", callbacks[0].NotificationID)
	}
}
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/worker/ -v`
Expected: all tests PASS (existing + new)

- [ ] **Step 7: Commit**

```bash
git add internal/worker/worker.go internal/worker/worker_test.go
git commit -m "feat: use accepted_statuses in worker, insert callback at terminal states"
```

---

### Task 6: CallbackWorker

**Files:**
- Create: `internal/worker/callback_worker.go`
- Test: `internal/worker/callback_worker_test.go`

- [ ] **Step 1: Write callback_worker.go**

```go
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"rc_stewarthuang/internal/config"
	"rc_stewarthuang/internal/db"
	"rc_stewarthuang/internal/model"
)

type CallbackWorker struct {
	store    *db.Store
	cfg      *config.WorkerConfig
	client   *http.Client
	ctx      context.Context
	cancel   context.CancelFunc
	stopChan chan struct{}
	wg       sync.WaitGroup
}

func NewCallbackWorker(store *db.Store, cfg *config.WorkerConfig) *CallbackWorker {
	timeout, _ := time.ParseDuration(cfg.HTTPTimeout)
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &CallbackWorker{
		store: store,
		cfg:   cfg,
		client: &http.Client{
			Timeout: timeout,
		},
		ctx:      ctx,
		cancel:   cancel,
		stopChan: make(chan struct{}),
	}
}

func (cw *CallbackWorker) Start() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cw.processBatch()
		case <-cw.stopChan:
			return
		}
	}
}

func (cw *CallbackWorker) Stop() {
	cw.cancel()
	close(cw.stopChan)
	cw.wg.Wait()
}

func (cw *CallbackWorker) processBatch() {
	callbacks, err := cw.store.FindPendingCallbacks(5)
	if err != nil {
		log.Printf("callback_worker: failed to find pending callbacks: %v", err)
		return
	}

	sem := make(chan struct{}, 5)
	for i := range callbacks {
		select {
		case <-cw.stopChan:
			return
		case sem <- struct{}{}:
		}
		cw.wg.Add(1)
		go func(cb model.Callback) {
			defer cw.wg.Done()
			defer func() { <-sem }()
			cw.execCallback(cb)
		}(callbacks[i])
	}
	cw.wg.Wait()
}

func (cw *CallbackWorker) execCallback(cb model.Callback) {
	payload := map[string]string{
		"notification_id": cb.NotificationID,
		"status":          cb.NotificationStatus,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(cw.ctx, http.MethodPost, cb.CallbackURL, bytes.NewReader(body))
	if err != nil {
		cw.recordCallbackFailure(&cb, nil, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := cw.client.Do(req)
	if err != nil {
		cw.recordCallbackFailure(&cb, nil, err.Error())
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	respStatus := resp.StatusCode

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		cw.recordCallbackSuccess(&cb, respStatus, string(respBody))
	} else {
		cw.recordCallbackFailure(&cb, &respStatus, string(respBody))
	}
}

func (cw *CallbackWorker) recordCallbackSuccess(cb *model.Callback, status int, body string) {
	now := time.Now().UTC()
	cb.Status = "completed"
	cb.UpdatedAt = now
	if err := cw.store.UpdateCallback(cb); err != nil {
		log.Printf("callback_worker: failed to update callback %d: %v", cb.ID, err)
	}
	cw.store.CreateCallbackAttempt(&model.CallbackAttempt{
		CallbackID:    cb.ID,
		AttemptNumber: cb.AttemptCount + 1,
		Status:        "success",
		ResponseStatus: &status,
		ResponseBody:  &body,
		AttemptedAt:   now,
	})
}

func (cw *CallbackWorker) recordCallbackFailure(cb *model.Callback, status *int, errMsg string) {
	now := time.Now().UTC()
	cb.AttemptCount++
	cb.UpdatedAt = now

	attempt := &model.CallbackAttempt{
		CallbackID:     cb.ID,
		AttemptNumber:  cb.AttemptCount,
		Status:         "failed",
		ResponseStatus: status,
		AttemptedAt:    now,
	}
	if status != nil {
		attempt.ResponseBody = &errMsg
		desc := fmt.Sprintf("HTTP %d", *status)
		attempt.ErrorMessage = &desc
	} else {
		attempt.ErrorMessage = &errMsg
	}

	if cb.AttemptCount >= cb.MaxAttempts {
		cb.Status = "failed"
		cb.LastError = &errMsg
	} else {
		nextRetry := now.Add(time.Duration(cb.RetryDelayMs) * time.Millisecond)
		cb.NextRetryAt = &nextRetry
	}

	if err := cw.store.UpdateCallback(cb); err != nil {
		log.Printf("callback_worker: failed to update callback %d: %v", cb.ID, err)
	}
	if err := cw.store.CreateCallbackAttempt(attempt); err != nil {
		log.Printf("callback_worker: failed to record callback attempt for %d: %v", cb.ID, err)
	}
}
```

- [ ] **Step 2: Write callback_worker_test.go**

```go
package worker

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rc_stewarthuang/internal/config"
	"rc_stewarthuang/internal/model"
)

func TestCallbackWorkerSuccess(t *testing.T) {
	s := newTestStore(t)
	cbURL := "http://callback-test.local/cb"

	// Insert a callback record directly
	s.CreateCallback(&model.Callback{
		NotificationID:     "n1-cb",
		NotificationStatus: "delivered",
		CallbackURL:        cbURL,
		Status:             "pending",
		MaxAttempts:        3,
		RetryDelayMs:       100,
	})

	// Override with test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"received":true}`))
	}))
	defer server.Close()

	cb, _ := s.GetCallback(1)
	cb.CallbackURL = server.URL
	s.UpdateCallback(cb)

	cfg := &config.WorkerConfig{HTTPTimeout: "5s"}
	cw := NewCallbackWorker(s, cfg)
	go cw.Start()
	time.Sleep(500 * time.Millisecond)
	cw.Stop()

	updated, _ := s.GetCallback(1)
	if updated.Status != "completed" {
		t.Fatalf("expected completed, got %s", updated.Status)
	}
}

func TestCallbackWorkerRetryThenFailed(t *testing.T) {
	s := newTestStore(t)

	s.CreateCallback(&model.Callback{
		NotificationID:     "n2-cb",
		NotificationStatus: "dead",
		CallbackURL:        "http://localhost:19995/cb",
		Status:             "pending",
		MaxAttempts:        2,
		RetryDelayMs:       50,
	})

	cfg := &config.WorkerConfig{HTTPTimeout: "1s"}
	cw := NewCallbackWorker(s, cfg)
	go cw.Start()
	time.Sleep(1 * time.Second)
	cw.Stop()

	updated, _ := s.GetCallback(1)
	if updated.Status != "failed" {
		t.Fatalf("expected failed, got %s", updated.Status)
	}
	if updated.LastError == nil {
		t.Fatal("expected non-nil last_error")
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/worker/ -v`
Expected: all tests PASS

- [ ] **Step 4: Commit**

```bash
git add internal/worker/callback_worker.go internal/worker/callback_worker_test.go
git commit -m "feat: add CallbackWorker for async callback delivery with retry"
```

---

### Task 7: Wire up main.go

**Files:**
- Modify: `cmd/server/main.go` — start CallbackWorker alongside main Worker, graceful shutdown

- [ ] **Step 1: Wire CallbackWorker in main.go**

Add after the main Worker creation and start:
```go
	w := worker.NewWorker(store, &cfg.Worker)
	go w.Start()

	cw := worker.NewCallbackWorker(store, &cfg.Worker)
	go cw.Start()
```

In the shutdown section, add callback worker stop:
```go
	log.Println("shutting down...")
	w.Stop()
	cw.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
```

- [ ] **Step 2: Verify build**

Run: `go build ./cmd/server`
Expected: BUILD OK

- [ ] **Step 3: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat: wire CallbackWorker into main, graceful shutdown"
```

---

### Task 8: Final test verification

- [ ] **Step 1: Run full test suite**

Run: `go test ./...`
Expected: all tests PASS

- [ ] **Step 2: Build**

Run: `go build ./cmd/server`
Expected: BUILD OK

- [ ] **Step 3: Commit any remaining changes**

Run: `git status`
Expected: clean working tree
