//go:build !windows

package main

import (
	"os"
	"syscall"
)

func isRootUser() bool {
	return os.Geteuid() == 0
}

func terminationSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
