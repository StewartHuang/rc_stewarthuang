package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"rc_stewarthuang/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type supplierRequest struct {
	Name             string            `json:"name"`
	URL              string            `json:"url"`
	Method           string            `json:"method"`
	Headers          map[string]string `json:"headers"`
	Retry            *retryRequest     `json:"retry"`
	AcceptedStatuses []int             `json:"accepted_statuses"`
}

type retryRequest struct {
	MaxAttempts int    `json:"max_attempts"`
	BaseDelay   string `json:"base_delay"`
	MaxDelay    string `json:"max_delay"`
}

func (a *App) ListSuppliers(c *gin.Context) {
	suppliers, err := a.Store.ListSuppliers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, suppliers)
}

func (a *App) GetSupplier(c *gin.Context) {
	sup, err := a.Store.GetSupplier(c.Param("name"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("supplier %q not found", c.Param("name"))})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, sup)
}

func (a *App) CreateSupplier(c *gin.Context) {
	var req supplierRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.Name == "" || req.URL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and url are required"})
		return
	}
	method := req.Method
	if method == "" {
		method = "POST"
	}
	headersJSON := "{}"
	if len(req.Headers) > 0 {
		b, _ := json.Marshal(req.Headers)
		headersJSON = string(b)
	}
	sup := model.Supplier{
		Name: req.Name, URL: req.URL, Method: method,
		Headers:          headersJSON,
		RetryMaxAttempts: 15, RetryBaseDelayMs: 1000, RetryMaxDelayMs: 240000,
		AcceptedStatuses: "[200]",
		Enabled:          true,
	}
	if req.Retry != nil {
		if req.Retry.MaxAttempts > 0 {
			sup.RetryMaxAttempts = req.Retry.MaxAttempts
		}
		if d, err := time.ParseDuration(req.Retry.BaseDelay); err == nil {
			sup.RetryBaseDelayMs = int(d.Milliseconds())
		}
		if d, err := time.ParseDuration(req.Retry.MaxDelay); err == nil {
			sup.RetryMaxDelayMs = int(d.Milliseconds())
		}
	}
	if len(req.AcceptedStatuses) > 0 {
		b, _ := json.Marshal(req.AcceptedStatuses)
		sup.AcceptedStatuses = string(b)
	}
	if err := a.Store.CreateSupplier(&sup); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("supplier %q already exists", req.Name)})
		return
	}
	c.JSON(http.StatusCreated, sup)
}

func (a *App) UpdateSupplier(c *gin.Context) {
	name := c.Param("name")
	existing, err := a.Store.GetSupplier(name)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("supplier %q not found", name)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var req supplierRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.URL != "" {
		existing.URL = req.URL
	}
	if req.Method != "" {
		existing.Method = req.Method
	}
	if len(req.Headers) > 0 {
		b, _ := json.Marshal(req.Headers)
		existing.Headers = string(b)
	}
	if req.Retry != nil {
		if req.Retry.MaxAttempts > 0 {
			existing.RetryMaxAttempts = req.Retry.MaxAttempts
		}
		if d, err := time.ParseDuration(req.Retry.BaseDelay); err == nil {
			existing.RetryBaseDelayMs = int(d.Milliseconds())
		}
		if d, err := time.ParseDuration(req.Retry.MaxDelay); err == nil {
			existing.RetryMaxDelayMs = int(d.Milliseconds())
		}
	}
	if len(req.AcceptedStatuses) > 0 {
		b, _ := json.Marshal(req.AcceptedStatuses)
		existing.AcceptedStatuses = string(b)
	}
	if err := a.Store.UpdateSupplier(existing); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, existing)
}

func (a *App) DeleteSupplier(c *gin.Context) {
	if err := a.Store.DeleteSupplier(c.Param("name")); err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("supplier %q not found", c.Param("name"))})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
