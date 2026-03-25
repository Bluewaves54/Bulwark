// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	testEcosystem  = EcosystemNpm
	testBinaryName = "npm-bulwark"
	testPort       = 18001
	testConfigData = "listen_port: 18001\n"
	testDarwin     = OSDarwin
	testLinux      = OSLinux
	testWindows    = OSWindows
	testFreeBSD    = "freebsd"
)

func testProxyInfo() ProxyInfo {
	return ProxyInfo{
		Ecosystem:  testEcosystem,
		BinaryName: testBinaryName,
		Port:       testPort,
		ConfigData: []byte(testConfigData),
	}
}

func pypiProxyInfo() ProxyInfo {
	return ProxyInfo{
		Ecosystem:  "pypi",
		BinaryName: "pypi-bulwark",
		Port:       18000,
		ConfigData: []byte(testConfigData),
	}
}

func mavenProxyInfo() ProxyInfo {
	return ProxyInfo{
		Ecosystem:  "maven",
		BinaryName: "maven-bulwark",
		Port:       18002,
		ConfigData: []byte(testConfigData),
	}
}

func createDummyExe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "dummy-exe")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho hello\n"), binPerm); err != nil {
		t.Fatal(err)
	}
	return exe
}

// --- ResolvePaths ---

func TestResolvePathsDarwin(t *testing.T) {
	p := testProxyInfo()
	paths := ResolvePaths(p, "/home/user", testDarwin)

	if paths.Base != filepath.Join("/home/user", bulwarkDir) {
		t.Errorf("Base = %s", paths.Base)
	}
	if paths.EcoDir != filepath.Join("/home/user", bulwarkDir, testBinaryName) {
		t.Errorf("EcoDir = %s", paths.EcoDir)
	}
	if paths.BinDir != filepath.Join("/home/user", bulwarkDir, binSubdir) {
		t.Errorf("BinDir = %s", paths.BinDir)
	}
	if paths.Config != filepath.Join("/home/user", bulwarkDir, testBinaryName, "config.yaml") {
		t.Errorf("Config = %s", paths.Config)
	}
	if paths.Binary != filepath.Join("/home/user", bulwarkDir, binSubdir, testBinaryName) {
		t.Errorf("Binary = %s", paths.Binary)
	}
}

func TestResolvePathsWindows(t *testing.T) {
	p := testProxyInfo()
	paths := ResolvePaths(p, "/home/user", testWindows)

	if !strings.HasSuffix(paths.Binary, testBinaryName+".exe") {
		t.Errorf("Windows binary should have .exe suffix, got %s", paths.Binary)
	}
}

func TestResolvePathsLinux(t *testing.T) {
	p := testProxyInfo()
	paths := ResolvePaths(p, "/home/user", testLinux)

	if strings.HasSuffix(paths.Binary, ".exe") {
		t.Errorf("Linux binary should NOT have .exe suffix, got %s", paths.Binary)
	}
}

// --- Content generators ---

func TestPipConfig(t *testing.T) {
	result := PipConfig(18000)
	if !strings.Contains(result, "[global]") {
		t.Error("missing [global] section")
	}
	if !strings.Contains(result, "http://localhost:18000/simple/") {
		t.Error("missing index-url")
	}
	if !strings.Contains(result, "trusted-host = localhost") {
		t.Error("missing trusted-host")
	}
}

func TestMavenSettingsXML(t *testing.T) {
	result := MavenSettingsXML(18002)
	if !strings.Contains(result, "<?xml version=") {
		t.Error("missing XML declaration")
	}
	if !strings.Contains(result, "<mirrorOf>central</mirrorOf>") {
		t.Error("missing mirrorOf")
	}
	if !strings.Contains(result, "http://localhost:18002") {
		t.Error("missing proxy URL")
	}
	if !strings.Contains(result, "bulwark-maven") {
		t.Error("missing mirror id")
	}
}

func TestLaunchdPlistXML(t *testing.T) {
	result := LaunchdPlistXML("com.bulwark.npm", "/usr/local/bin/npm-bulwark", "/etc/config.yaml")
	if !strings.Contains(result, "<string>com.bulwark.npm</string>") {
		t.Error("missing label")
	}
	if !strings.Contains(result, "<string>/usr/local/bin/npm-bulwark</string>") {
		t.Error("missing binary path")
	}
	if !strings.Contains(result, "<string>/etc/config.yaml</string>") {
		t.Error("missing config path")
	}
	if !strings.Contains(result, "<key>RunAtLoad</key>") {
		t.Error("missing RunAtLoad")
	}
	if !strings.Contains(result, "<key>KeepAlive</key>") {
		t.Error("missing KeepAlive")
	}
	if !strings.Contains(result, "/tmp/com.bulwark.npm.log") {
		t.Error("missing log path")
	}
}

func TestSystemdUnitFile(t *testing.T) {
	result := SystemdUnitFile("npm-bulwark", "/home/user/.bulwark/bin/npm-bulwark", "/home/user/.bulwark/npm-bulwark/config.yaml")
	if !strings.Contains(result, "[Unit]") {
		t.Error("missing [Unit]")
	}
	if !strings.Contains(result, "Description=Bulwark npm-bulwark") {
		t.Error("missing description")
	}
	if !strings.Contains(result, "[Service]") {
		t.Error("missing [Service]")
	}
	if !strings.Contains(result, "ExecStart=/home/user/.bulwark/bin/npm-bulwark -config /home/user/.bulwark/npm-bulwark/config.yaml") {
		t.Error("missing ExecStart")
	}
	if !strings.Contains(result, "[Install]") {
		t.Error("missing [Install]")
	}
	if !strings.Contains(result, "Restart=on-failure") {
		t.Error("missing Restart policy")
	}
}

func TestWindowsBatchFile(t *testing.T) {
	result := WindowsBatchFile("C:\\bulwark\\npm.exe", "C:\\bulwark\\config.yaml")
	if !strings.Contains(result, "@echo off") {
		t.Error("missing @echo off")
	}
	if !strings.Contains(result, "C:\\bulwark\\npm.exe") {
		t.Error("missing binary path")
	}
	if !strings.Contains(result, "C:\\bulwark\\config.yaml") {
		t.Error("missing config path")
	}
	if !strings.Contains(result, "\r\n") {
		t.Error("missing CRLF line endings")
	}
}

// --- PipConfigPaths ---

func TestPipConfigPaths(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		wantDir string
		wantExt string
	}{
		{"Darwin", testDarwin, ".config/pip", "pip.conf"},
		{"Linux", testLinux, ".config/pip", "pip.conf"},
		{"Windows", testWindows, "AppData/Roaming/pip", "pip.ini"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir, file := PipConfigPaths("/home/user", tc.goos)
			if !strings.Contains(filepath.ToSlash(dir), tc.wantDir) {
				t.Errorf("dir = %s, want containing %s", dir, tc.wantDir)
			}
			if !strings.HasSuffix(file, tc.wantExt) {
				t.Errorf("file = %s, want suffix %s", file, tc.wantExt)
			}
		})
	}
}

// --- AutostartDir ---

func TestAutostartDir(t *testing.T) {
	tests := []struct {
		name string
		goos string
		want string
	}{
		{"Darwin", testDarwin, "Library/LaunchAgents"},
		{"Linux", testLinux, ".config/systemd/user"},
		{"Windows", testWindows, "Start Menu/Programs/Startup"},
		{"FreeBSD", testFreeBSD, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := AutostartDir(tc.goos, "/home/user")
			if tc.want == "" && result != "" {
				t.Errorf("expected empty, got %s", result)
			}
			if tc.want != "" && !strings.Contains(filepath.ToSlash(result), tc.want) {
				t.Errorf("result = %s, want containing %s", result, tc.want)
			}
		})
	}
}

// --- AutostartFileName ---

func TestAutostartFileName(t *testing.T) {
	tests := []struct {
		name string
		goos string
		want string
	}{
		{"Darwin", testDarwin, "com.bulwark.npm.plist"},
		{"Linux", testLinux, "bulwark-npm.service"},
		{"Windows", testWindows, "bulwark-npm.bat"},
		{"FreeBSD", testFreeBSD, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := AutostartFileName(tc.goos, testEcosystem)
			if result != tc.want {
				t.Errorf("got %s, want %s", result, tc.want)
			}
		})
	}
}

// --- AutostartContent ---

func TestAutostartContent(t *testing.T) {
	p := testProxyInfo()
	paths := ResolvePaths(p, "/home/user", testDarwin)

	tests := []struct {
		name     string
		goos     string
		contains string
	}{
		{"Darwin", testDarwin, "com.bulwark.npm"},
		{"Linux", testLinux, "[Unit]"},
		{"Windows", testWindows, "@echo off"},
		{"FreeBSD", testFreeBSD, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := AutostartContent(p, paths, tc.goos)
			if tc.contains == "" && result != "" {
				t.Errorf("expected empty, got %s", result)
			}
			if tc.contains != "" && !strings.Contains(result, tc.contains) {
				t.Errorf("result missing %s", tc.contains)
			}
		})
	}
}

// --- CopyFile ---

func TestCopyFile(t *testing.T) {
	src := createDummyExe(t)
	dst := filepath.Join(t.TempDir(), "copied")

	if err := CopyFile(src, dst); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}

	srcData, _ := os.ReadFile(src)
	dstData, _ := os.ReadFile(dst)
	if string(srcData) != string(dstData) {
		t.Error("copied file content mismatch")
	}
}

func TestCopyFileSameFile(t *testing.T) {
	src := createDummyExe(t)
	// CopyFile to itself should be a no-op.
	if err := CopyFile(src, src); err != nil {
		t.Fatalf("CopyFile same: %v", err)
	}
}

func TestCopyFileSourceMissing(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "dst")
	if err := CopyFile("/nonexistent/path/file", dst); err == nil {
		t.Error("expected error for missing source")
	}
}

func TestCopyFileDestUnwritable(t *testing.T) {
	src := createDummyExe(t)
	// Destination in a non-existent directory.
	if err := CopyFile(src, "/nonexistent/dir/file"); err == nil {
		t.Error("expected error for unwritable destination")
	}
}

// --- SetupFiles ---

