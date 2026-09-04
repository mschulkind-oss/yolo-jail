package banner

import (
	"runtime"
	"strings"
	"testing"
)

// env returns a getenv func over a fixed map, so a test states the environment
// it means instead of mutating the process's.
func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestMachineNaming(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         string
	}{
		{"linux", "amd64", "x86_64"},
		{"linux", "arm64", "aarch64"},
		{"darwin", "arm64", "arm64"},
		{"darwin", "amd64", "x86_64"},
		{"linux", "riscv64", "riscv64"},
	}
	for _, tc := range cases {
		if got := Machine(tc.goos, tc.goarch); got != tc.want {
			t.Errorf("Machine(%q, %q) = %q, want %q", tc.goos, tc.goarch, got, tc.want)
		}
	}

	p := Platform()
	if want := runtime.GOOS + "/" + Machine(runtime.GOOS, runtime.GOARCH); p != want {
		t.Errorf("Platform() = %q, want %q", p, want)
	}
	if strings.Contains(p, "amd64") {
		t.Errorf("Platform() = %q, must not contain Go's amd64 (want x86_64)", p)
	}
	if runtime.GOOS != "darwin" && strings.Contains(p, "arm64") {
		t.Errorf("Platform() = %q on %s, arm64 must map to aarch64 off macOS", p, runtime.GOOS)
	}
}

// The side field is the banner's answer to "which half of yolo was this run on",
// and YOLO_VERSION is the only thing that distinguishes them: inside a jail the
// host sets it, and version.Get then returns the HOST's version verbatim, so the
// version string alone cannot tell the two apart.
func TestSideReadsTheJailDiscriminator(t *testing.T) {
	if got := Side(env(nil)); got != "host" {
		t.Errorf("Side(no YOLO_VERSION) = %q, want host", got)
	}
	if got := Side(env(map[string]string{"YOLO_VERSION": ""})); got != "host" {
		t.Errorf("Side(empty YOLO_VERSION) = %q, want host — an empty value is not a jail", got)
	}
	if got := Side(env(map[string]string{"YOLO_VERSION": "0.8.0"})); got != "in-jail" {
		t.Errorf("Side(YOLO_VERSION set) = %q, want in-jail", got)
	}
}

func TestStartupLine(t *testing.T) {
	host := Startup("0.8.0+881.ga6f61864", env(nil))
	want := "yolo-jail 0.8.0+881.ga6f61864 | " + Platform() + " | host"
	if host != want {
		t.Errorf("Startup(host) = %q, want %q", host, want)
	}
	if strings.Contains(host, "\n") {
		t.Errorf("Startup returned %q — it must be ONE line with no terminator; the caller adds it", host)
	}

	jail := Startup("0.8.0", env(map[string]string{"YOLO_VERSION": "0.8.0"}))
	if !strings.HasSuffix(jail, " | in-jail") {
		t.Errorf("Startup(in jail) = %q, want an in-jail suffix", jail)
	}
}

// The hatch is off by default and any non-empty value arms it — "0" and "false"
// included. That is deliberate: a user who exported YOLO_NO_BANNER=0 meant to
// name the variable, and the alternative is a hatch that silently does nothing.
func TestSuppressedIsOffByDefaultAndOnForAnyValue(t *testing.T) {
	if Suppressed(env(nil)) {
		t.Error("Suppressed with no env — the banner must be ON by default")
	}
	if Suppressed(env(map[string]string{SuppressEnv: ""})) {
		t.Error("Suppressed with an empty value — an empty variable must not arm the hatch")
	}
	for _, v := range []string{"1", "true", "0", "yes"} {
		if !Suppressed(env(map[string]string{SuppressEnv: v})) {
			t.Errorf("Suppressed(%s=%q) = false, want true", SuppressEnv, v)
		}
	}
}

// The hatch's spelling is a user-facing contract: it is documented in
// `yolo --help` and docs/guides/USER_GUIDE.md, and a rename would silently strand
// every shell profile that sets it.
func TestSuppressEnvSpelling(t *testing.T) {
	if SuppressEnv != "YOLO_NO_BANNER" {
		t.Errorf("SuppressEnv = %q — the documented hatch is YOLO_NO_BANNER", SuppressEnv)
	}
}
