package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE MAC'S HOST FLOOR, from a FORK'S BUILD (docs/design/forked-programs-as-packs.md FP-D24): on a
// Mac `yolo host -- <forked bin>` builds the fork's pinned commit for darwin through the macos-user
// fork-build act — the build line run once as the sandbox account under the sealed capture profile,
// in a throwaway staging home, on the darwin floor's tools — and runs the floor's copy of that build,
// relocated out of the staging home. Nothing of it has run on hardware.
//
// HERMETIC, like forkbuild_test.go: the fork's "remote" is a local git repository (git+file://), its
// build writes a marker script that reads a data file BY ABSOLUTE PATH under the build's home, so the
// marker prints only if the floor's materialize rewrote that path out of the staging home. The base's
// installer is never run: the fork supplies the program. No agent starts.
//
// ⚠ Mac-only, behind requireMacosUser: the sandbox account, passwordless sudo and nix. A nested jail,
// and every Linux runner, cannot reach it.
func TestMacosUserHostFloorBuildsAForkFixtureForTheMac(t *testing.T) {
	requireMacosUser(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo, _ := forkFixtureRepo(t)
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "pack.json"), []byte(`{"name":"`+forkFixtureBasePack+
		`","contributes":[{"kind":"program","bin":"`+forkFixtureBin+`","via":"installer",`+
		`"url":"file:///nonexistent/`+forkFixtureBin+`-install.sh"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fork := t.TempDir()
	data := `"$HOME/.local/share/` + forkFixtureBin + `/rev"`
	build := `mkdir -p "$HOME/.local/bin" "$HOME/.local/share/` + forkFixtureBin + `" && cp rev.txt ` + data +
		` && printf '#!/bin/sh\necho ` + forkFixtureMarker + `_$(cat %s)\n' ` + data + ` > "$HOME/.local/bin/` +
		forkFixtureBin + `" && chmod +x "$HOME/.local/bin/` + forkFixtureBin + `"`
	buildJSON := strings.ReplaceAll(strings.ReplaceAll(build, `\`, `\\`), `"`, `\"`)
	if err := os.WriteFile(filepath.Join(fork, "pack.json"), []byte(`{"name":"`+forkFixtureForkPack+
		`","contributes":[{"kind":"program","bin":"`+forkFixtureBin+`","via":"source","fork_of":"`+
		forkFixtureBasePack+`","source":"git+file://`+repo+`?ref=main","build":"`+buildJSON+`",`+
		`"produces":[".local/bin/`+forkFixtureBin+`",".local/share/`+forkFixtureBin+`/rev"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"packs": [{"source": "file://`+base+`", "name": "`+forkFixtureBasePack+`"}, `+
		`{"source": "file://`+fork+`", "name": "`+forkFixtureForkPack+`"}]}`)
	home := os.Getenv("HOME")
	store := paths.CapturesDirUnder(home)
	before := captureEntryNames(t, store)
	t.Cleanup(func() { removeNewCaptureEntries(t, store, before) })
	dir := t.TempDir()

	// FIRST LAUNCH: the pin, one darwin build as the sandbox account, and the floor's copy runs.
	r := runCommand(t, dir, []string{"host", "--", forkFixtureBin}, withEnv("YOLO_VERSION="))
	if r.rc != 0 || !strings.Contains(r.stdout, forkFixtureMarker+"_1") {
		t.Fatalf("yolo host -- %s: rc %d, want the fork's build of rev 1, relocated\nstdout:\n%s\nstderr:\n%s",
			forkFixtureBin, r.rc, r.stdout, r.stderr)
	}
	for _, want := range []string{"pinned fork " + forkFixtureForkPack + "/" + forkFixtureBin + " at ",
		"as the macos-user sandbox account, sealed under Seatbelt", "yolo's floor copy"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("the first launch's stderr lacks %q:\n%s", want, r.stderr)
		}
	}
	floor := paths.HostFloorDirUnder(home)
	raw, err := os.ReadFile(filepath.Join(floor, "records", forkFixtureBin+".json"))
	if err != nil {
		t.Fatalf("the floor kept no record of %s: %v", forkFixtureBin, err)
	}
	var rec struct{ Via, Capture, Revision, Entry string }
	if err := json.Unmarshal(raw, &rec); err != nil || rec.Via != "source" || rec.Capture == "" || rec.Revision == "" {
		t.Fatalf("the floor's record %s is not a fork build's (%v)", raw, err)
	}
	entry := filepath.Join(store, "entries", rec.Capture)
	m, err := capture.ReadManifest(entry)
	if err != nil {
		t.Fatal(err)
	}
	if want := "darwin/" + runtime.GOARCH; m.Platform != want || !m.Relocatable ||
		!strings.HasPrefix(m.Home, "/Users/Shared/yolo-captures/fork-") {
		t.Errorf("the build is %s relocatable=%v under %s, want a relocatable %s build in a fork staging home",
			m.Platform, m.Relocatable, m.Home, want)
	}
	builds, err := entrypoint.ReadBuildReceipts(capture.ReceiptsPath(entry))
	if err != nil || len(builds) != 1 || builds[0].Revision != rec.Revision ||
		builds[0].Platform != "darwin/"+runtime.GOARCH || !strings.Contains(builds[0].Toolchain, "macOS") {
		t.Errorf("the build's receipts are %+v (%v), want one darwin build at %s naming its macOS toolchain",
			builds, err, rec.Revision)
	}
	if script, err := os.ReadFile(rec.Entry); err != nil || strings.Contains(string(script), m.Home) ||
		!strings.Contains(string(script), floor) {
		t.Errorf("the floor's copy %s still names the build's staging home, or not the floor (%v):\n%s",
			rec.Entry, err, script)
	}

	// SECOND LAUNCH: the floor's copy is current, and nothing is built.
	r = runCommand(t, dir, []string{"host", "--", forkFixtureBin}, withEnv("YOLO_VERSION="))
	if r.rc != 0 || !strings.Contains(r.stdout, forkFixtureMarker+"_1") || strings.Contains(r.stderr, "building it") {
		t.Fatalf("second launch: rc %d, want the same build with no build line\nstdout:\n%s\nstderr:\n%s",
			r.rc, r.stdout, r.stderr)
	}
}
