// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Daemonize re-launches the current binary as a detached background process.
// It filters out the -background flag from os.Args so the child runs in
// foreground mode (but detached from the terminal). Returns the child PID.
func Daemonize(p ProxyInfo, home string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("finding executable: %w", err)
	}

	// Build child args: everything except -background.
	childArgs := filterArgs(os.Args[1:], "background")

	logPath := filepath.Join(home, bulwarkDir, p.BinaryName, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, cfgPerm)
	if err != nil {
		return 0, fmt.Errorf("opening log file %s: %w", logPath, err)
	}

	cmd := exec.Command(exe, childArgs...) //nolint:gosec // re-execing self with user-supplied flags
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = daemonSysProcAttr()

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return 0, fmt.Errorf("starting background process: %w", err)
	}
	logFile.Close()

	return cmd.Process.Pid, nil
}

// filterArgs returns args with the named boolean flag removed.
func filterArgs(args []string, flagName string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "-"+flagName || a == "--"+flagName {
			continue
		}
		out = append(out, a)
	}
	return out
}
