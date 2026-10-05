package hostwrap

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// realTemp is t.TempDir() with its symlinks resolved, so a fixture path compared against code
// that resolves them (os.SameFile, os.Executable on Linux) matches on darwin, where t.TempDir()
// is under the /var -> /private/var symlink.
func realTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// stubYolo writes an executable named yolo under a fresh directory and returns its path. Nothing
// runs it except where a test says so.
func stubYolo(t *testing.T) string {
	t.Helper()
	return filepath.Join(mkexec(t, filepath.Join(realTemp(t), "bin"), "yolo"), "yolo")
}

func TestBodyIsThreeLinesAndExecsHost(t *testing.T) {
	body := BodyFor("/opt/homebrew/bin/yolo", "claude")
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("wrapper body has %d lines, want 3:\n%s", len(lines), body)
	}
	if lines[0] != "#!/usr/bin/env bash" {
		t.Errorf("shebang = %q", lines[0])
	}
	// yolo BY PATH: the launcher of an IDE or desktop app hands the wrapper a PATH no rc built,
	// and a bare `yolo` there is exit 127.
	if got, want := lines[2], `exec /opt/homebrew/bin/yolo host -- claude "$@"`; got != want {
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
	body := BodyFor("/usr/local/bin/yolo", "we;ird")
	if strings.Contains(body, "-- we;ird ") {
		t.Errorf("unquoted program name reached the body:\n%s", body)
	}
}

// TestBodyQuotesTheYoloPath: the path is the applier's, and a home or an install prefix may hold
// a space or a quote; the exec line must still name that one file.
func TestBodyQuotesTheYoloPath(t *testing.T) {
	yolo := "/Users/Jo O'Neil/bin/yolo"
	body := BodyFor(yolo, "claude")
	if !strings.Contains(body, "exec "+shquote.Quote(yolo)+" host -- claude") {
		t.Errorf("the yolo path is not shell-quoted:\n%s", body)
	}
}

// TestBodyNamesTheRunningYolo: Body is BodyFor with this process's own spelling.
func TestBodyNamesTheRunningYolo(t *testing.T) {
	if got, want := Body("claude"), BodyFor(Running(os.Getenv("PATH")), "claude"); got != want {
		t.Errorf("Body = %q, want %q", got, want)
	}
}

// TestAWrapperStartsYoloFromAPathWithoutYolo is the defect: an IDE or desktop launcher starts the
// wrapper with the PATH its own launcher was handed, which no shell rc built. The wrapper used to
// exec `yolo` by name and exited 127 ("exec: yolo: not found") there; naming yolo by absolute
// path reaches it from a PATH holding nothing but bash and env.
func TestAWrapperStartsYoloFromAPathWithoutYolo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("wrappers are shell scripts")
	}
	root := realTemp(t)
	// A space in the folder, so the quoting the exec line needs is exercised by a real shell.
	yoloDir := filepath.Join(root, "yolo bin")
	if err := os.MkdirAll(yoloDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yolo := filepath.Join(yoloDir, "yolo")
	argsFile := filepath.Join(root, "args")
	stub := "#!/usr/bin/env bash\nprintf '%s\\n' \"$@\" > " + shquote.Quote(argsFile) + "\n"
	if err := os.WriteFile(yolo, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	// The launcher's PATH: bash and env and nothing else, so no yolo.
	launcherPath := filepath.Join(root, "launcher-path")
	if err := os.MkdirAll(launcherPath, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bash", "env"} {
		real, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("no %s on this machine's PATH: %v", name, err)
		}
		if err := os.Symlink(real, filepath.Join(launcherPath, name)); err != nil {
			t.Fatal(err)
		}
	}

	wrap := filepath.Join(root, "wrap")
	if _, err := Generate(wrap, yolo, []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(wrap, "claude"), "--version")
	cmd.Env = []string{"PATH=" + launcherPath, "HOME=" + root}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the wrapper did not reach yolo from a PATH without it: %v\n%s", err, out)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("the stub yolo never ran: %v", err)
	}
	if want := "host\n--\nclaude\n--version\n"; string(got) != want {
		t.Errorf("yolo got argv %q, want %q", got, want)
	}
}

