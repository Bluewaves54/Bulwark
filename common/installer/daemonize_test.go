// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilterArgsRemovesBackground(t *testing.T) {
	args := []string{"-config", "test.yaml", "-background", "-auth-token", "xyz"}
	got := filterArgs(args, "background")
	if len(got) != 4 {
		t.Fatalf("expected 4 args, got %d: %v", len(got), got)
	}
	for _, a := range got {
		if a == "-background" {
			t.Error("filterArgs did not remove -background")
		}
	}
}

func TestFilterArgsRemovesDoubleDash(t *testing.T) {
	args := []string{"--background", "-config", "test.yaml"}
	got := filterArgs(args, "background")
	if len(got) != 2 {
		t.Fatalf("expected 2 args, got %d: %v", len(got), got)
	}
}

func TestFilterArgsNoMatch(t *testing.T) {
	args := []string{"-config", "test.yaml"}
	got := filterArgs(args, "background")
	if len(got) != 2 {
		t.Fatalf("expected 2 args, got %d: %v", len(got), got)
	}
}

func TestFilterArgsEmpty(t *testing.T) {
	got := filterArgs(nil, "background")
	if len(got) != 0 {
		t.Fatalf("expected 0 args, got %d: %v", len(got), got)
	}
}

func TestDaemonizeMissingLogDir(t *testing.T) {
	p := testProxyInfo()
	// Use a home dir that exists but has no .bulwark subdirectory —
	// Daemonize should fail when it cannot open the log file.
	home := filepath.Join(t.TempDir(), "no", "such", "path")
	_, err := Daemonize(p, home)
	if err == nil {
		t.Error("expected error for missing log directory")
	}
}

func TestDaemonizeCreatesLogFile(t *testing.T) {
	p := testProxyInfo()
	home := t.TempDir()
	ecoDir := filepath.Join(home, bulwarkDir, p.BinaryName)
	if err := os.MkdirAll(ecoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Daemonize will try to re-exec os.Executable. In test, that is the
	// test binary itself, which will exit quickly with a non-zero code.
	// We only verify it reaches the Start() call (creates the log file).
	pid, _ := Daemonize(p, home)
	// Kill the spawned process so it releases the log-file handle before
	// t.TempDir cleanup runs (required on Windows where open handles
	// prevent directory deletion).
	if pid > 0 {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Kill()
			_, _ = proc.Wait()
		}
	}

	logPath := filepath.Join(ecoDir, "daemon.log")
	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("expected daemon.log to be created: %v", err)
	}
}

func TestDaemonSysProcAttrNotNil(t *testing.T) {
	attr := daemonSysProcAttr()
	if attr == nil {
		t.Error("daemonSysProcAttr returned nil")
	}
}
