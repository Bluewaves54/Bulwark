// SPDX-License-Identifier: Apache-2.0

// Package installer provides one-click setup and uninstall for Bulwark proxies.
// Each proxy binary embeds its best-practices config and calls Setup or Uninstall
// to install, configure the package manager, and create OS autostart entries.
package installer

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	bulwarkDir = ".bulwark"
	binSubdir  = "bin"
	dirPerm    = 0o755
	cfgPerm    = 0o644
	binPerm    = 0o755

	// OSDarwin is the GOOS value for macOS.
	OSDarwin = "darwin"
	// OSLinux is the GOOS value for Linux.
	OSLinux = "linux"
	// OSWindows is the GOOS value for Windows.
	OSWindows = "windows"

	// EcosystemNpm is the ecosystem identifier for npm.
	EcosystemNpm = "npm"
	// EcosystemPypi is the ecosystem identifier for PyPI.
	EcosystemPypi = "pypi"
	// EcosystemMaven is the ecosystem identifier for Maven.
	EcosystemMaven = "maven"
)

// ProxyInfo describes a Bulwark proxy ecosystem for installation.
type ProxyInfo struct {
	// Ecosystem is the short name: "npm", "pypi", or "maven".
	Ecosystem string
	// BinaryName is the proxy binary name without extension.
	BinaryName string
	// Port is the default proxy listen port.
	Port int
	// ConfigData is the embedded best-practices YAML configuration.
	ConfigData []byte
}

// Paths holds resolved filesystem paths used during setup and uninstall.
type Paths struct {
	Base   string
	EcoDir string
	BinDir string
	Config string
	Binary string
}

// ResolvePaths computes installation paths relative to the given home directory.
func ResolvePaths(p ProxyInfo, home, goos string) Paths {
	base := filepath.Join(home, bulwarkDir)
	binName := p.BinaryName
	if goos == OSWindows {
		binName += ".exe"
	}
	return Paths{
		Base:   base,
		EcoDir: filepath.Join(base, p.BinaryName),
		BinDir: filepath.Join(base, binSubdir),
		Config: filepath.Join(base, p.BinaryName, "config.yaml"),
		Binary: filepath.Join(base, binSubdir, binName),
	}
}

// --- Content generators (pure functions) ---

// PipConfig returns the pip configuration file content.
func PipConfig(port int) string {
	return fmt.Sprintf("[global]\nindex-url = http://localhost:%d/simple/\ntrusted-host = localhost\n", port)
}

// MavenSettingsXML returns a Maven settings.xml that mirrors central through Bulwark.
func MavenSettingsXML(port int) string {
	return fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"+
		"<settings xmlns=\"http://maven.apache.org/SETTINGS/1.0.0\"\n"+
		"          xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\"\n"+
		"          xsi:schemaLocation=\"http://maven.apache.org/SETTINGS/1.0.0\n"+
		"                              http://maven.apache.org/xsd/settings-1.0.0.xsd\">\n"+
		"  <mirrors>\n"+
		"    <mirror>\n"+
		"      <id>bulwark-maven</id>\n"+
		"      <mirrorOf>central</mirrorOf>\n"+
		"      <url>http://localhost:%d</url>\n"+
		"    </mirror>\n"+
		"  </mirrors>\n"+
		"</settings>\n", port)
}

// LaunchdPlistXML returns a macOS LaunchAgent plist.
func LaunchdPlistXML(label, binary, config string) string {
	return fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"+
		"<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\"\n"+
		"  \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n"+
		"<plist version=\"1.0\">\n"+
		"<dict>\n"+
		"  <key>Label</key>\n"+
		"  <string>%s</string>\n"+
		"  <key>ProgramArguments</key>\n"+
		"  <array>\n"+
		"    <string>%s</string>\n"+
		"    <string>-config</string>\n"+
		"    <string>%s</string>\n"+
		"  </array>\n"+
		"  <key>RunAtLoad</key>\n"+
		"  <true/>\n"+
		"  <key>KeepAlive</key>\n"+
		"  <true/>\n"+
		"  <key>StandardOutPath</key>\n"+
		"  <string>/tmp/%s.log</string>\n"+
		"  <key>StandardErrorPath</key>\n"+
		"  <string>/tmp/%s.log</string>\n"+
		"</dict>\n"+
		"</plist>\n", label, binary, config, label, label)
}

