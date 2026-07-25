//go:build windows

package main

import "os"

// terminateGracefully has no graceful-signal equivalent on Windows: os/exec's
// Process.Signal(syscall.SIGTERM) is unsupported there, so we fall back to Kill.
// The watch loop still applies its grace window before this, but a Windows child
// receives no soft-shutdown notification.
func terminateGracefully(p *os.Process) error {
	return p.Kill()
}
