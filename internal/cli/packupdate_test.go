package cli

import (
	"bytes"
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
	if err := os.MkdirAll(e.LauncherDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, bin := range launchers {
		if err := os.WriteFile(filepath.Join(e.LauncherDir(), bin),
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
