package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostfloor_test.go is the REAL-HOST tier of the host agent floor
// (docs/design/host-tool-provisioning.md §8): `yolo host -- <bin>` run on the machine the suite runs
// on, with the floor's own Node fetched from nodejs.org and checked against the digest compiled into
// yolo, the npm that tarball carries, and a real `yolo capture` in a real capture jail. Every test of
// internal/hostfloor serves a fake Node distribution, a fake registry and a fake capture store
// (internal/hostfloor/floortest), so until these the bytes a user's floor runs were run by no test.
//
// THREE CELLS, on the triggers docs/reference/agent-install-in-ci.md assigns by cause:
//
//   - TestHostFloorInstallsAPinnedNpmProgramOnTheRealNode — every push. A pure function of the
//     repository (P1): the Node release and its four digests are compiled in, and the package is
//     the jail npm cell's pinned specimen. It is a cell of its own rather than a re-run of that one
//     because the floor's npm is a different install path — its own interpreter, verified against
//     yolo's digest, and its own npm invocation and prefix (internal/hostfloor/node.go, ensure.go,
//     run.go) — and nothing else on the push path runs it.
//   - TestHostFloorRunsARealCaptureOfAnInstallerFixture — every push. HERMETIC: capture_test.go's
//     installer fixture, its script inside the pack's own tree, so nothing reaches a vendor.
//   - TestHostFloorInstallsTheVendorsRelease — Pack Installs only (requireRealPackInstalls), one
//     subtest per packMatrix row, so `-run '…/^<pack>$'` gives each vendor its own job.
//
// TWO THINGS ABOUT THE HOME. requireJail isolates $HOME, but the isolated home's yolo state dir is a
// link to the run's store, which every test of the run shares — and `yolo host apply --assert`
// removes every floor entry outside its selection. So each test here takes a PRIVATE host-floor
// (privateStateEntries), and the capture cell a private capture store too, so "no capture yet" is a
// fact rather than an ordering accident and nothing is left behind. And every host launch here
// blanks YOLO_VERSION: inside a development jail it is set, and `yolo host` there stands its floor
// aside for the jail's own launchers (config.InJail).

// floorNpmPack is the npm fixture's pack name. The entry names it explicitly (the capture fixture's
// trap: a staged pack is named by its source URL's last segment, which under t.TempDir() is a
// counter), so the floor's record names a pack this file can expect.
const floorNpmPack = "host-floor-npm-fixture"

// floorMinimalPath is the PATH a Waybar widget or a cron job hands a launch: system directories,
// no mise, no ~/.local/bin, no npm prefix. The floor installs and runs its program from it, which is
// the whole point of the floor (§8's first row).
const floorMinimalPath = "PATH=/usr/bin:/bin"

// hostLaunchEnv is what every host launch here runs with: YOLO_VERSION blanked (the header), and the
// extra entries given.
func hostLaunchEnv(extra ...string) runOption {
	return withEnv(append([]string{"YOLO_VERSION="}, extra...)...)
}

