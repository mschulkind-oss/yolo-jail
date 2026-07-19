package frontdoor

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestRewriteArgv(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"--", "echo", "foo"}, []string{"run", "--", "echo", "foo"}},
		{[]string{"run", "--", "echo"}, []string{"run", "--", "echo"}},
		{[]string{"broker", "restart"}, []string{"broker", "restart"}},
		{[]string{"-v", "--", "ls"}, []string{"-v", "run", "--", "ls"}},
		{[]string{"check"}, []string{"check"}},
		{[]string{"ps"}, []string{"ps"}},
		{nil, nil},
	}
	for _, tc := range cases {
		got := RewriteArgv(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("RewriteArgv(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSubcommand(t *testing.T) {
	cases := map[string]string{
		"run --":      "run",
		"check":       "check",
		"broker stop": "broker",
		"-v run":      "run",
		"--version":   "",
		"":            "",
		"bogus -- x":  "",
	}
	for in, want := range cases {
		args := strings.Fields(in)
		if got := Subcommand(args); got != want {
			t.Errorf("Subcommand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsNative(t *testing.T) {
	for _, sub := range []string{"check", "doctor", "run", "ps", "broker", "prune"} {
		if !IsNative(sub) {
			t.Errorf("IsNative(%q) = false, want true", sub)
		}
	}
	if IsNative("not-a-subcommand") {
		t.Error("IsNative(\"not-a-subcommand\") = true, want false")
	}
	if IsNative("") {
		t.Error("IsNative(\"\") = true, want false")
	}
}

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
