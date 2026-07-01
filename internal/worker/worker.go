package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	// Wait for all goroutines in this batch to finish
	w.wg.Wait()
}

func (w *Worker) deliver(n model.Notification) {
	now := time.Now().UTC().Format(time.RFC3339)

	bodyReader := bytes.NewReader([]byte(n.Body))
	req, err := http.NewRequestWithContext(
		w.ctx,
		n.Method,
		n.URL,
		bodyReader,
	)
	if err != nil {
		w.recordFailure(&n, nil, err.Error(), now)
		return
	}

	var headers map[string]string
	json.Unmarshal([]byte(n.Headers), &headers)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		w.recordFailure(&n, nil, err.Error(), now)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	respStatus := resp.StatusCode

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		w.recordSuccess(&n, respStatus, string(respBody), now)
	} else {
		w.recordFailure(&n, &respStatus, string(respBody), now)
	}
}

func (w *Worker) recordSuccess(n *model.Notification, status int, body string, now string) {
	n.AttemptCount++
	n.Status = "delivered"
	n.UpdatedAt = now
	w.store.UpdateNotification(n)

	w.store.CreateDeliveryAttempt(&model.DeliveryAttempt{
		NotificationID: n.ID,
		AttemptNumber:  n.AttemptCount + 1,
		Status:         "success",
		ResponseStatus: &status,
		ResponseBody:   &body,
		AttemptedAt:    now,
	})
}

func (w *Worker) recordFailure(n *model.Notification, status *int, errMsg string, now string) {
	n.AttemptCount++
	n.UpdatedAt = now

	attempt := &model.DeliveryAttempt{
		NotificationID: n.ID,
		AttemptNumber:  n.AttemptCount,
		Status:         "failed",
		ResponseStatus: status,
		ErrorMessage:   &errMsg,
		AttemptedAt:    now,
	}
	if status != nil {
		body := ""
		attempt.ResponseBody = &body
	}
	w.store.CreateDeliveryAttempt(attempt)

	if n.AttemptCount >= n.MaxAttempts {
		n.Status = "dead"
		reason := fmt.Sprintf("max retries exceeded (%d attempts)", n.AttemptCount)
		n.DeadReason = &reason
	} else {
		n.Status = "failed"
		delay := calculateNextRetry(n.AttemptCount, w.getBaseDelay(*n), w.getMaxDelay(*n))
		nextRetry := time.Now().UTC().Add(delay).Format(time.RFC3339)
		n.NextRetryAt = &nextRetry
	}
	w.store.UpdateNotification(n)
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
