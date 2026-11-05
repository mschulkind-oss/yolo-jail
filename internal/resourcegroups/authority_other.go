//go:build !linux

package resourcegroups

import (
	"context"
	"errors"
)

var errUnsupportedDelegation = errors.New("delegated cgroup-v2 I/O is unsupported on this platform; run yolo check --no-build")

func CheckDelegatedIO(IOPaths, IOReadFile) error { return errUnsupportedDelegation }
func ReconcileDelegatedIO(context.Context, IOPaths, int, string, func() (IOPaths, error), IOReadFile, func(string) error, func(string, []byte) error) error {
	return errUnsupportedDelegation
}
func ReconcileDelegatedIOAndStart(context.Context, IOPaths, int, string, func() (IOPaths, error), IOReadFile, func(string) error, func(string, []byte) error, string, int, func(int) (string, error), func() error) error {
	return errUnsupportedDelegation
}
func VerifyDelegatedMember(IOPaths, string, int, func(int) (string, error)) error {
	return errUnsupportedDelegation
}