// SystemdUnitFile returns a systemd user service unit.
func SystemdUnitFile(desc, binary, config string) string {
	return fmt.Sprintf("[Unit]\n"+
		"Description=Bulwark %s\n"+
		"After=network.target\n"+
		"\n"+
		"[Service]\n"+
		"Type=simple\n"+
		"ExecStart=%s -config %s\n"+
		"Restart=on-failure\n"+
		"RestartSec=5\n"+
		"\n"+
		"[Install]\n"+
		"WantedBy=default.target\n", desc, binary, config)
}

// WindowsBatchFile returns a Windows startup batch script.
func WindowsBatchFile(binary, config string) string {
	return fmt.Sprintf("@echo off\r\nstart \"\" /B \"%s\" -config \"%s\"\r\n", binary, config)
}

// --- File operations ---

// CopyFile copies src to dst, creating or truncating dst.
func CopyFile(src, dst string) error {
	srcAbs, _ := filepath.Abs(src)
	dstAbs, _ := filepath.Abs(dst)
	if srcAbs == dstAbs {
		return nil // already in place
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening source: %w", err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, binPerm)
	if err != nil {
		return fmt.Errorf("creating destination: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copying data: %w", err)
	}
	return nil
}

// --- Package manager config (file-based) ---

// PipConfigPaths returns the directory and file path for pip config.
func PipConfigPaths(home, goos string) (string, string) {
	if goos == OSWindows {
		dir := filepath.Join(home, "AppData", "Roaming", "pip")
		return dir, filepath.Join(dir, "pip.ini")
	}
	dir := filepath.Join(home, ".config", "pip")
	return dir, filepath.Join(dir, "pip.conf")
}

func writePkgMgrConfig(p ProxyInfo, home, goos string, out io.Writer) {
	switch p.Ecosystem {
	case EcosystemPypi:
		cfgDir, cfgFile := PipConfigPaths(home, goos)
		if err := os.MkdirAll(cfgDir, dirPerm); err != nil {
			fmt.Fprintf(out, "[warn] pip config dir: %v\n", err)
			return
		}
		if err := os.WriteFile(cfgFile, []byte(PipConfig(p.Port)), cfgPerm); err != nil {
			fmt.Fprintf(out, "[warn] pip config: %v\n", err)
			return
		}
		fmt.Fprintf(out, "[ok] pip index configured: %s\n", cfgFile)
	case EcosystemMaven:
		m2Dir := filepath.Join(home, ".m2")
		if err := os.MkdirAll(m2Dir, dirPerm); err != nil {
			fmt.Fprintf(out, "[warn] .m2 dir: %v\n", err)
			return
		}
		settingsPath := filepath.Join(m2Dir, "settings.xml")
		if data, err := os.ReadFile(settingsPath); err == nil {
			backup := settingsPath + ".bulwark-backup"
			os.WriteFile(backup, data, cfgPerm) //nolint:errcheck // best-effort backup
			fmt.Fprintf(out, "[ok] Existing settings.xml backed up to %s\n", backup)
		}
		if err := os.WriteFile(settingsPath, []byte(MavenSettingsXML(p.Port)), cfgPerm); err != nil {
			fmt.Fprintf(out, "[warn] Maven settings: %v\n", err)
			return
		}
		fmt.Fprintf(out, "[ok] Maven mirror configured: %s\n", settingsPath)
	case EcosystemNpm:
		fmt.Fprintf(out, "[info] npm registry will be configured after service activation\n")
	}
}

// --- Autostart ---

// AutostartDir returns the OS-specific autostart directory.
func AutostartDir(goos, home string) string {
	switch goos {
	case OSDarwin:
		return filepath.Join(home, "Library", "LaunchAgents")
	case OSLinux:
		return filepath.Join(home, ".config", "systemd", "user")
	case OSWindows:
		return filepath.Join(home, "AppData", "Roaming", "Microsoft",
			"Windows", "Start Menu", "Programs", "Startup")
	default:
		return ""
	}
}

// AutostartFileName returns the filename for the autostart entry.
func AutostartFileName(goos, ecosystem string) string {
	switch goos {
	case OSDarwin:
		return fmt.Sprintf("com.bulwark.%s.plist", ecosystem)
	case OSLinux:
		return fmt.Sprintf("bulwark-%s.service", ecosystem)
	case OSWindows:
		return fmt.Sprintf("bulwark-%s.bat", ecosystem)
	default:
		return ""
	}
}

// AutostartContent returns the file content for the autostart entry.
func AutostartContent(p ProxyInfo, paths Paths, goos string) string {
	switch goos {
	case OSDarwin:
		label := fmt.Sprintf("com.bulwark.%s", p.Ecosystem)
		return LaunchdPlistXML(label, paths.Binary, paths.Config)
	case OSLinux:
		return SystemdUnitFile(p.BinaryName, paths.Binary, paths.Config)
	case OSWindows:
		return WindowsBatchFile(paths.Binary, paths.Config)
	default:
		return ""
	}
}

func writeAutostartFile(p ProxyInfo, paths Paths, home, goos string, out io.Writer) {
	dir := AutostartDir(goos, home)
	if dir == "" {
		fmt.Fprintf(out, "[info] Autostart not supported on %s; start manually\n", goos)
		return
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		fmt.Fprintf(out, "[warn] autostart dir: %v\n", err)
		return
	}

	fileName := AutostartFileName(goos, p.Ecosystem)
	content := AutostartContent(p, paths, goos)
	filePath := filepath.Join(dir, fileName)

	if err := os.WriteFile(filePath, []byte(content), cfgPerm); err != nil {
		fmt.Fprintf(out, "[warn] autostart file: %v\n", err)
		return
	}
	fmt.Fprintf(out, "[ok] Autostart entry: %s\n", filePath)
}

// --- Orchestration ---

// SetupFiles performs all file-system changes for installation. It does NOT run
// external commands (npm, launchctl, systemctl). Use Setup for a complete install.
func SetupFiles(p ProxyInfo, home, exePath, goos string, out io.Writer) error {
	paths := ResolvePaths(p, home, goos)

	for _, d := range []string{paths.EcoDir, paths.BinDir} {
		if err := os.MkdirAll(d, dirPerm); err != nil {
			return fmt.Errorf("creating %s: %w", d, err)
		}
	}

	if err := os.WriteFile(paths.Config, p.ConfigData, cfgPerm); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	fmt.Fprintf(out, "[ok] Config written to %s\n", paths.Config)

	if err := CopyFile(exePath, paths.Binary); err != nil {
		return fmt.Errorf("copying binary: %w", err)
	}
	fmt.Fprintf(out, "[ok] Binary installed to %s\n", paths.Binary)

	writePkgMgrConfig(p, home, goos, out)
	writeAutostartFile(p, paths, home, goos, out)
	PrintPostSetup(p, paths, out)
	return nil
}

// UninstallFiles removes files installed by SetupFiles.
func UninstallFiles(p ProxyInfo, home, goos string, out io.Writer) error {
	paths := ResolvePaths(p, home, goos)

	// Remove autostart entry.
	dir := AutostartDir(goos, home)
	if dir != "" {
		fileName := AutostartFileName(goos, p.Ecosystem)
		if fileName != "" {
			os.Remove(filepath.Join(dir, fileName)) //nolint:errcheck // may not exist
			fmt.Fprintf(out, "[ok] Removed autostart entry\n")
		}
	}

	// Remove pip config.
	if p.Ecosystem == EcosystemPypi {
		_, cfgFile := PipConfigPaths(home, goos)
		os.Remove(cfgFile) //nolint:errcheck // may not exist
		fmt.Fprintf(out, "[ok] Removed pip config\n")
	}

	// Restore Maven settings from backup.
	if p.Ecosystem == EcosystemMaven {
		settingsPath := filepath.Join(home, ".m2", "settings.xml")
		backup := settingsPath + ".bulwark-backup"
		if data, err := os.ReadFile(backup); err == nil {
			os.WriteFile(settingsPath, data, cfgPerm) //nolint:errcheck // best-effort restore
			os.Remove(backup)                         //nolint:errcheck // best-effort cleanup
			fmt.Fprintf(out, "[ok] Restored Maven settings.xml from backup\n")
		} else {
			os.Remove(settingsPath) //nolint:errcheck // may not exist
			fmt.Fprintf(out, "[ok] Removed Maven settings.xml\n")
		}
	}

	os.RemoveAll(paths.EcoDir) //nolint:errcheck // best-effort
	fmt.Fprintf(out, "[ok] Removed %s\n", paths.EcoDir)

	os.Remove(paths.Binary) //nolint:errcheck // may not exist
	fmt.Fprintf(out, "[ok] Removed %s\n", paths.Binary)

	fmt.Fprintf(out, "\n%s-bulwark has been uninstalled.\n", p.Ecosystem)
	return nil
}

// --- External command helpers (not unit-tested; documented exemption from coverage) ---

func activateNpm(port int, out io.Writer) {
	url := fmt.Sprintf("http://localhost:%d/", port)
	if _, err := exec.LookPath("npm"); err != nil {
		fmt.Fprintf(out, "[info] npm not found; set registry manually:\n")
		fmt.Fprintf(out, "       npm config set registry %s\n", url)
		return
	}
	cmd := exec.Command("npm", "config", "set", "registry", url) //nolint:gosec // user-initiated setup
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(out, "[warn] npm config: %s (%v)\n", strings.TrimSpace(string(output)), err)
		return
	}
	fmt.Fprintf(out, "[ok] npm registry set to %s\n", url)
}