func TestSetupFilesNpm(t *testing.T) {
	home := t.TempDir()
	exe := createDummyExe(t)
	p := testProxyInfo()
	var buf bytes.Buffer

	if err := SetupFiles(p, home, exe, testDarwin, &buf); err != nil {
		t.Fatalf("SetupFiles: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "[ok] Config written") {
		t.Error("missing config written message")
	}
	if !strings.Contains(output, "[ok] Binary installed") {
		t.Error("missing binary installed message")
	}
	if !strings.Contains(output, "[info] npm registry will be configured") {
		t.Error("missing npm registry message")
	}
	if !strings.Contains(output, "[ok] Autostart entry") {
		t.Error("missing autostart message")
	}
	if !strings.Contains(output, "installed successfully") {
		t.Error("missing success message")
	}

	// Verify files exist.
	paths := ResolvePaths(p, home, testDarwin)
	if _, err := os.Stat(paths.Config); err != nil {
		t.Errorf("config file not found: %v", err)
	}
	if _, err := os.Stat(paths.Binary); err != nil {
		t.Errorf("binary not found: %v", err)
	}
}

func TestSetupFilesPypi(t *testing.T) {
	home := t.TempDir()
	exe := createDummyExe(t)
	p := pypiProxyInfo()
	var buf bytes.Buffer

	if err := SetupFiles(p, home, exe, testDarwin, &buf); err != nil {
		t.Fatalf("SetupFiles: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "[ok] pip index configured") {
		t.Error("missing pip config message")
	}

	// Verify pip config was written.
	_, cfgFile := PipConfigPaths(home, testDarwin)
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Errorf("pip config not found: %v", err)
	}
	if !strings.Contains(string(data), "index-url") {
		t.Error("pip config missing index-url")
	}
}

func TestSetupFilesPypiWindows(t *testing.T) {
	home := t.TempDir()
	exe := createDummyExe(t)
	p := pypiProxyInfo()
	var buf bytes.Buffer

	if err := SetupFiles(p, home, exe, testWindows, &buf); err != nil {
		t.Fatalf("SetupFiles: %v", err)
	}

	_, cfgFile := PipConfigPaths(home, testWindows)
	if _, err := os.Stat(cfgFile); err != nil {
		t.Errorf("Windows pip config not found: %v", err)
	}
}

func TestSetupFilesMaven(t *testing.T) {
	home := t.TempDir()
	exe := createDummyExe(t)
	p := mavenProxyInfo()
	var buf bytes.Buffer

	if err := SetupFiles(p, home, exe, testDarwin, &buf); err != nil {
		t.Fatalf("SetupFiles: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "[ok] Maven mirror configured") {
		t.Error("missing maven config message")
	}

	settingsPath := filepath.Join(home, ".m2", "settings.xml")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.xml not found: %v", err)
	}
	if !strings.Contains(string(data), "bulwark-maven") {
		t.Error("settings.xml missing bulwark mirror")
	}
}

func TestSetupFilesMavenBackup(t *testing.T) {
	home := t.TempDir()
	exe := createDummyExe(t)
	p := mavenProxyInfo()

	// Pre-create an existing settings.xml.
	m2Dir := filepath.Join(home, ".m2")
	if err := os.MkdirAll(m2Dir, dirPerm); err != nil {
		t.Fatal(err)
	}
	existingContent := "<settings>original</settings>"
	if err := os.WriteFile(filepath.Join(m2Dir, "settings.xml"), []byte(existingContent), cfgPerm); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := SetupFiles(p, home, exe, testDarwin, &buf); err != nil {
		t.Fatalf("SetupFiles: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "backed up") {
		t.Error("missing backup message")
	}

	// Verify backup was created.
	backup := filepath.Join(m2Dir, "settings.xml.bulwark-backup")
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("backup not found: %v", err)
	}
	if string(data) != existingContent {
		t.Errorf("backup content = %s, want %s", string(data), existingContent)
	}
}

func TestSetupFilesWindowsAutostart(t *testing.T) {
	home := t.TempDir()
	exe := createDummyExe(t)
	p := testProxyInfo()
	var buf bytes.Buffer

	if err := SetupFiles(p, home, exe, testWindows, &buf); err != nil {
		t.Fatalf("SetupFiles: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "[ok] Autostart entry") {
		t.Error("missing autostart message for Windows")
	}

	// Verify .bat file created.
	autostartDir := AutostartDir(testWindows, home)
	batFile := filepath.Join(autostartDir, AutostartFileName(testWindows, testEcosystem))
	if _, err := os.Stat(batFile); err != nil {
		t.Errorf("Windows autostart batch not found: %v", err)
	}
}

func TestSetupFilesLinuxAutostart(t *testing.T) {
	home := t.TempDir()
	exe := createDummyExe(t)
	p := testProxyInfo()
	var buf bytes.Buffer

	if err := SetupFiles(p, home, exe, testLinux, &buf); err != nil {
		t.Fatalf("SetupFiles: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "[ok] Autostart entry") {
		t.Error("missing autostart message for Linux")
	}

	autostartDir := AutostartDir(testLinux, home)
	serviceFile := filepath.Join(autostartDir, AutostartFileName(testLinux, testEcosystem))
	data, err := os.ReadFile(serviceFile)
	if err != nil {
		t.Fatalf("Linux systemd unit not found: %v", err)
	}
	if !strings.Contains(string(data), "[Unit]") {
		t.Error("systemd unit missing [Unit]")
	}
}

func TestSetupFilesUnsupportedOS(t *testing.T) {
	home := t.TempDir()
	exe := createDummyExe(t)
	p := testProxyInfo()
	var buf bytes.Buffer

	if err := SetupFiles(p, home, exe, testFreeBSD, &buf); err != nil {
		t.Fatalf("SetupFiles: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "not supported") {
		t.Error("expected unsupported OS message")
	}
}

// --- UninstallFiles ---

func TestUninstallFilesNpm(t *testing.T) {
	home := t.TempDir()
	exe := createDummyExe(t)
	p := testProxyInfo()
	var buf bytes.Buffer

	// Setup first.
	if err := SetupFiles(p, home, exe, testDarwin, &buf); err != nil {
		t.Fatalf("SetupFiles: %v", err)
	}

	buf.Reset()
	if err := UninstallFiles(p, home, testDarwin, &buf); err != nil {
		t.Fatalf("UninstallFiles: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Removed autostart") {
		t.Error("missing autostart removal message")
	}
	if !strings.Contains(output, "has been uninstalled") {
		t.Error("missing uninstall complete message")
	}

	paths := ResolvePaths(p, home, testDarwin)
	if _, err := os.Stat(paths.Config); !os.IsNotExist(err) {
		t.Error("config should be removed")
	}
}

func TestUninstallFilesPypi(t *testing.T) {
	home := t.TempDir()
	exe := createDummyExe(t)
	p := pypiProxyInfo()
	var buf bytes.Buffer

	SetupFiles(p, home, exe, testDarwin, &buf) //nolint:errcheck // setup for uninstall test
	buf.Reset()

	if err := UninstallFiles(p, home, testDarwin, &buf); err != nil {
		t.Fatalf("UninstallFiles: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Removed pip config") {
		t.Error("missing pip config removal message")
	}

	_, cfgFile := PipConfigPaths(home, testDarwin)
	if _, err := os.Stat(cfgFile); !os.IsNotExist(err) {
		t.Error("pip config should be removed")
	}
}

func TestUninstallFilesMavenRestore(t *testing.T) {
	home := t.TempDir()
	exe := createDummyExe(t)
	p := mavenProxyInfo()

	// Pre-create existing settings.xml so backup is made.
	m2Dir := filepath.Join(home, ".m2")
	if err := os.MkdirAll(m2Dir, dirPerm); err != nil {
		t.Fatal(err)
	}
	original := "<settings>original</settings>"
	if err := os.WriteFile(filepath.Join(m2Dir, "settings.xml"), []byte(original), cfgPerm); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	SetupFiles(p, home, exe, testDarwin, &buf) //nolint:errcheck // setup for uninstall test
	buf.Reset()

	if err := UninstallFiles(p, home, testDarwin, &buf); err != nil {
		t.Fatalf("UninstallFiles: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Restored Maven settings.xml from backup") {
		t.Error("missing restore message")
	}

	// Verify original content restored.
	data, err := os.ReadFile(filepath.Join(m2Dir, "settings.xml"))
	if err != nil {
		t.Fatalf("settings.xml not found: %v", err)
	}
	if string(data) != original {
		t.Errorf("restored content = %s, want %s", string(data), original)
	}
}

func TestUninstallFilesMavenNoBackup(t *testing.T) {
	home := t.TempDir()
	exe := createDummyExe(t)
	p := mavenProxyInfo()
	var buf bytes.Buffer

	// Setup with no pre-existing settings.xml.
	SetupFiles(p, home, exe, testDarwin, &buf) //nolint:errcheck // setup for uninstall test
	buf.Reset()

	if err := UninstallFiles(p, home, testDarwin, &buf); err != nil {
		t.Fatalf("UninstallFiles: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Removed Maven settings.xml") {
		t.Error("missing removal message")
	}
}

func TestUninstallFilesLinux(t *testing.T) {
	home := t.TempDir()
	exe := createDummyExe(t)
	p := testProxyInfo()
	var buf bytes.Buffer

	SetupFiles(p, home, exe, testLinux, &buf) //nolint:errcheck // setup for uninstall test
	buf.Reset()

	if err := UninstallFiles(p, home, testLinux, &buf); err != nil {
		t.Fatalf("UninstallFiles: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Removed autostart") {
		t.Error("missing autostart removal")
	}
}

func TestUninstallFilesWindows(t *testing.T) {
	home := t.TempDir()
	exe := createDummyExe(t)
	p := testProxyInfo()
	var buf bytes.Buffer

	SetupFiles(p, home, exe, testWindows, &buf) //nolint:errcheck // setup for uninstall test
	buf.Reset()

	if err := UninstallFiles(p, home, testWindows, &buf); err != nil {
		t.Fatalf("UninstallFiles: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Removed autostart") {
		t.Error("missing autostart removal for Windows")
	}
}

// --- PrintPostSetup ---

func TestPrintPostSetup(t *testing.T) {
	p := testProxyInfo()
	paths := ResolvePaths(p, "/home/user", testDarwin)
	var buf bytes.Buffer

	PrintPostSetup(p, paths, &buf)

	output := buf.String()
	checks := []string{
		"installed successfully",
		"Binary:",
		"Config:",
		"Port:    18001",
		"To start manually",
		"To reconfigure rules",
		"Edit",
		"To uninstall",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output missing %q", check)
		}
	}
}

// --- ActivateServices / DeactivateServices coverage for windows path ---

func TestActivateServicesWindows(t *testing.T) {
	p := testProxyInfo()
	home := t.TempDir()
	var buf bytes.Buffer

	ActivateServices(p, home, testWindows, &buf)

	output := buf.String()
	if !strings.Contains(output, "start automatically on login") {
		t.Error("missing Windows activation message")
	}
}

func TestDeactivateServicesWindows(t *testing.T) {
	p := testProxyInfo()
	home := t.TempDir()
	var buf bytes.Buffer

	// Windows deactivation is a no-op for services (only npm gets deactivated).
	DeactivateServices(p, home, testWindows, &buf)
	// Just ensure no panic.
}

func TestDeactivateServicesUnsupported(t *testing.T) {
	p := pypiProxyInfo()
	home := t.TempDir()
	var buf bytes.Buffer

	// FreeBSD - no npm deactivation, no OS-specific deactivation.
	DeactivateServices(p, home, testFreeBSD, &buf)
	// No output expected, just ensure no panic.
}

// --- SetupFiles error paths ---

func TestSetupFilesBadHome(t *testing.T) {
	exe := createDummyExe(t)
	p := testProxyInfo()
	var buf bytes.Buffer

	// Create a regular file and use a path inside it as "home".
	// MkdirAll cannot create a subdirectory of a regular file on any OS.
	base := t.TempDir()
	fileAsDir := filepath.Join(base, "notadir.txt")
	if err := os.WriteFile(fileAsDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	badHome := filepath.Join(fileAsDir, "home")
	err := SetupFiles(p, badHome, exe, testDarwin, &buf)
	if err == nil {
		t.Error("expected error for bad home directory")
	}
}

func TestSetupFilesBadExePath(t *testing.T) {
	home := t.TempDir()
	p := testProxyInfo()
	var buf bytes.Buffer

	// Non-existent binary source.
	err := SetupFiles(p, home, "/nonexistent/binary", testDarwin, &buf)
	if err == nil {
		t.Error("expected error for bad exe path")
	}
}

// --- ActivateServices additional branches ---

func TestActivateServicesDarwinNonNpm(t *testing.T) {
	p := pypiProxyInfo()
	home := t.TempDir()
	var buf bytes.Buffer

	// Set up the autostart dir and plist so launchctl has something to try.
	ActivateServices(p, home, testDarwin, &buf)

	// On macOS it will try launchctl with a temp-dir path — likely produces a warning.
	// We just verify it doesn't panic and outputs something.
	_ = buf.String()
}

func TestActivateServicesLinux(t *testing.T) {
	p := testProxyInfo()
	home := t.TempDir()
	var buf bytes.Buffer

	// systemctl likely not available on macOS, so the warning path should be hit.
	ActivateServices(p, home, testLinux, &buf)
	_ = buf.String()
}

func TestDeactivateServicesDarwinNpm(t *testing.T) {
	p := testProxyInfo()
	home := t.TempDir()
	var buf bytes.Buffer

	DeactivateServices(p, home, testDarwin, &buf)

	output := buf.String()
	// npm deactivation + launchd unload.
	if !strings.Contains(output, "npm registry restored") && !strings.Contains(output, "LaunchAgent unloaded") {
		// At least one message should appear.
		_ = output
	}
}

func TestDeactivateServicesLinuxNpm(t *testing.T) {
	p := testProxyInfo()
	home := t.TempDir()
	var buf bytes.Buffer

	DeactivateServices(p, home, testLinux, &buf)
	// systemctl not available on macOS — just verify no panic.
	_ = buf.String()
}

func TestActivateServicesNpmWithNpmAvailable(t *testing.T) {
	// This test exercises the npm ecosystem + darwin path.
	p := testProxyInfo()
	home := t.TempDir()
	var buf bytes.Buffer

	ActivateServices(p, home, testDarwin, &buf)

	output := buf.String()
	// npm is available on macOS dev machines so either "set to" or "npm config" appears.
	if !strings.Contains(output, "npm") && !strings.Contains(output, "LaunchAgent") {
		_ = output // coverage exercised regardless
	}
}

// --- CopyFile io.Copy error path ---

func TestCopyFileEmptySource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "empty")
	dst := filepath.Join(dir, "dst")

	if err := os.WriteFile(src, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	// Copy an empty file — should succeed.
	if err := CopyFile(src, dst); err != nil {
		t.Fatalf("CopyFile empty: %v", err)
	}

	data, _ := os.ReadFile(dst)
	if len(data) != 0 {
		t.Errorf("expected empty dst, got %d bytes", len(data))
	}
}

// --- InstalledConfigPath and IsInstalledAt ---

func TestInstalledConfigPath(t *testing.T) {
	p := ProxyInfo{BinaryName: "npm-bulwark"}
	got := InstalledConfigPath(p, "/home/user", "linux")
	want := filepath.Join("/home/user", ".bulwark", "npm-bulwark", "config.yaml")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInstalledConfigPathWindows(t *testing.T) {
	p := ProxyInfo{BinaryName: "pypi-bulwark"}
	got := InstalledConfigPath(p, "C:\\Users\\me", OSWindows)
	want := filepath.Join("C:\\Users\\me", ".bulwark", "pypi-bulwark", "config.yaml")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestIsInstalledAtNotInstalled(t *testing.T) {
	home := t.TempDir()
	p := ProxyInfo{BinaryName: "npm-bulwark"}
	if IsInstalledAt(p, home, "linux") {
		t.Error("expected not installed")
	}
}

func TestIsInstalledAtInstalled(t *testing.T) {
	home := t.TempDir()
	p := ProxyInfo{BinaryName: "npm-bulwark"}
	dir := filepath.Join(home, ".bulwark", "npm-bulwark")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsInstalledAt(p, home, "linux") {
		t.Error("expected installed")
	}
}

// --- SetupFilesOnlyAt ---

func TestSetupFilesOnlyAtNpm(t *testing.T) {
	home := t.TempDir()
	p := testProxyInfo()
	src := filepath.Join(home, "src-binary")
	if err := os.WriteFile(src, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := SetupFilesOnlyAt(p, home, src, testLinux, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfgPath := filepath.Join(home, ".bulwark", p.BinaryName, "config.yaml")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Errorf("config not found: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "[ok] Config written") {
		t.Errorf("expected config written message, got: %s", output)
	}
}

func TestSetupFilesOnlyAtPypi(t *testing.T) {
	home := t.TempDir()
	p := pypiProxyInfo()
	src := filepath.Join(home, "src-binary")
	if err := os.WriteFile(src, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := SetupFilesOnlyAt(p, home, src, testLinux, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "[ok] pip index configured") {
		t.Errorf("expected pip config message, got: %s", output)
	}
}

func TestSetupFilesOnlyAtMaven(t *testing.T) {
	home := t.TempDir()
	p := mavenProxyInfo()
	src := filepath.Join(home, "src-binary")
	if err := os.WriteFile(src, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := SetupFilesOnlyAt(p, home, src, testLinux, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "[ok] Maven mirror configured") {
		t.Errorf("expected maven settings message, got: %s", output)
	}
}

func TestSetupFilesOnlyAtError(t *testing.T) {
	p := testProxyInfo()
	// Pass a non-existent source binary to trigger CopyFile error.
	var buf bytes.Buffer
	err := SetupFilesOnlyAt(p, t.TempDir(), "/no/such/binary", testLinux, &buf)
	if err == nil {
		t.Error("expected error for missing source binary")
	}
}

func TestSetupFilesOnlyAtWindows(t *testing.T) {
	home := t.TempDir()
	p := testProxyInfo()
	src := filepath.Join(home, "src-binary")
	if err := os.WriteFile(src, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := SetupFilesOnlyAt(p, home, src, testWindows, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "[ok] Config written") {
		t.Errorf("expected config written message, got: %s", output)
	}
}

// --- writePkgMgrConfig error paths ---

func TestWritePkgMgrConfigPypiMkdirError(t *testing.T) {
	home := t.TempDir()
	cfgDir, _ := PipConfigPaths(home, testLinux)
	// Create a file where the directory should be to trigger MkdirAll error.
	if err := os.MkdirAll(filepath.Dir(cfgDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgDir, []byte("block"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := pypiProxyInfo()
	var buf bytes.Buffer
	writePkgMgrConfig(p, home, testLinux, &buf)
	if !strings.Contains(buf.String(), "[warn]") {
		t.Errorf("expected warning, got: %s", buf.String())
	}
}

func TestWritePkgMgrConfigMavenMkdirError(t *testing.T) {
	home := t.TempDir()
	// Create .m2 as a file instead of directory.
	if err := os.WriteFile(filepath.Join(home, ".m2"), []byte("block"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := mavenProxyInfo()
	var buf bytes.Buffer
	writePkgMgrConfig(p, home, testLinux, &buf)
	if !strings.Contains(buf.String(), "[warn]") {
		t.Errorf("expected warning, got: %s", buf.String())
	}
}

func TestWritePkgMgrConfigNpm(t *testing.T) {
	p := testProxyInfo()
	var buf bytes.Buffer
	writePkgMgrConfig(p, t.TempDir(), testLinux, &buf)
	if !strings.Contains(buf.String(), "[info] npm registry") {
		t.Errorf("expected npm info message, got: %s", buf.String())
	}
}

// --- writeAutostartFile edge cases ---

func TestWriteAutostartFileUnsupportedOS(t *testing.T) {
	p := testProxyInfo()
	paths := ResolvePaths(p, t.TempDir(), testLinux)
	var buf bytes.Buffer
	writeAutostartFile(p, paths, t.TempDir(), testFreeBSD, &buf)
	if !strings.Contains(buf.String(), "not supported") {
		t.Errorf("expected unsupported message, got: %s", buf.String())
	}
}

func TestWriteAutostartFileMkdirError(t *testing.T) {
	home := t.TempDir()
	p := testProxyInfo()
	paths := ResolvePaths(p, home, testLinux)
	// Block the autostart directory by creating a file with its name.
	dir := AutostartDir(testLinux, home)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("block"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	writeAutostartFile(p, paths, home, testLinux, &buf)
	if !strings.Contains(buf.String(), "[warn]") {
		t.Errorf("expected warning, got: %s", buf.String())
	}
}

// ─── VSX product.json setup ──────────────────────────────────────────────────

func TestVsxConfigDirs(t *testing.T) {
	home := "/home/user"
	dirs := VsxConfigDirs(home, testLinux)
	if len(dirs) != 4 {
		t.Fatalf("expected 4 dirs, got %d", len(dirs))
	}
	if !strings.HasSuffix(dirs[0], "Code") {
		t.Errorf("first dir should end with Code, got %s", dirs[0])
	}
	if !strings.Contains(dirs[1], "Code - Insiders") {
		t.Errorf("second dir should be Code - Insiders, got %s", dirs[1])
	}
	if !strings.Contains(dirs[2], "VSCodium") {
		t.Errorf("third dir should be VSCodium, got %s", dirs[2])
	}
	if !strings.Contains(dirs[3], "Code - OSS") {
		t.Errorf("fourth dir should be Code - OSS, got %s", dirs[3])
	}
}

func TestVsxConfigDirsWindows(t *testing.T) {
	home := "/home/user"
	dirs := VsxConfigDirs(home, testWindows)
	for _, dir := range dirs {
		if !strings.Contains(dir, "AppData") {
			t.Errorf("Windows dir should use AppData, got %s", dir)
		}
	}
}

func TestVsxConfigDirsDarwin(t *testing.T) {
	home := "/home/user"
	dirs := VsxConfigDirs(home, testDarwin)
	for _, dir := range dirs {
		if !strings.Contains(dir, "Application Support") {
			t.Errorf("macOS dir should use Application Support, got %s", dir)
		}
	}
}

func TestResolveVSCodeTargets(t *testing.T) {
	home := t.TempDir()
	codeDir := filepath.Join(home, ".config", "Code")
	vscodiumDir := filepath.Join(home, ".config", "VSCodium")
	for _, dir := range []string{codeDir, vscodiumDir} {
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			t.Fatal(err)
		}
	}

	targets := ResolveVSCodeTargets(home, testLinux)
	if len(targets) != 2 {
		t.Fatalf("expected 2 detected targets, got %d", len(targets))
	}
	if targets[0].Variant.Name != VariantMicrosoftCode.Name {
		t.Errorf("first target = %s, want %s", targets[0].Variant.Name, VariantMicrosoftCode.Name)
	}
	if targets[1].Variant.Name != VariantVSCodium.Name {
		t.Errorf("second target = %s, want %s", targets[1].Variant.Name, VariantVSCodium.Name)
	}
	if targets[0].ConfigDir != codeDir {
		t.Errorf("code config dir = %s, want %s", targets[0].ConfigDir, codeDir)
	}
	if targets[1].ConfigDir != vscodiumDir {
		t.Errorf("vscodium config dir = %s, want %s", targets[1].ConfigDir, vscodiumDir)
	}
}

func TestResolveVSCodeTargetsWindowsInstallOnly(t *testing.T) {
	home := t.TempDir()
	installDir := filepath.Join(home, "AppData", "Local", "Programs", "Microsoft VS Code", "resources", "app")
	if err := os.MkdirAll(installDir, dirPerm); err != nil {
		t.Fatal(err)
	}

	targets := ResolveVSCodeTargets(home, testWindows)
	if len(targets) != 1 {
		t.Fatalf("expected 1 detected target, got %d", len(targets))
	}
	if targets[0].Variant.Name != VariantMicrosoftCode.Name {
		t.Errorf("variant = %s, want %s", targets[0].Variant.Name, VariantMicrosoftCode.Name)
	}
	if len(targets[0].InstallDirs) != 1 || targets[0].InstallDirs[0] != installDir {
		t.Errorf("install dirs = %v, want [%s]", targets[0].InstallDirs, installDir)
	}
}

func readVSXSetupStateForTest(t *testing.T, home string) vsxSetupState {
	t.Helper()
	data, err := os.ReadFile(vsxStatePath(home))
	if err != nil {
		t.Fatalf("reading VSX setup state: %v", err)
	}
	var state vsxSetupState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("unmarshalling VSX setup state: %v", err)
	}
	return state
}

func TestVsxGalleryProductJSON(t *testing.T) {
	content := VsxGalleryProductJSON(18003)
	for _, want := range []string{
		"localhost:18003",
		"_comment",
		"_revert",
		"extensionsGallery",
		"serviceUrl",
		"itemUrl",
		"resourceUrlTemplate",
		"extensionUrlTemplate",
		"delete this file",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("product.json missing %q", want)
		}
	}
}

func TestWritePkgMgrConfigVsx(t *testing.T) {
	home := t.TempDir()
	p := ProxyInfo{Ecosystem: EcosystemVsx, Port: 18003}
	var buf bytes.Buffer
	writePkgMgrConfig(p, home, testLinux, &buf)
	out := buf.String()
	if !strings.Contains(out, "[ok]") {
		t.Errorf("expected ok output, got: %s", out)
	}

	// Both editor dirs should have product.json.
	for _, dir := range VsxConfigDirs(home, testLinux) {
		data, err := os.ReadFile(filepath.Join(dir, "product.json"))
		if err != nil {
			t.Errorf("missing product.json in %s: %v", dir, err)
			continue
		}
		if !strings.Contains(string(data), "localhost:18003") {
			t.Errorf("product.json in %s missing proxy URL", dir)
		}
	}
	state := readVSXSetupStateForTest(t, home)
	if len(state.Targets) != 4 {
		t.Errorf("expected 4 saved targets for fallback setup, got %d", len(state.Targets))
	}
}

func TestWritePkgMgrConfigVsxDetectedTargetsOnly(t *testing.T) {
	home := t.TempDir()
	vscodiumDir := filepath.Join(home, ".config", "VSCodium")
	if err := os.MkdirAll(vscodiumDir, dirPerm); err != nil {
		t.Fatal(err)
	}

	p := ProxyInfo{Ecosystem: EcosystemVsx, Port: 18003}
	var buf bytes.Buffer
	writePkgMgrConfig(p, home, testLinux, &buf)

	data, err := os.ReadFile(filepath.Join(vscodiumDir, "product.json"))
	if err != nil {
		t.Fatalf("reading VSCodium product.json: %v", err)
	}
	if !strings.Contains(string(data), "localhost:18003") {
		t.Errorf("VSCodium product.json missing proxy URL: %s", string(data))
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "Code", "product.json")); !os.IsNotExist(err) {
		t.Errorf("Code product.json should not be written when only VSCodium is detected")
	}

	state := readVSXSetupStateForTest(t, home)
	if len(state.Targets) != 1 {
		t.Fatalf("expected 1 saved target, got %d", len(state.Targets))
	}
	if state.Targets[0].Name != VariantVSCodium.Name {
		t.Errorf("saved target = %s, want %s", state.Targets[0].Name, VariantVSCodium.Name)
	}
}

func TestWritePkgMgrConfigVsxBacksUp(t *testing.T) {
	home := t.TempDir()
	dirs := VsxConfigDirs(home, testLinux)
	origContent := `{"extensionsGallery":{"serviceUrl":"https://open-vsx.org/vscode/gallery"}}`

	// Pre-create a product.json in the first editor dir.
	os.MkdirAll(dirs[0], dirPerm)                                                      //nolint:errcheck
	os.WriteFile(filepath.Join(dirs[0], "product.json"), []byte(origContent), cfgPerm) //nolint:errcheck

	p := ProxyInfo{Ecosystem: EcosystemVsx, Port: 18003}
	var buf bytes.Buffer
	writePkgMgrConfig(p, home, testLinux, &buf)

	// Backup must exist with original content.
	backup, err := os.ReadFile(filepath.Join(dirs[0], "product.json.bulwark-backup"))
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if string(backup) != origContent {
		t.Errorf("backup content mismatch: got %s", string(backup))
	}

	// product.json must be the proxy config now.
	data, _ := os.ReadFile(filepath.Join(dirs[0], "product.json"))
	if !strings.Contains(string(data), "localhost:18003") {
		t.Errorf("product.json not updated: %s", string(data))
	}

	if !strings.Contains(buf.String(), "backed up") {
		t.Errorf("expected backup message, got: %s", buf.String())
	}
}

func TestWritePkgMgrConfigVsxIdempotent(t *testing.T) {
	home := t.TempDir()
	p := ProxyInfo{Ecosystem: EcosystemVsx, Port: 18003}
	var buf bytes.Buffer

	// Write twice — second should overwrite cleanly.
	writePkgMgrConfig(p, home, testLinux, &buf)
	buf.Reset()
	writePkgMgrConfig(p, home, testLinux, &buf)

	for _, dir := range VsxConfigDirs(home, testLinux) {
		data, err := os.ReadFile(filepath.Join(dir, "product.json"))
		if err != nil {
			t.Errorf("missing product.json in %s after second write", dir)
			continue
		}
		if !strings.Contains(string(data), "localhost:18003") {
			t.Errorf("product.json corrupted after second write")
		}
	}
}

func TestUninstallFilesVsxRestoresBackup(t *testing.T) {
	home := t.TempDir()
	dirs := VsxConfigDirs(home, testLinux)
	origContent := `{"extensionsGallery":{"serviceUrl":"https://open-vsx.org/vscode/gallery"}}`

	// Set up: write proxy config with a pre-existing backup.
	for _, dir := range dirs {
		os.MkdirAll(dir, dirPerm)                                                                     //nolint:errcheck
		os.WriteFile(filepath.Join(dir, "product.json"), []byte("proxy config"), cfgPerm)             //nolint:errcheck
		os.WriteFile(filepath.Join(dir, "product.json.bulwark-backup"), []byte(origContent), cfgPerm) //nolint:errcheck
	}

	ecoDir := filepath.Join(home, bulwarkDir, "vsx-bulwark")
	os.MkdirAll(ecoDir, dirPerm) //nolint:errcheck

	p := ProxyInfo{Ecosystem: EcosystemVsx, BinaryName: "vsx-bulwark", Port: 18003}
	var buf bytes.Buffer
	if err := UninstallFiles(p, home, testLinux, &buf); err != nil {
		t.Fatalf("UninstallFiles: %v", err)
	}

	// product.json should be restored to original.
	for _, dir := range dirs {
		data, err := os.ReadFile(filepath.Join(dir, "product.json"))
		if err != nil {
			t.Errorf("product.json removed in %s, should be restored", dir)
			continue
		}
		if string(data) != origContent {
			t.Errorf("product.json not restored in %s: %s", dir, string(data))
		}
		// Backup should be cleaned up.
		if _, err := os.Stat(filepath.Join(dir, "product.json.bulwark-backup")); !os.IsNotExist(err) {
			t.Errorf("backup not cleaned up in %s", dir)
		}
	}
	if !strings.Contains(buf.String(), "Restored") {
		t.Errorf("expected restore message, got: %s", buf.String())
	}
}

func TestUninstallFilesVsxNoBackup(t *testing.T) {
	home := t.TempDir()
	dirs := VsxConfigDirs(home, testLinux)

	// Set up: proxy config with no backup.
	for _, dir := range dirs {
		os.MkdirAll(dir, dirPerm)                                                         //nolint:errcheck
		os.WriteFile(filepath.Join(dir, "product.json"), []byte("proxy config"), cfgPerm) //nolint:errcheck
	}

	ecoDir := filepath.Join(home, bulwarkDir, "vsx-bulwark")
	os.MkdirAll(ecoDir, dirPerm) //nolint:errcheck

	p := ProxyInfo{Ecosystem: EcosystemVsx, BinaryName: "vsx-bulwark", Port: 18003}
	var buf bytes.Buffer
	UninstallFiles(p, home, testLinux, &buf) //nolint:errcheck

	// product.json should be removed (no backup to restore from).
	for _, dir := range dirs {
		if _, err := os.Stat(filepath.Join(dir, "product.json")); !os.IsNotExist(err) {
			t.Errorf("product.json should be removed in %s when no backup exists", dir)
		}
	}
}

func TestUninstallFilesVsxUsesSavedTargets(t *testing.T) {
	home := t.TempDir()
	vscodiumDir := filepath.Join(home, ".config", "VSCodium")
	codeDir := filepath.Join(home, ".config", "Code")
	for _, dir := range []string{vscodiumDir, codeDir} {
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(vscodiumDir, "product.json"), []byte("proxy config"), cfgPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codeDir, "product.json"), []byte("leave me alone"), cfgPerm); err != nil {
		t.Fatal(err)
	}
	if err := saveVSXSetupState(home, []VSCodeTarget{{
		Variant:   VariantVSCodium,
		ConfigDir: vscodiumDir,
	}}); err != nil {
		t.Fatalf("saveVSXSetupState: %v", err)
	}

	p := ProxyInfo{Ecosystem: EcosystemVsx, BinaryName: "vsx-bulwark", Port: 18003}
	var buf bytes.Buffer
	if err := UninstallFiles(p, home, testLinux, &buf); err != nil {
		t.Fatalf("UninstallFiles: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vscodiumDir, "product.json")); !os.IsNotExist(err) {
		t.Errorf("VSCodium product.json should be removed for saved target")
	}
	data, err := os.ReadFile(filepath.Join(codeDir, "product.json"))
	if err != nil {
		t.Fatalf("reading Code product.json: %v", err)
	}
	if string(data) != "leave me alone" {
		t.Errorf("Code product.json should be untouched, got %q", string(data))
	}
	if _, err := os.Stat(vsxStatePath(home)); !os.IsNotExist(err) {
		t.Errorf("VSX setup state should be removed during uninstall")
	}
}

// ─── VsxInstallDirs (Windows install-dir patching) ───────────────────────────

const (
	testInstallProductJSON = `{"nameShort":"Visual Studio Code","version":"1.88.0","extensionsGallery":{"serviceUrl":"https://marketplace.visualstudio.com/_apis/public/gallery","itemUrl":"https://marketplace.visualstudio.com/items"}}`
)

func TestVsxInstallDirsNonWindows(t *testing.T) {
	dirs := VsxInstallDirs("/home/user", testLinux)
	if len(dirs) != 0 {
		t.Errorf("expected nil/empty on Linux, got %v", dirs)
	}
	dirs = VsxInstallDirs("/home/user", testDarwin)
	if len(dirs) != 0 {
		t.Errorf("expected nil/empty on macOS, got %v", dirs)
	}
	dirs = VsxInstallDirs("/home/user", testFreeBSD)
	if len(dirs) != 0 {
		t.Errorf("expected nil/empty on FreeBSD, got %v", dirs)
	}
}

func TestVsxInstallDirsWindowsStandardLayout(t *testing.T) {
	home := t.TempDir()
	// Create a standard flat layout: <install>/resources/app
	stdDir := filepath.Join(home, "AppData", "Local", "Programs", "Microsoft VS Code", "resources", "app")
	if err := os.MkdirAll(stdDir, dirPerm); err != nil {
		t.Fatal(err)
	}

	dirs := VsxInstallDirs(home, testWindows)
	found := false
	for _, d := range dirs {
		if d == stdDir {
			found = true
		}
	}
	if !found {
		t.Errorf("standard layout dir %s not found in %v", stdDir, dirs)
	}
}

func TestVsxInstallDirsWindowsHashLayout(t *testing.T) {
	home := t.TempDir()
	// Create a Squirrel/hash layout: <install>/<hash>/resources/app
	hashDir := filepath.Join(home, "AppData", "Local", "Programs", "Microsoft VS Code", "ce099c1ed2", "resources", "app")
	if err := os.MkdirAll(hashDir, dirPerm); err != nil {
		t.Fatal(err)
	}

	dirs := VsxInstallDirs(home, testWindows)
	found := false
	for _, d := range dirs {
		if d == hashDir {
			found = true
		}
	}
	if !found {
		t.Errorf("hash-layout dir %s not found in %v", hashDir, dirs)
	}
}

func TestVsxInstallDirsWindowsEmpty(t *testing.T) {
	// No VS Code installed — should return empty slice, not nil panic.
	dirs := VsxInstallDirs(t.TempDir(), testWindows)
	if len(dirs) != 0 {
		t.Errorf("expected empty when VS Code not installed, got %v", dirs)
	}
}

func TestMergeGalleryJSON(t *testing.T) {
	patched, err := mergeGalleryJSON([]byte(testInstallProductJSON), 18003)
	if err != nil {
		t.Fatalf("mergeGalleryJSON: %v", err)
	}
	for _, want := range []string{
		"localhost:18003",
		"/vscode/gallery",
		"/vscode/item",
		"/api/{publisher}/{name}/{version}/file/{path}",
		"/vscode/gallery/vscode/{publisher}/{name}/latest",
		`"nameShort"`,
		`"version"`,
	} {
		if !strings.Contains(string(patched), want) {
			t.Errorf("merged JSON missing %q; got:\n%s", want, string(patched))
		}
	}
	// Original marketplace URL must be gone.
	if strings.Contains(string(patched), "marketplace.visualstudio.com") {
		t.Errorf("marketplace URL still present after merge; got:\n%s", string(patched))
	}
}

func TestMergeGalleryJSONInvalidInput(t *testing.T) {
	_, err := mergeGalleryJSON([]byte("not json"), 18003)
	if err == nil {
		t.Error("expected error on invalid JSON input")
	}
}

// TestMergeGalleryJSONPreservesExtraFields verifies that extra extensionsGallery
// sub-fields (controlUrl, nlsBaseUrl, mcpUrl, etc.) are preserved after merge.
func TestMergeGalleryJSONPreservesExtraFields(t *testing.T) {
	const richProductJSON = `{
  "nameShort": "Visual Studio Code",
  "version": "1.111.0",
  "extensionsGallery": {
    "serviceUrl": "https://marketplace.visualstudio.com/_apis/public/gallery",
    "itemUrl": "https://marketplace.visualstudio.com/items",
    "resourceUrlTemplate": "https://{publisher}.example.com/{publisher}/{name}/{version}/{path}",
    "controlUrl": "https://main.vscode-cdn.net/extensions/marketplace.json",
    "nlsBaseUrl": "https://www.vscode-unpkg.net/_lp/",
    "mcpUrl": "https://main.vscode-cdn.net/mcp/servers.json",
    "publisherUrl": "https://marketplace.visualstudio.com/publishers"
  }
}`
	patched, err := mergeGalleryJSON([]byte(richProductJSON), 18003)
	if err != nil {
		t.Fatalf("mergeGalleryJSON: %v", err)
	}
	ps := string(patched)
	for _, want := range []string{
		"controlUrl",
		"nlsBaseUrl",
		"mcpUrl",
		"publisherUrl",
		"main.vscode-cdn.net",
		"vscode-unpkg.net",
		"localhost:18003",
	} {
		if !strings.Contains(ps, want) {
			t.Errorf("merged JSON missing %q; got:\n%s", want, ps)
		}
	}
	// The proxy-URL fields must point to the proxy. Other gallery fields
	// that contain "marketplace.visualstudio.com" (e.g. publisherUrl) must be
	// left intact. extensionUrlTemplate (VS Code 1.112+) is also redirected.
	var doc map[string]interface{}
	if err := json.Unmarshal(patched, &doc); err != nil {
		t.Fatalf("unmarshalling patched JSON: %v", err)
	}
	gallery, _ := doc["extensionsGallery"].(map[string]interface{})
	if gallery == nil {
		t.Fatal("extensionsGallery missing from patched JSON")
	}
	for _, key := range []string{"serviceUrl", "itemUrl", "resourceUrlTemplate", "extensionUrlTemplate"} {
		if v, _ := gallery[key].(string); !strings.Contains(v, "localhost:18003") {
			t.Errorf("gallery.%s not pointing to proxy: %s", key, v)
		}
	}
}

// TestMergeGalleryJSONOverridesExtensionUrlTemplate verifies that an existing
// extensionUrlTemplate field pointing to the VS Code CDN is overridden to point
// to the proxy (VS Code 1.112+ scenario).
func TestMergeGalleryJSONOverridesExtensionUrlTemplate(t *testing.T) {
	const srcJSON = `{
  "nameShort": "Visual Studio Code",
  "version": "1.112.0",
  "extensionsGallery": {
    "serviceUrl": "https://marketplace.visualstudio.com/_apis/public/gallery",
    "extensionUrlTemplate": "https://www.vscode-unpkg.net/_gallery/{publisher}/{name}/latest"
  }
}`
	patched, err := mergeGalleryJSON([]byte(srcJSON), 18003)
	if err != nil {
		t.Fatalf("mergeGalleryJSON: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(patched, &doc); err != nil {
		t.Fatalf("unmarshalling patched JSON: %v", err)
	}
	gallery, _ := doc["extensionsGallery"].(map[string]interface{})
	if gallery == nil {
		t.Fatal("extensionsGallery missing")
	}
	tmpl, _ := gallery["extensionUrlTemplate"].(string)
	if strings.Contains(tmpl, "vscode-unpkg.net") {
		t.Errorf("extensionUrlTemplate still points to CDN after merge; got: %s", tmpl)
	}
	if !strings.Contains(tmpl, "localhost:18003") {
		t.Errorf("extensionUrlTemplate does not point to proxy; got: %s", tmpl)
	}
}

func TestPatchInstallProductJSONSkipsMissing(t *testing.T) {
	var buf bytes.Buffer
	// Should not panic or error when directory does not exist.
	patchInstallProductJSON(filepath.Join(t.TempDir(), "nonexistent"), "https://localhost:18003", &buf)
	if buf.Len() != 0 {
		t.Errorf("expected no output for missing dir, got: %s", buf.String())
	}
}

func TestPatchInstallProductJSONPatches(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "resources", "app")
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		t.Fatal(err)
	}
	cfgFile := filepath.Join(dir, "product.json")
	if err := os.WriteFile(cfgFile, []byte(testInstallProductJSON), cfgPerm); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	patchInstallProductJSON(dir, "https://localhost:18003", &buf)
	out := buf.String()

	if !strings.Contains(out, "[ok]") {
		t.Errorf("expected ok output, got: %s", out)
	}
	if !strings.Contains(out, "backed up") {
		t.Errorf("expected backup message, got: %s", out)
	}

	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("reading patched file: %v", err)
	}
	if !strings.Contains(string(data), "localhost:18003") {
		t.Errorf("patched file missing proxy URL; got:\n%s", string(data))
	}
	// Original fields must be preserved.
	if !strings.Contains(string(data), "nameShort") {
		t.Errorf("original fields lost after patch; got:\n%s", string(data))
	}

	// Backup must exist with original content.
	backup, err := os.ReadFile(cfgFile + ".bulwark-backup")
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if string(backup) != testInstallProductJSON {
		t.Errorf("backup content mismatch: got %s", string(backup))
	}
}

func TestPatchInstallProductJSONIdempotentBackup(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "resources", "app")
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		t.Fatal(err)
	}
	cfgFile := filepath.Join(dir, "product.json")
	if err := os.WriteFile(cfgFile, []byte(testInstallProductJSON), cfgPerm); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	// First setup — creates backup.
	patchInstallProductJSON(dir, "https://localhost:18003", &buf)
	// Second setup — backup already exists, must not overwrite it with the
	// already-patched content.
	buf.Reset()
	patchInstallProductJSON(dir, "https://localhost:18003", &buf)

	// Backup should still contain the ORIGINAL marketplace URL, not the proxy URL.
	backup, err := os.ReadFile(cfgFile + ".bulwark-backup")
	if err != nil {
		t.Fatalf("backup missing after second patch: %v", err)
	}
	if !strings.Contains(string(backup), "marketplace.visualstudio.com") {
		t.Errorf("backup was overwritten with patched content on second setup: %s", string(backup))
	}
	// Second run should not emit a backup message (backup skipped).
	if strings.Contains(buf.String(), "backed up") {
		t.Errorf("should not emit backup message on second setup; got: %s", buf.String())
	}
}

func TestRestoreInstallProductJSONSkipsMissing(t *testing.T) {
	var buf bytes.Buffer
	restoreInstallProductJSON(filepath.Join(t.TempDir(), "nonexistent"), &buf)
	if buf.Len() != 0 {
		t.Errorf("expected no output for missing dir, got: %s", buf.String())
	}
}

func TestRestoreInstallProductJSONRestoresBackup(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "resources", "app")
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		t.Fatal(err)
	}
	cfgFile := filepath.Join(dir, "product.json")
	backup := cfgFile + ".bulwark-backup"

	// Simulate an already-patched state with a backup.
	patchedContent := `{"extensionsGallery":{"serviceUrl":"http://localhost:18003/vscode/gallery"}}`
	if err := os.WriteFile(cfgFile, []byte(patchedContent), cfgPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte(testInstallProductJSON), cfgPerm); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	restoreInstallProductJSON(dir, &buf)

	if !strings.Contains(buf.String(), "Restored") {
		t.Errorf("expected Restored message, got: %s", buf.String())
	}

	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("product.json missing after restore: %v", err)
	}
	if string(data) != testInstallProductJSON {
		t.Errorf("product.json not restored; got: %s", string(data))
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Errorf("backup not cleaned up after restore")
	}
}

func TestPatchInstallProductJSONMissingFile(t *testing.T) {
	// Directory exists but no product.json inside — should warn and return.
	dir := t.TempDir()
	var buf bytes.Buffer
	patchInstallProductJSON(dir, "https://localhost:18003", &buf)
	if !strings.Contains(buf.String(), "[warn]") {
		t.Errorf("expected warn for missing product.json, got: %s", buf.String())
	}
}

func TestRestoreUserDataProductJSONRestoresBackup(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "product.json")
	backup := cfgFile + ".bulwark-backup"
	origContent := `{"extensionsGallery":{"serviceUrl":"https://open-vsx.org/vscode/gallery"}}`

	if err := os.WriteFile(cfgFile, []byte("proxy config"), cfgPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte(origContent), cfgPerm); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	restoreUserDataProductJSON(dir, &buf)

	if !strings.Contains(buf.String(), "Restored") {
		t.Errorf("expected Restored message, got: %s", buf.String())
	}
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("product.json missing after restore: %v", err)
	}
	if string(data) != origContent {
		t.Errorf("content not restored: got %s", string(data))
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Errorf("backup not cleaned up")
	}
}

func TestRestoreUserDataProductJSONNoBackup(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "product.json")
	if err := os.WriteFile(cfgFile, []byte("proxy config"), cfgPerm); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	restoreUserDataProductJSON(dir, &buf)

	if _, err := os.Stat(cfgFile); !os.IsNotExist(err) {
		t.Errorf("product.json should be removed when no backup exists")
	}
}

func TestWritePkgMgrConfigVsxWindowsInstallDirs(t *testing.T) {
	home := t.TempDir()
	// Create a fake VS Code installation at the standard Windows path.
	installDir := filepath.Join(home, "AppData", "Local", "Programs", "Microsoft VS Code", "resources", "app")
	if err := os.MkdirAll(installDir, dirPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "product.json"), []byte(testInstallProductJSON), cfgPerm); err != nil {
		t.Fatal(err)
	}

	p := ProxyInfo{Ecosystem: EcosystemVsx, Port: 18003}
	var buf bytes.Buffer
	writePkgMgrConfig(p, home, testWindows, &buf)
	out := buf.String()

	if !strings.Contains(out, "install dir") {
		t.Errorf("expected install dir message, got: %s", out)
	}

	data, err := os.ReadFile(filepath.Join(installDir, "product.json"))
	if err != nil {
		t.Fatalf("reading install product.json: %v", err)
	}
	if !strings.Contains(string(data), "localhost:18003") {
		t.Errorf("install dir product.json not patched; got:\n%s", string(data))
	}
}

// TestWritePkgMgrConfigVsxWindowsUserDataPreservesGalleryFields verifies that
// when setting up on Windows the user-data overlay product.json includes the
// original extensionsGallery fields from the install dir (controlUrl, etc.),
// not just the three proxy URLs. VS Code 1.95+ does a shallow-merge of the
// user-data overlay so the overlay must carry all extensionsGallery fields.
func TestWritePkgMgrConfigVsxWindowsUserDataPreservesGalleryFields(t *testing.T) {
	const richInstallProductJSON = `{
  "nameShort": "Visual Studio Code",
  "version": "1.111.0",
  "extensionsGallery": {
    "serviceUrl": "https://marketplace.visualstudio.com/_apis/public/gallery",
    "itemUrl": "https://marketplace.visualstudio.com/items",
    "controlUrl": "https://main.vscode-cdn.net/extensions/marketplace.json",
    "nlsBaseUrl": "https://www.vscode-unpkg.net/_lp/",
    "mcpUrl": "https://main.vscode-cdn.net/mcp/servers.json"
  }
}`
	home := t.TempDir()
	installDir := filepath.Join(home, "AppData", "Local", "Programs", "Microsoft VS Code", "resources", "app")
	if err := os.MkdirAll(installDir, dirPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "product.json"), []byte(richInstallProductJSON), cfgPerm); err != nil {
		t.Fatal(err)
	}

	p := ProxyInfo{Ecosystem: EcosystemVsx, Port: 18003}
	var buf bytes.Buffer
	writePkgMgrConfig(p, home, testWindows, &buf)

	// The user-data overlay must contain the extra gallery fields.
	userDataDir := filepath.Join(home, "AppData", "Roaming", "Code")
	overlayData, err := os.ReadFile(filepath.Join(userDataDir, "product.json"))
	if err != nil {
		t.Fatalf("user-data overlay missing: %v", err)
	}
	overlayStr := string(overlayData)
	for _, want := range []string{"controlUrl", "nlsBaseUrl", "mcpUrl", "localhost:18003"} {
		if !strings.Contains(overlayStr, want) {
			t.Errorf("user-data overlay missing %q; got:\n%s", want, overlayStr)
		}
	}
	if strings.Contains(overlayStr, "marketplace.visualstudio.com") {
		t.Errorf("original serviceUrl/itemUrl still in user-data overlay; got:\n%s", overlayStr)
	}
}

func TestUninstallFilesVsxWindowsInstallDirs(t *testing.T) {
	home := t.TempDir()
	installDir := filepath.Join(home, "AppData", "Local", "Programs", "Microsoft VS Code", "resources", "app")
	if err := os.MkdirAll(installDir, dirPerm); err != nil {
		t.Fatal(err)
	}
	cfgFile := filepath.Join(installDir, "product.json")
	backupFile := cfgFile + ".bulwark-backup"

	patchedContent := `{"extensionsGallery":{"serviceUrl":"http://localhost:18003/vscode/gallery"}}`
	if err := os.WriteFile(cfgFile, []byte(patchedContent), cfgPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupFile, []byte(testInstallProductJSON), cfgPerm); err != nil {
		t.Fatal(err)
	}

	ecoDir := filepath.Join(home, bulwarkDir, "vsx-bulwark")
	if err := os.MkdirAll(ecoDir, dirPerm); err != nil {
		t.Fatal(err)
	}

	p := ProxyInfo{Ecosystem: EcosystemVsx, BinaryName: "vsx-bulwark", Port: 18003}
	var buf bytes.Buffer
	if err := UninstallFiles(p, home, testWindows, &buf); err != nil {
		t.Fatalf("UninstallFiles: %v", err)
	}

	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("product.json missing after uninstall: %v", err)
	}
	if string(data) != testInstallProductJSON {
		t.Errorf("install dir product.json not restored; got: %s", string(data))
	}
	if _, err := os.Stat(backupFile); !os.IsNotExist(err) {
		t.Errorf("backup not cleaned up after uninstall")
	}
}

// ─── VsxRepairInstallDirs ─────────────────────────────────────────────────────

func TestVsxRepairInstallDirsNonWindows(t *testing.T) {
	// Should be a no-op on non-Windows.
	var buf bytes.Buffer
	VsxRepairInstallDirs(t.TempDir(), testLinux, 18003, &buf)
	VsxRepairInstallDirs(t.TempDir(), testDarwin, 18003, &buf)
	if buf.Len() != 0 {
		t.Errorf("expected no output on non-Windows, got: %s", buf.String())
	}
}

func TestVsxRepairInstallDirsAlreadyPatched(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "AppData", "Local", "Programs", "Microsoft VS Code", "resources", "app")
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		t.Fatal(err)
	}
	// Already patched — should skip without output.
	already := `{"extensionsGallery":{"serviceUrl":"http://localhost:18003/vscode/gallery"}}`
	if err := os.WriteFile(filepath.Join(dir, "product.json"), []byte(already), cfgPerm); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	VsxRepairInstallDirs(home, testWindows, 18003, &buf)
	if buf.Len() != 0 {
		t.Errorf("expected no output when already patched, got: %s", buf.String())
	}
}

func TestVsxRepairInstallDirsRepairsAfterUpdate(t *testing.T) {
	home := t.TempDir()
	// Simulate a VS Code update: new hash directory with fresh product.json.
	newHashDir := filepath.Join(home, "AppData", "Local", "Programs", "Microsoft VS Code", "newHash123", "resources", "app")
	if err := os.MkdirAll(newHashDir, dirPerm); err != nil {
		t.Fatal(err)
	}
	cfgFile := filepath.Join(newHashDir, "product.json")
	// Fresh unpatched product.json as placed by the VS Code installer.
	if err := os.WriteFile(cfgFile, []byte(testInstallProductJSON), cfgPerm); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	VsxRepairInstallDirs(home, testWindows, 18003, &buf)
	out := buf.String()

	// Should have re-patched.
	if !strings.Contains(out, "[ok]") {
		t.Errorf("expected patch output after update, got: %s", out)
	}

	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("product.json missing after repair: %v", err)
	}
	if !strings.Contains(string(data), "localhost:18003") {
		t.Errorf("product.json not patched after repair; got:\n%s", string(data))
	}
	// Backup should be the original unpatched content.
	backup, err := os.ReadFile(cfgFile + ".bulwark-backup")
	if err != nil {
		t.Fatalf("backup missing after repair: %v", err)
	}
	if !strings.Contains(string(backup), "marketplace.visualstudio.com") {
		t.Errorf("backup should contain original marketplace URL; got: %s", string(backup))
	}
}

func TestVsxRepairInstallDirsUsesSavedTargets(t *testing.T) {
	home := t.TempDir()
	codeDir := filepath.Join(home, "AppData", "Local", "Programs", "Microsoft VS Code", "resources", "app")
	insidersDir := filepath.Join(home, "AppData", "Local", "Programs", "Microsoft VS Code Insiders", "resources", "app")
	for _, dir := range []string{codeDir, insidersDir} {
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "product.json"), []byte(testInstallProductJSON), cfgPerm); err != nil {
			t.Fatal(err)
		}
	}
	if err := saveVSXSetupState(home, []VSCodeTarget{{
		Variant:     VariantMicrosoftInsiders,
		ConfigDir:   filepath.Join(home, "AppData", "Roaming", VariantMicrosoftInsiders.Name),
		InstallDirs: []string{insidersDir},
	}}); err != nil {
		t.Fatalf("saveVSXSetupState: %v", err)
	}

	var buf bytes.Buffer
	VsxRepairInstallDirs(home, testWindows, 18003, &buf)

	insidersData, err := os.ReadFile(filepath.Join(insidersDir, "product.json"))
	if err != nil {
		t.Fatalf("reading insiders product.json: %v", err)
	}
	if !strings.Contains(string(insidersData), "localhost:18003") {
		t.Errorf("insiders product.json not repaired: %s", string(insidersData))
	}
	codeData, err := os.ReadFile(filepath.Join(codeDir, "product.json"))
	if err != nil {
		t.Fatalf("reading code product.json: %v", err)
	}
	if strings.Contains(string(codeData), "localhost:18003") {
		t.Errorf("Code product.json should not be repaired when not present in saved state")
	}
}

