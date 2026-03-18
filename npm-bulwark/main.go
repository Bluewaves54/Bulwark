// SPDX-License-Identifier: Apache-2.0

// Package main implements the npm Bulwark.
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"Bulwark/common/installer"
)

//go:embed config-best-practices.yaml
var defaultConfig []byte

func newProxyInfo() installer.ProxyInfo {
	return installer.ProxyInfo{
		Ecosystem:  "npm",
		BinaryName: "npm-bulwark",
		Port:       18001,
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

// resolveConfig determines the effective config path.
// If -config was explicitly set, that path is returned unchanged.
// If the default config file exists in the working directory, it is used.
// If the proxy is already installed, the installed config path is returned.
// Otherwise a first-run auto-setup is performed and the installed config is used.
func resolveConfig(cfgFlag string, explicit bool, proxy installer.ProxyInfo, home, goos string, out io.Writer, setupFn installFunc) (string, error) {
	if explicit {
		return cfgFlag, nil
	}
	if _, err := os.Stat(cfgFlag); err == nil {
		return cfgFlag, nil
	}
	if installer.IsInstalledAt(proxy, home, goos) {
		return installer.InstalledConfigPath(proxy, home, goos), nil
	}
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "=== Bulwark First-Run Setup ===")
	fmt.Fprintln(out, "No existing installation found. Setting up automatically...")
	fmt.Fprintln(out, "")
	if err := setupFn(proxy, out); err != nil {
		return "", fmt.Errorf("auto-setup: %w", err)
	}
	fmt.Fprintln(out, "Starting proxy server...")
	fmt.Fprintln(out, "")
	return installer.InstalledConfigPath(proxy, home, goos), nil
}

// version is set via ldflags at build time.
var version = "dev"

// run drives the proxy lifecycle after flags are parsed. It returns an error
// instead of calling os.Exit, making it testable.
func run(ctx context.Context, cfgPath string, configExplicit, setupMode, uninstallMode, uninstallAllMode, updateMode, backgroundMode bool,
	authToken, authUser, authPass string, out io.Writer) error {

	if uninstallAllMode {
		return installer.UninstallAll(out)
	}
	if updateMode {
		return installer.Update(version, out)
	}

	proxy := newProxyInfo()
	handled, err := handleInstallMode(setupMode, uninstallMode, proxy, out, installer.Setup, installer.Uninstall)
	if err != nil {
		return fmt.Errorf("install mode: %w", err)
	}
	if handled {
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	if backgroundMode {
		pid, derr := installer.Daemonize(proxy, home)
		if derr != nil {
			return fmt.Errorf("background start: %w", derr)
		}
		fmt.Fprintf(out, "npm-bulwark started in background (PID %d)\n", pid)
		return nil
	}

	effectiveCfg, err := resolveConfig(cfgPath, configExplicit, proxy, home, runtime.GOOS, out, installer.SetupFilesOnly)
	if err != nil {
		return fmt.Errorf("config resolution: %w", err)
	}

	installer.EnsurePkgMgrConfig(proxy, home, runtime.GOOS, out)

	srv, logger, logFile, err := initServer(effectiveCfg, authToken, authUser, authPass)
	if err != nil {
		return fmt.Errorf("initialisation: %w", err)
	}
	if logFile != nil {
		defer logFile.Close()
	}

	return runServer(ctx, srv, logger, "npm-bulwark")
}

func main() {
	setupMode := flag.Bool("setup", false, "install Bulwark with best-practices config and configure npm")
	uninstallMode := flag.Bool("uninstall", false, "remove Bulwark and restore npm registry")
	uninstallAllMode := flag.Bool("uninstall-all", false, "remove all Bulwark proxies and restore all package manager configs")
	updateMode := flag.Bool("update", false, "update all installed Bulwark proxies to the latest stable release")
	backgroundMode := flag.Bool("background", false, "start the proxy as a background process")
	cfgPath := flag.String("config", "config.yaml", "path to configuration file")
	authToken := flag.String("auth-token", "", "upstream auth bearer token (overrides config)")
	authUser := flag.String("auth-username", "", "upstream auth username (overrides config)")
	authPass := flag.String("auth-password", "", "upstream auth password (overrides config)")
	flag.Parse()

	configExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			configExplicit = true
		}
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, *cfgPath, configExplicit, *setupMode, *uninstallMode, *uninstallAllMode, *updateMode, *backgroundMode, *authToken, *authUser, *authPass, os.Stdout); err != nil {
		slog.Default().Error(err.Error())
		os.Exit(1)
	}
}
