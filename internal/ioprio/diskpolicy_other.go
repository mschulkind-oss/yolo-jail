//go:build !darwin

package ioprio

import "errors"

// errNoDiskPolicy: the process disk I/O policy is a macOS call (setiopolicy_np). Off darwin
// the macos-user launcher that calls these never runs a launch, so this is the answer a
// misrouted caller gets rather than a silent no-op.
var errNoDiskPolicy = errors.New("the process disk I/O policy (setiopolicy_np) is set only on macOS")

// SetProcessDiskPolicy is macOS-only; see diskpolicy_darwin.go.
func SetProcessDiskPolicy(int) error { return errNoDiskPolicy }

// GetProcessDiskPolicy is macOS-only; see diskpolicy_darwin.go.
func GetProcessDiskPolicy() (int, error) { return 0, errNoDiskPolicy }
