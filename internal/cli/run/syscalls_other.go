//go:build !linux

package run

// sysconfPhysMem is Linux-only (macOS uses `sysctl hw.memsize` in the AC
// default-memory path). Off-Linux this reports failure so the caller falls back
// to the "8g" default — the AC default-memory path never reaches here anyway
// (it takes the IsMacOS sysctl branch), so this is inert.
func sysconfPhysMem() (int64, bool) { return 0, false }

// hostHardMemlock: off Linux the GPU passthrough path is not exercised (GPU
// requires podman+Linux). Report unlimited so the memlock ulimit degrades to
// the always-accepted "-1:-1" literal — inert, since gpuArgs isn't reached.
func hostHardMemlock() (int64, bool) { return 0, true }
