package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"arcticexpress/internal/booking"
	"arcticexpress/internal/domain"
	"arcticexpress/internal/httpapi"
	"arcticexpress/internal/payment"
	"arcticexpress/internal/scheduler"
	"arcticexpress/internal/storage"
	"arcticexpress/internal/store"
	"arcticexpress/internal/voyage"
)

type rawConfig struct {
	Port              int    `json:"port"`
	StorePath         string `json:"store_path"`
	HoldDuration      string `json:"hold_duration"`
	CutoffThreshold   string `json:"cutoff_threshold"`
	PortChangeWindow  string `json:"port_change_window"`
	SchedulerInterval string `json:"scheduler_interval"`
}

type config struct {
	port              int
	storePath         string
	holdDuration      time.Duration
	cutoffThreshold   time.Duration
	portChangeWindow  time.Duration
	schedulerInterval time.Duration
}

func loadConfig(path string) (config, error) {
	c := config{
		port:              58594,
		storePath:         "data/store.json",
		holdDuration:      2 * time.Hour,
		cutoffThreshold:   48 * time.Hour,
		portChangeWindow:  72 * time.Hour,
		schedulerInterval: 30 * time.Second,
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	var raw rawConfig
	if err := json.Unmarshal(b, &raw); err != nil {
		return c, fmt.Errorf("parse config: %w", err)
	}
	if raw.Port > 0 {
		c.port = raw.Port
	}
	if raw.StorePath != "" {
		c.storePath = raw.StorePath
	}
	if raw.HoldDuration != "" {
		d, err := time.ParseDuration(raw.HoldDuration)
		if err != nil {
			return c, fmt.Errorf("hold_duration: %w", err)
		}
		c.holdDuration = d
	}
	if raw.CutoffThreshold != "" {
		d, err := time.ParseDuration(raw.CutoffThreshold)
		if err != nil {
			return c, fmt.Errorf("cutoff_threshold: %w", err)
		}
		c.cutoffThreshold = d
	}
	if raw.PortChangeWindow != "" {
		d, err := time.ParseDuration(raw.PortChangeWindow)
		if err != nil {
			return c, fmt.Errorf("port_change_window: %w", err)
		}
		c.portChangeWindow = d
	}
	if raw.SchedulerInterval != "" {
		d, err := time.ParseDuration(raw.SchedulerInterval)
		if err != nil {
			return c, fmt.Errorf("scheduler_interval: %w", err)
		}
		c.schedulerInterval = d
	}
	return c, nil
}

func main() {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config.json"
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	st, err := store.New(cfg.storePath)
	if err != nil {
		slog.Error("failed to open store", "error", err)
		os.Exit(1)
	}

	clock := domain.RealClock{}
	bookingSvc := booking.New(st, clock, booking.Config{
		HoldDuration:     cfg.holdDuration,
		CutoffThreshold:  cfg.cutoffThreshold,
		PortChangeWindow: cfg.portChangeWindow,
	})
	voyageSvc := voyage.New(st, clock)
	storageSvc := storage.New(st)
	paymentSvc := payment.New(st, clock)

	sched := scheduler.New(st, bookingSvc, clock, scheduler.Config{
		Interval:        cfg.schedulerInterval,
		CutoffThreshold: cfg.cutoffThreshold,
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go sched.Run(ctx)

	handler := httpapi.NewHandler(bookingSvc, voyageSvc, storageSvc, paymentSvc)
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.port),
		Handler:      httpapi.Router(handler),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	slog.Info("arctic express server starting", "port", cfg.port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
	slog.Info("server stopped")
}
