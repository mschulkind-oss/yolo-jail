package check

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// section_hostfloor_test.go pins §7 of docs/design/host-tool-provisioning.md: a row per floor
// entry with its disposition, the other copies named as not run by `yolo host`, an interrupted
// install warned about — and nothing installed by the observe verb. TestEverySectionIsWired pins
// the call from Check().

// hostFloorCheckFixture is a temp HOME, one selected pack declaring `program floorcli via npm`,
// a hand-installed floorcli on the PATH check reads, and Options whose HostFloor is the floor over
// a fake Node distribution.
func hostFloorCheckFixture(t *testing.T, userConfig string) (*Options, *hostfloor.Floor, *floortest.Dist, string) {
	t.Helper()
	home := floortest.ResolvedTemp(t)
	t.Setenv("HOME", home)
	// OUT OF A JAIL, pinned rather than inherited, as launchPathCheckFixture does: the Getenv seam
	// below answers "" for YOLO_VERSION, but hostpath.Resolve asks the PROCESS environment, and in
	// a jail it returns the seam's PATH alone, so a `host_path` folder was on no launch PATH.
	// Unset, not emptied, because internal/loopholes reads the variable's presence.
	t.Setenv("YOLO_VERSION", "")
	os.Unsetenv("YOLO_VERSION")
	cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(userConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	packDir := filepath.Join(floortest.ResolvedTemp(t), "floorpack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(`{"name":"floorpack","contributes":[
	  {"kind":"program","bin":"floorcli","via":"npm","package":"floorcli-pkg"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pack, problems := packload.LoadDir(packDir, "floorpack")
	if len(problems) > 0 {
		t.Fatalf("fixture pack: %v", problems)
	}
	handBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(handBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(handBin, "floorcli"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dist := floortest.NewDist(t)
	dist.Publish("floorcli-pkg", "1.0.0", "bin=floorcli")
	// A filesystem root of the fixture's own for the loader check (HP-D15), holding each loader
	// Node's official Linux builds ask for, so no row depends on this machine's /lib64.
	root := floortest.ResolvedTemp(t)
	for _, loader := range []string{"/lib64/ld-linux-x86-64.so.2", "/lib/ld-linux-aarch64.so.1"} {
		p := filepath.Join(root, filepath.FromSlash(loader))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("a dynamic loader\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	floor := &hostfloor.Floor{
		Dir: paths.HostFloorDir(), GOOS: dist.GOOS, GOARCH: dist.GOARCH,
		Node: hostfloor.NodeDist{BaseURL: dist.URL, Shipped: floortest.Shipped,
			Pinned: map[string]string{dist.Platform: dist.SHA256}},
		Environ: append(os.Environ(), dist.Environ()...),
		Home:    home,
		Root:    root,
	}
	o := &Options{Getenv: func(k string) string {
		if k == "PATH" {
			return handBin + string(os.PathListSeparator) + "/usr/bin:/bin"
		}
		return ""
	}}
	o.selectedPacks, o.selectedPacksKnown = []*packload.Pack{pack}, true
	o.HostFloor = func([]hostfloor.Program) *hostfloor.Floor { return floor }
	return o, floor, dist, filepath.Join(handBin, "floorcli")
}

func runHostFloorSection(o *Options) (string, *reporter) {
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o.sectionHostFloor(r)
	return buf.String(), r
}

// TestCheckReportsAMissingFloorEntryAndInstallsNothing: before any launch the row says the entry
// is not in the floor and what installs it, ungraded — and the check itself installs nothing.
func TestCheckReportsAMissingFloorEntryAndInstallsNothing(t *testing.T) {
	o, floor, dist, hand := hostFloorCheckFixture(t, `{}`)
	out, r := runHostFloorSection(o)
	for _, want := range []string{"Host agent floor", "floorcli — not in the floor yet",
		"floorcli: also at " + hand + " — not run by `yolo host`"} {
		if !strings.Contains(out, want) {
			t.Errorf("section lacks %q:\n%s", want, out)
		}
	}
	if r.warned != 0 || r.failed != 0 {
		t.Errorf("a missing entry graded (%d warn, %d fail): the next launch installs it", r.warned, r.failed)
	}
	if len(dist.NpmCalls("install")) != 0 {
		t.Error("`yolo check` installed a program")
	}
	if _, err := os.Stat(floor.Dir); err == nil {
		t.Errorf("`yolo check` created %s", floor.Dir)
	}
}

// TestCheckSendsANewerYolosFloorRecordToTheUpdate: a record a newer yolo wrote is one this yolo
// refuses to install over (hostfloor.Floor.Ensure), so the row names `yolo update` and no longer
// says the first `yolo host -- <bin>` installs it, which that launch refuses to do.
func TestCheckSendsANewerYolosFloorRecordToTheUpdate(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	o, floor, _, _ := hostFloorCheckFixture(t, `{}`)
	rec := filepath.Join(floor.Dir, "records", "floorcli.json")
	if err := os.MkdirAll(filepath.Dir(rec), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rec, []byte(`{"schema": 99, "bin": "floorcli"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _ := runHostFloorSection(o)
	for _, want := range []string{"floorcli — ", rec, "a newer yolo wrote it", "run `yolo update`"} {
		if !strings.Contains(out, want) {
			t.Errorf("section lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "installs it") {
		t.Errorf("the row says a launch installs it, which the launch refuses to do:\n%s", out)
	}
}

// TestCheckReportsAProvisionedEntryItsOtherCopiesAndALeftover.
func TestCheckReportsAProvisionedEntryItsOtherCopiesAndALeftover(t *testing.T) {
	o, floor, _, hand := hostFloorCheckFixture(t, `{}`)
	prog := hostfloor.Program{Pack: "floorpack", Install: o.selectedPacks[0].Decl.InstallContributions()[0]}
	if _, _, err := floor.Ensure(context.Background(), prog); err != nil {
		t.Fatal(err)
	}
	torn := filepath.Join(floor.Dir, "programs", "floorcli", "torn-install")
	if err := os.MkdirAll(torn, 0o700); err != nil {
		t.Fatal(err)
	}
	out, r := runHostFloorSection(o)
	for _, want := range []string{"[PASS] floorcli — yolo's floor copy 1.0.0, installed ",
		floor.Launcher("floorcli"), "floorcli: also at " + hand + " — not run by `yolo host`",
		"[WARN] 1 interrupted install(s) left in the floor", torn, "yolo prune --apply"} {
		if !strings.Contains(out, want) {
			t.Errorf("section lacks %q:\n%s", want, out)
		}
	}
	if r.warned != 1 {
		t.Errorf("warned %d times, want exactly the leftover", r.warned)
	}
}

// TestCheckSaysWhyAProgramHasNoFloorEntry: `host_floor` leaves the pack out, read through the
// section's own default floor (no HostFloor wired), and the row says so and what runs instead.
func TestCheckSaysWhyAProgramHasNoFloorEntry(t *testing.T) {
	o, _, _, hand := hostFloorCheckFixture(t, `{"host_floor": {"floorpack": false}}`)
	o.HostFloor = nil
	out, _ := runHostFloorSection(o)
	if !strings.Contains(out, "floorcli — no floor entry: the user config's `host_floor` leaves pack floorpack out") ||
		!strings.Contains(out, "runs the one on the PATH it is started with, then host_path's folders (here, "+hand+")") {
		t.Errorf("section:\n%s", out)
	}
	// With no floor entry the PATH copy is what runs, so it is not listed as one yolo host does
	// not run.
	if strings.Contains(out, "not run by `yolo host`") {
		t.Errorf("the copy that runs is named as not run:\n%s", out)
	}
	if strings.Contains(out, "still holds a copy") {
		t.Errorf("a deselected entry was reported with nothing in the floor:\n%s", out)
	}
}

// TestCheckNamesADeselectedEntryOfAProgramWithNoFloorEntry: the floor installed floorcli, then
// `host_floor` left its pack out. The row says there is no floor entry, and one more line says the
// copy still in the prefix is a deselected entry `yolo host` does not run, and what removes it.
func TestCheckNamesADeselectedEntryOfAProgramWithNoFloorEntry(t *testing.T) {
	o, floor, _, _ := hostFloorCheckFixture(t, `{}`)
	prog := hostfloor.Program{Pack: "floorpack", Install: o.selectedPacks[0].Decl.InstallContributions()[0]}
	if _, _, err := floor.Ensure(context.Background(), prog); err != nil {
		t.Fatal(err)
	}
	floor.Include = func(string) bool { return false }
	out, _ := runHostFloorSection(o)
	for _, want := range []string{"floorcli — no floor entry",
		"floorcli: yolo's floor still holds a copy it no longer keeps, which `yolo host` does not run — " +
			"`yolo host apply --assert` removes it"} {
		if !strings.Contains(out, want) {
			t.Errorf("section lacks %q:\n%s", want, out)
		}
	}
}

// TestCheckNamesTheLoaderAProgramLacksAndTheNixLDStep: on a Linux host with no dynamic loader for
// Node's official build (NixOS without nix-ld, a musl system), an npm agent has no floor entry, and
// the row says why — the loader, the two kinds of host, the nix-ld step — and that the copy on the
// PATH runs instead. Ungraded, as every no-floor-entry row is: a fact about this machine.
func TestCheckNamesTheLoaderAProgramLacksAndTheNixLDStep(t *testing.T) {
	o, floor, _, hand := hostFloorCheckFixture(t, `{}`)
	floor.GOOS, floor.Root = "linux", floortest.ResolvedTemp(t)
	out, r := runHostFloorSection(o)
	for _, want := range []string{"floorcli — no floor entry: the floor runs it on Node's official linux-",
		"needs the dynamic loader ", "(a NixOS host without nix-ld, or a musl system)", "programs.nix-ld.enable = true;",
		"runs the one on the PATH it is started with, then host_path's folders (here, " + hand + ")"} {
		if !strings.Contains(out, want) {
			t.Errorf("section lacks %q:\n%s", want, out)
		}
	}
	if r.warned != 0 || r.failed != 0 {
		t.Errorf("a program this machine cannot start graded (%d warn, %d fail)", r.warned, r.failed)
	}
}
