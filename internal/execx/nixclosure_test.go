package execx

import (
	"strings"
	"testing"
)

// TestNixClosureEnvDropsOnlyTheLoaderOverrides: both loader variables go, every
// spelling of them (a duplicate entry included), and nothing else — a scrub that
// also lost PATH or HOME would trade one startup failure for another.
func TestNixClosureEnvDropsOnlyTheLoaderOverrides(t *testing.T) {
	in := []string{"PATH=/bin", "LD_LIBRARY_PATH=/lib", "HOME=/h", "LD_PRELOAD=/x.so",
		"LD_LIBRARY_PATH=/usr/lib", "LD_LIBRARY_PATHX=kept", "NOEQUALS"}
	orig := append([]string(nil), in...)
	got := NixClosureEnv(in)
	want := []string{"PATH=/bin", "HOME=/h", "LD_LIBRARY_PATHX=kept", "NOEQUALS"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("NixClosureEnv = %q, want %q", got, want)
	}
	if strings.Join(in, "\n") != strings.Join(orig, "\n") {
		t.Errorf("NixClosureEnv mutated its input: %q", in)
	}
}

// TestNixClosureCommandRunsWithoutTheLoaderOverrides runs a real child, so the
// helper is pinned by what the child sees rather than by the Env field.
func TestNixClosureCommandRunsWithoutTheLoaderOverrides(t *testing.T) {
	decoy := t.TempDir()
	t.Setenv("LD_LIBRARY_PATH", decoy)
	t.Setenv("LD_PRELOAD", decoy+"/decoy.so")
	t.Setenv("YOLO_EXECX_MARKER", "kept")
	// "set" or nothing, never a value, so a failure prints none of the
	// environment the test ran in.
	out, err := NixClosureCommand("/bin/sh", "-c",
		`echo "${LD_LIBRARY_PATH+set},${LD_PRELOAD+set},${YOLO_EXECX_MARKER+set}"`).Output()
	if err != nil {
		t.Fatalf("child: %v", err)
	}
	if got, want := strings.TrimSpace(string(out)), ",,set"; got != want {
		t.Errorf("child saw LD_LIBRARY_PATH,LD_PRELOAD,marker = %q, want %q", got, want)
	}
}
