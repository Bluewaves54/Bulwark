// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"Bulwark/common/config"
	"Bulwark/common/rules"
)

const (
	levelDebug = "debug"
	levelInfo  = "info"
	levelWarn  = "warn"
	levelError = "error"
	formatJSON = "json"
)

var hostOS = runtime.GOOS

// reclaimGrace is the time to wait after SIGINT before escalating to SIGKILL.
const reclaimGrace = 1 * time.Second

// Server holds all runtime state for the npm Bulwark.
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
	// npm routes — scoped packages use /@scope/pkg pattern.
	mux.HandleFunc("GET /{pkg...}", s.handleNpm)
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
	addr := addrFromPort(srv.cfg.Server.Port)
	httpSrv := &http.Server{
		Handler:      srv.mux,
		ReadTimeout:  time.Duration(srv.cfg.Server.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(srv.cfg.Server.WriteTimeoutSeconds) * time.Second,
		IdleTimeout:  time.Duration(srv.cfg.Server.IdleTimeoutSeconds) * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if isAddrInUse(err) {
			logger.Warn("port in use, attempting to reclaim", slog.String("addr", addr))
			killProcessOnPort(srv.cfg.Server.Port, logger)
			ln, err = listenWithRetry(addr, 5, 500*time.Millisecond)
		}
		if err != nil {
			return fmt.Errorf("listen on %s: %w", addr, err)
		}
	}

	go func() {
		logger.Info(serviceName+" listening", slog.String("addr", addr))
		if serveErr := httpSrv.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			logger.Error("serve error", slog.String("error", serveErr.Error()))
		}
	}()

	<-ctx.Done()

	shutCtx, cancel := context.WithTimeout(context.Background(),
		time.Duration(srv.cfg.Server.WriteTimeoutSeconds)*time.Second)
	defer cancel()

	logger.Info("shutting down")
	return httpSrv.Shutdown(shutCtx)
}

// isAddrInUse reports whether err is an "address already in use" error.
func isAddrInUse(err error) bool {
	return err != nil && strings.Contains(err.Error(), "address already in use")
}

// killProcessOnPort finds and terminates the process occupying the given TCP port.
func killProcessOnPort(port int, logger *slog.Logger) {
	var cmd *exec.Cmd
	portStr := strconv.Itoa(port)

	switch hostOS {
	case "darwin", "linux":
		cmd = exec.Command("lsof", "-ti", ":"+portStr) //nolint:gosec // port is an integer
	default:
		logger.Warn("automatic port reclaim not supported on this OS; kill the old process manually",
			slog.Int("port", port))
		return
	}

	out, err := cmd.Output()
	if err != nil {
		logger.Warn("could not find process on port", slog.Int("port", port))
		return
	}

	myPID := os.Getpid()
	for _, line := range strings.Fields(strings.TrimSpace(string(out))) {
		pid, convErr := strconv.Atoi(line)
		if convErr != nil || pid == myPID || pid == 0 {
			continue
		}
		proc, findErr := os.FindProcess(pid)
		if findErr != nil {
			continue
		}
		logger.Info("killing old process on port", slog.Int("port", port), slog.Int("pid", pid))
		if killErr := proc.Signal(os.Interrupt); killErr != nil {
			if errors.Is(killErr, os.ErrProcessDone) {
				continue
			}
			_ = proc.Kill()
			continue
		}
		// Wait briefly for graceful exit, then force-kill if still alive.
		time.Sleep(reclaimGrace)
		if killErr := proc.Signal(os.Kill); killErr == nil {
			logger.Info("force-killed old process", slog.Int("pid", pid))
		}
	}
}

// listenWithRetry attempts net.Listen up to maxAttempts times with the given
// delay between attempts. It returns the listener on success or the last error.
func listenWithRetry(addr string, maxAttempts int, delay time.Duration) (net.Listener, error) {
	var ln net.Listener
	var err error
	for i := range maxAttempts {
		ln, err = net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		if i < maxAttempts-1 {
			time.Sleep(delay)
		}
	}
	return nil, err
}
