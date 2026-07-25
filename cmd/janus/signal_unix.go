//go:build !windows

package main

import (
	"os"
	"syscall"
)

// terminateGracefully asks the process to exit cleanly (SIGTERM). On POSIX this
// gives the child a chance to run shutdown handlers before it is killed.
func terminateGracefully(p *os.Process) error {
	return p.Signal(syscall.SIGTERM)
}
