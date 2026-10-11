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
	"syscall"
	"time"
)

func main() {
	cfg := config.Load()
	// SIGTERM is how Kubernetes and docker stop a container; it starts the same drain as Ctrl+C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database unavailable", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	for _, err := range []error{ats.ValidateGreenhouseBase(cfg.ATSGreenhouseURL), ats.ValidateAshbyBase(cfg.ATSAshbyURL), ats.ValidateRecruiteeBase(cfg.ATSRecruiteeURL)} {
		if err != nil {
			slog.Error("invalid configuration", "error", err)
			os.Exit(1)
		}
	}
	dataStore := store.New(pool)
	// Catalog upkeep for jobs stored before a feature existed runs in the
	// background, so a large catalog does not hold up startup. No step is
	// needed to serve requests, so a failure is logged rather than fatal: an
	// unnamed job stays reachable by id, a description keeps its old text, and
	// an older copy of a reposted job stays open.
	go func() {
		if named, err := dataStore.NameUnnamedJobs(ctx); err != nil {
			slog.Error("naming jobs failed", "error", err, "named", named)
		} else if named > 0 {
			slog.Info("named jobs", "count", named)
		}
		// Descriptions stored before Readable existed can still hold tags or
		// entities; convert them once. Later runs find nothing to do.
		if rewritten, err := dataStore.RewriteMarkedUpDescriptions(ctx, discovery.Readable); err != nil {
			slog.Error("tidying job descriptions failed", "error", err, "rewritten", rewritten)
		} else if rewritten > 0 {
			slog.Info("tidied job descriptions", "count", rewritten)
		}
		// Openings a board reposted were stored once per posting until
		// reposts were recognised; close the older copies once.
		if closed, err := discovery.CloseStoredReposts(ctx, dataStore); err != nil {
			slog.Error("closing older copies of reposted jobs failed", "error", err, "closed", closed)
		} else if closed > 0 {
			slog.Info("closed older copies of reposted jobs", "count", closed)
		}
	}()
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
	discoveryService.Fetcher.UseGreenhouseOrigin(cfg.ATSGreenhouseURL)
	go discoveryService.Start(ctx)
	srv := &http.Server{Addr: cfg.Addr, Handler: (&httpapi.Server{Store: dataStore, Discovery: discoveryService, LLM: aiClient, ATS: formReader(cfg), Secret: cfg.JWTSecret, TokenTTL: cfg.TokenTTL, FrontendURL: cfg.FrontendURL}).Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 2 * time.Minute, WriteTimeout: 2 * time.Minute, IdleTimeout: 60 * time.Second}
	// ListenAndServe returns as soon as Shutdown starts; main waits on drained so requests in flight
	// get up to 10 seconds to finish (well inside Kubernetes' 30-second grace period).
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("shutdown did not finish draining", "error", err)
		}
	}()
	slog.Info("JobPilot API listening", "addr", cfg.Addr)
	if err = srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
	<-drained
}

// formReader reads employer forms from the ATS origins the configuration names.
func formReader(cfg config.Config) *ats.Reader {
	return ats.NewReader().WithGreenhouseBase(cfg.ATSGreenhouseURL).WithAshbyBase(cfg.ATSAshbyURL).WithRecruiteeBase(cfg.ATSRecruiteeURL)
}
