//go:build !linux

package capture

import (
	"errors"
	"runtime"
)

// landlock_other.go is the non-Linux half of the host capture's confinement
// (docs/design/host-tool-provisioning.md HP-D18): Landlock is a Linux security module, so a host here
// has none, and the host capture is never chosen. A Mac's host capture is the macos-user capture act
// (HP-D2), Seatbelt and the sandbox account, which needs nothing from this file.

// LandlockMinABI is the oldest Landlock ABI the host capture runs under (landlock_linux.go).
const LandlockMinABI = 2

// ErrLandlockUnavailable is why there is no Landlock: on this OS, there is none to have.
var ErrLandlockUnavailable = errors.New("this machine is " + runtime.GOOS + ", and Landlock is a Linux security module")

// LandlockABI reports that this OS has no Landlock.
func LandlockABI() (int, error) { return 0, ErrLandlockUnavailable }

// HostConfinementABI reports that no host capture can be confined here.
func HostConfinementABI() (int, error) { return 0, ErrLandlockUnavailable }
