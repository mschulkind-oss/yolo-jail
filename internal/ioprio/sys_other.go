//go:build !linux

package ioprio

import "errors"

// errUnsupported: process I/O priority (ioprio_set) is a Linux call. The package's darwin
// build is live (the macos-user bootstrap compiles the entrypoint package), but that path
// never reaches a caller of these: macos-user's mechanism is the process disk policy,
// setiopolicy_np, which the launcher sets through SetProcessDiskPolicy
// (diskpolicy_darwin.go; docs/design/io-priority.md §5.5).
var errUnsupported = errors.New("I/O priority is set only on Linux")

// SetCurrentThread is Linux-only; see sys_linux.go.
func SetCurrentThread(int) error { return errUnsupported }

// GetThread is Linux-only; see sys_linux.go.
func GetThread(int) (int, error) { return 0, errUnsupported }