// pinnedNpmFloorConfig writes the npm fixture pack — the jail cell's pinned specimen,
// installmechanism_test.go's pinnedNpmPackage — and returns the user config selecting it.
func pinnedNpmFloorConfig(t *testing.T, packDir string) string {
	t.Helper()
	manifest := `{
  "name": "` + floorNpmPack + `",
  "description": "the host floor's npm mechanism, pinned",
  "contributes": [
    {"kind": "program", "bin": "` + pinnedNpmBin + `", "via": "npm", "package": "` + pinnedNpmPackage + `"}
  ]
}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return `{"packs": [{"source": "file://` + packDir + `", "name": "` + floorNpmPack + `"}]}`
}

// floorNodePlatform is Node's own name for this platform, the one its release tarballs carry.
func floorNodePlatform(t *testing.T) string {
	t.Helper()
	arch, ok := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	if !ok {
		t.Skipf("Node publishes no official build the floor uses for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return runtime.GOOS + "-" + arch
}

// readFloorRecord reads the floor's record of bin, the file its launcher was generated from.
func readFloorRecord(t *testing.T, floor, bin string) hostfloor.Record {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(floor, "records", bin+".json"))
	if err != nil {
		t.Fatalf("the floor keeps no record of %s: %v", bin, err)
	}
	var rec hostfloor.Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("the floor's record of %s is not JSON: %v\n%s", bin, err, raw)
	}
	return rec
}

// installPinnedNpmOnTheFloor is the floor's npm cell: the first `yolo host -- cowsay mechanism` on
// a machine whose floor is empty, from a minimal PATH. It fetches the shipped Node for this platform, verifies
// it against the digest compiled into yolo, installs the pinned package with that Node's npm, and
// runs the floor's copy. Every assertion is about bytes this repository chose. It returns the floor.
//
// Shared with the Mac's TestMacosUserHostFloorIsTheHostUsersAlone, which runs it on darwin-arm64
// (the macOS nightly runs this file on darwin-x64), so all four ShippedNodeSHA256 digests are
// checked against nodejs.org's real tarballs by some job.
func installPinnedNpmOnTheFloor(t *testing.T, dir, home string) string {
	t.Helper()
	tarball := "node-v" + hostfloor.ShippedNodeVersion + "-" + floorNodePlatform(t) + ".tar.gz"
	floor := paths.HostFloorDirUnder(home)
	r := runCommand(t, dir, []string{"host", "--", pinnedNpmBin, "mechanism"}, hostLaunchEnv(floorMinimalPath))
	if r.rc != 0 {
		t.Fatalf("the first `yolo host -- %s` failed: rc %d\nstdout:\n%s\nstderr:\n%s",
			pinnedNpmBin, r.rc, r.stdout, r.stderr)
	}
	for _, want := range []string{
		"installing " + pinnedNpmBin + " into yolo's floor",
		// THE DIGEST CHECK, against the real tarball: a ShippedNodeSHA256 entry copied wrong refuses
		// the install here, where the unit tier's own tarball cannot tell.
		"verified " + tarball + " against the digest yolo ships",
		"starting " + pinnedNpmBin + " (yolo's floor copy",
	} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("the first launch does not say %q:\n%s", want, r.stderr)
		}
	}
	if !strings.Contains(r.stdout, "< mechanism >") || !strings.Contains(r.stdout, "(oo)") {
		t.Errorf("the floor's %s did not run (stdout carries no cow):\n%s", pinnedNpmBin, r.stdout)
	}
	// What npm INSTALLED, from npm's own record of it rather than a --version flag (the jail cell's
	// rule): the declaration chose the bytes, not the registry's `latest`.
	pkgs, err := filepath.Glob(filepath.Join(floor, "programs", pinnedNpmBin, "*", "npm", "lib", "node_modules",
		pinnedNpmBin, "package.json"))
	if err != nil || len(pkgs) != 1 {
		t.Fatalf("want one installed %s package.json in the floor, got %v (%v)", pinnedNpmBin, pkgs, err)
	}
	raw, err := os.ReadFile(pkgs[0])
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil || pkg.Version != pinnedNpmVersion {
		t.Errorf("the floor's installed %s is version %q (%v), want exactly %s", pinnedNpmBin, pkg.Version, err,
			pinnedNpmVersion)
	}
	rec := readFloorRecord(t, floor, pinnedNpmBin)
	if rec.Via != "npm" || rec.Declared != pinnedNpmPackage || rec.Pack != floorNpmPack ||
		rec.Node != hostfloor.ShippedNodeVersion || !strings.HasPrefix(pkgs[0], rec.Dir+string(os.PathSeparator)) {
		t.Errorf("the floor's record is %+v; want pack %s's %s via npm on Node %s, installed in the directory "+
			"holding %s", rec, floorNpmPack, pinnedNpmPackage, hostfloor.ShippedNodeVersion, pkgs[0])
	}
	if len(rec.Exec) != 2 || rec.Exec[0] != filepath.Join(floor, "node", "v"+hostfloor.ShippedNodeVersion, "bin", "node") {
		t.Errorf("the floor starts %s with %q, want the floor's own Node by absolute path, then the script", pinnedNpmBin,
			rec.Exec)
	}
	// THE PREFIX IS THE USER'S ALONE (§3, HP-D8): 0700, so another uid — the macos-user sandbox
	// account — can neither read nor replace what this user's host launches run; and its receipts 0600.
	for path, want := range map[string]os.FileMode{floor: 0o700, filepath.Join(floor, "receipts.jsonl"): 0o600} {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: mode %v (%v), want %04o", path, modeOf(fi), err, want)
		}
	}
	// THE LAUNCHER NEEDS NO ENVIRONMENT (`env -i`, a launcher that hands over nothing): bin/<bin> names
	// its interpreter and its program by absolute path. An empty, non-nil Env is exec's `env -i`.
	cmd := exec.Command(filepath.Join(floor, "bin", pinnedNpmBin), "env-i")
	cmd.Env = []string{}
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "< env-i >") {
		t.Errorf("the floor's launcher does not run with an empty environment: %v\n%s", err, out)
	}
	// THE FIRST LAUNCH'S STDOUT IS THE PROGRAM'S ALONE: the floor's npm and Node fetch print on its
	// stderr, since an agent's stdout is routinely parsed. The same launcher with the same argument
	// prints exactly what the launch's stdout carried.
	cmd = exec.Command(filepath.Join(floor, "bin", pinnedNpmBin), "mechanism")
	cmd.Env = []string{}
	if own, err := cmd.Output(); err != nil || string(own) != r.stdout {
		t.Errorf("the first launch's stdout is not the floor's %s alone (%v):\nthe launch's:\n%s\nthe program's:\n%s",
			pinnedNpmBin, err, r.stdout, own)
	}
	return floor
}

// ageFloorRecordCheck moves the floor's record of bin's last evergreen check (hostfloor.Record's
// `checked`) back by age, leaving every other field as the floor wrote it.
func ageFloorRecordCheck(t *testing.T, floor, bin string, age time.Duration) {
	t.Helper()
	if age <= hostfloor.DefaultUpdateInterval {
		t.Fatalf("an age of %s is inside the floor's update interval (%s), which would stop the poll first",
			age, hostfloor.DefaultUpdateInterval)
	}
	path := filepath.Join(floor, "records", bin+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("the floor's record of %s is not JSON: %v\n%s", bin, err, raw)
	}
	if _, ok := rec["checked"]; !ok {
		t.Fatalf("the floor's record of %s has no `checked` field to age:\n%s", bin, raw)
	}
	rec["checked"] = time.Now().Add(-age).UTC().Format(time.RFC3339Nano)
	out, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, fi.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if got := readFloorRecord(t, floor, bin).Checked; time.Since(got) < age-time.Minute {
		t.Fatalf("the floor's record of %s was checked %s ago after aging it, want about %s", bin,
			time.Since(got).Round(time.Second), age)
	}
}

// modeOf is fi's permission bits, 0 for a missing file, for a message.
func modeOf(fi os.FileInfo) os.FileMode {
	if fi == nil {
		return 0
	}
	return fi.Mode().Perm()
}

// TestHostFloorInstallsAPinnedNpmProgramOnTheRealNode is the floor's npm cell (§8's first row): the
// first launch installs on the real Node, a second from the same minimal PATH runs the same copy with
// nothing installed, and `yolo host apply --assert` keeps it.
func TestHostFloorInstallsAPinnedNpmProgramOnTheRealNode(t *testing.T) {
	requireJail(t)
	packHome(t, pinnedNpmFloorConfig(t, t.TempDir()))
	home := os.Getenv("HOME")
	privateStateEntries(t, home, filepath.Base(paths.HostFloorDirUnder(home)))
	dir := t.TempDir()

	floor := installPinnedNpmOnTheFloor(t, dir, home)
	first := readFloorRecord(t, floor, pinnedNpmBin)

	// THE PIN, NOT THE THROTTLE, KEEPS THE SECOND LAUNCH OFF THE REGISTRY. A launch seconds after the
	// install would stop at the hourly update interval (Floor.refresh, internal/hostfloor/ensure.go)
	// before it reached the pinned rule, and pass with that rule deleted. So the record's last check
	// is moved two days back first, past the interval: what is left to keep the launch from polling is
	// the jail launcher's rule that a PINNED package is never polled.
	ageFloorRecordCheck(t, floor, pinnedNpmBin, 48*time.Hour)

	again := runCommand(t, dir, []string{"host", "--", pinnedNpmBin, "again"}, hostLaunchEnv(floorMinimalPath))
	if again.rc != 0 || !strings.Contains(again.stdout, "< again >") {
		t.Fatalf("the second `yolo host -- %s` did not run the floor's copy: rc %d\nstdout:\n%s\nstderr:\n%s",
			pinnedNpmBin, again.rc, again.stdout, again.stderr)
	}
	if !strings.Contains(again.stderr, "starting "+pinnedNpmBin+" (yolo's floor copy") {
		t.Errorf("the second launch does not name the floor's copy:\n%s", again.stderr)
	}
	// A PINNED package is never polled, even once its last check is older than the update interval,
	// so a provisioned entry launches with no network at all.
	for _, never := range []string{"installing " + pinnedNpmBin, "fetching Node", "checking the npm registry"} {
		if strings.Contains(again.stderr, never) {
			t.Errorf("the second launch says %q, so a provisioned, pinned entry was installed or polled again:\n%s",
				never, again.stderr)
		}
	}

	// THE APPLY KEEPS A SELECTED ENTRY: `--assert` removes only what no selected pack delivers.
	a := runCommand(t, dir, []string{"host", "apply", "--assert"}, hostLaunchEnv(floorMinimalPath))
	if a.rc != 0 {
		t.Fatalf("yolo host apply --assert: rc %d\nstdout:\n%s\nstderr:\n%s", a.rc, a.stdout, a.stderr)
	}
	if strings.Contains(a.stdout+a.stderr, pinnedNpmBin+": removed") {
		t.Errorf("the apply removed the floor's one entry, which a selected pack delivers:\n%s%s", a.stdout, a.stderr)
	}
	if after := readFloorRecord(t, floor, pinnedNpmBin); after.Dir != first.Dir {
		t.Errorf("the apply reinstalled %s (%s → %s), where it should have kept the provisioned copy",
			pinnedNpmBin, first.Dir, after.Dir)
	}
	if _, err := os.Stat(filepath.Join(floor, "bin", pinnedNpmBin)); err != nil {
		t.Errorf("the apply left no launcher for %s: %v", pinnedNpmBin, err)
	}
}

// TestHostFloorRunsARealCaptureOfAnInstallerFixture is the floor's installer cell (HP-D7): with no
// capture on the machine, the first `yolo host -- <bin>` runs a real `yolo capture` in a capture
// jail, materializes the entry into the floor with the confined materialize, relocates the
// installer's absolute /home/agent link into the floor, and runs the floor's copy; a second launch,
// from a minimal PATH, runs it with no second capture. capture_test.go's hermetic fixture installs,
// so nothing reaches a vendor.
//
// ON A MAC the floor captures through the macos-user act instead (HP-D2), whose cell is
// TestMacosUserHostFloorMaterializesAFixtureInstallerCapture on the macos-user workflow; this
// fixture's installer URL is a jail path that act cannot read. So here a Mac asserts the act's own
// refusal where it cannot run — no sandbox account, as on the macOS nightly's runners — and skips
// where it can.
func TestHostFloorRunsARealCaptureOfAnInstallerFixture(t *testing.T) {
	requireJail(t)
	pack := t.TempDir()
	if err := os.WriteFile(filepath.Join(pack, "install.sh"), []byte(captureFixtureInstallerScript), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "name": "` + captureFixturePack + `",
  "description": "the host floor's installer mechanism, captured",
  "contributes": [
    {"kind": "program", "bin": "` + captureFixtureBin + `", "via": "installer",
     "url": "file:///ctx/packs/` + captureFixturePack + `/install.sh"}
  ]
}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "`+captureFixturePack+`"}]}`)
	home := os.Getenv("HOME")
	floor := paths.HostFloorDirUnder(home)
	store := paths.CapturesDirUnder(home)
	privateStateEntries(t, home, filepath.Base(floor), filepath.Base(store))
	if have := captureEntryNames(t, store); len(have) != 0 {
		t.Fatalf("precondition: the private capture store already holds %v", have)
	}
	dir := t.TempDir()
	if runtime.GOOS == "darwin" {
		hostFloorCaptureOnAMac(t, dir, store)
		return
	}

	r := runCommand(t, dir, []string{"host", "--", captureFixtureBin}, hostLaunchEnv())
	if r.rc != 0 {
		t.Fatalf("the first `yolo host -- %s` failed: rc %d\nstdout:\n%s\nstderr:\n%s",
			captureFixtureBin, r.rc, r.stdout, r.stderr)
	}
	// Every line of the floor's and of the capture's own is on STDERR, the installer's output with
	// them: the capture serves the launch, whose stdout is the program's.
	for _, want := range []string{
		"no capture of " + captureFixtureBin + " on this machine yet; running `yolo capture " + captureFixtureBin + "`",
		captureFixtureRan + "-INSTALL", // the pack's installer ran, in the capture jail
		"captured " + captureFixtureBin,
		"materialized " + captureFixtureBin + " from capture ",
		"starting " + captureFixtureBin + " (yolo's floor copy",
	} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("the first launch's stderr does not say %q:\nstdout:\n%s\nstderr:\n%s", want, r.stdout, r.stderr)
		}
	}
	// AND STDOUT IS THE FLOOR COPY'S ALONE. An agent's stdout is routinely parsed, and the capture
	// jail's output once reached it ahead of the program's own: the installer's FIXTURE_INODE and
	// -INSTALL lines (run.Options.JailStdout).
	for _, never := range []string{captureFixtureRan + "-INSTALL", "FIXTURE_INODE"} {
		if strings.Contains(r.stdout, never) {
			t.Errorf("the first launch's stdout carries the capture jail's %q, which belongs on its stderr:\n%s",
				never, r.stdout)
		}
	}
	if strings.TrimSpace(r.stdout) != captureFixtureRan {
		t.Errorf("the first launch's stdout is not the floor's %s alone (want the one line %q):\n%s",
			captureFixtureBin, captureFixtureRan, r.stdout)
	}

	added := newCaptureEntries(t, store, nil)
	if len(added) != 1 {
		t.Fatalf("got %d capture entries, want the one this launch's capture admitted: %v", len(added), added)
	}
	entry := filepath.Join(store, "entries", added[0])
	m, err := capture.ReadManifest(entry)
	if err != nil {
		t.Fatal(err)
	}
	// A CAPTURE JAIL'S entry: recorded in the jail's home, for the jail's platform, with the full
	// reference scan that lets it move out of that home (HP-D7).
	if want := "linux/" + runtime.GOARCH; m.Platform != want || m.Home != "/home/agent" || !m.Relocatable {
		t.Errorf("the capture is platform %s, home %s, relocatable=%v (%v); want a relocatable %s capture "+
			"recorded in /home/agent", m.Platform, m.Home, m.Relocatable, m.NotRelocatable, want)
	}
	rec := readFloorRecord(t, floor, captureFixtureBin)
	if rec.Via != "installer" || rec.Capture != added[0] {
		t.Errorf("the floor's record is %+v; want an installer program materialized from capture %s", rec, added[0])
	}
	// THE RELOCATION: the installer linked ~/.local/bin/<bin> to an absolute /home/agent path, which on
	// this host names nothing of the floor's (or, worse, something of someone else's). The floor's copy
	// of that link names the floor's own tree.
	link, err := os.Readlink(rec.Entry)
	if err != nil || !strings.HasPrefix(link, floor+string(os.PathSeparator)) || strings.HasPrefix(link, "/home/agent/") {
		t.Errorf("the floor's ~/.local/bin/%s links to %q (%v), want a path inside the floor %s", captureFixtureBin,
			link, err, floor)
	}

	again := runCommand(t, dir, []string{"host", "--", captureFixtureBin}, hostLaunchEnv(floorMinimalPath))
	if again.rc != 0 || !hasLine(again.stdout, captureFixtureRan) {
		t.Fatalf("the second `yolo host -- %s` did not run the floor's copy: rc %d\nstdout:\n%s\nstderr:\n%s",
			captureFixtureBin, again.rc, again.stdout, again.stderr)
	}
	for _, never := range []string{"yolo capture " + captureFixtureBin, "installing " + captureFixtureBin} {
		if strings.Contains(again.stderr, never) {
			t.Errorf("the second launch says %q, so a provisioned entry was captured or installed again:\n%s",
				never, again.stderr)
		}
	}
	if got := newCaptureEntries(t, store, nil); len(got) != 1 {
		t.Errorf("the second launch changed the capture store: %v", got)
	}
}

