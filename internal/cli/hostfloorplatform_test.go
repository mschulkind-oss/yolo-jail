package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostfloorplatform_test.go pins the no-copy line's wording per platform through hostExec. The
// platform is the FLOOR's (Floor.GOOS, which decided the disposition and wires its capture act), so
// each case runs on every CI host: the line an installer agent on a Mac gets when the macos-user
// capture act cannot run (HP-D2) is checked on Linux too, and never only where darwin CI happens to
// run.

// withFloorPlatform makes this test's floor believe it runs on goos.
func withFloorPlatform(t *testing.T, goos string) {
	t.Helper()
	orig := newHostFloor
	newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
		f := orig(out, progs)
		f.GOOS = goos
		return f
	}
	t.Cleanup(func() { newHostFloor = orig })
}

// nativeFloorFixture is a temp HOME whose user config selects one fixture pack declaring an
// INSTALLER program, nativecli, with the test floor under it. It returns the pack's directory.
func nativeFloorFixture(t *testing.T) (pack string) {
	t.Helper()
	home := floortest.ResolvedTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(floortest.ResolvedTemp(t))
	pack = filepath.Join(floortest.ResolvedTemp(t), "nativepack")
	writeFile(t, filepath.Join(pack, "pack.json"), `{"name":"nativepack","contributes":[
	  {"kind":"program","bin":"nativecli","via":"installer","url":"https://example.invalid/nativecli/install.sh"}]}`)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[{"source":"file://`+pack+`","name":"nativepack"}]}`)
	orig := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = orig })
	withTestFloor(t)
	return pack
}

// TestTheNoCopyLineNamesTheFloorsPlatform: an installer agent on a Mac whose sandbox account is not
// set up has no floor copy, its reason naming `yolo macos-setup` (HP-D2's act cannot run), an npm
// agent `host_floor` leaves out has none on a Mac or on Linux. The installer agent's launch refuses
// (HNR-D2); the agent `host_floor` leaves out runs the copy on the caller's PATH (HNR-D4), said by
// the hand-over line. No case says "yet": every reason names
// what ends it, or says nothing does.
func TestTheNoCopyLineNamesTheFloorsPlatform(t *testing.T) {
	for _, c := range []struct {
		name, goos, bin string
		setup           func(t *testing.T)
		want            []string
		never           string
		refused         bool
	}{
		{name: "an installer agent on a Mac before macos-setup", goos: "darwin", bin: "nativecli",
			setup: func(t *testing.T) { nativeFloorFixture(t); withMac(t, macSetup{terminal: true}) },
			want: []string{"yolo host: yolo has no copy of nativecli on this Mac (there is no capture of nativecli " +
				"on this machine, and the sandbox account _yolojail",
				"run the one-time setup, `yolo macos-setup`, and the next `yolo host` launch captures it)" +
					declaredNoCopyRefusal},
			never: "yet", refused: true},
		{name: "an npm agent host_floor leaves out, on a Mac", goos: "darwin", bin: "floorcli",
			setup: func(t *testing.T) { floorHostFixture(t, `,"host_floor":{"floorpack":false}`) },
			want:  []string{"yolo host: yolo has no copy of floorcli on this Mac (the user config's `host_floor`"},
			never: "yet"},
		{name: "an npm agent host_floor leaves out, on Linux", goos: "linux", bin: "floorcli",
			setup: func(t *testing.T) { floorHostFixture(t, `,"host_floor":{"floorpack":false}`) },
			want:  []string{"yolo host: yolo has no copy of floorcli on this machine (the user config's `host_floor`"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.setup(t)
			withFloorPlatform(t, c.goos)
			stub := filepath.Join(stubBins(t, c.bin), c.bin)
			got := captureHostExec(t)
			var errw bytes.Buffer
			rc := hostExec(nil, []string{c.bin}, io.Discard, &errw, nil)
			want := c.want
			if c.refused {
				if rc != 127 || got.execed {
					t.Fatalf("rc=%d target=%s, want 127 and no exec, never the PATH copy %s\n%s", rc, got.target,
						stub, errw.String())
				}
			} else {
				if rc != 0 || got.target != stub {
					t.Fatalf("rc=%d target=%s, want the PATH copy %s\n%s", rc, got.target, stub, errw.String())
				}
				want = append(want, "yolo host: starting "+c.bin+" (from your PATH, "+stub+")")
			}
			for _, want := range want {
				if !strings.Contains(errw.String(), want) {
					t.Errorf("stderr lacks %q:\n%s", want, errw.String())
				}
			}
			if c.never != "" && strings.Contains(errw.String(), "on this Mac "+c.never) {
				t.Errorf("the line says %q for a case that is not a matter of time:\n%s", c.never, errw.String())
			}
			if strings.Contains(errw.String(), "HP-D2") {
				t.Errorf("the line cites a design decision rather than naming a step:\n%s", errw.String())
			}
			if _, err := os.Stat(paths.HostFloorDir()); err == nil {
				t.Errorf("a launch with no floor entry created %s", paths.HostFloorDir())
			}
		})
	}
}
