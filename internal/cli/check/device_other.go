//go:build !linux

package check

import "os"

// The KVM and ROCm device-node sections only ever execute on Linux (Python
// gates them behind `not IS_MACOS` and the enclosing gpu/kvm config, and the
// device nodes are Linux kernel features). On non-Linux builds these are never
// reached at runtime, but the orchestration references them, so provide inert
// stubs that keep `GOOS=darwin GOARCH=arm64 go build ./...` green. If a caller
// ever did reach them off-Linux, the conservative answers below (no access, no
// group) match "device not usable", never a false PASS.

func accessRW(string) bool { return false }

func nodeGIDReal(string) (int, string, bool) { return 0, "", false }

func inUserGroupsReal(int) bool { return false }

// isExecutable falls back to the file mode's any-execute bit on non-Linux. This
// runs for the inline-loopholes exec check, which is host-side and not
// jail-gated; on macOS the mode bit is a faithful-enough proxy for os.X_OK for
// an owned file (the real check runs on Linux CI/hosts).
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}
