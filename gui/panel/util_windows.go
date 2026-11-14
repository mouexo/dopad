//go:build windows

package main

import "os"

func isRootUser() bool {
	return false
}

func terminationSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
