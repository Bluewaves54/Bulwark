// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"PKGuard/common/config"
	"PKGuard/common/rules"
)

// Server holds all runtime state for the PyPI PKGuard.
type Server struct {
	cfg        *config.Config
	engine     *rules.RuleEngine
	cache      *rules.Cache
	upstream   *http.Client
	mux        *http.ServeMux
	logger     *slog.Logger
	reqTotal   atomic.Int64
	reqAllowed atomic.Int64
	reqDenied  atomic.Int64
	reqDryRun  atomic.Int64
}

func buildServer(cfg *config.Config, logger *slog.Logger) (*Server, error) {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.Upstream.TLS.InsecureSkipVerify, //nolint:gosec // user-configured
		},
	}
	if cfg.Upstream.TLS.InsecureSkipVerify {
		logger.Warn("TLS verification disabled for upstream — not recommended in production")
	}

	s := &Server{
		cfg:      cfg,
		engine:   rules.New(cfg.Policy),
		cache:    rules.NewCache(time.Duration(cfg.Cache.TTLSeconds) * time.Second),
		upstream: &http.Client{Timeout: time.Duration(cfg.Upstream.TimeoutSeconds) * time.Second, Transport: transport},
		logger:   logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	if cfg.Metrics.Enabled {
		mux.HandleFunc("GET /metrics", s.handleMetrics)
	}
	mux.HandleFunc("GET /pypi/{pkg}/json", s.handlePackageJSON)
	mux.HandleFunc("GET /simple/{pkg}/", s.handleSimple)
	mux.HandleFunc("GET /simple/{pkg}", s.handleSimpleRedirect)
	mux.HandleFunc("GET /external", s.handleExternal)
	s.mux = mux

	return s, nil
}

func createLogger(format, level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	if format == formatJSON {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}

func loadConfig(path string) (*config.Config, error) {
	return config.Load(path)
}

func applyFlagOverrides(cfg *config.Config, token, username, password string) {
	if token != "" {
		cfg.Upstream.Token = token
	}
	if username != "" {
		cfg.Upstream.Username = username
	}
	if password != "" {
		cfg.Upstream.Password = password
	}
}

func applyPortEnvOverride(cfg *config.Config) {
	// applyEnvOverrides already handles PORT; nothing additional needed here.
}

func addrFromPort(port int) string {
	return fmt.Sprintf(":%d", port)
}

// initServer loads configuration, applies flag overrides, and builds a ready-to-use
// Server and logger. Extracted from main to allow unit testing of the initialisation path.
func initServer(cfgPath, token, user, pass string) (*Server, *slog.Logger, error) {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return nil, nil, fmt.Errorf("loading config: %w", err)
	}
	applyFlagOverrides(cfg, token, user, pass)
	logger := createLogger(cfg.Logging.Format, cfg.Logging.Level)
	srv, err := buildServer(cfg, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("building server: %w", err)
	}
	return srv, logger, nil
}

// runServer starts the HTTP server and blocks until ctx is cancelled, then
// performs a graceful shutdown. serviceName is used only for the startup log line.
func runServer(ctx context.Context, srv *Server, logger *slog.Logger, serviceName string) error {
	httpSrv := &http.Server{
		Addr:         addrFromPort(srv.cfg.Server.Port),
		Handler:      srv.mux,
		ReadTimeout:  time.Duration(srv.cfg.Server.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(srv.cfg.Server.WriteTimeoutSeconds) * time.Second,
		IdleTimeout:  time.Duration(srv.cfg.Server.IdleTimeoutSeconds) * time.Second,
	}

	go func() {
		logger.Info(serviceName+" listening", slog.String("addr", httpSrv.Addr))
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("listen error", slog.String("error", err.Error()))
		}
	}()

	<-ctx.Done()

	shutCtx, cancel := context.WithTimeout(context.Background(),
		time.Duration(srv.cfg.Server.WriteTimeoutSeconds)*time.Second)
	defer cancel()

	logger.Info("shutting down")
	return httpSrv.Shutdown(shutCtx)
}
