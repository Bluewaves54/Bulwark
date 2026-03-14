// SPDX-License-Identifier: Apache-2.0

// Package main implements the PyPI PKGuard.
// It proxies requests to an upstream PyPI-compatible registry and applies
// configurable policy rules before returning responses to clients.
package main

import (
	"context"
	_ "embed"
	"flag"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"PKGuard/common/installer"
)

//go:embed config-best-practices.yaml
var defaultConfig []byte

func newProxyInfo() installer.ProxyInfo {
	return installer.ProxyInfo{
		Ecosystem:  "pypi",
		BinaryName: "pypi-pkguard",
		Port:       18000,
		ConfigData: defaultConfig,
	}
}

// installFunc is a function that performs a setup or uninstall operation.
type installFunc func(installer.ProxyInfo, io.Writer) error

// handleInstallMode handles -setup and -uninstall flags.
// Returns true if a mode was handled and main should exit.
func handleInstallMode(doSetup, doUninstall bool, proxy installer.ProxyInfo, out io.Writer, setupFn, uninstallFn installFunc) (bool, error) {
	if doSetup {
		return true, setupFn(proxy, out)
	}
	if doUninstall {
		return true, uninstallFn(proxy, out)
	}
	return false, nil
}

func main() {
	setupMode := flag.Bool("setup", false, "install PKGuard with best-practices config and configure pip")
	uninstallMode := flag.Bool("uninstall", false, "remove PKGuard and restore pip config")
	cfgPath := flag.String("config", "config.yaml", "path to configuration file")
	authToken := flag.String("auth-token", "", "upstream auth bearer token (overrides config)")
	authUser := flag.String("auth-username", "", "upstream auth username (overrides config)")
	authPass := flag.String("auth-password", "", "upstream auth password (overrides config)")
	flag.Parse()

	proxy := newProxyInfo()
	handled, err := handleInstallMode(*setupMode, *uninstallMode, proxy, os.Stdout, installer.Setup, installer.Uninstall)
	if err != nil {
		slog.Default().Error("install mode failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if handled {
		return
	}

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
