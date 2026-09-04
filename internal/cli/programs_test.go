package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// programs_test.go covers `yolo programs` — the on-demand spelling of the boot's two
// read-only reports, and OQ-PD4's explicit removal act.
//
// EVERY TEST HERE POINTS THE COMMAND AT A TEMPORARY HOME, and that is not hygiene, it is the
// point: this command's job is to delete installed programs, and the real environment it
// reads (HOME, NPM_CONFIG_PREFIX, GOPATH) is set inside a live jail. programsJail overrides
// all four so a test can never reach the home the test process is running in.

// programsJail stages a temp home with one pack declaring one npm and one native program,
// points every path variable the command reads at it, and returns the home.
func programsJail(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	packRoot := t.TempDir()
	packDir := filepath.Join(packRoot, "toolpack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"toolpack","contributes":[` +
		`{"kind":"program","bin":"declared-npm","via":"npm","package":"declared-pkg"},` +
		`{"kind":"program","bin":"declared-native","via":"installer","url":"https://x.invalid/i.sh"}` +
		`]}`
	if err := os.WriteFile(filepath.Join(packDir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("JAIL_HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("NPM_CONFIG_PREFIX", filepath.Join(home, ".npm-global"))
	t.Setenv("GOPATH", filepath.Join(home, "go"))
	t.Setenv("YOLO_PACK_ROOT", packRoot)
	t.Setenv("YOLO_LSP_NPM_INSTALL", "")
	t.Setenv("YOLO_LSP_GO_INSTALL", "")
	t.Setenv("YOLO_MCP_PRESETS", "[]")
	return home
}

// seedProgramsNpm writes a global npm package with one file and one bin symlink.
func seedProgramsNpm(t *testing.T, home, name string, size int) string {
	t.Helper()
	pkg := filepath.Join(home, ".npm-global", "lib", "node_modules", name)
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "index.js"), make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(home, ".npm-global", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "lib", "node_modules", name, "index.js"),
		filepath.Join(binDir, name)); err != nil {
		t.Fatal(err)
	}
	return pkg
}

// homeSnapshot renders every path under root with its mode and size, for before/after.
func homeSnapshot(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		lines = append(lines, rel+" "+fi.Mode().String())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func runPrograms2(t *testing.T, args ...string) (rc int, stdout, stderr string) {
	t.Helper()
	var out, errw bytes.Buffer
	rc = programsMain(args, &out, &errw, false)
	return rc, out.String(), errw.String()
}

// `ls` READS. It is the report both halves of §10 lack a caller for outside a boot, and a
// report that changed the machine would be the defect self-documenting-cli.md item 1 names.
func TestProgramsLsReportsAndTouchesNothing(t *testing.T) {
	home := programsJail(t)
	seedProgramsNpm(t, home, "leftover-agent", 4096)
	seedProgramsNpm(t, home, "declared-pkg", 128)

	before := homeSnapshot(t, home)
	rc, out, errw := runPrograms2(t, "ls")
	if rc != 0 {
		t.Fatalf("exit = %d (%s)", rc, errw)
	}
	if after := homeSnapshot(t, home); after != before {
		t.Errorf("`programs ls` changed the home:\n--- before\n%s\n--- after\n%s", before, after)
	}
	if !strings.Contains(out, "leftover-agent") {
		t.Errorf("the orphan is missing from the report:\n%s", out)
	}
	if strings.Contains(out, "declared-pkg") {
		t.Errorf("a DECLARED package was reported as an orphan:\n%s", out)
	}
	// Both sections, because `ls` is the on-demand caller for BOTH boot reports and a
	// version that quietly dropped one would look identical on a clean jail.
	for _, want := range []string{"Orphans", "Record"} {
		if !strings.Contains(out, want) {
			t.Errorf("the %q section is missing:\n%s", want, out)
		}
	}
}

// THE DEFAULT IS A DRY RUN. This is the property the whole act turns on: `yolo programs
// remove` with no flag prints the plan and leaves every byte in place.
//
// MUTATION: initialise `apply` to true in programsRemove and this goes red.
func TestProgramsRemoveIsADryRunByDefault(t *testing.T) {
	home := programsJail(t)
	pkg := seedProgramsNpm(t, home, "leftover-agent", 4096)

	before := homeSnapshot(t, home)
	rc, out, errw := runPrograms2(t, "remove")
	if rc != 0 {
		t.Fatalf("exit = %d (%s)", rc, errw)
	}
	if after := homeSnapshot(t, home); after != before {
		t.Errorf("a dry run removed something:\n--- before\n%s\n--- after\n%s", before, after)
	}
	if _, err := os.Stat(pkg); err != nil {
		t.Fatalf("the orphan is gone after a dry run: %v", err)
	}
	if !strings.Contains(out, "Would remove") || !strings.Contains(out, "Nothing was removed") {
		t.Errorf("a dry run must SAY it removed nothing:\n%s", out)
	}
	// It names the paths, not just the program: the announcement is the plan, so what a
	// user reads is exactly what --apply would unlink.
	if !strings.Contains(out, pkg) ||
		!strings.Contains(out, filepath.Join(home, ".npm-global", "bin", "leftover-agent")) {
		t.Errorf("the dry run did not name every path it would unlink:\n%s", out)
	}
}

// --apply is the act, and it goes through the SAME plan.
func TestProgramsRemoveApplyRemovesTheOrphan(t *testing.T) {
	home := programsJail(t)
	orphan := seedProgramsNpm(t, home, "leftover-agent", 4096)
	declared := seedProgramsNpm(t, home, "declared-pkg", 128)

	rc, out, errw := runPrograms2(t, "remove", "--apply")
	if rc != 0 {
		t.Fatalf("exit = %d (%s)", rc, errw)
	}
	if _, err := os.Stat(orphan); err == nil {
		t.Errorf("the orphan survived --apply:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(home, ".npm-global", "bin", "leftover-agent")); err == nil {
		t.Error("the orphan's global-bin symlink survived --apply")
	}
	if _, err := os.Stat(declared); err != nil {
		t.Errorf("--apply removed a DECLARED package: %v", err)
	}
}

// THE SAFETY PROPERTY: a NAME cannot widen the candidate set. Naming a declared program is
// refused, and refused LOUDLY — an empty plan would read as "there was nothing to do".
//
// MUTATION: make selectOrphans synthesise an Orphan for an unmatched name (or drop the
// `unknown` return and its caller's check) and this goes red.
func TestProgramsRemoveRefusesToTouchADeclaredProgram(t *testing.T) {
	home := programsJail(t)
	declared := seedProgramsNpm(t, home, "declared-pkg", 128)

	before := homeSnapshot(t, home)
	rc, _, errw := runPrograms2(t, "remove", "declared-pkg", "--apply")
	if rc != 2 {
		t.Errorf("exit = %d, want 2 — naming a program that is not an orphan is a user "+
			"error, not a no-op", rc)
	}
	if !strings.Contains(errw, "not an orphan") {
		t.Errorf("the refusal does not say why:\n%s", errw)
	}
	if _, err := os.Stat(declared); err != nil {
		t.Fatalf("A DECLARED PROGRAM WAS REMOVED BY NAME: %v", err)
	}
	if after := homeSnapshot(t, home); after != before {
		t.Errorf("the refused act changed the home:\n--- before\n%s\n--- after\n%s", before, after)
	}
}

// A named orphan is removed and its siblings are not — the filter narrows, and only that.
func TestProgramsRemoveByNameSparesTheOtherOrphans(t *testing.T) {
	home := programsJail(t)
	named := seedProgramsNpm(t, home, "leftover-agent", 512)
	other := seedProgramsNpm(t, home, "other-orphan", 512)

	if rc, _, errw := runPrograms2(t, "remove", "leftover-agent", "--apply"); rc != 0 {
		t.Fatalf("exit = %d (%s)", rc, errw)
	}
	if _, err := os.Stat(named); err == nil {
		t.Error("the named orphan survived")
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("an orphan nobody named was removed: %v", err)
	}
}

// Outside a jail the command declines and says where to run it, rather than computing a
// candidate set from declarations it cannot see — which on the host is EVERY declaration, so
// the plan would be "remove everything installed".
//
// MUTATION: delete the YOLO_PACK_ROOT check in programsEnv and this goes red.
func TestProgramsDeclinesWithoutAStagedPackTree(t *testing.T) {
	home := programsJail(t)
	orphan := seedProgramsNpm(t, home, "leftover-agent", 512)
	t.Setenv("YOLO_PACK_ROOT", "")

	before := homeSnapshot(t, home)
	rc, out, _ := runPrograms2(t, "remove", "--apply")
	if rc != 0 {
		t.Errorf("exit = %d, want 0 — not being in a jail is not a failure", rc)
	}
	if !strings.Contains(out, "No staged packs here") {
		t.Errorf("the refusal does not name the reason or the remedy:\n%s", out)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("a host-side --apply removed a program: %v", err)
	}
	if after := homeSnapshot(t, home); after != before {
		t.Error("a declined run changed the home")
	}
}

// THE CALL SITE, through the real dispatcher: registry → runPrograms → the act. Every other
// test in this file calls programsMain directly and would stay green with the command
// unregistered, or registered to something else.
//
// MUTATION: delete the `"programs": runPrograms` entry from dispatch.go's registry and this
// goes red (unknown command, exit 1, the orphan untouched).
func TestProgramsRemoveReachesTheActThroughDispatch(t *testing.T) {
	home := programsJail(t)
	orphan := seedProgramsNpm(t, home, "leftover-agent", 256)

	var rc int
	stdout, stderr := captureStdio(t, func() {
		rc = dispatchNative("programs", []string{"programs", "remove", "--apply"})
	})
	if rc != 0 {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", rc, stdout, stderr)
	}
	if _, err := os.Stat(orphan); err == nil {
		t.Fatalf("`yolo programs remove --apply` did not reach the act through dispatch\n"+
			"stdout: %s", stdout)
	}
}

// Help is a REQUEST: it prints and changes nothing, on every spelling — including
// `remove --help`, the one where getting it wrong deletes files.
func TestProgramsHelpNeverActs(t *testing.T) {
	home := programsJail(t)
	orphan := seedProgramsNpm(t, home, "leftover-agent", 256)

	for _, args := range [][]string{{}, {"--help"}, {"-h"}, {"help"}, {"ls", "--help"},
		{"remove", "--help"}, {"remove", "--apply", "--help"}} {
		rc, out, _ := runPrograms2(t, args...)
		if rc != 0 {
			t.Errorf("`programs %v` exit = %d, want 0", args, rc)
		}
		if !strings.Contains(out, "Usage: yolo programs") {
			t.Errorf("`programs %v` printed no usage:\n%s", args, out)
		}
		if _, err := os.Stat(orphan); err != nil {
			t.Fatalf("`programs %v` REMOVED something: %v", args, err)
		}
	}
}

// An unknown subcommand or flag is exit 2 with the usage, never a silent act.
func TestProgramsRejectsUnknownTokens(t *testing.T) {
	programsJail(t)
	for _, args := range [][]string{{"nope"}, {"remove", "--force"}, {"ls", "extra"}} {
		if rc, _, _ := runPrograms2(t, args...); rc != 2 {
			t.Errorf("`programs %v` exit = %d, want 2", args, rc)
		}
	}
}
