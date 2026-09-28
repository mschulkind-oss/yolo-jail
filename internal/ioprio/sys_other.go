//go:build !linux

package ioprio

import "errors"

// errUnsupported: process I/O priority is a Linux call. The package's darwin build is live
// (the macos-user bootstrap compiles the entrypoint package), but that path never reaches
// a caller of these, and macos-user's own mechanism is setiopolicy_np, which is build
// step 5 of docs/design/io-priority.md.
var errUnsupported = errors.New("I/O priority is set only on Linux")

// SetCurrentThread is Linux-only; see sys_linux.go.
func SetCurrentThread(int) error { return errUnsupported }

// GetThread is Linux-only; see sys_linux.go.
func GetThread(int) (int, error) { return 0, errUnsupported }
