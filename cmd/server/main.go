package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"rc_stewarthuang/internal/api"
	"rc_stewarthuang/internal/config"
	"rc_stewarthuang/internal/db"
	"rc_stewarthuang/internal/model"
	"rc_stewarthuang/internal/worker"
)

func main() {
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	store, err := db.NewStore(cfg.Database.Path)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer store.Close()

	// Sync suppliers from config to DB
	suppliers := make([]model.Supplier, 0, len(cfg.Suppliers))
	now := time.Now().UTC().Format(time.RFC3339)
	for _, sc := range cfg.Suppliers {
		headersJSON := "{}"
		if len(sc.Headers) > 0 {
			b, _ := json.Marshal(sc.Headers)
			headersJSON = string(b)
		}
		baseDelay := parseDuration(sc.Retry.BaseDelay, 1000)
		maxDelay := parseDuration(sc.Retry.MaxDelay, 240000)
		maxAttempts := sc.Retry.MaxAttempts
		if maxAttempts == 0 {
			maxAttempts = 15
		}
		suppliers = append(suppliers, model.Supplier{
			Name:             sc.Name,
			URL:              sc.URL,
			Method:           sc.Method,
			Headers:          headersJSON,
			RetryMaxAttempts: maxAttempts,
			RetryBaseDelayMs: baseDelay,
			RetryMaxDelayMs:  maxDelay,
			Enabled:          true,
			CreatedAt:        now,
			UpdatedAt:        now,
		})
	}
	if err := store.SyncSuppliersFromConfig(suppliers); err != nil {
		log.Fatalf("failed to sync suppliers: %v", err)
	}

	app := api.NewApp(store)
	w := worker.NewWorker(store, &cfg.Worker)
	go w.Start()

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: app.Router,
	}

	go func() {
		log.Printf("server starting on :%d", cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")
	w.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}

func parseDuration(s string, defaultMS int) int {
	if s == "" {
		return defaultMS
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return defaultMS
	}
	return int(d.Milliseconds())
}
