package main

import (
	"context"
	"jobpilot/core/api/internal/ats"
	"jobpilot/core/api/internal/config"
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/discovery"
	"jobpilot/core/api/internal/httpapi"
	"jobpilot/core/api/internal/llm"
	"jobpilot/core/api/internal/store"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"
)

func main() {
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database unavailable", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := ats.ValidateGreenhouseBase(cfg.ATSGreenhouseURL); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	dataStore := store.New(pool)
	aiClient, err := llm.Build(llm.Options{
		Provider:     cfg.LLM.Provider,
		Model:        cfg.LLM.Model,
		CLICommand:   cfg.LLM.CLICommand,
		CLIArgs:      cfg.LLM.CLIArgs,
		OllamaURL:    cfg.LLM.OllamaURL,
		Timeout:      cfg.LLM.Timeout,
		CallsPerHour: cfg.LLM.CallsPerHour,
	}, dataStore)
	if err != nil {
		slog.Error("invalid AI configuration", "error", err)
		os.Exit(1)
	}
	slog.Info("AI adapter configured", "provider", aiClient.Describe())
	discoveryService := discovery.NewService(dataStore)
	go discoveryService.Start(ctx)
	srv := &http.Server{Addr: cfg.Addr, Handler: (&httpapi.Server{Store: dataStore, Discovery: discoveryService, LLM: aiClient, ATS: ats.NewReader().WithGreenhouseBase(cfg.ATSGreenhouseURL), Secret: cfg.JWTSecret, TokenTTL: cfg.TokenTTL, FrontendURL: cfg.FrontendURL}).Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 2 * time.Minute, WriteTimeout: 2 * time.Minute, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	slog.Info("JobPilot API listening", "addr", cfg.Addr)
	if err = srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
