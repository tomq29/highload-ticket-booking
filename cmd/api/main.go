package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tomq29/highload-ticket-booking/internal/booking"
	"github.com/tomq29/highload-ticket-booking/internal/config"
	"github.com/tomq29/highload-ticket-booking/internal/httpapi"
	"github.com/tomq29/highload-ticket-booking/internal/metrics"
	"github.com/tomq29/highload-ticket-booking/internal/postgres"
)

func main() {
	// The image is distroless, so the container healthcheck has no curl to call:
	// the binary probes itself instead.
	healthcheck := flag.Bool("healthcheck", false, "probe the local /healthz endpoint and exit")
	flag.Parse()
	if *healthcheck {
		os.Exit(probe())
	}

	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func probe() int {
	addr := os.Getenv("API_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 1
	}

	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := postgres.Migrate(cfg.DatabaseURL); err != nil {
		return err
	}

	pool, err := postgres.Pool(ctx, cfg.DatabaseURL, cfg.MaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()

	repo, err := postgres.NewRepository(pool, postgres.Strategy(cfg.Strategy))
	if err != nil {
		return err
	}
	service := booking.NewService(repo, cfg.HoldTTL)

	probes := metrics.New(cfg.Strategy)
	probes.Register(metrics.NewPoolCollector(pool))

	expirer := booking.NewExpirer(repo, cfg.ExpireEvery, cfg.ExpireBatch, log)
	go expirer.Run(ctx)

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpapi.New(httpapi.Config{
			Service: service,
			Ready:   pool.Ping,
			Metrics: probes,
			Scrape:  probes.Handler(),
			Logger:  log,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr, "strategy", cfg.Strategy, "hold_ttl", cfg.HoldTTL.String())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
