//go:build linux

package main

import (
	"os/exec"
	"syscall"
)

// tieToBridge makes the kernel end cmd when the bridge dies, however it dies:
// kill -9 gives the bridge no chance to stop its own ssh, and an ssh left
// running keeps the server's forwarded port. The signal is tied to the thread
// that starts cmd, so the caller keeps that thread (runtime.LockOSThread) for
// as long as cmd runs.
func tieToBridge(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