// TestSpellingPrefersAPathEntryNamingTheSameFile is the Homebrew shape: a stable link on PATH
// (bin/yolo) to a versioned file (Cellar/yolo/1.0/bin/yolo) the next upgrade deletes.
// os.Executable on Linux answers with the versioned file, so baking that would break every
// wrapper at the upgrade; the PATH spelling naming the same file is what gets baked. A yolo
// earlier on PATH that is ANOTHER file does not decide the spelling, and a relative entry is
// skipped.
func TestSpellingPrefersAPathEntryNamingTheSameFile(t *testing.T) {
	root := realTemp(t)
	cellar := mkexec(t, filepath.Join(root, "Cellar", "yolo", "1.0", "bin"), "yolo")
	exe := filepath.Join(cellar, "yolo")
	brewBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(brewBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(brewBin, "yolo")); err != nil {
		t.Fatal(err)
	}
	other := mkexec(t, filepath.Join(root, "other"), "yolo")
	sep := string(os.PathListSeparator)

	if got, want := Spelling(exe, "relative"+sep+other+sep+brewBin), filepath.Join(brewBin, "yolo"); got != want {
		t.Errorf("Spelling = %q, want the PATH link naming the same file, %q", got, want)
	}
	if got := Spelling(exe, other); got != exe {
		t.Errorf("no PATH entry names the running file: Spelling = %q, want the executable %q", got, exe)
	}
	t.Chdir(root)
	if got := Spelling(exe, "bin"); got != exe {
		t.Errorf("a relative PATH entry decided the spelling: %q, want %q", got, exe)
	}
	if got := Spelling(filepath.Join(root, "gone", "yolo"), brewBin); got != "yolo" {
		t.Errorf("an executable that is gone: Spelling = %q, want the bare name", got)
	}
}

// TestRunningSpellsTheRunningFileThroughAPathLink pins Running's call into Spelling, which
// TestSpellingPrefersAPathEntryNamingTheSameFile cannot: that test hands Spelling an exe of its
// own. Here the file is this process's own (os.Executable), and the PATH holds a `yolo` link to
// it, the Homebrew shape; Running must bake the link, not the file it resolves to. It fails if
// Running stops passing the executable and PATH through Spelling.
func TestRunningSpellsTheRunningFileThroughAPathLink(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable cannot answer here: %v", err)
	}
	linkDir := filepath.Join(realTemp(t), "bin")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkDir, "yolo")
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}
	if got := Running(linkDir); got != link {
		t.Errorf("Running = %q, want the PATH link %q to the running file", got, link)
	}
	// With no PATH entry naming the running file, the file itself is the answer.
	if got, want := Running(realTemp(t)), filepath.Clean(exe); got != want {
		t.Errorf("Running over a PATH without it = %q, want the executable %q", got, want)
	}
}