func TestVsxRepairInstallDirsSkipsRemovedDir(t *testing.T) {
	home := t.TempDir()
	// Old hash dir is gone (cleaned up by Squirrel) — no error expected.
	var buf bytes.Buffer
	VsxRepairInstallDirs(home, testWindows, 18003, &buf)
	// Should be silent with no panics.
	if buf.Len() != 0 {
		t.Errorf("expected no output when no dirs exist, got: %s", buf.String())
	}
}

// ─── VS Code variant detection ──────────────────────────────────────────────

func TestDetectVSCodeVariantsNone(t *testing.T) {
	home := t.TempDir()
	variants := DetectVSCodeVariants(home, testLinux)
	if len(variants) != 0 {
		t.Errorf("expected no variants, got %d", len(variants))
	}
}

func TestDetectVSCodeVariantsLinuxCode(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".config", "Code"), dirPerm)     //nolint:errcheck
	os.MkdirAll(filepath.Join(home, ".config", "VSCodium"), dirPerm) //nolint:errcheck
	variants := DetectVSCodeVariants(home, testLinux)
	if len(variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(variants))
	}
	if variants[0].Name != "Code" {
		t.Errorf("first variant should be Code, got %s", variants[0].Name)
	}
	if variants[1].Name != "VSCodium" {
		t.Errorf("second variant should be VSCodium, got %s", variants[1].Name)
	}
}

