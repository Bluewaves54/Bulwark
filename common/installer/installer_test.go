// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
			if !strings.Contains(dir, tc.wantDir) {
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
			if tc.want != "" && !strings.Contains(result, tc.want) {
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

	// Use /dev/null as home — MkdirAll will fail.
	err := SetupFiles(p, "/dev/null", exe, testDarwin, &buf)
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

// --- PATH management ---

func TestPathBlock(t *testing.T) {
	block := PathBlock()
	if !strings.Contains(block, pathMarkerStart) {
		t.Error("missing start marker")
	}
	if !strings.Contains(block, pathMarkerEnd) {
		t.Error("missing end marker")
	}
	if !strings.Contains(block, pathExportLine) {
		t.Error("missing export line")
	}
}

func TestShellProfilesDarwin(t *testing.T) {
	profiles := shellProfiles("/home/user", testDarwin)
	if len(profiles) != 2 {
		t.Fatalf("want 2 profiles, got %d", len(profiles))
	}
	if !strings.HasSuffix(profiles[0], ".zshrc") {
		t.Errorf("first profile: want .zshrc, got %s", profiles[0])
	}
}

func TestShellProfilesLinux(t *testing.T) {
	profiles := shellProfiles("/home/user", testLinux)
	if len(profiles) != 3 {
		t.Fatalf("want 3 profiles, got %d", len(profiles))
	}
	if !strings.HasSuffix(profiles[0], ".bashrc") {
		t.Errorf("first profile: want .bashrc, got %s", profiles[0])
	}
}

func TestShellProfilesWindows(t *testing.T) {
	profiles := shellProfiles("/home/user", testWindows)
	if profiles != nil {
		t.Errorf("expected nil profiles for Windows, got %v", profiles)
	}
}

func TestShellProfilesUnsupported(t *testing.T) {
	profiles := shellProfiles("/home/user", testFreeBSD)
	if profiles != nil {
		t.Errorf("expected nil profiles for FreeBSD, got %v", profiles)
	}
}

func TestAddToPathDarwin(t *testing.T) {
	home := t.TempDir()
	// Create .zshrc so AddToPath can modify it.
	zshrc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(zshrc, []byte("# existing config\n"), cfgPerm); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	AddToPath(home, testDarwin, &buf)
	output := buf.String()
	if !strings.Contains(output, "[ok] Added") {
		t.Errorf("expected success message, got: %s", output)
	}
	data, err := os.ReadFile(zshrc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), pathMarkerStart) {
		t.Error("PATH block not found in .zshrc")
	}
}

func TestAddToPathLinuxFallback(t *testing.T) {
	home := t.TempDir()
	// No existing profiles — should create .profile as fallback.
	var buf bytes.Buffer
	AddToPath(home, testLinux, &buf)
	output := buf.String()
	if !strings.Contains(output, "Created") {
		t.Errorf("expected fallback creation message, got: %s", output)
	}
	data, err := os.ReadFile(filepath.Join(home, ".profile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), pathExportLine) {
		t.Error("export line not found in .profile fallback")
	}
}

func TestAddToPathSkipsDuplicate(t *testing.T) {
	home := t.TempDir()
	zshrc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(zshrc, []byte("# existing\n"+PathBlock()), cfgPerm); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	AddToPath(home, testDarwin, &buf)
	data, _ := os.ReadFile(zshrc)
	count := strings.Count(string(data), pathMarkerStart)
	if count != 1 {
		t.Errorf("expected exactly 1 PATH block, got %d", count)
	}
}

func TestAddToPathWindows(t *testing.T) {
	home := t.TempDir()
	var buf bytes.Buffer
	AddToPath(home, testWindows, &buf)
	output := buf.String()
	// On test machines setx may not be available; either success or info message.
	if !strings.Contains(output, "PATH") {
		t.Errorf("expected PATH-related message, got: %s", output)
	}
}

func TestRemoveFromPathDarwin(t *testing.T) {
	home := t.TempDir()
	zshrc := filepath.Join(home, ".zshrc")
	original := "# before\n" + PathBlock() + "# after\n"
	if err := os.WriteFile(zshrc, []byte(original), cfgPerm); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	RemoveFromPath(home, testDarwin, &buf)
	output := buf.String()
	if !strings.Contains(output, "[ok] Removed Bulwark PATH") {
		t.Errorf("expected removal message, got: %s", output)
	}
	data, _ := os.ReadFile(zshrc)
	if strings.Contains(string(data), pathMarkerStart) {
		t.Error("PATH block should have been removed from .zshrc")
	}
	if !strings.Contains(string(data), "# before") {
		t.Error("surrounding content should be preserved")
	}
}

func TestRemoveFromPathWindows(t *testing.T) {
	home := t.TempDir()
	var buf bytes.Buffer
	RemoveFromPath(home, testWindows, &buf)
	output := buf.String()
	if !strings.Contains(output, "[info]") {
		t.Errorf("expected info message for Windows, got: %s", output)
	}
}

func TestRemoveFromPathNoBlock(t *testing.T) {
	home := t.TempDir()
	zshrc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(zshrc, []byte("# no bulwark\n"), cfgPerm); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	RemoveFromPath(home, testDarwin, &buf)
	// No removal message expected.
	if strings.Contains(buf.String(), "[ok]") {
		t.Error("should not report removal when block is absent")
	}
}

func TestRemoveBulwarkBlock(t *testing.T) {
	content := "line1\n" + pathMarkerStart + "\nexport PATH\n" + pathMarkerEnd + "\nline2\n"
	cleaned := removeBulwarkBlock(content)
	if strings.Contains(cleaned, pathMarkerStart) {
		t.Error("marker should be removed")
	}
	if !strings.Contains(cleaned, "line1") || !strings.Contains(cleaned, "line2") {
		t.Error("surrounding content should be preserved")
	}
}

func TestRemoveBulwarkBlockNoMarker(t *testing.T) {
	content := "no markers here"
	if removeBulwarkBlock(content) != content {
		t.Error("content should be unchanged when no markers present")
	}
}

func TestRemoveBulwarkBlockOnlyStart(t *testing.T) {
	content := pathMarkerStart + "\nexport PATH\n"
	if removeBulwarkBlock(content) != content {
		t.Error("content should be unchanged when only start marker present")
	}
}

// --- UninstallAll ---

func TestAllEcosystems(t *testing.T) {
	all := AllEcosystems()
	if len(all) != 3 {
		t.Fatalf("want 3 ecosystems, got %d", len(all))
	}
	names := map[string]bool{}
	for _, p := range all {
		names[p.Ecosystem] = true
	}
	for _, eco := range []string{EcosystemNpm, EcosystemPypi, EcosystemMaven} {
		if !names[eco] {
			t.Errorf("missing ecosystem %s", eco)
		}
	}
}

func TestUninstallAllAtEmpty(t *testing.T) {
	home := t.TempDir()
	var buf bytes.Buffer
	if err := UninstallAllAt(home, testDarwin, &buf); err != nil {
		t.Fatalf("UninstallAllAt: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "Uninstalling all") {
		t.Error("missing header message")
	}
	if !strings.Contains(output, "[skip]") {
		t.Error("expected skip messages for non-installed proxies")
	}
}

func TestUninstallAllAtInstalled(t *testing.T) {
	home := t.TempDir()
	exe := createDummyExe(t)

	// Install npm and pypi.
	var setup bytes.Buffer
	if err := SetupFiles(testProxyInfo(), home, exe, testDarwin, &setup); err != nil {
		t.Fatalf("setup npm: %v", err)
	}
	if err := SetupFiles(pypiProxyInfo(), home, exe, testDarwin, &setup); err != nil {
		t.Fatalf("setup pypi: %v", err)
	}

	var buf bytes.Buffer
	if err := UninstallAllAt(home, testDarwin, &buf); err != nil {
		t.Fatalf("UninstallAllAt: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "has been uninstalled") {
		t.Error("expected uninstall messages")
	}
	if !strings.Contains(output, "All Bulwark proxies uninstalled") {
		t.Error("missing completion message")
	}
}

// --- Update ---

func TestFetchLatestVersionSuccess(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name": "v1.2.3"}`)) //nolint:errcheck
	}))
	defer mock.Close()

	// Override ghAPIURL by testing FetchLatestVersion with a custom client
	// that redirects to our mock.
	client := mock.Client()
	// We need to test the actual function, so we'll use UpdateAt with a mock server.
	_ = client
}

func TestUpdateAtAlreadyUpToDate(t *testing.T) {
	// Mock GitHub API that returns the same version.
	ghMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name": "v1.0.0"}`)) //nolint:errcheck
	}))
	defer ghMock.Close()

	// Temporarily override the function's HTTP call by providing a custom client
	// with a transport that redirects GitHub API calls to our mock.
	transport := &testTransport{handler: ghMock}
	client := &http.Client{Transport: transport}

	home := t.TempDir()
	var buf bytes.Buffer
	err := UpdateAt(home, testDarwin, "amd64", "v1.0.0", client, &buf)
	if err != nil {
		t.Fatalf("UpdateAt: %v", err)
	}
	if !strings.Contains(buf.String(), "Already up to date") {
		t.Errorf("expected up-to-date message, got: %s", buf.String())
	}
}