func activateLaunchd(plistPath string, out io.Writer) {
	exec.Command("launchctl", "unload", plistPath).Run() //nolint:errcheck,gosec // best-effort unload before reload
	cmd := exec.Command("launchctl", "load", plistPath)  //nolint:gosec // user-initiated
	if _, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(out, "[warn] launchctl load failed; start manually\n")
		return
	}
	fmt.Fprintf(out, "[ok] LaunchAgent loaded\n")
}

func activateSystemd(unitName string, out io.Writer) {
	exec.Command("systemctl", "--user", "daemon-reload").Run()              //nolint:errcheck,gosec // best-effort
	cmd := exec.Command("systemctl", "--user", "enable", "--now", unitName) //nolint:gosec // user-initiated
	if _, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(out, "[warn] systemctl enable failed; start manually\n")
		return
	}
	fmt.Fprintf(out, "[ok] systemd service enabled: %s\n", unitName)
}

func deactivateNpm(out io.Writer) {
	if _, err := exec.LookPath("npm"); err != nil {
		return
	}
	exec.Command("npm", "config", "delete", "registry").Run() //nolint:errcheck,gosec // best-effort
	fmt.Fprintf(out, "[ok] npm registry restored to default\n")
}

func deactivateLaunchd(plistPath string, out io.Writer) {
	exec.Command("launchctl", "unload", plistPath).Run() //nolint:errcheck,gosec // best-effort
	fmt.Fprintf(out, "[ok] LaunchAgent unloaded\n")
}