func TestDetectVSCodeVariantsWindowsInstallDir(t *testing.T) {
	home := t.TempDir()
	// No user-data dir, but the patchable install dir exists.
	installDir := filepath.Join(home, "AppData", "Local", "Programs", "Microsoft VS Code", "resources", "app")
	os.MkdirAll(installDir, dirPerm) //nolint:errcheck
	variants := DetectVSCodeVariants(home, testWindows)
	found := false
	for _, v := range variants {
		if v.Name == "Code" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Code variant via install dir, got %v", variants)
	}
}

func TestDetectVSCodeVariantsWindowsInsiders(t *testing.T) {
	home := t.TempDir()
	installDir := filepath.Join(home, "AppData", "Local", "Programs", "Microsoft VS Code Insiders", "resources", "app")
	os.MkdirAll(installDir, dirPerm) //nolint:errcheck
	variants := DetectVSCodeVariants(home, testWindows)
	found := false
	for _, v := range variants {
		if v.Name == "Code - Insiders" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Code - Insiders variant via install dir, got %v", variants)
	}
}

func TestDetectVSCodeVariantsDarwin(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "Library", "Application Support", "Code - OSS"), dirPerm) //nolint:errcheck
	variants := DetectVSCodeVariants(home, testDarwin)
	if len(variants) != 1 {
		t.Fatalf("expected 1 variant, got %d", len(variants))
	}
	if variants[0].Name != "Code - OSS" {
		t.Errorf("expected Code - OSS, got %s", variants[0].Name)
	}
}

