package check

import "testing"

// TestMachineForPlatform pins the platform.machine() spelling per OS/arch.
// The load-bearing case is darwin/arm64 → "arm64" (audit §C): an unconditional
// arm64→aarch64 map made `yolo check` report "aarch64" on Apple Silicon,
// diverging from the run banner and from Python's platform.machine().
func TestMachineForPlatform(t *testing.T) {
	cases := []struct{ goos, goarch, want string }{
		{"darwin", "arm64", "arm64"},    // Apple Silicon keeps arm64
		{"linux", "arm64", "aarch64"},   // Linux uname
		{"darwin", "amd64", "x86_64"},   // Intel mac
		{"linux", "amd64", "x86_64"},    // Linux x86
		{"linux", "riscv64", "riscv64"}, // pass-through
	}
	for _, c := range cases {
		if got := machineForPlatform(c.goos, c.goarch); got != c.want {
			t.Errorf("machineForPlatform(%q, %q) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}
