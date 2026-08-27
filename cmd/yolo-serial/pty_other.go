//go:build !linux

package main

import (
	"fmt"
	"os"
)

func openPty() (*os.File, string, error) {
	return nil, "", fmt.Errorf("virtual PTY bridge is only supported on linux")
}
