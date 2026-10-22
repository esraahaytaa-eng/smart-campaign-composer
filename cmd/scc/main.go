// Command scc runs one of the app's processes: migrate or api (D20).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // the alpine runtime image has no zoneinfo; campaign time zones need it

	"github.com/jackc/pgx/v5/pgxpool"

	"smart-campaign-composer/internal/auth"
	"smart-campaign-composer/internal/campaign"
	"smart-campaign-composer/internal/config"
	"smart-campaign-composer/internal/db"
	"smart-campaign-composer/internal/llm"
	"smart-campaign-composer/internal/obs"
	"smart-campaign-composer/internal/provider"
	"smart-campaign-composer/internal/worker"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: scc migrate|api|worker|mock-provider")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1]); err != nil {
		slog.Error("exiting", "process", os.Args[1], "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, process string) error {
	switch process {
	case "migrate", "api", "worker", "mock-provider":
	default:
		return fmt.Errorf("unknown process %q (want migrate, api, worker or mock-provider)", process)
	}
	cfg, err := config.Load(process)
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database config: %w", err)
	}
	defer pool.Close()

	switch process {
	case "migrate":
		return migrate(ctx, cfg, pool)
	case "mock-provider":
		return serveProvider(ctx, cfg, pool)
	case "worker":
		return runWorker(ctx, cfg, pool)
	}
	return serveAPI(ctx, cfg, pool)
}

// migrate applies the schema and seeds the demo data once (D20, D28).
func migrate(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) error {
	if err := db.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if err := db.SetProviderPassword(ctx, pool, cfg.ProviderDBPassword); err != nil {
		return fmt.Errorf("provider role: %w", err)
	}
	if err := db.Seed(ctx, pool, db.DefaultRNGSeed, time.Now(), cfg.DemoMarketerPassword); err != nil {
		return err
	}
	var users int
	var ref time.Time
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM users), reference_time FROM seed_meta`).Scan(&users, &ref); err != nil {
		return err
	}
	slog.Info("migrate: done", "users", users, "seed_reference_time", ref)
	return nil
}

func serveAPI(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) error {
	// A short pause between mock chunks makes streaming visible in the UI.
	model := llm.LLM(&llm.Mock{ChunkDelay: 30 * time.Millisecond})
	if cfg.LLMMode == "real" {
		model = llm.NewAnthropic(cfg.AnthropicAPIKey, cfg.AnthropicModel)
	}
	mux := http.NewServeMux()
	obs.Routes(mux, pool)
	api := auth.Routes(mux, pool, cfg.LLMMode)
	(&campaign.Handler{Pool: pool, LLM: model, Timeout: cfg.LLMTimeout, Now: time.Now, Stopping: ctx.Done()}).Routes(api)
	slog.Info("api starting", "addr", cfg.HTTPAddr, "llm_mode", cfg.LLMMode)
	return serve(ctx, cfg.HTTPAddr, mux)
}

// serveProvider runs the mock SMS and email provider (D20). It connects as
// its own database role, which can only use the provider schema (D6).
func serveProvider(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) error {
	mux := http.NewServeMux()
	obs.Routes(mux, pool)
	provider.New(pool, cfg.Provider, cfg.ProviderSeed).Routes(mux)
	slog.Info("mock provider starting", "addr", cfg.HTTPAddr, "sms", cfg.Provider["sms"], "email", cfg.Provider["email"])
	return serve(ctx, cfg.HTTPAddr, mux)
}

// runWorker sends approved campaigns (D18, D19) and serves health and metrics.
func runWorker(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	mux := http.NewServeMux()
	obs.Routes(mux, pool)
	var serveErr error
	served := make(chan struct{})
	go func() {
		serveErr = serve(ctx, cfg.HTTPAddr, mux)
		cancel() // a server that cannot start stops the worker too
		close(served)
	}()

	w := &worker.Worker{Queue: &worker.Queue{Pool: pool}, ProviderURL: cfg.ProviderURL, MaxAttempts: cfg.MaxAttempts}
	slog.Info("worker starting", "provider", cfg.ProviderURL, "max_attempts", cfg.MaxAttempts)
	err := w.Run(ctx)
	cancel()
	<-served
	return errors.Join(err, serveErr)
}

// serve runs an HTTP server until ctx ends, then shuts it down gracefully.
func serve(ctx context.Context, addr string, handler http.Handler) error {
	// No WriteTimeout: SSE responses (T8, T14) and slow provider replies stay open.
	srv := &http.Server{Addr: addr, Handler: obs.Instrument(handler), ReadHeaderTimeout: 10 * time.Second}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
