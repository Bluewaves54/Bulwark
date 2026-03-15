// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package installer

import "syscall"

func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