func deactivateSystemd(unitName string, out io.Writer) {
	exec.Command("systemctl", "--user", "disable", "--now", unitName).Run() //nolint:errcheck,gosec // best-effort
	exec.Command("systemctl", "--user", "daemon-reload").Run()              //nolint:errcheck,gosec // best-effort
	fmt.Fprintf(out, "[ok] systemd service disabled\n")
}

// --- Public API ---

// ActivateServices runs OS-specific commands to start services and configure
// tools that require CLI execution (npm config, launchctl, systemctl).
func ActivateServices(p ProxyInfo, home, goos string, out io.Writer) {
	if p.Ecosystem == EcosystemNpm {
		activateNpm(p.Port, out)
	}

	switch goos {
	case OSDarwin:
		label := fmt.Sprintf("com.bulwark.%s", p.Ecosystem)
		plistPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
		activateLaunchd(plistPath, out)
	case OSLinux:
		unitName := fmt.Sprintf("bulwark-%s.service", p.Ecosystem)
		activateSystemd(unitName, out)
	case OSWindows:
		paths := ResolvePaths(p, home, goos)
		fmt.Fprintf(out, "[ok] Proxy will start automatically on login\n")
		fmt.Fprintf(out, "     To start now: %s -config %s\n", paths.Binary, paths.Config)
	}
}

