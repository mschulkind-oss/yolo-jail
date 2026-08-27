//go:build !linux && !darwin

package main

import (
	"fmt"
	"os"
)

func openPty() (*os.File, string, error) {
	return nil, "", fmt.Errorf("virtual PTY bridge is not supported on this platform")
}
