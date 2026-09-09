//go:build !linux

package hostcas

// DefaultProbe off Linux reports "nothing here", which is inert: every non-Linux
// host is already refused by the macOS gate (darwin) or by the backend gate
// (Apple Container, macos-user), so no decision reaches this. It exists so the
// package builds for `go build ./...` on a darwin developer machine and for the
// darwin half of CI, not as a second code path — see storepackages.go's own
// non-Linux stubs for the same shape.
func DefaultProbe(string) Presence { return Presence{} }