// TestPlanKeepsAnotherSpellingOfTheSameYolo: the launch gate's apply runs from whatever PATH
// started the agent, so a wrapper naming the same yolo through another spelling must not be
// Rewritten — or every launch from the other shell would rewrite the wrappers and say so.
func TestPlanKeepsAnotherSpellingOfTheSameYolo(t *testing.T) {
	real, alias := symlinkedTemp(t)
	mkexec(t, real, "yolo")
	dir := filepath.Join(t.TempDir(), "wrap")
	if _, err := Generate(dir, filepath.Join(alias, "yolo"), []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanFor(dir, filepath.Join(real, "yolo"), []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Changed() {
		t.Errorf("another spelling of the same yolo planned a change: %+v", plan)
	}
	if plan, err = Generate(dir, filepath.Join(real, "yolo"), []string{"claude"}); err != nil || plan.Changed() {
		t.Fatalf("Generate = %+v, %v; want no change", plan, err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "claude"))
	if string(got) != BodyFor(filepath.Join(alias, "yolo"), "claude") {
		t.Errorf("the existing spelling was not kept:\n%s", got)
	}
	// A wrapper added beside it takes the spelling the directory already uses.
	if _, err := Generate(dir, filepath.Join(real, "yolo"), []string{"claude", "pi"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "pi")); string(got) != BodyFor(filepath.Join(alias, "yolo"), "pi") {
		t.Errorf("an added wrapper did not adopt the directory's spelling:\n%s", got)
	}
}

// TestPlanRewritesAWrapperNamingAnotherYolo: a different file is a different yolo, and a named
// yolo that is gone is never the same file, so the next apply repairs the wrapper.
func TestPlanRewritesAWrapperNamingAnotherYolo(t *testing.T) {
	a, b := stubYolo(t), stubYolo(t)
	dir := filepath.Join(t.TempDir(), "wrap")
	if _, err := Generate(dir, a, []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	plan, err := Generate(dir, b, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Rewritten, []string{"claude"}) {
		t.Errorf("Rewritten = %q, want [claude]", plan.Rewritten)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "claude")); string(got) != BodyFor(b, "claude") {
		t.Errorf("the wrapper still names the other yolo:\n%s", got)
	}
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	if plan, _ := PlanFor(dir, a, []string{"claude"}); !reflect.DeepEqual(plan.Rewritten, []string{"claude"}) {
		t.Errorf("a wrapper naming a yolo that is gone: Rewritten = %q, want [claude]", plan.Rewritten)
	}
}

// TestPlanRewritesABodyAnOlderYoloWrote: the bare-`yolo` body every wrapper had before this one
// is out of date, so an apply after the upgrade rewrites it.
func TestPlanRewritesABodyAnOlderYoloWrote(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wrap")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := bodyHeader + `exec yolo host -- claude "$@"` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanFor(dir, stubYolo(t), []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Rewritten, []string{"claude"}) {
		t.Errorf("Rewritten = %q, want [claude]", plan.Rewritten)
	}
}

func TestNamedYoloReadsBackWhatBodyForWrote(t *testing.T) {
	for _, yolo := range []string{"/opt/homebrew/bin/yolo", "/Users/Jo O'Neil/bin/yolo", "yolo"} {
		if got, ok := NamedYolo(BodyFor(yolo, "claude"), "claude"); !ok || got != yolo {
			t.Errorf("NamedYolo(BodyFor(%q)) = %q, %v", yolo, got, ok)
		}
	}
	for name, body := range map[string]string{
		"another script":       "#!/bin/sh\n",
		"another program":      BodyFor("/bin/yolo", "pi"),
		"a hand-edited body":   BodyFor("/bin/yolo", "claude") + "echo hi\n",
		"an empty yolo":        bodyHeader + `exec '' host -- claude "$@"` + "\n",
		"an unbalanced quote":  bodyHeader + `exec '/a b host -- claude "$@"` + "\n",
		"a quote needing none": bodyHeader + `exec '/bin/yolo' host -- claude "$@"` + "\n",
	} {
		if got, ok := NamedYolo(body, "claude"); ok {
			t.Errorf("%s: NamedYolo = %q, true; want false", name, got)
		}
	}
}

func TestRunnable(t *testing.T) {
	ok := stubYolo(t)
	if err := Runnable(ok); err != nil {
		t.Errorf("Runnable(executable) = %v", err)
	}
	if err := Runnable(filepath.Join(filepath.Dir(ok), "gone")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Runnable(missing) = %v, want a not-exist error", err)
	}
	plain := filepath.Join(filepath.Dir(ok), "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Runnable(plain); err == nil {
		t.Error("Runnable(non-executable) = nil")
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
	yolo := stubYolo(t)
	plan, err := Generate(dir, yolo, []string{"claude", "pi"})
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
		if string(got) != BodyFor(yolo, bin) {
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
	yolo := stubYolo(t)
	if _, err := Generate(dir, yolo, []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	plan, err := Generate(dir, yolo, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Changed() {
		t.Errorf("second Generate reported a change: %+v", plan)
	}
}

func TestGenerateRemovesStaleWrapperAndRewritesDriftedBody(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wrap")
	yolo := stubYolo(t)
	if _, err := Generate(dir, yolo, []string{"claude", "dropped"}); err != nil {
		t.Fatal(err)
	}
	// Drift one body, the way a yolo upgrade that changed BodyFor would.
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\nexec claude\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := Generate(dir, yolo, []string{"claude"})
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
	if string(got) != BodyFor(yolo, "claude") {
		t.Errorf("drifted wrapper was not rewritten: %q", got)
	}
}

// TestGenerateAndClearNeverRemoveTheDirectoryItself is the anchor invariant. Prepending
// the dir to PATH means a shell (or a live jail's bind mount) has captured its identity;
// unlinking and recreating it hands back a NEW INODE and silently detaches every one of
// those references. Contents-only, always.
func TestGenerateAndClearNeverRemoveTheDirectoryItself(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wrap")
	yolo := stubYolo(t)
	if _, err := Generate(dir, yolo, []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(dir, yolo, []string{"pi"}); err != nil {
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
// The wrapper execs `<yolo> host -- claude`; if `yolo host` resolved "claude" the ordinary
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
	_, err := Generate(dir, "/usr/local/bin/yolo", []string{"sub/../../pwn"})
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

// symlinkedTemp returns (real, alias): one directory, spelled two ways. It is the macOS
// t.TempDir() shape (/var/folders/… is a symlink to /private/var/folders/…) reproduced on
// any OS, so the darwin PATH-RESOLUTION class fails here rather than only on check-macos.
func symlinkedTemp(t *testing.T) (real, alias string) {
	t.Helper()
	base := t.TempDir()
	real = filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	alias = filepath.Join(base, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	return real, alias
}

// TestOnPathMatchesASymlinkedSpellingOfTheDir: a PATH entry that spells the wrap dir through
// a symlinked parent reaches the wrappers, so it is on PATH. Spelling-only comparison said
// "NOT on PATH" for every macOS home under a symlinked prefix.
func TestOnPathMatchesASymlinkedSpellingOfTheDir(t *testing.T) {
	real, alias := symlinkedTemp(t)
	wrap := filepath.Join(real, "wrap")
	if err := os.MkdirAll(wrap, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasWrap := filepath.Join(alias, "wrap")
	sep := string(os.PathListSeparator)
	if !OnPath("/bin"+sep+aliasWrap, wrap) {
		t.Errorf("PATH names the wrap dir through a symlink (%s) and OnPath said no", aliasWrap)
	}
	if !OnPath("/bin"+sep+wrap, aliasWrap) {
		t.Errorf("the reverse spelling (dir given through the symlink) must match too")
	}
	if OnPath("/bin"+sep+real, wrap) {
		t.Errorf("the wrap dir's PARENT is not the wrap dir")
	}
}

func TestResolveIsFirstMatchWins(t *testing.T) {
	root := t.TempDir()
	first := mkexec(t, filepath.Join(root, "a"), "claude")
	second := mkexec(t, filepath.Join(root, "b"), "claude")
	sep := string(os.PathListSeparator)
	got, ok := Resolve(first+sep+second, "claude")
	if !ok || got != filepath.Join(first, "claude") {
		t.Errorf("Resolve = %q, %v; want the FIRST entry's %s", got, ok, filepath.Join(first, "claude"))
	}
	got, ok = Resolve(second+sep+first, "claude")
	if !ok || got != filepath.Join(second, "claude") {
		t.Errorf("reversed PATH: Resolve = %q, %v; want %s", got, ok, filepath.Join(second, "claude"))
	}
	if _, ok := Resolve(first, "nope"); ok {
		t.Errorf("a name nothing provides must not resolve")
	}
	if _, ok := Resolve(first, filepath.Join(first, "claude")); ok {
		t.Errorf("Resolve is a PATH lookup of a bare name; a path must not resolve")
	}
}

// TestResolveSkipsANonExecutableEarlierEntry is the shell's rule: a same-named file without
// the execute bit does not stop the search.
func TestResolveSkipsANonExecutableEarlierEntry(t *testing.T) {
	root := t.TempDir()
	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plain, "claude"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	real := mkexec(t, filepath.Join(root, "real"), "claude")
	got, ok := Resolve(plain+string(os.PathListSeparator)+real, "claude")
	if !ok || got != filepath.Join(real, "claude") {
		t.Errorf("Resolve = %q, %v; want %s", got, ok, filepath.Join(real, "claude"))
	}
}

// TestPrecedenceNamesTheBinaryThatShadowsAWrapper is the "Prepend, not append" rule observed:
// with the wrap dir APPENDED, a real claude earlier on PATH wins, and the Shadow must name it.
// A wrapper with no competitor wins even from the back.
func TestPrecedenceNamesTheBinaryThatShadowsAWrapper(t *testing.T) {
	root := t.TempDir()
	wrap := filepath.Join(root, "wrap")
	mkexec(t, wrap, "claude")
	mkexec(t, wrap, "pi")
	local := mkexec(t, filepath.Join(root, "local", "bin"), "claude")
	sep := string(os.PathListSeparator)

	wins, shadowed := Precedence(local+sep+wrap, wrap, []string{"claude", "pi"})
	if !reflect.DeepEqual(wins, []string{"pi"}) {
		t.Errorf("wins = %v, want [pi]", wins)
	}
	want := []Shadow{{Bin: "claude", Winner: filepath.Join(local, "claude")}}
	if !reflect.DeepEqual(shadowed, want) {
		t.Errorf("shadowed = %+v, want %+v", shadowed, want)
	}

	// Prepended: both win.
	wins, shadowed = Precedence(wrap+sep+local, wrap, []string{"claude", "pi"})
	if len(shadowed) != 0 || len(wins) != 2 {
		t.Errorf("prepended: wins=%v shadowed=%+v, want both winning", wins, shadowed)
	}
}

// TestPrecedenceComparesFilesNotSpellings is the darwin PATH-RESOLUTION class: PATH names the
// wrap dir through a symlinked parent while dir is the resolved spelling (or the reverse).
// A string comparison calls the wrapper shadowed by ITSELF.
func TestPrecedenceComparesFilesNotSpellings(t *testing.T) {
	real, alias := symlinkedTemp(t)
	wrap := filepath.Join(real, "wrap")
	mkexec(t, wrap, "claude")
	aliasWrap := filepath.Join(alias, "wrap")

	for _, tc := range []struct{ pathDir, dir string }{
		{aliasWrap, wrap},
		{wrap, aliasWrap},
	} {
		wins, shadowed := Precedence(tc.pathDir, tc.dir, []string{"claude"})
		if len(shadowed) != 0 || !reflect.DeepEqual(wins, []string{"claude"}) {
			t.Errorf("PATH=%s dir=%s: wins=%v shadowed=%+v — the wrapper was reported "+
				"shadowed by itself", tc.pathDir, tc.dir, wins, shadowed)
		}
	}
}

// TestPrecedenceReportsAWrapperNothingOnPathReaches: the wrap dir is not on PATH and nothing
// else provides the name — a Shadow with an empty Winner, never a win.
func TestPrecedenceReportsAWrapperNothingOnPathReaches(t *testing.T) {
	root := t.TempDir()
	wrap := filepath.Join(root, "wrap")
	mkexec(t, wrap, "claude")
	wins, shadowed := Precedence(filepath.Join(root, "empty"), wrap, []string{"claude"})
	if len(wins) != 0 || !reflect.DeepEqual(shadowed, []Shadow{{Bin: "claude"}}) {
		t.Errorf("wins=%v shadowed=%+v, want one Shadow with no winner", wins, shadowed)
	}
}
