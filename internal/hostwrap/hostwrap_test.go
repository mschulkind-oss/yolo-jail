package hostwrap

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestBodyIsThreeLinesAndExecsHost(t *testing.T) {
	body := Body("claude")
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("wrapper body has %d lines, want 3:\n%s", len(lines), body)
	}
	if lines[0] != "#!/usr/bin/env bash" {
		t.Errorf("shebang = %q", lines[0])
	}
	if got, want := lines[2], `exec yolo host -- claude "$@"`; got != want {
		t.Errorf("exec line = %q, want %q", got, want)
	}
	// The wrapper must hold NO environment logic — that lives in `yolo host` alone, and
	// a wrapper that composed anything itself would be the second implementation P4
	// exists to prevent.
	for _, forbidden := range []string{"export ", "AWS_", "CLAUDE_", "eval "} {
		if strings.Contains(body, forbidden) {
			t.Errorf("wrapper body contains %q — it must hold no logic:\n%s", forbidden, body)
		}
	}
}

// TestBodyQuotesTheProgramName: a program name is pack-declared text, so it reaches the
// wrapper body as data. Quoting it keeps a name with a shell metacharacter from becoming
// a command.
func TestBodyQuotesTheProgramName(t *testing.T) {
	body := Body("we;ird")
	if strings.Contains(body, "-- we;ird ") {
		t.Errorf("unquoted program name reached the body:\n%s", body)
	}
}

func packWith(name string, bins ...string) *packload.Pack {
	var contribs []packdecl.Contribution
	for _, b := range bins {
		contribs = append(contribs, packdecl.Contribution{Kind: packdecl.KindProgram, Bin: b, Via: "npm", Package: b})
	}
	return &packload.Pack{
		Name: name,
		Decl: &packdecl.Manifest{Name: name, Contributes: contribs},
	}
}

func TestBinsIsSortedDedupedAndSkipsProgramlessPacks(t *testing.T) {
	got := Bins([]*packload.Pack{
		packWith("pi", "pi"),
		packWith("claude", "claude"),
		packWith("audio"), // a loophole-only pack installs nothing
		packWith("dupe", "claude"),
		nil,
	})
	want := []string{"claude", "pi"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Bins = %q, want %q", got, want)
	}
}

// TestBinsIncludesAnInstallerDeclaredProgram: an installer-declared program gets a host
// wrapper like any other.
//
// It was TestBinsSkipsRefusedInstaller, pinning the other half of the deleted origin gate — a
// fetched pack's curl-piped installer was refused, so advertising a wrapper for a program
// yolo would not install was a wrapper that could only fail. OQ-TP9 (docs/design/trust-paths.md,
// 2026-09-04) deleted the refusal; the program installs, so the wrapper is correct.
//
// Bins reads HonoredInstalls rather than InstallContributions, which is why this test lives
// here at all: it is the assertion that hostwrap and packload agree about what installs.
func TestBinsIncludesAnInstallerDeclaredProgram(t *testing.T) {
	p := &packload.Pack{
		Name: "sketchy",
		Decl: &packdecl.Manifest{Name: "sketchy", Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindProgram, Bin: "sketchy", Via: "installer", URL: "https://acme.test/i.sh"},
		}},
	}
	if got := Bins([]*packload.Pack{p}); !reflect.DeepEqual(got, []string{"sketchy"}) {
		t.Errorf("Bins = %q, want [sketchy] — the installer is honored, so the wrapper is "+
			"advertised for a program that will actually be there", got)
	}
}

func TestGenerateCreatesWrappersAndReportsAdded(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wrap")
	plan, err := Generate(dir, []string{"claude", "pi"})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Changed() {
		t.Error("first Generate reported no change")
	}
	if !reflect.DeepEqual(plan.Added, []string{"claude", "pi"}) {
		t.Errorf("Added = %q", plan.Added)
	}
	for _, bin := range []string{"claude", "pi"} {
		path := filepath.Join(dir, bin)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", bin, err)
		}
		if string(got) != Body(bin) {
			t.Errorf("%s body = %q", bin, got)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
			t.Errorf("%s is not executable (mode %v) — it cannot work on PATH", bin, info.Mode())
		}
	}
}

