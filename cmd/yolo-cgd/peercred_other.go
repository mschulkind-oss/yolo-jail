//go:build !linux

package main

import "net"

// peerCredPID is a non-Linux stub. The cgroup-delegate daemon is Linux-only
// (cgroup v2), so it never runs on darwin — but the whole Go tree must still
// build there (macos-user is a supported host; scripts/build-go.sh builds
// every cmd/). Returns 0, which the handler treats as "could not determine
// caller PID".
func peerCredPID(_ *net.UnixConn) int { return 0 }