// DeactivateServices stops running services before uninstall.
func DeactivateServices(p ProxyInfo, home, goos string, out io.Writer) {
	if p.Ecosystem == EcosystemNpm {
		deactivateNpm(out)
	}

	switch goos {
	case OSDarwin:
		label := fmt.Sprintf("com.bulwark.%s", p.Ecosystem)
		plistPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
		deactivateLaunchd(plistPath, out)
	case OSLinux:
		unitName := fmt.Sprintf("bulwark-%s.service", p.Ecosystem)
		deactivateSystemd(unitName, out)
	}
}

// Setup performs a complete installation: writes files, configures the package
// manager, creates autostart entries, and activates the service.
func Setup(p ProxyInfo, out io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("finding home directory: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable: %w", err)
	}
	goos := runtime.GOOS
	if err := SetupFiles(p, home, exe, goos, out); err != nil {
		return err
	}
	ActivateServices(p, home, goos, out)
	return nil
}

// Uninstall stops the service, removes files, and restores package manager config.
func Uninstall(p ProxyInfo, out io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("finding home directory: %w", err)
	}
	goos := runtime.GOOS
	DeactivateServices(p, home, goos, out)
	return UninstallFiles(p, home, goos, out)
}

// PrintPostSetup prints instructions after a successful setup.
func PrintPostSetup(p ProxyInfo, paths Paths, out io.Writer) {
	fmt.Fprintf(out, "\n=== %s-bulwark installed successfully ===\n\n", p.Ecosystem)
	fmt.Fprintf(out, "Binary:  %s\n", paths.Binary)
	fmt.Fprintf(out, "Config:  %s\n", paths.Config)
	fmt.Fprintf(out, "Port:    %d\n\n", p.Port)
	fmt.Fprintf(out, "To start manually:\n")
	fmt.Fprintf(out, "  %s -config %s\n\n", paths.Binary, paths.Config)
	fmt.Fprintf(out, "To reconfigure rules after install:\n")
	fmt.Fprintf(out, "  1. Edit %s\n", paths.Config)
	fmt.Fprintf(out, "  2. Restart the proxy (the service restarts automatically on reboot).\n\n")
	fmt.Fprintf(out, "To uninstall:\n")
	fmt.Fprintf(out, "  %s -uninstall\n", paths.Binary)
}

// InstalledConfigPath returns the standard config file path for a proxy
// installation under the given home directory.
func InstalledConfigPath(p ProxyInfo, home, goos string) string {
	return ResolvePaths(p, home, goos).Config
}

// IsInstalledAt checks whether the proxy config exists at the standard
// installation location under the given home directory.
func IsInstalledAt(p ProxyInfo, home, goos string) bool {
	_, err := os.Stat(InstalledConfigPath(p, home, goos))
	return err == nil
}

// SetupFilesOnly installs files and configures the package manager but does not
// activate autostart services (launchctl/systemd). The autostart entry is still
// written so the proxy starts automatically on next login. Use this when the
// calling process will continue running as the proxy.
func SetupFilesOnly(p ProxyInfo, out io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("finding home directory: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable: %w", err)
	}
	return SetupFilesOnlyAt(p, home, exe, runtime.GOOS, out)
}

// SetupFilesOnlyAt is the testable implementation of SetupFilesOnly.
func SetupFilesOnlyAt(p ProxyInfo, home, exe, goos string, out io.Writer) error {
	if err := SetupFiles(p, home, exe, goos, out); err != nil {
		return err
	}
	if p.Ecosystem == EcosystemNpm {
		activateNpm(p.Port, out)
	}
	return nil
}
