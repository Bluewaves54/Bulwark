// SPDX-License-Identifier: Apache-2.0

// Package main implements the PyPI PKGuard.
// It proxies requests to an upstream PyPI-compatible registry and applies
// configurable policy rules before returning responses to clients.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to configuration file")
	authToken := flag.String("auth-token", "", "upstream auth bearer token (overrides config)")
	authUser := flag.String("auth-username", "", "upstream auth username (overrides config)")
	authPass := flag.String("auth-password", "", "upstream auth password (overrides config)")
	flag.Parse()

	srv, logger, logFile, err := initServer(*cfgPath, *authToken, *authUser, *authPass)
	if err != nil {
		slog.Default().Error("initialisation failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if logFile != nil {
		defer logFile.Close()
	}

	applyPortEnvOverride(srv.cfg)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := runServer(ctx, srv, logger, "pypi-pkguard"); err != nil {
		logger.Error("server error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