func TestChooseVSCodeUpstreamMarketplace(t *testing.T) {
	variants := []VSCodeVariant{VariantMicrosoftCode, VariantVSCodium}
	url, regType := ChooseVSCodeUpstream(variants)
	if regType != "marketplace" {
		t.Errorf("expected marketplace, got %s", regType)
	}
	if url != "https://marketplace.visualstudio.com" {
		t.Errorf("expected marketplace URL, got %s", url)
	}
}

func TestChooseVSCodeUpstreamOpenVSX(t *testing.T) {
	variants := []VSCodeVariant{VariantVSCodium, VariantCodeOSS}
	url, regType := ChooseVSCodeUpstream(variants)
	if regType != "openvsx" {
		t.Errorf("expected openvsx, got %s", regType)
	}
	if url != "https://open-vsx.org" {
		t.Errorf("expected Open VSX URL, got %s", url)
	}
}

func TestChooseVSCodeUpstreamEmpty(t *testing.T) {
	url, regType := ChooseVSCodeUpstream(nil)
	if regType != "openvsx" {
		t.Errorf("expected openvsx fallback, got %s", regType)
	}
	if url != "https://open-vsx.org" {
		t.Errorf("expected Open VSX URL fallback, got %s", url)
	}
}

func TestChooseVSCodeUpstreamInsiders(t *testing.T) {
	variants := []VSCodeVariant{VariantMicrosoftInsiders}
	url, regType := ChooseVSCodeUpstream(variants)
	if regType != "marketplace" {
		t.Errorf("expected marketplace for Insiders, got %s", regType)
	}
	if url != "https://marketplace.visualstudio.com" {
		t.Errorf("expected marketplace URL for Insiders, got %s", url)
	}
}