// TestGenerateIsIdempotent: apply prints the PATH line exactly when it CHANGED the
// directory, so an unchanged re-apply must report no change. Otherwise every apply nags.
func TestGenerateIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wrap")
	if _, err := Generate(dir, []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	plan, err := Generate(dir, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Changed() {
		t.Errorf("second Generate reported a change: %+v", plan)
	}
}

func TestGenerateRemovesStaleWrapperAndRewritesDriftedBody(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wrap")
	if _, err := Generate(dir, []string{"claude", "dropped"}); err != nil {
		t.Fatal(err)
	}
	// Drift one body, the way a yolo upgrade that changed Body would.
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\nexec claude\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := Generate(dir, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Removed, []string{"dropped"}) {
		t.Errorf("Removed = %q, want [dropped]", plan.Removed)
	}
	if !reflect.DeepEqual(plan.Rewritten, []string{"claude"}) {
		t.Errorf("Rewritten = %q, want [claude]", plan.Rewritten)
	}
	if _, err := os.Stat(filepath.Join(dir, "dropped")); !os.IsNotExist(err) {
		t.Error("the dropped pack's wrapper survived")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "claude"))
	if string(got) != Body("claude") {
		t.Errorf("drifted wrapper was not rewritten: %q", got)
	}
}

// TestGenerateAndClearNeverRemoveTheDirectoryItself is the anchor invariant. Prepending
// the dir to PATH means a shell (or a live jail's bind mount) has captured its identity;
// unlinking and recreating it hands back a NEW INODE and silently detaches every one of
// those references. Contents-only, always.
func TestGenerateAndClearNeverRemoveTheDirectoryItself(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wrap")
	if _, err := Generate(dir, []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(dir, []string{"pi"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Clear(dir); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("the wrap directory itself was removed: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Error("the wrap directory was recreated (new inode) — a captured PATH entry would detach")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("Clear left %d entries", len(entries))
	}
}

func TestClearOnAbsentDirIsNotAnError(t *testing.T) {
	plan, err := Clear(filepath.Join(t.TempDir(), "never-made"))
	if err != nil {
		t.Fatalf("Clear on an absent dir: %v", err)
	}
	if plan.Changed() {
		t.Errorf("Clear on an absent dir reported a change: %+v", plan)
	}
}

func TestPathLinePrepends(t *testing.T) {
	line := PathLine("/home/u/.local/share/yolo-jail/bin/wrap")
	if !strings.Contains(line, `"/home/u/.local/share/yolo-jail/bin/wrap:$PATH"`) {
		t.Errorf("PathLine = %q — it must PREPEND, or the real binary wins and the wrapper never runs", line)
	}
}

func TestOnPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	dir := "/home/u/.local/share/yolo-jail/bin/wrap"
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"present", "/bin" + sep + dir + sep + "/usr/bin", true},
		{"absent", "/bin" + sep + "/usr/bin", false},
		{"trailing slash still matches", "/bin" + sep + dir + "/", true},
		{"empty PATH", "", false},
		{"empty entries ignored", sep + sep + dir, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := OnPath(tc.path, dir); got != tc.want {
				t.Errorf("OnPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// mkexec writes an executable file and returns its dir.
func mkexec(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestLookPathSkippingIsTheRecursionGuard is the test the whole wrapper design rests on.
// The wrapper execs `yolo host -- claude`; if `yolo host` resolved "claude" the ordinary
// way it would find the WRAPPER again and fork-bomb. The real binary must win even though
// the wrap dir comes first on PATH.
func TestLookPathSkippingIsTheRecursionGuard(t *testing.T) {
	root := t.TempDir()
	wrapDir := mkexec(t, filepath.Join(root, "state", "bin", "wrap"), "claude")
	realDir := mkexec(t, filepath.Join(root, "local", "bin"), "claude")
	pathEnv := wrapDir + string(os.PathListSeparator) + realDir

	got, err := LookPathSkipping(pathEnv, "claude", []string{filepath.Join(root, "state", "bin")})
	if err != nil {
		t.Fatalf("LookPathSkipping: %v", err)
	}
	if want := filepath.Join(realDir, "claude"); got != want {
		t.Errorf("resolved %q, want %q — the wrapper would exec itself", got, want)
	}
}

// TestLookPathSkippingSkipsWholeSubtree: skipping the bin/ parent must also skip
// bin/wrap, bin/block and bin/launch beneath it.
func TestLookPathSkippingSkipsWholeSubtree(t *testing.T) {
	root := t.TempDir()
	deep := mkexec(t, filepath.Join(root, "state", "bin", "wrap", "nested"), "claude")
	real := mkexec(t, filepath.Join(root, "usr", "bin"), "claude")
	pathEnv := deep + string(os.PathListSeparator) + real
	got, err := LookPathSkipping(pathEnv, "claude", []string{filepath.Join(root, "state", "bin")})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(real, "claude"); got != want {
		t.Errorf("resolved %q, want %q", got, want)
	}
}

// TestLookPathSkippingSiblingPrefixIsNotSkipped: a directory whose name merely starts
// with the skip root's name is a different directory.
func TestLookPathSkippingSiblingPrefixIsNotSkipped(t *testing.T) {
	root := t.TempDir()
	sibling := mkexec(t, filepath.Join(root, "bin-other"), "claude")
	got, err := LookPathSkipping(sibling, "claude", []string{filepath.Join(root, "bin")})
	if err != nil {
		t.Fatalf("a sibling directory was wrongly skipped: %v", err)
	}
	if want := filepath.Join(sibling, "claude"); got != want {
		t.Errorf("resolved %q, want %q", got, want)
	}
}

func TestLookPathSkippingNotFound(t *testing.T) {
	root := t.TempDir()
	wrapDir := mkexec(t, filepath.Join(root, "wrap"), "claude")
	_, err := LookPathSkipping(wrapDir, "claude", []string{wrapDir})
	if err == nil {
		t.Fatal("want an error when the only candidate is inside a skipped directory")
	}
	if !strings.Contains(err.Error(), "claude") {
		t.Errorf("error = %v, want it to name the program", err)
	}
}

func TestLookPathSkippingRejectsNonExecutable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LookPathSkipping(dir, "claude", nil); err == nil {
		t.Error("a non-executable file was accepted")
	}
}

// TestLookPathSkippingHonorsAnExplicitPath: a name containing a separator is a file the
// user named, not a PATH lookup — including one inside the wrap dir, which is how
// `<wrap dir>/claude` stays an addressable surface.
func TestLookPathSkippingHonorsAnExplicitPath(t *testing.T) {
	dir := mkexec(t, filepath.Join(t.TempDir(), "somewhere"), "claude")
	explicit := filepath.Join(dir, "claude")
	got, err := LookPathSkipping("", explicit, []string{dir})
	if err != nil {
		t.Fatalf("explicit path rejected: %v", err)
	}
	if got != explicit {
		t.Errorf("resolved %q, want %q", got, explicit)
	}
}

func TestLookPathSkippingEmptyBin(t *testing.T) {
	if _, err := LookPathSkipping("/bin", "", nil); err == nil {
		t.Error("want an error for an empty command name")
	}
}

// TestBinsSkipsNamesThatAreNotBarePrograms: a wrapper is FILED at filepath.Join(dir, bin),
// so a bin carrying path structure is a traversal vector (a pack declaring
// bin:"../../.bashrc" would have yolo overwrite the user's bashrc on `host apply`). The
// schema refuses such a manifest at pack load; Bins filtering is the writer-side half, so
// no caller can smuggle one through even off a loader that skipped validation.
func TestBinsSkipsNamesThatAreNotBarePrograms(t *testing.T) {
	got := Bins([]*packload.Pack{
		packWith("evil", "ok", "../../pwn", "/abs/pwn", "sub/dir/pwn", "a:b"),
	})
	want := []string{"ok"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Bins = %q, want %q (only the bare program name)", got, want)
	}
}

// TestGenerateRefusesPathTraversalNames: Generate writes filepath.Join(dir, bin) directly,
// so a traversal bin writes an executable OUTSIDE the wrap dir. It refuses loudly rather
// than skipping: the guarded pipeline (Bins → Generate) can never produce such a name, so
// reaching this error means a caller bypassed validation — and the one thing yolo must not
// do then is write the file anyway.
func TestGenerateRefusesPathTraversalNames(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "wrap")
	_, err := Generate(dir, []string{"sub/../../pwn"})
	if err == nil || !strings.Contains(err.Error(), "bare program name") {
		t.Fatalf("err = %v, want a bare-program-name refusal", err)
	}
	if _, statErr := os.Stat(filepath.Join(tmp, "pwn")); statErr == nil {
		t.Error("a traversal bin wrote outside the wrapper directory")
	}
}

// TestLookPathSkippingFallsThroughDeniedCandidates: a PATH entry whose candidate cannot
// run must not stop the search — the shell that launched yolo skipped it silently, so
// resolving it made `yolo host -- claude` exit 126 where a bare `claude` succeeded.
//
// The 0644 case (mode-bit denial) is constructible for any user. The effective-access
// case that motivated canExecute (a 0750 candidate owned by another user) needs a
// non-root euid AND the ability to create foreign-owned files, which a test process has
// nowhere: as root the two checks agree, and as non-root chown is not permitted. The
// fall-through this test pins is the shared behavior of both denial kinds; the
// effective-access half is delegated to access(2) by construction.
func TestLookPathSkippingFallsThroughDeniedCandidates(t *testing.T) {
	denied := t.TempDir()
	ok := t.TempDir()
	if err := os.WriteFile(filepath.Join(denied, "claude"), nil, 0o644); err != nil { // present, NOT executable
		t.Fatal(err)
	}
	good := filepath.Join(ok, "claude")
	if err := os.WriteFile(good, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := LookPathSkipping(denied+string(os.PathListSeparator)+ok, "claude", nil)
	if err != nil {
		t.Fatalf("a denied first candidate must not end the search: %v", err)
	}
	if got != good {
		t.Errorf("resolved %q, want the executable candidate %q", got, good)
	}
}