// hostFloorCaptureOnAMac is the capture cell on darwin: with no sandbox account the macos-user
// capture act cannot run, so the floor has no entry for the installer program and the launch says
// which step gives it one (macCaptureBlocked, HP-D2), runs nothing of the floor's, and captures
// nothing. With the account, the act can run, and its cell is the Mac-only one named below.
func hostFloorCaptureOnAMac(t *testing.T, dir, store string) {
	t.Helper()
	if runQuiet(5*time.Second, "id", macosuser.SandboxUser) {
		t.Skipf("this Mac has the %s account, so the floor would capture through the macos-user act, whose cell "+
			"is TestMacosUserHostFloorMaterializesAFixtureInstallerCapture (the macos-user workflow): this "+
			"fixture's installer URL is a jail path that act cannot read", macosuser.SandboxUser)
	}
	r := runCommand(t, dir, []string{"host", "--", captureFixtureBin}, hostLaunchEnv())
	if r.rc != 127 {
		t.Errorf("`yolo host -- %s` on a Mac with no sandbox account: rc %d, want 127 (no floor copy and none "+
			"on PATH)\nstdout:\n%s\nstderr:\n%s", captureFixtureBin, r.rc, r.stdout, r.stderr)
	}
	for _, want := range []string{"yolo has no copy of " + captureFixtureBin + " on this Mac", "`yolo macos-setup`"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("the launch does not say %q:\n%s", want, r.stderr)
		}
	}
	if strings.Contains(r.combined(), "running `yolo capture") || strings.Contains(r.stdout, captureFixtureRan) {
		t.Errorf("the launch captured or ran the fixture on a Mac whose capture act cannot run:\n%s", r.combined())
	}
	if got := newCaptureEntries(t, store, nil); len(got) != 0 {
		t.Errorf("the launch admitted capture entries %v on a Mac whose capture act cannot run", got)
	}
}