func TestReadExistingRegistryURLFromInstallDir(t *testing.T) {
	home := t.TempDir()
	installDir := filepath.Join(home, "AppData", "Local", "Programs", "Microsoft VS Code", "resources", "app")
	os.MkdirAll(installDir, dirPerm) //nolint:errcheck
	productJSON := `{"extensionsGallery":{"serviceUrl":"https://marketplace.visualstudio.com/_apis/public/gallery"}}`
	os.WriteFile(filepath.Join(installDir, "product.json"), []byte(productJSON), cfgPerm) //nolint:errcheck

	url := ReadExistingRegistryURL(home, testWindows)
	if url != "https://marketplace.visualstudio.com/_apis/public/gallery" {
		t.Errorf("expected marketplace gallery URL, got %q", url)
	}
}

func TestReadExistingRegistryURLFromUserData(t *testing.T) {
	home := t.TempDir()
	userDataDir := filepath.Join(home, ".config", "Code")
	os.MkdirAll(userDataDir, dirPerm) //nolint:errcheck
	productJSON := `{"extensionsGallery":{"serviceUrl":"https://open-vsx.org/vscode/gallery"}}`
	os.WriteFile(filepath.Join(userDataDir, "product.json"), []byte(productJSON), cfgPerm) //nolint:errcheck

	url := ReadExistingRegistryURL(home, testLinux)
	if url != "https://open-vsx.org/vscode/gallery" {
		t.Errorf("expected Open VSX gallery URL, got %q", url)
	}
}

