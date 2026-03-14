// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"PKGuard/common/config"
	"PKGuard/common/rules"
)

const (
	levelDebug = "debug"
	levelInfo  = "info"
	levelWarn  = "warn"
	levelError = "error"
	formatJSON = "json"
)

// Server holds all runtime state for the Maven PKGuard.
type Server struct {
	cfg        *config.Config
	engine     *rules.RuleEngine
	cache      *rules.Cache
	upstream   *http.Client
	mux        *http.ServeMux
	logger     *slog.Logger
	logLevel   *slog.LevelVar
	reqTotal   atomic.Int64
	reqAllowed atomic.Int64
	reqDenied  atomic.Int64
	reqDryRun  atomic.Int64
}

func buildServer(cfg *config.Config, logger *slog.Logger, logLevel *slog.LevelVar) (*Server, error) {
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
		logLevel: logLevel,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	if cfg.Metrics.Enabled {
		mux.HandleFunc("GET /metrics", s.handleMetrics)
	}
	mux.HandleFunc("GET /admin/log-level", s.handleGetLogLevel)
	mux.HandleFunc("PUT /admin/log-level", s.handleSetLogLevel)
	mux.HandleFunc("GET /", s.handleMaven)
	s.mux = mux

	return s, nil
}

func createLogger(format, level, filePath string) (*slog.Logger, *slog.LevelVar, *os.File) {
	var lvl slog.LevelVar
	switch level {
	case levelDebug:
		lvl.Set(slog.LevelDebug)
	case levelWarn:
		lvl.Set(slog.LevelWarn)
	case levelError:
		lvl.Set(slog.LevelError)
	default:
		lvl.Set(slog.LevelInfo)
	}
	opts := &slog.HandlerOptions{Level: &lvl}

	var w io.Writer = os.Stdout
	var logFile *os.File
	if filePath != "" {
		f, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			w = io.MultiWriter(os.Stdout, f)
			logFile = f
		}
	}

	if format == formatJSON {
		return slog.New(slog.NewJSONHandler(w, opts)), &lvl, logFile
	}
	return slog.New(slog.NewTextHandler(w, opts)), &lvl, logFile
}

// parseLogLevel converts a level string to slog.Level. Returns false if invalid.
func parseLogLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(s) {
	case levelDebug:
		return slog.LevelDebug, true
	case levelInfo:
		return slog.LevelInfo, true
	case levelWarn:
		return slog.LevelWarn, true
	case levelError:
		return slog.LevelError, true
	default:
		return slog.LevelInfo, false
	}
}

// handleGetLogLevel returns the current log level.
func (s *Server) handleGetLogLevel(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"level":%q}`, strings.ToLower(s.logLevel.Level().String()))
}

// handleSetLogLevel dynamically changes the log level at runtime.
func (s *Server) handleSetLogLevel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Level string `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	lvl, ok := parseLogLevel(req.Level)
	if !ok {
		http.Error(w, `{"error":"invalid level; use debug, info, warn, or error"}`, http.StatusBadRequest)
		return
	}
	s.logLevel.Set(lvl)
	s.logger.Info("log level changed", slog.String("new_level", strings.ToLower(lvl.String())))
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"level":%q}`, strings.ToLower(lvl.String()))
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

func addrFromPort(port int) string {
	return fmt.Sprintf(":%d", port)
}

// initServer loads configuration, applies flag overrides, and builds a ready-to-use
// Server and logger. Extracted from main to allow unit testing of the initialisation path.
func initServer(cfgPath, token, user, pass string) (*Server, *slog.Logger, *os.File, error) {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading config: %w", err)
	}
	applyFlagOverrides(cfg, token, user, pass)
	logger, logLevel, logFile := createLogger(cfg.Logging.Format, cfg.Logging.Level, cfg.Logging.FilePath)
	srv, err := buildServer(cfg, logger, logLevel)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("building server: %w", err)
	}
	return srv, logger, logFile, nil
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
