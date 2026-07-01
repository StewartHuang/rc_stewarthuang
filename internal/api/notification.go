package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"rc_stewarthuang/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type notificationRequest struct {
	Supplier       string            `json:"supplier"`
	URL            string            `json:"url"`
	Method         string            `json:"method"`
	Headers        map[string]string `json:"headers"`
	Body           json.RawMessage   `json:"body"`
	IdempotencyKey string            `json:"idempotency_key"`
	CallbackURL    string            `json:"callback_url"`
}

func (a *App) SubmitNotification(c *gin.Context) {
	var req notificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.Supplier == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "supplier is required"})
		return
	}

	sup, err := a.Store.GetSupplier(req.Supplier)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("supplier %q not found", req.Supplier)})
		return
	}

	if req.IdempotencyKey != "" {
		existing, err := a.Store.FindByIdempotencyKey(req.IdempotencyKey)
		if err == nil && existing != nil {
			c.JSON(http.StatusOK, gin.H{"id": existing.ID, "status": "accepted"})
			return
		}
	}

	notifID := uuid.New().String()

	url := sup.URL
	if req.URL != "" {
		url = req.URL
	}
	method := sup.Method
	if req.Method != "" {
		method = req.Method
	}
	headersJSON := sup.Headers
	if len(req.Headers) > 0 {
		merged := make(map[string]string)
		json.Unmarshal([]byte(sup.Headers), &merged)
		for k, v := range req.Headers {
			merged[k] = v
		}
		b, _ := json.Marshal(merged)
		headersJSON = string(b)
	}
	bodyJSON := "{}"
	if len(req.Body) > 0 {
		bodyJSON = string(req.Body)
	}

	n := model.Notification{
		ID: notifID, Supplier: req.Supplier,
		URL: url, Method: method,
		Headers: headersJSON, Body: bodyJSON,
		Status: "pending", AttemptCount: 0,
		MaxAttempts: sup.RetryMaxAttempts,
	}
	if req.IdempotencyKey != "" {
		n.IdempotencyKey = &req.IdempotencyKey
	}
	if req.CallbackURL != "" {
		n.CallbackURL = &req.CallbackURL
	}
	if err := a.Store.CreateNotification(&n); err != nil {
		if req.IdempotencyKey != "" {
			existing, lookupErr := a.Store.FindByIdempotencyKey(req.IdempotencyKey)
			if lookupErr == nil && existing != nil {
				c.JSON(http.StatusOK, gin.H{"id": existing.ID, "status": "accepted"})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create notification"})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"id": notifID, "status": "accepted"})
}

func (a *App) GetNotification(c *gin.Context) {
	n, err := a.Store.GetNotification(c.Param("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "notification not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, n)
}

func (a *App) ListNotifications(c *gin.Context) {
	status := c.Query("status")
	if status == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "status query parameter is required"})
		return
	}
	notifications, err := a.Store.ListNotificationsByStatus(status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, notifications)
}