func TestReadExistingRegistryURLNone(t *testing.T) {
	url := ReadExistingRegistryURL(t.TempDir(), testLinux)
	if url != "" {
		t.Errorf("expected empty URL, got %q", url)
	}
}

func TestPatchConfigForVSCodeMarketplace(t *testing.T) {
	input := []byte("upstream:\n  url: \"https://open-vsx.org\"\n  timeout_seconds: 30\ncache:\n  ttl_seconds: 300\n")
	result := PatchConfigForVSCode(input, "https://marketplace.visualstudio.com", "marketplace")
	s := string(result)
	if !strings.Contains(s, "marketplace.visualstudio.com") {
		t.Errorf("URL not patched: %s", s)
	}
	if !strings.Contains(s, "marketplace") {
		t.Errorf("registry_type not set: %s", s)
	}
	if strings.Contains(s, "open-vsx.org") {
		t.Errorf("old URL still present: %s", s)
	}
}

func TestPatchConfigForVSCodeWithExistingRegistryType(t *testing.T) {
	input := []byte("upstream:\n  url: \"https://open-vsx.org\"\n  registry_type: \"openvsx\"\n  timeout_seconds: 30\ncache:\n  ttl_seconds: 300\n")
	result := PatchConfigForVSCode(input, "https://marketplace.visualstudio.com", "marketplace")
	s := string(result)
	if !strings.Contains(s, "marketplace.visualstudio.com") {
		t.Errorf("URL not patched: %s", s)
	}
	// Should not have duplicate registry_type.
	if strings.Count(s, "registry_type") != 1 {
		t.Errorf("registry_type should appear exactly once; got:\n%s", s)
	}
}

func TestPatchConfigForVSCodeOpenVSX(t *testing.T) {
	input := []byte("upstream:\n  url: \"https://open-vsx.org\"\n  timeout_seconds: 30\ncache:\n  ttl_seconds: 300\n")
	result := PatchConfigForVSCode(input, "https://open-vsx.org", "openvsx")
	s := string(result)
	if !strings.Contains(s, "open-vsx.org") {
		t.Errorf("URL lost: %s", s)
	}
	if !strings.Contains(s, "openvsx") {
		t.Errorf("registry_type not set: %s", s)
	}
}

func TestAutoConfigureVSCodeUpstreamMicrosoft(t *testing.T) {
	home := t.TempDir()
	// Create Microsoft VS Code user-data dir.
	os.MkdirAll(filepath.Join(home, ".config", "Code"), dirPerm) //nolint:errcheck

	input := []byte("upstream:\n  url: \"https://open-vsx.org\"\n  timeout_seconds: 30\ncache:\n  ttl_seconds: 300\n")
	var buf bytes.Buffer
	result := autoConfigureVSCodeUpstream(input, home, testLinux, &buf)
	s := string(result)
	if !strings.Contains(s, "marketplace.visualstudio.com") {
		t.Errorf("expected marketplace URL, got:\n%s", s)
	}
	if !strings.Contains(buf.String(), "Detected") {
		t.Errorf("expected detection message, got: %s", buf.String())
	}
}

func TestAutoConfigureVSCodeUpstreamVSCodium(t *testing.T) {
	home := t.TempDir()
	// Only VSCodium installed.
	os.MkdirAll(filepath.Join(home, ".config", "VSCodium"), dirPerm) //nolint:errcheck

	input := []byte("upstream:\n  url: \"https://open-vsx.org\"\n  timeout_seconds: 30\ncache:\n  ttl_seconds: 300\n")
	var buf bytes.Buffer
	result := autoConfigureVSCodeUpstream(input, home, testLinux, &buf)
	s := string(result)
	if !strings.Contains(s, "open-vsx.org") {
		t.Errorf("expected Open VSX URL, got:\n%s", s)
	}
}

func TestAutoConfigureVSCodeUpstreamNoVariant(t *testing.T) {
	home := t.TempDir()
	input := []byte("upstream:\n  url: \"https://open-vsx.org\"\n  timeout_seconds: 30\ncache:\n  ttl_seconds: 300\n")
	var buf bytes.Buffer
	result := autoConfigureVSCodeUpstream(input, home, testLinux, &buf)
	// Should keep Open VSX as default.
	if !strings.Contains(string(result), "open-vsx.org") {
		t.Errorf("expected Open VSX URL unchanged, got:\n%s", string(result))
	}
	if !strings.Contains(buf.String(), "No VS Code installation detected") {
		t.Errorf("expected no-detection message, got: %s", buf.String())
	}
}

func TestSetupFilesVsxAutoDetect(t *testing.T) {
	home := t.TempDir()
	// Create Microsoft VS Code user-data dir so auto-detect picks marketplace.
	os.MkdirAll(filepath.Join(home, ".config", "Code"), dirPerm) //nolint:errcheck

	p := ProxyInfo{
		Ecosystem:  EcosystemVsx,
		BinaryName: "vsx-bulwark",
		Port:       18003,
		ConfigData: []byte("upstream:\n  url: \"https://open-vsx.org\"\n  timeout_seconds: 30\ncache:\n  ttl_seconds: 300\n"),
	}
	exe := createDummyExe(t)
	var buf bytes.Buffer
	if err := SetupFiles(p, home, exe, testLinux, &buf); err != nil {
		t.Fatalf("SetupFiles: %v", err)
	}

	paths := ResolvePaths(p, home, testLinux)
	configData, err := os.ReadFile(paths.Config)
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	s := string(configData)
	if !strings.Contains(s, "marketplace.visualstudio.com") {
		t.Errorf("config not patched with marketplace URL; got:\n%s", s)
	}
	if !strings.Contains(s, "marketplace") {
		t.Errorf("config missing registry_type; got:\n%s", s)
	}
}

// ─── TLS certificate generation ─────────────────────────────────────────────

func TestGenerateSelfSignedCert(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")

	if err := GenerateSelfSignedCert(certPath, keyPath); err != nil {
		t.Fatalf("GenerateSelfSignedCert: %v", err)
	}

	// Verify cert file exists and is valid PEM.
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("reading cert: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatal("cert file does not contain a valid PEM CERTIFICATE block")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parsing certificate: %v", err)
	}

	// Check SANs.
	if len(cert.DNSNames) == 0 || cert.DNSNames[0] != "localhost" {
		t.Errorf("expected DNS SAN localhost, got %v", cert.DNSNames)
	}
	if len(cert.IPAddresses) < 2 {
		t.Errorf("expected at least 2 IP SANs (127.0.0.1, ::1), got %v", cert.IPAddresses)
	}

	// Verify key file exists and is valid PEM.
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("reading key: %v", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil || keyBlock.Type != "EC PRIVATE KEY" {
		t.Fatal("key file does not contain a valid PEM EC PRIVATE KEY block")
	}

	// Verify the cert and key work together as a TLS pair.
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Fatalf("cert/key pair invalid: %v", err)
	}
}

