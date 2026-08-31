package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// packupdate_test.go pins the INSTALL/UPDATE split (docs/design/trust-paths.md §1 row 1).
//
// The ruling is about which ACT may resolve a new npm version, so the thing worth testing
// is not that the refresh works — it is that `install` never reaches it. Those two verbs
// shared one case arm for the whole life of the command and printed identical output, so
// a regression that quietly re-merged them would be invisible from the outside: both would
// still fetch, both would still write the lockfile, and only the silent-update path nobody
// watches would come back.
//
// A note on a global these tests trip: refreshNpmPrograms reads the staged tree through
// entrypoint.LoadJailPacks, which calls packload.TolerateSkew() — a process-wide switch to
// the version-tolerant manifest decoder. That is correct where the refresh runs (in-jail,
// reading a tree a possibly-different-aged host CLI staged), and it is why the refresh is
// gated on YOLO_PACK_ROOT rather than run unconditionally. In this binary it means a test
// ordered after one of these sees tolerant decoding; no internal/cli test depends on the
// strict behaviour today, but a future one asserting "an unknown manifest field is refused"
// would need to stop relying on process order.

// stageNpmPackRoot writes one pack tree carrying the given manifest, plus a launcher dir
// holding an inert executable for each named bin. Returns the Env a refresh would read —
// a jail Env, i.e. one with YOLO_PACK_ROOT set.
func stageNpmPackRoot(t *testing.T, manifest string, launchers ...string) *entrypoint.Env {
	t.Helper()
	home := t.TempDir()
	packRoot := t.TempDir()
	packDir := filepath.Join(packRoot, "acme")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	e := entrypoint.NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	if err := os.MkdirAll(e.LaunchDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, bin := range launchers {
		if err := os.WriteFile(filepath.Join(e.LaunchDir(), bin),
			[]byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// TestInstallAndUpdateAreDifferentActs is the ruling at the dispatch: update refreshes an
// npm-declared program, install never does.
//
// It drives packMain rather than the two functions directly, because "which verb reaches
// the refresh" is the entire claim — calling packUpdate and observing a refresh would
// prove only that packUpdate calls what packUpdate calls.
func TestInstallAndUpdateAreDifferentActs(t *testing.T) {
	// A HOME with no config: LoadPacks finds nothing, so the git/lockfile half is a
	// no-op and the only thing left to observe is the npm half.
	t.Setenv("HOME", t.TempDir())

	calls := 0
	saved := npmRefresh
	npmRefresh = func(pr richtext.Printer, errw io.Writer) int { calls++; return 0 }
	t.Cleanup(func() { npmRefresh = saved })

	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("pack install rc = %d: %s", rc, errw.String())
	}
	if calls != 0 {
		t.Errorf("`pack install` resolved an npm version — install installs what is "+
			"recorded and NEVER asks a registry what is latest (trust-paths.md §1 row 1); "+
			"got %d refresh calls", calls)
	}

	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"update"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("pack update rc = %d: %s", rc, errw.String())
	}
	if calls != 1 {
		t.Errorf("`pack update` must be the act that refreshes an npm-declared program — "+
			"with the launcher's hourly reinstall gone it is the only one left; got %d "+
			"refresh calls", calls)
	}
}

// TestUpdateReachesTheLauncherInUpdateMode is the CALL-SITE pin, and it exists because every
// other test in this file passes with the refresh disconnected from the command.
//
// Two single-point mutations survived the whole unit gate on 2026-08-18, and each is the same
// shape: a test that pins a FUNCTION while the wiring that reaches it is pinned nowhere.
//
//   - `var npmRefresh = func(richtext.Printer, io.Writer) int { return 0 }` — green. The
//     dispatch test above installs its own stub over that variable, and the refresh tests
//     below call refreshNpmPrograms / refreshNpmProgramsFromOS directly, so nothing asserted
//     which function the seam is actually wired to. `yolo pack update` would refresh nothing,
//     print nothing, and exit 0.
//   - dropping `npmLauncherUpdateEnv+"=1"` from execLauncherUpdate — also green, and worse
//     than doing nothing: without the variable the launcher takes its NORMAL path, which
//     ends in `exec`. `yolo pack update` would then START every agent CLI it was asked to
//     refresh, one after another, into the user's terminal.
//
// So this drives packMain end to end against a real launcher script on disk that records the
// environment it was handed. It is the only test here that fails if either seam is cut, and
// the `install` half re-asserts the ruling's split at the production wiring rather than over
// a stub.
func TestUpdateReachesTheLauncherInUpdateMode(t *testing.T) {
	home := t.TempDir()
	packRoot := t.TempDir()
	packDir := filepath.Join(packRoot, "acme")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"),
		[]byte(`{"name":"acme","contributes":[`+
			`{"kind":"program","bin":"npmtool","via":"npm","package":"npmtool"}]}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	// JAIL_HOME as well as HOME: EnvFromOS prefers it, and this jail sets it — left alone,
	// LaunchDir would resolve to the REAL ~/.yolo/bin/launch and the refresh would run
	// whatever agent launcher it found there.
	t.Setenv("HOME", home)
	t.Setenv("JAIL_HOME", home)
	t.Setenv("YOLO_PACK_ROOT", packRoot)

	e := entrypoint.NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	if err := os.MkdirAll(e.LaunchDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(t.TempDir(), "handed-env")
	if err := os.WriteFile(filepath.Join(e.LaunchDir(), "npmtool"),
		[]byte("#!/bin/sh\nprintf '%s' \"${"+npmLauncherUpdateEnv+":-<unset>}\" > "+record+"\n"),
		0o755); err != nil {
		t.Fatal(err)
	}

	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("pack install rc = %d: %s", rc, errw.String())
	}
	if _, err := os.Stat(record); err == nil {
		t.Error("`pack install` ran the npm launcher — install installs what is recorded and " +
			"NEVER asks a registry what is latest (trust-paths.md §1 row 1)")
	}

	out.Reset()
	errw.Reset()
	if rc := packMain([]string{"update"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("pack update rc = %d: %s", rc, errw.String())
	}
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("`pack update` never ran the npm launcher, so nothing resolves a new version "+
			"at all — with the hourly reinstall gone this command is the only act left: %v\n%s",
			err, out.String()+errw.String())
	}
	if string(got) != "1" {
		t.Errorf("the launcher was handed %s=%q, want \"1\". Without it the launcher takes its "+
			"normal path and EXECS the tool, so `yolo pack update` starts every agent CLI it "+
			"was asked to refresh instead of refreshing them", npmLauncherUpdateEnv, got)
	}
}

// TestUpdateReportsAFailedRefresh: the npm half's exit code has to reach the caller, or a
// scripted `yolo pack update && …` would proceed on a jail whose agent never updated.
func TestUpdateReportsAFailedRefresh(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	saved := npmRefresh
	npmRefresh = func(pr richtext.Printer, errw io.Writer) int { return 1 }
	t.Cleanup(func() { npmRefresh = saved })

	var out, errw bytes.Buffer
	if rc := packMain([]string{"update"}, &out, &errw, false, nil); rc == 0 {
		t.Error("a failed npm refresh must fail the update")
	}
}

// TestRefreshRunsOnlyNpmDeclaredLaunchers: the launcher dir also holds native-installer
// launchers and the package-manager ones, and running a native launcher in "update mode"
// would exec the vendor's own updater — or, for pnpm, exec pnpm. The manifest is what says
// which launcher is an npm program, so walking the packs (not the directory) is the
// correctness condition, not a stylistic choice.
func TestRefreshRunsOnlyNpmDeclaredLaunchers(t *testing.T) {
	e := stageNpmPackRoot(t, `{"name":"acme","contributes":[`+
		`{"kind":"program","bin":"npmtool","via":"npm","package":"npmtool"},`+
		`{"kind":"program","bin":"nativetool","via":"installer","url":"https://example/i.sh"},`+
		`{"kind":"requires","bin":"jq"}]}`,
		"npmtool", "nativetool", "pnpm")

	var ran []string
	var out, errw bytes.Buffer
	rc := refreshNpmPrograms(e, richtext.Printer{W: &out}, &errw,
		func(bin, path string) error { ran = append(ran, bin); return nil })

	if rc != 0 {
		t.Fatalf("refresh rc = %d: %s", rc, errw.String())
	}
	if len(ran) != 1 || ran[0] != "npmtool" {
		t.Errorf("only the npm-declared program may be refreshed; ran %v", ran)
	}
	if !strings.Contains(out.String(), "npmtool") {
		t.Errorf("the refresh must say which program it resolved:\n%s", out.String())
	}
}

// TestRefreshReportsALauncherThatFailed is the CONSUMER half of "an update that failed is
// not an update that happened", and it was the unpinned half.
//
// The launcher side of that rule is held by internal/entrypoint's script tests: update
// mode exits non-zero for a failed `npm install` and for a registry that did not answer.
// Nothing held the side that READS that exit code. `TestUpdateReportsAFailedRefresh`
// stubs npmRefresh out wholesale, so it pins packUpdate -> npmRefresh and says nothing
// about run() -> rc; `TestRefreshReportsAProgramWithNoLauncher` covers the launcher that
// is ABSENT, which is a different branch. Deleting the `rc = 1` beside the run error left
// the whole unit gate green (measured 2026-08-18) with the launcher correctly reporting a
// failure nobody read — the silent no-op the split exists to delete, one layer up from
// where it was fixed.
func TestRefreshReportsALauncherThatFailed(t *testing.T) {
	e := stageNpmPackRoot(t, `{"name":"acme","contributes":[`+
		`{"kind":"program","bin":"npmtool","via":"npm","package":"npmtool"}]}`, "npmtool")

	var out, errw bytes.Buffer
	rc := refreshNpmPrograms(e, richtext.Printer{W: &out}, &errw,
		func(bin, path string) error { return errors.New("exit status 1") })

	if rc == 0 {
		t.Error("a launcher that exited non-zero must fail the refresh: `yolo pack update` " +
			"is the ONLY act allowed to resolve a version now, so a scripted " +
			"`yolo pack update && …` that proceeds after a failed resolve leaves the jail " +
			"running the old CLI while reporting success")
	}
	if !strings.Contains(errw.String(), "npmtool") {
		t.Errorf("the failure must name the program — a refresh walks every npm-declared "+
			"program in turn, so 'something failed' does not say which:\n%s", errw.String())
	}
}

// TestRefreshReportsAProgramWithNoLauncher: a declared program whose launcher this jail
// never generated (two packs claiming one bin name; a home older than the declaration)
// has nothing to refresh, and silence there would read as "updated".
func TestRefreshReportsAProgramWithNoLauncher(t *testing.T) {
	e := stageNpmPackRoot(t, `{"name":"acme","contributes":[`+
		`{"kind":"program","bin":"npmtool","via":"npm","package":"npmtool"}]}`)

	var out, errw bytes.Buffer
	rc := refreshNpmPrograms(e, richtext.Printer{W: &out}, &errw,
		func(bin, path string) error {
			t.Errorf("a missing launcher must not be executed: %s", path)
			return nil
		})
	if rc == 0 {
		t.Error("a program with no launcher must fail the refresh, not pass silently")
	}
	if !strings.Contains(errw.String(), "npmtool") {
		t.Errorf("the failure must name the program:\n%s", errw.String())
	}
}

// TestRefreshOnTheHostSaysWhereToRunIt. An npm-declared program is installed inside a
// jail, into that jail's npm prefix, by the launcher the entrypoint generated. On the host
// there is nothing to refresh — and a verb that silently did nothing would leave a user
// believing their agent CLI had just been updated.
func TestRefreshOnTheHostSaysWhereToRunIt(t *testing.T) {
	t.Setenv("YOLO_PACK_ROOT", "") // no staged tree: this is the host

	var out, errw bytes.Buffer
	if rc := refreshNpmProgramsFromOS(richtext.Printer{W: &out}, &errw); rc != 0 {
		t.Fatalf("the host case is not an error: rc = %d, %s", rc, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "jail") {
		t.Errorf("the host case must say WHERE an npm program lives:\n%s", got)
	}
	if !strings.Contains(got, "yolo pack update") {
		t.Errorf("...and name the command to run there:\n%s", got)
	}
}
