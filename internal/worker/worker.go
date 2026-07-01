package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"rc_stewarthuang/internal/config"
	"rc_stewarthuang/internal/db"
	"rc_stewarthuang/internal/model"
)

type Worker struct {
	store    *db.Store
	cfg      *config.WorkerConfig
	client   *http.Client
	ctx      context.Context
	cancel   context.CancelFunc
	stopChan chan struct{}
	wg       sync.WaitGroup
}

func NewWorker(store *db.Store, cfg *config.WorkerConfig) *Worker {
	pollInterval, _ := time.ParseDuration(cfg.PollInterval)
	if pollInterval == 0 {
		pollInterval = 500 * time.Millisecond
	}
	timeout, _ := time.ParseDuration(cfg.HTTPTimeout)
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Worker{
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

func (w *Worker) Start() {
	pollInterval, _ := time.ParseDuration(w.cfg.PollInterval)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.processBatch()
		case <-w.stopChan:
			return
		}
	}
}

func (w *Worker) Stop() {
	w.cancel()
	close(w.stopChan)
	w.wg.Wait()
}

func (w *Worker) processBatch() {
	notifications, err := w.store.FindPendingNotifications(w.cfg.MaxConcurrency)
	if err != nil {
		log.Printf("worker: failed to find pending notifications: %v", err)
		return
	}

	sem := make(chan struct{}, w.cfg.MaxConcurrency)
	for i := range notifications {
		select {
		case <-w.stopChan:
			return
		case sem <- struct{}{}:
		}
		w.wg.Add(1)
		go func(n model.Notification) {
			defer w.wg.Done()
			defer func() { <-sem }()
			w.deliver(n)
		}(notifications[i])
	}
	w.wg.Wait()
}

func (w *Worker) deliver(n model.Notification) {
	bodyReader := bytes.NewReader([]byte(n.Body))
	req, err := http.NewRequestWithContext(
		w.ctx,
		n.Method,
		n.URL,
		bodyReader,
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
	if len(acceptedStatuses) == 0 {
		acceptedStatuses = []int{200}
	}
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

	if n.CallbackURL != nil && *n.CallbackURL != "" {
		w.insertCallback(n.ID, *n.CallbackURL, n.Status)
	}
}

func (w *Worker) recordFailure(n *model.Notification, status *int, errMsg string) {
	n.AttemptCount++

	attempt := &model.DeliveryAttempt{
		NotificationID: n.ID,
		AttemptNumber:  n.AttemptCount,
		Status:         "failed",
		ResponseStatus: status,
		AttemptedAt:    time.Now().UTC(),
	}
	if status != nil {
		attempt.ResponseBody = &errMsg
		desc := fmt.Sprintf("HTTP %d", *status)
		attempt.ErrorMessage = &desc
	} else {
		attempt.ErrorMessage = &errMsg
	}
	if err := w.store.CreateDeliveryAttempt(attempt); err != nil {
		log.Printf("worker: failed to record delivery attempt for %s: %v", n.ID, err)
	}

	if n.AttemptCount >= n.MaxAttempts {
		n.Status = "dead"
		reason := fmt.Sprintf("max retries exceeded (%d attempts)", n.AttemptCount)
		n.DeadReason = &reason
	} else {
		n.Status = "failed"
		delay := calculateNextRetry(n.AttemptCount, w.getBaseDelay(*n), w.getMaxDelay(*n))
		nextRetry := time.Now().UTC().Add(delay)
		n.NextRetryAt = &nextRetry
	}
	if err := w.store.UpdateNotification(n); err != nil {
		log.Printf("worker: failed to update notification %s: %v", n.ID, err)
	}

	if n.Status == "dead" && n.CallbackURL != nil && *n.CallbackURL != "" {
		w.insertCallback(n.ID, *n.CallbackURL, n.Status)
	}
}

func calculateNextRetry(attemptCount int, baseDelayMs int, maxDelayMs int) time.Duration {
	delay := float64(baseDelayMs) * math.Pow(2, float64(attemptCount))
	if delay > float64(maxDelayMs) {
		delay = float64(maxDelayMs)
	}
	jitter := float64(rand.Intn(50)) / 100.0
	delay = delay * (1 + jitter)
	return time.Duration(delay) * time.Millisecond
}

func (w *Worker) getBaseDelay(n model.Notification) int {
	sup, err := w.store.GetSupplier(n.Supplier)
	if err != nil {
		return 1000
	}
	return sup.RetryBaseDelayMs
}

func (w *Worker) getMaxDelay(n model.Notification) int {
	sup, err := w.store.GetSupplier(n.Supplier)
	if err != nil {
		return 240000
	}
	return sup.RetryMaxDelayMs
}

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