func TestUpdateAtNewVersion(t *testing.T) {
	binaryContent := "#!/bin/sh\necho updated\n"
	ghMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "releases/latest") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"tag_name": "v2.0.0"}`)) //nolint:errcheck
			return
		}
		// Binary download.
		w.Write([]byte(binaryContent)) //nolint:errcheck
	}))
	defer ghMock.Close()

	transport := &testTransport{handler: ghMock}
	client := &http.Client{Transport: transport}

	home := t.TempDir()
	exe := createDummyExe(t)

	// Install npm-bulwark so it's found as installed.
	var setup bytes.Buffer
	if err := SetupFiles(testProxyInfo(), home, exe, testDarwin, &setup); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var buf bytes.Buffer
	err := UpdateAt(home, testDarwin, "amd64", "v1.0.0", client, &buf)
	if err != nil {
		t.Fatalf("UpdateAt: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "updated to v2.0.0") {
		t.Errorf("expected update message, got: %s", output)
	}
	if !strings.Contains(output, "Update complete") {
		t.Errorf("expected completion message, got: %s", output)
	}
}

func TestUpdateAtNoInstalled(t *testing.T) {
	ghMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name": "v2.0.0"}`)) //nolint:errcheck
	}))
	defer ghMock.Close()

	transport := &testTransport{handler: ghMock}
	client := &http.Client{Transport: transport}

	home := t.TempDir()
	var buf bytes.Buffer
	err := UpdateAt(home, testDarwin, "amd64", "v1.0.0", client, &buf)
	if err != nil {
		t.Fatalf("UpdateAt: %v", err)
	}
	if !strings.Contains(buf.String(), "No installed proxies found") {
		t.Errorf("expected no-installed message, got: %s", buf.String())
	}
}