// hasLine reports whether s has a line that is exactly line, once trimmed.
func hasLine(s, line string) bool {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}

// TestHostFloorInstallsTheVendorsRelease asks the Pack Installs question at the host: does each
// shipped pack's program, at its vendor's current release, install into yolo's floor and run from it
// with `--version`? npm packs install on the floor's Node; installer packs (claude, agy, codex) are
// materialized from a capture the floor makes, or reuses from the run's own store, which a jail
// launch's auto-capture in the same job may have filled ("one package inside and outside", OQ-HP3).
//
// `agent_updates: false`, so a launch neither polls nor runs a program's pre-launch refresh (pi's
// `update --extensions`): AGENTS.md allows `--version` probes only, and the first install is not an
// update, so it is unaffected.
//
// NOT ON A MAC: a vendor install there is OQ-CI7's question (docs/reference/agent-install-in-ci.md),
// open with the maintainer.
func TestHostFloorInstallsTheVendorsRelease(t *testing.T) {
	requireJail(t)
	requireRealPackInstalls(t)
	if runtime.GOOS == "darwin" {
		t.Skip("vendor installs on a Mac are OQ-CI7's open question (docs/reference/agent-install-in-ci.md#oq-ci7)")
	}
	for _, tc := range packMatrix {
		t.Run(tc.pack, func(t *testing.T) {
			requireJail(t)
			if tc.vendorSkipArch != "" && runtime.GOARCH == tc.vendorSkipArch {
				t.Skipf("%s is not installable on %s: %s", tc.binary, runtime.GOARCH, tc.vendorSkipReason)
			}
			kind := shippedInstallKind(t, tc.pack, tc.binary)
			packHome(t, `{"packs": ["`+tc.pack+`"], "agent_updates": false}`)
			home := os.Getenv("HOME")
			floor := paths.HostFloorDirUnder(home)
			privateStateEntries(t, home, filepath.Base(floor))
			r := runCommand(t, t.TempDir(), []string{"host", "--", tc.binary, tc.versionArg}, hostLaunchEnv())
			if r.rc != 0 {
				t.Fatalf("yolo host -- %s %s: rc %d\nstdout:\n%s\nstderr:\n%s", tc.binary, tc.versionArg, r.rc,
					r.stdout, r.stderr)
			}
			if !strings.Contains(r.stderr, "starting "+tc.binary+" (yolo's floor copy") {
				t.Errorf("the launch did not run the floor's copy of %s:\n%s", tc.binary, r.stderr)
			}
			// Logged, not asserted: a vendor chooses which stream its version goes to, and what else it
			// prints. That nothing but the program writes the launch's stdout — no capture jail's output,
			// no npm's — is the hermetic cells' to assert, on a fixture whose output is known.
			t.Logf("%s %s: %s", tc.binary, tc.versionArg, strings.TrimSpace(r.stdout))
			rec := readFloorRecord(t, floor, tc.binary)
			switch kind {
			case "npm":
				if rec.Via != "npm" || rec.Node == "" {
					t.Errorf("the floor's record of %s is %+v, want an npm install on the floor's Node", tc.binary, rec)
				}
			case "native":
				if rec.Via != "installer" || rec.Capture == "" {
					t.Errorf("the floor's record of %s is %+v, want one materialized from a capture", tc.binary, rec)
				}
			}
		})
	}
}

// shippedInstallKind is the recipe the shipped pack declares for bin ("npm", "native"), from its
// own pack.json, so a pack moving between recipes moves what this test expects with it.
func shippedInstallKind(t *testing.T, pack, bin string) string {
	t.Helper()
	for _, p := range packload.Embedded() {
		if p.Name != pack {
			continue
		}
		for _, in := range p.Decl.InstallContributions() {
			if in.Bin == bin {
				return in.Kind
			}
		}
	}
	t.Fatalf("shipped pack %s declares no program %s", pack, bin)
	return ""
}
