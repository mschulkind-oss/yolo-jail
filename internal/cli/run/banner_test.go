package run

import (
	"runtime"
	"strings"
	"testing"
)

func TestHostPlatformNaming(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         string
	}{
		{"linux", "amd64", "x86_64"},
		{"linux", "arm64", "aarch64"},
		{"darwin", "arm64", "arm64"},
		{"darwin", "amd64", "x86_64"},
	}
	for _, tc := range cases {
		if got := platformMachine(tc.goos, tc.goarch); got != tc.want {
			t.Errorf("platformMachine(%q, %q) = %q, want %q", tc.goos, tc.goarch, got, tc.want)
		}
	}

	p := hostPlatform()
	wantMachine := platformMachine(runtime.GOOS, runtime.GOARCH)
	if want := runtime.GOOS + "/" + wantMachine; p != want {
		t.Errorf("hostPlatform() = %q, want %q", p, want)
	}
	if strings.Contains(p, "amd64") {
		t.Errorf("hostPlatform() = %q, must not contain Go's amd64 (want x86_64)", p)
	}
	if runtime.GOOS != "darwin" && strings.Contains(p, "arm64") {
		t.Errorf("hostPlatform() = %q on %s, arm64 must map to aarch64 off macOS", p, runtime.GOOS)
	}
}

func TestStartupBanner(t *testing.T) {
	got := StartupBanner("0.6.0", "podman", "yolo-x-abc", nil, "")
	if !strings.HasPrefix(got, "yolo-jail 0.6.0 | ") || !strings.HasSuffix(got, " | podman | yolo-x-abc") {
		t.Errorf("banner = %q", got)
	}
	got = StartupBanner("0.6.0", "podman", "c", nil, "0.5.0")
	if !strings.Contains(got, "attached to jail built at 0.5.0") {
		t.Errorf("attached banner = %q", got)
	}
	got = StartupBanner("0.6.0", "podman", "c", []string{"pids=32768"}, "")
	if !strings.Contains(got, "\nResource limits: pids=32768") {
		t.Errorf("res banner = %q", got)
	}
}
