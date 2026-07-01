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
	pollInterval, _ := time.ParseDuration(cw.cfg.PollInterval)
	if pollInterval == 0 {
		pollInterval = 2 * time.Second
	}
	ticker := time.NewTicker(pollInterval)
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
	cb.NextRetryAt = nil
	if err := cw.store.UpdateCallback(cb); err != nil {
		log.Printf("callback_worker: failed to update callback %d: %v", cb.ID, err)
	}
	if err := cw.store.CreateCallbackAttempt(&model.CallbackAttempt{
		CallbackID:     cb.ID,
		AttemptNumber:  cb.AttemptCount + 1,
		Status:         "success",
		ResponseStatus: &status,
		ResponseBody:   &body,
		AttemptedAt:    now,
	}); err != nil {
		log.Printf("callback_worker: failed to record callback attempt for %d: %v", cb.ID, err)
	}
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
		cb.NextRetryAt = nil
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