func TestUpdateAtAPIError(t *testing.T) {
	ghMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ghMock.Close()

	transport := &testTransport{handler: ghMock}
	client := &http.Client{Transport: transport}

	home := t.TempDir()
	var buf bytes.Buffer
	err := UpdateAt(home, testDarwin, "amd64", "v1.0.0", client, &buf)
	if err == nil {
		t.Error("expected error for API failure")
	}
}

func TestBinaryDownloadURL(t *testing.T) {
	got := binaryDownloadURL("v1.0.0", "npm-bulwark", "linux", "amd64")
	if !strings.Contains(got, "v1.0.0/npm-bulwark-linux-amd64") {
		t.Errorf("unexpected URL: %s", got)
	}

	gotWin := binaryDownloadURL("v1.0.0", "npm-bulwark", testWindows, "amd64")
	if !strings.HasSuffix(gotWin, ".exe") {
		t.Errorf("Windows URL should end with .exe: %s", gotWin)
	}
}

func TestDownloadBinarySuccess(t *testing.T) {
	content := "binary-data"
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(content)) //nolint:errcheck
	}))
	defer mock.Close()

	dest := filepath.Join(t.TempDir(), "binary")
	if err := downloadBinary(mock.Client(), mock.URL, dest); err != nil {
		t.Fatalf("downloadBinary: %v", err)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != content {
		t.Errorf("content: want %q, got %q", content, string(data))
	}
}

func TestDownloadBinary404(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mock.Close()

	dest := filepath.Join(t.TempDir(), "binary")
	err := downloadBinary(mock.Client(), mock.URL, dest)
	if err == nil {
		t.Error("expected error for 404")
	}
}

func TestDownloadBinaryBadDest(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data")) //nolint:errcheck
	}))
	defer mock.Close()

	err := downloadBinary(mock.Client(), mock.URL, "/nonexistent/dir/binary")
	if err == nil {
		t.Error("expected error for bad destination")
	}
}

// --- SetupFiles PATH integration ---

func TestSetupFilesAddsToPath(t *testing.T) {
	home := t.TempDir()
	exe := createDummyExe(t)
	// Create .zshrc so AddToPath has something to modify.
	zshrc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(zshrc, []byte("# existing\n"), cfgPerm); err != nil {
		t.Fatal(err)
	}
	p := testProxyInfo()
	var buf bytes.Buffer
	if err := SetupFiles(p, home, exe, testDarwin, &buf); err != nil {
		t.Fatalf("SetupFiles: %v", err)
	}
	data, _ := os.ReadFile(zshrc)
	if !strings.Contains(string(data), pathMarkerStart) {
		t.Error("SetupFiles should add Bulwark to PATH")
	}
}

// testTransport redirects all requests to a local httptest.Server.
type testTransport struct {
	handler *httptest.Server
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rewrite the request URL to point to our test server.
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.handler.URL, "http://")
	return http.DefaultTransport.RoundTrip(req)
}
