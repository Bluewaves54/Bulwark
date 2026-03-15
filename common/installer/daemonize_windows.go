// SPDX-License-Identifier: Apache-2.0

//go:build windows

package installer

import "syscall"

// createNewProcessGroup detaches the child from the parent's console.
const createNewProcessGroup = 0x00000200

func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}