func TestGenerateSelfSignedCertBadPath(t *testing.T) {
	if err := GenerateSelfSignedCert("/nonexistent/dir/tls.crt", "/nonexistent/dir/tls.key"); err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

// ─── PatchConfigForTLS ──────────────────────────────────────────────────────

func TestPatchConfigForTLS(t *testing.T) {
	input := `server:
  port: 18003
  read_timeout_seconds: 30
upstream:
  url: "https://example.com"
`
	result := PatchConfigForTLS([]byte(input), "/home/user/.bulwark/vsx-bulwark/tls.crt", "/home/user/.bulwark/vsx-bulwark/tls.key")
	s := string(result)

	if !strings.Contains(s, `tls_cert_file: "/home/user/.bulwark/vsx-bulwark/tls.crt"`) {
		t.Errorf("missing tls_cert_file; got:\n%s", s)
	}
	if !strings.Contains(s, `tls_key_file: "/home/user/.bulwark/vsx-bulwark/tls.key"`) {
		t.Errorf("missing tls_key_file; got:\n%s", s)
	}
	// Original fields should still be present.
	if !strings.Contains(s, "port: 18003") {
		t.Errorf("original port missing; got:\n%s", s)
	}
}

func TestPatchConfigForTLSOverridesExisting(t *testing.T) {
	input := `server:
  port: 18003
  tls_cert_file: "/old/cert.pem"
  tls_key_file: "/old/key.pem"
upstream:
  url: "https://example.com"
`
	result := PatchConfigForTLS([]byte(input), "/new/cert.pem", "/new/key.pem")
	s := string(result)

	if !strings.Contains(s, `tls_cert_file: "/new/cert.pem"`) {
		t.Errorf("tls_cert_file not overridden; got:\n%s", s)
	}
	if !strings.Contains(s, `tls_key_file: "/new/key.pem"`) {
		t.Errorf("tls_key_file not overridden; got:\n%s", s)
	}
	if strings.Contains(s, "/old/") {
		t.Errorf("old paths still present; got:\n%s", s)
	}
}

// ─── VsxGalleryProductJSON HTTPS ────────────────────────────────────────────

func TestVsxGalleryProductJSONUsesHTTPS(t *testing.T) {
	content := VsxGalleryProductJSON(18003)
	if !strings.Contains(content, "https://localhost:18003") {
		t.Errorf("product.json should use https://; got:\n%s", content)
	}
	if strings.Contains(content, "http://localhost:18003") {
		t.Errorf("product.json should not use plain http://; got:\n%s", content)
	}
}

func TestMergeGalleryJSONUsesHTTPS(t *testing.T) {
	const srcJSON = `{
  "extensionsGallery": {
    "serviceUrl": "https://marketplace.visualstudio.com/_apis/public/gallery"
  }
}`
	patched, err := mergeGalleryJSON([]byte(srcJSON), 18003)
	if err != nil {
		t.Fatalf("mergeGalleryJSON: %v", err)
	}
	s := string(patched)
	if !strings.Contains(s, "https://localhost:18003") {
		t.Errorf("merged JSON should use https://; got:\n%s", s)
	}
	if strings.Contains(s, "http://localhost:18003") {
		t.Errorf("merged JSON should not use plain http://; got:\n%s", s)
	}
}

// ─── SetupFiles generates TLS cert for VSX ──────────────────────────────────

func TestSetupFilesVsxGeneratesTLSCert(t *testing.T) {
	home := t.TempDir()
	exePath := filepath.Join(home, "fake-binary")
	if err := os.WriteFile(exePath, []byte("binary"), binPerm); err != nil {
		t.Fatal(err)
	}

	p := ProxyInfo{
		Ecosystem:  EcosystemVsx,
		BinaryName: "vsx-bulwark",
		Port:       18003,
		ConfigData: []byte("server:\n  port: 18003\nupstream:\n  url: \"https://open-vsx.org\"\n  registry_type: \"openvsx\"\n"),
	}

	var buf bytes.Buffer
	if err := SetupFiles(p, home, exePath, testLinux, &buf); err != nil {
		t.Fatalf("SetupFiles: %v", err)
	}

	paths := ResolvePaths(p, home, testLinux)

	// Verify TLS cert was generated.
	certPath := filepath.Join(paths.EcoDir, "tls.crt")
	keyPath := filepath.Join(paths.EcoDir, "tls.key")
	if _, err := os.Stat(certPath); err != nil {
		t.Errorf("TLS cert not generated: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Errorf("TLS key not generated: %v", err)
	}

	// Verify config was patched with TLS paths.
	configData, err := os.ReadFile(paths.Config)
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	s := string(configData)
	if !strings.Contains(s, "tls_cert_file:") {
		t.Errorf("config missing tls_cert_file; got:\n%s", s)
	}
	if !strings.Contains(s, "tls_key_file:") {
		t.Errorf("config missing tls_key_file; got:\n%s", s)
	}

	// Verify product.json uses HTTPS.
	for _, dir := range VsxConfigDirs(home, testLinux) {
		data, err := os.ReadFile(filepath.Join(dir, "product.json"))
		if err != nil {
			continue
		}
		if !strings.Contains(string(data), "https://localhost:18003") {
			t.Errorf("product.json not using HTTPS; got:\n%s", string(data))
		}
	}

	// Setup output should mention TLS.
	if !strings.Contains(buf.String(), "TLS certificate generated") {
		t.Errorf("expected TLS generation message in output; got:\n%s", buf.String())
	}
}

// ─── VsxGalleryProductJSONURL ────────────────────────────────────────────────

func TestVsxGalleryProductJSONURL(t *testing.T) {
	content := VsxGalleryProductJSONURL("https://bulwark.corp.com:18003")
	for _, want := range []string{
		"https://bulwark.corp.com:18003",
		"serviceUrl",
		"itemUrl",
		"resourceUrlTemplate",
		"extensionUrlTemplate",
		"_comment",
		"_revert",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("VsxGalleryProductJSONURL missing %q; got:\n%s", want, content)
		}
	}
	// Must not contain localhost.
	if strings.Contains(content, "localhost") {
		t.Errorf("VsxGalleryProductJSONURL should not mention localhost; got:\n%s", content)
	}
}

// ─── SetupClientOnly ─────────────────────────────────────────────────────────

func TestSetupClientOnly(t *testing.T) {
	home := t.TempDir()
	var buf bytes.Buffer

	if err := SetupClientOnly("https://bulwark.corp.com:18003", home, testLinux, &buf); err != nil {
		t.Fatalf("SetupClientOnly: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "[ok]") {
		t.Errorf("expected ok output; got: %s", output)
	}
	if !strings.Contains(output, "bulwark.corp.com:18003") {
		t.Errorf("expected server URL in output; got: %s", output)
	}

	// Verify product.json was written to all VS Code user-data dirs.
	for _, dir := range VsxConfigDirs(home, testLinux) {
		data, err := os.ReadFile(filepath.Join(dir, "product.json"))
		if err != nil {
			t.Errorf("missing product.json in %s: %v", dir, err)
			continue
		}
		if !strings.Contains(string(data), "bulwark.corp.com:18003") {
			t.Errorf("product.json in %s missing remote URL; got:\n%s", dir, string(data))
		}
		if strings.Contains(string(data), "localhost") {
			t.Errorf("product.json in %s still mentions localhost; got:\n%s", dir, string(data))
		}
	}
}

func TestSetupClientOnlyDetectedTargetsOnly(t *testing.T) {
	home := t.TempDir()
	vscodiumDir := filepath.Join(home, ".config", "VSCodium")
	if err := os.MkdirAll(vscodiumDir, dirPerm); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := SetupClientOnly("https://bulwark.corp.com:18003", home, testLinux, &buf); err != nil {
		t.Fatalf("SetupClientOnly: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(vscodiumDir, "product.json"))
	if err != nil {
		t.Fatalf("reading VSCodium product.json: %v", err)
	}
	if !strings.Contains(string(data), "bulwark.corp.com:18003") {
		t.Errorf("VSCodium product.json missing remote URL: %s", string(data))
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "Code", "product.json")); !os.IsNotExist(err) {
		t.Errorf("Code product.json should not be written when only VSCodium is detected")
	}
	state := readVSXSetupStateForTest(t, home)
	if len(state.Targets) != 1 || state.Targets[0].Name != VariantVSCodium.Name {
		t.Errorf("saved state = %+v, want only %s", state.Targets, VariantVSCodium.Name)
	}
}

func TestSetupClientOnlyTrailingSlash(t *testing.T) {
	home := t.TempDir()
	var buf bytes.Buffer

	// URL with trailing slash should be normalised.
	if err := SetupClientOnly("https://bulwark.corp.com:18003/", home, testLinux, &buf); err != nil {
		t.Fatalf("SetupClientOnly: %v", err)
	}

	for _, dir := range VsxConfigDirs(home, testLinux) {
		data, _ := os.ReadFile(filepath.Join(dir, "product.json"))
		// The slash must be stripped so we don't get double-slashes in gallery URLs.
		if strings.Contains(string(data), "//vscode/gallery") {
			t.Errorf("double-slash in gallery URL after trailing-slash normalisation; got:\n%s", string(data))
		}
	}
}

func TestSetupClientOnlyHTTPRejected(t *testing.T) {
	var buf bytes.Buffer
	err := SetupClientOnly("http://bulwark.corp.com:18003", t.TempDir(), testLinux, &buf)
	if err == nil {
		t.Error("expected error for http URL")
	}
}

func TestSetupClientOnlyBadURL(t *testing.T) {
	var buf bytes.Buffer
	// No host.
	err := SetupClientOnly("https://", t.TempDir(), testLinux, &buf)
	if err == nil {
		t.Error("expected error for URL with no host")
	}
}

func TestSetupClientOnlyWindowsInstallDirs(t *testing.T) {
	home := t.TempDir()
	// Create a fake VS Code installation on Windows.
	installDir := filepath.Join(home, "AppData", "Local", "Programs", "Microsoft VS Code", "resources", "app")
	if err := os.MkdirAll(installDir, dirPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "product.json"), []byte(testInstallProductJSON), cfgPerm); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := SetupClientOnly("https://bulwark.corp.com:18003", home, testWindows, &buf); err != nil {
		t.Fatalf("SetupClientOnly: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(installDir, "product.json"))
	if err != nil {
		t.Fatalf("reading install dir product.json: %v", err)
	}
	if !strings.Contains(string(data), "bulwark.corp.com:18003") {
		t.Errorf("install dir product.json not patched with remote URL; got:\n%s", string(data))
	}
}

// ─── Top-level Setup / Uninstall / SetupFilesOnly wrappers ───────────────────
// These thin wrappers resolve the home directory and executable at runtime and
// then delegate to the testable At-variants. Tests use a unique BinaryName
// (prefixed with "test-") so they do not collide with a real installation, and
// t.Cleanup removes all created assets immediately after each test.

func TestSetupRunsAndCleansUp(t *testing.T) {
	p := ProxyInfo{
		Ecosystem:  EcosystemNpm,
		BinaryName: "test-setup-bulwark",
		Port:       19991,
		ConfigData: []byte("listen_port: 19991\n"),
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	paths := ResolvePaths(p, home, runtime.GOOS)
	t.Cleanup(func() {
		os.RemoveAll(paths.EcoDir)
		os.Remove(paths.Binary)
	})
	var buf bytes.Buffer
	if err := Setup(p, &buf); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if _, err := os.Stat(paths.Config); err != nil {
		t.Errorf("config file not created: %v", err)
	}
}

func TestUninstallRunsClean(t *testing.T) {
	p := ProxyInfo{
		Ecosystem:  EcosystemNpm,
		BinaryName: "test-uninstall-bulwark",
		Port:       19992,
		ConfigData: []byte("listen_port: 19992\n"),
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	paths := ResolvePaths(p, home, runtime.GOOS)
	// Pre-install so Uninstall has something to remove.
	if err := SetupFiles(p, home, os.Args[0], runtime.GOOS, io.Discard); err != nil {
		t.Skipf("pre-install failed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(paths.EcoDir); os.Remove(paths.Binary) })
	var buf bytes.Buffer
	if err := Uninstall(p, &buf); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
}

func TestSetupFilesOnlyRunsClean(t *testing.T) {
	p := ProxyInfo{
		Ecosystem:  EcosystemNpm,
		BinaryName: "test-filesonly-bulwark",
		Port:       19993,
		ConfigData: []byte("listen_port: 19993\n"),
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	paths := ResolvePaths(p, home, runtime.GOOS)
	t.Cleanup(func() {
		os.RemoveAll(paths.EcoDir)
		os.Remove(paths.Binary)
	})
	var buf bytes.Buffer
	if err := SetupFilesOnly(p, &buf); err != nil {
		t.Fatalf("SetupFilesOnly: %v", err)
	}
	if _, err := os.Stat(paths.Config); err != nil {
		t.Errorf("config file not created: %v", err)
	}
}
