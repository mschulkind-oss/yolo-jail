package check

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// section_hostlaunchpath_test.go pins `yolo check`'s host launch section
// (docs/design/host-launch-environment.md §3, HE-D7): it says which PATH it read, names each
// host_path folder and whether it exists, resolves every dependency the floor does not answer for
// through the launch PATH's lookup, and warns with the miss line for one it cannot find. Every
// fixture is a temp HOME and a fake PATH read through the Getenv seam. TestEverySectionIsWired pins
// the call from Check().

// launchPathCheckFixture is a temp HOME whose user config is userConfig, one selected pack declaring
// contributions, and Options whose PATH is a fresh empty folder.
func launchPathCheckFixture(t *testing.T, userConfig string, contributions ...string) (*Options, string, string) {
	t.Helper()
	home := floortest.ResolvedTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	writeCheckFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), userConfig, 0o644)
	packDir := filepath.Join(floortest.ResolvedTemp(t), "needpack")
	writeCheckFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"needpack","contributes":[`+strings.Join(contributions, ",")+`]}`, 0o644)
	pack, problems := packload.LoadDir(packDir, "needpack")
	if len(problems) > 0 {
		t.Fatalf("fixture pack: %v", problems)
	}
	pathDir := floortest.ResolvedTemp(t)
	o := &Options{Getenv: func(k string) string {
		if k == "PATH" {
			return pathDir
		}
		return ""
	}}
	o.selectedPacks, o.selectedPacksKnown = []*packload.Pack{pack}, true
	// The floor the sections ask which programs it answers for: the default one, its loader check
	// under a filesystem root of the fixture's own (checkLoaderRoot), so no answer depends on this
	// machine's /lib64.
	root := checkLoaderRoot(t)
	o.HostFloor = func(progs []hostfloor.Program) *hostfloor.Floor {
		f := (&Options{}).hostFloor(progs)
		f.Root = root
		return f
	}
	return o, home, pathDir
}

func writeCheckFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func runLaunchPathSection(o *Options) (string, *reporter) {
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o.sectionHostLaunchPath(r)
	return buf.String(), r
}

// TestCheckHostLaunchPathSaysWhichPathItRead: the section says it read the PATH this check was
// started with plus host_path's folders (HE-D7), names each host_path folder — an absent one as
// absent — passes a required tool host_path holds, naming host_path as its source, and warns for one
// only a hint folder holds, with the miss line as the note. A program the floor answers for is left
// to the floor section. Resolve the section's lookup without host_path, or drop its miss line, and
// this fails.
func TestCheckHostLaunchPathSaysWhichPathItRead(t *testing.T) {
	o, home, pathDir := launchPathCheckFixture(t, `{"host_path": ["~/tools/bin", "~/absent/bin"]}`,
		`{"kind":"requires","bin":"yolo-hp-tool"}`,
		`{"kind":"requires","bin":"yolo-hp-missing"}`,
		`{"kind":"program","bin":"floorcli","via":"npm","package":"floorcli-pkg"}`)
	tool := filepath.Join(home, "tools", "bin", "yolo-hp-tool")
	writeCheckFile(t, tool, "#!/bin/sh\n", 0o755)
	writeCheckFile(t, filepath.Join(home, ".cargo", "bin", "yolo-hp-missing"), "#!/bin/sh\n", 0o755)

	out, r := runLaunchPathSection(o)
	for _, want := range []string{
		"Host launch PATH",
		"read here from the PATH this check was started with, then host_path's folders. A launcher with " +
			"another PATH (a Waybar button, cron, a hotkey launcher) searches its own",
		"the PATH this check was started with: " + pathDir,
		"host_path: ~/tools/bin, searched after the PATH this check was started with",
		"host_path: ~/absent/bin does not exist; a lookup skips it",
		"[PASS] yolo-hp-tool — " + tool + " (host_path)",
		"[WARN] yolo-hp-missing — not on this PATH",
		"yolo-hp-missing (required by the needpack pack) is not on the PATH yolo searched, " + pathDir +
			", the PATH yolo was started with, then host_path's ~/tools/bin:~/absent/bin. If yolo-hp-missing " +
			`is installed, add its folder to "host_path" in ~/.config/yolo-jail/config.jsonc; ~/.cargo/bin has one.`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("section lacks %q:\n%s", want, out)
		}
	}
	if r.warned != 1 {
		t.Errorf("warned %d times, want once, for the missing tool", r.warned)
	}
	if strings.Contains(out, "floorcli") {
		t.Errorf("a program the floor answers for was resolved on the PATH:\n%s", out)
	}
}

// TestCheckHostLaunchPathGivesAProgramWithNoBuildHereTheMissLine: a program whose vendor publishes no
// build for this host is not graded — nothing could install it — but it is a program `yolo host`
// looks up on the launch PATH, so a miss there carries the miss line like any other (HE-D2). Drop the
// note from the section's no-build row, and this fails.
func TestCheckHostLaunchPathGivesAProgramWithNoBuildHereTheMissLine(t *testing.T) {
	other := "darwin"
	if runtime.GOOS == "darwin" {
		other = "linux"
	}
	o, home, pathDir := launchPathCheckFixture(t, `{}`,
		`{"kind":"program","bin":"yolo-hp-uptool","via":"npm","package":"yolo-hp-uptool-pkg","platforms":["`+other+`"]}`)
	writeCheckFile(t, filepath.Join(home, ".cargo", "bin", "yolo-hp-uptool"), "#!/bin/sh\n", 0o755)
	out, r := runLaunchPathSection(o)
	for _, want := range []string{
		"yolo-hp-uptool — not on this PATH, and no build for this host",
		"yolo-hp-uptool (a program of the needpack pack) is not on the PATH yolo searched, " + pathDir +
			`, the PATH yolo was started with. If yolo-hp-uptool is installed, add its folder to "host_path" in ` +
			"~/.config/yolo-jail/config.jsonc; ~/.cargo/bin has one.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("section lacks %q:\n%s", want, out)
		}
	}
	if r.warned != 0 || r.failed != 0 {
		t.Errorf("a program with no build here graded (%d warn, %d fail)", r.warned, r.failed)
	}
}

// TestCheckHostLaunchPathNamesARefusedHostPathEntry: an entry validation refuses reaches no PATH, so
// the section names it as never searched, with the fix — even with nothing else to report — and
// resolves nothing in it. Drop the section's refused-entry line, and this fails.
func TestCheckHostLaunchPathNamesARefusedHostPathEntry(t *testing.T) {
	o, _, _ := launchPathCheckFixture(t, `{"host_path": ["$HOME/.cargo/bin"]}`,
		`{"kind":"program","bin":"floorcli","via":"npm","package":"floorcli-pkg"}`)
	out, _ := runLaunchPathSection(o)
	if want := `host_path: "$HOME/.cargo/bin" is ignored, so no lookup searches it: host_path expands no ` +
		`variable, so write it "~/.cargo/bin"`; !strings.Contains(out, want) {
		t.Errorf("section lacks %q:\n%s", want, out)
	}
}

// TestCheckHostLaunchPathIsSilentWithNothingToSay: no host_path and no dependency the floor does not
// answer for is no section at all — and a jail has none either.
func TestCheckHostLaunchPathIsSilentWithNothingToSay(t *testing.T) {
	o, _, _ := launchPathCheckFixture(t, `{}`,
		`{"kind":"program","bin":"floorcli","via":"npm","package":"floorcli-pkg"}`)
	if out, _ := runLaunchPathSection(o); out != "" {
		t.Errorf("section printed with nothing to say:\n%s", out)
	}
	o, _, _ = launchPathCheckFixture(t, `{"host_path": ["/opt/x/bin"]}`, `{"kind":"requires","bin":"rg"}`)
	get := o.Getenv
	o.Getenv = func(k string) string {
		if k == "YOLO_VERSION" {
			return "0.0.0-test"
		}
		return get(k)
	}
	if out, _ := runLaunchPathSection(o); out != "" {
		t.Errorf("in a jail the host section printed:\n%s", out)
	}
}

// TestCheckFloorSectionFindsANoFloorEntryProgramInHostPath: a program `host_floor` leaves out runs
// from the launch PATH, so the floor section names the copy a host_path folder holds as the one that
// runs — the exec's own lookup, host_path included.
func TestCheckFloorSectionFindsANoFloorEntryProgramInHostPath(t *testing.T) {
	o, home, _ := launchPathCheckFixture(t, `{"host_floor": {"needpack": false}, "host_path": ["~/tools/bin"]}`,
		`{"kind":"program","bin":"floorcli","via":"npm","package":"floorcli-pkg"}`)
	copyInHostPath := filepath.Join(home, "tools", "bin", "floorcli")
	writeCheckFile(t, copyInHostPath, "#!/bin/sh\n", 0o755)
	out, _ := runHostFloorSection(o)
	if !strings.Contains(out, "(here, "+copyInHostPath+")") {
		t.Errorf("the floor section did not find the host_path copy:\n%s", out)
	}
}

// TestCheckFloorSectionNamesACopyInHostPathAsNotRun: a program the floor delivers, with another
// copy only in a `host_path` folder — on no PATH this check was started with and at no hint
// location. The floor section's other-copies list reads the launch PATH, so it names that copy as
// one `yolo host` does not run. Hand OtherCopies the bare PATH this check was started with, and
// this fails.
func TestCheckFloorSectionNamesACopyInHostPathAsNotRun(t *testing.T) {
	o, _, _, _ := hostFloorCheckFixture(t, `{"host_path": ["~/tools/bin"]}`)
	copyInHostPath := filepath.Join(paths.Home(), "tools", "bin", "floorcli")
	writeCheckFile(t, copyInHostPath, "#!/bin/sh\n", 0o755)
	out, _ := runHostFloorSection(o)
	if want := "floorcli: also at " + copyInHostPath + " — not run by `yolo host`"; !strings.Contains(out, want) {
		t.Errorf("the floor section lacks %q:\n%s", want, out)
	}
}
