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

	"github.com/jackc/pgx/v5/pgxpool"

	"smart-campaign-composer/internal/config"
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
		// shortcut: only checks the connection; T2 adds the schema migrations and seed.
		if err := pool.Ping(ctx); err != nil {
			return fmt.Errorf("database unreachable: %w", err)
		}
		slog.Info("migrate: database reachable, no migrations yet")
		return nil
	}
	return serveAPI(ctx, cfg, pool)
}

func serveAPI(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) error {
	mux := http.NewServeMux()
	obs.Routes(mux, pool)
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
