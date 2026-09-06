package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"atlas/internal/app"
	"atlas/internal/config"
	"atlas/internal/observability"

	"go.uber.org/zap"
)

// @title Atlas API
// @version 1.0
// @description Decision-support trading-signal API: market data, portfolio, watchlist,
// @description recommendations, ML predictions, and backtests. Atlas never executes trades —
// @description every recommendation is advisory only.
// @description
// @description Every route except /health, /metrics, /login, and /static/* requires a session
// @description cookie (see internal/app/auth_middleware.go) whenever ADMIN_USERNAME/
// @description ADMIN_PASSWORD_HASH are set — log in via the dashboard in the same browser first,
// @description then "Try it out" here reuses that session automatically.
// @BasePath /
func main() {
	cfg := config.Load()

	logger, err := observability.NewLogger(cfg.LogLevel)
	if err != nil {
		// No logger exists yet to log through (CLAUDE.md bans panic() and bare stderr
		// printing for everything else — this is the one unavoidable exception, at the
		// single point before logging itself is available).
		fmt.Fprintln(os.Stderr, "failed to initialize logger:", err)
		os.Exit(1)
	}
	defer logger.Sync() //nolint:errcheck

	metrics := observability.NewMetrics()

	connectCtx, cancelConnect := context.WithTimeout(context.Background(), 10*time.Second)
	application, err := app.New(connectCtx, cfg, metrics, logger)
	cancelConnect()
	if err != nil {
		logger.Fatal("failed to initialize application", zap.Error(err))
	}
	defer func() {
		if err := application.Close(); err != nil {
			logger.Error("error closing application resources", zap.Error(err))
		}
	}()

	stopAlerts := application.StartAlertPolling()
	defer stopAlerts()

	stopDependencyPolling := application.StartDependencyStatusPolling()
	defer stopDependencyPolling()

	handler := application.NewHandler()
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		logger.Info("atlas api listening", zap.String("addr", cfg.HTTPAddr))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server failed", zap.Error(err))
		}
	}()

	<-stop
	logger.Info("shutting down atlas api")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", zap.Error(err))
	}
}
