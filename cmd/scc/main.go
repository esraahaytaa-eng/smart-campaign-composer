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
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: scc migrate|api")
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
	case "migrate", "api":
	default:
		return fmt.Errorf("unknown process %q (want migrate or api)", process)
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

	if process == "migrate" {
		return migrate(ctx, cfg, pool)
	}
	return serveAPI(ctx, cfg, pool)
}

// migrate applies the schema and seeds the demo data once (D20, D28).
func migrate(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) error {
	if err := db.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrations: %w", err)
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
	model := llm.LLM(llm.NewMock())
	if cfg.LLMMode == "real" {
		model = llm.NewAnthropic(cfg.AnthropicAPIKey, cfg.AnthropicModel)
	}
	mux := http.NewServeMux()
	obs.Routes(mux, pool)
	api := auth.Routes(mux, pool, cfg.LLMMode)
	(&campaign.Handler{Pool: pool, LLM: model, Timeout: cfg.LLMTimeout, Now: time.Now}).Routes(api)
	// No WriteTimeout: SSE responses (T8, T14) stay open.
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("api listening", "addr", cfg.HTTPAddr, "llm_mode", cfg.LLMMode)

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
