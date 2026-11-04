package macosuser

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const skipExpansion = `${skip[@]+"${skip[@]}"}`

// Only a file's owner (or root) may change its access list on macOS, and the walk runs as the
// invoking user, so every find that runs `chmod -h +a` must leave out what the sandbox account
// owns: those files were created under the inheriting entries and already carry the grant. The
// exclusion is a find PREDICATE, so it must come before the -exec it filters.
func TestFixPermissionsScriptSkipsWhatTheSandboxAccountOwns(t *testing.T) {
	script := FixPermissionsScript("/Users/Shared/yolo", "", "/tmp/failed")
	if !strings.Contains(script, "/usr/bin/id -u "+SandboxUser) {
		t.Errorf("the walk does not ask whether %s exists:\n%s", SandboxUser, script)
	}
	if !strings.Contains(script, "skip=('!' -user "+shQuote(SandboxUser)+")") {
		t.Errorf("the walk builds no owner exclusion:\n%s", script)
	}
	var execLines []string
	for _, line := range strings.Split(script, "\n") {
		if strings.Contains(line, "-exec") {
			execLines = append(execLines, line)
		}
	}
	if len(execLines) != 1 {
		t.Fatalf("want the one find in grant(), got %d -exec lines:\n%s", len(execLines), script)
	}
	s, e := strings.Index(execLines[0], skipExpansion), strings.Index(execLines[0], "-exec")
	if s < 0 || s > e {
		t.Errorf("the owner exclusion does not come before -exec:\n%s", execLines[0])
	}
	aces := WorkspaceACLAces("")
	for _, want := range []string{
		"grant " + shQuote(aces["dir"]) + " -type d",
		"grant " + shQuote(aces["file_inherit"]) + " -type d",
		"grant " + shQuote(aces["file"]) + " '!' -type d",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("missing walk %q:\n%s", want, script)
		}
	}
}

// The script is bash that parses, so the exclusion's spelling cannot break the whole walk.
func TestFixPermissionsScriptParses(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	cmd := exec.Command(bash, "-n", "-c", FixPermissionsScript("/Users/Shared/yolo", "", "/tmp/failed"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v\n%s", err, out)
	}
}

// One path chmod cannot change must not stop the walk: every walk still runs, the path that
// failed is the one listed, and the script exits non-zero. Run for real against a stub chmod
// that refuses one directory, so a failed walk that ends the script fails here.
func TestFixPermissionsScriptKeepsWalkingAfterAFailure(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "shared")
	locked := filepath.Join(root, "locked")
	for _, d := range []string{locked, filepath.Join(root, "open")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(root, "open", "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(tmp, "chmod.log")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$*\" >>" + shQuote(log) + "\n" +
		"for a; do [ \"$a\" = " + shQuote(locked) + " ] && exit 1; done\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "chmod"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	failed := filepath.Join(tmp, "failed")
	cmd := exec.Command(bash, "-c", FixPermissionsScript(root, "", failed))
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"YOLO_BYPASS_SHIMS=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("the script exited 0 with a path it could not change:\n%s", out)
	}
	logged, _ := os.ReadFile(log)
	aces := WorkspaceACLAces("")
	for _, want := range []string{
		"-h +a " + aces["file_inherit"] + " ",
		"-h +a " + aces["file"] + " " + file,
	} {
		if !strings.Contains(string(logged), want) {
			t.Errorf("a walk did not run after the failure (%q):\n%s\nscript output:\n%s",
				want, logged, out)
		}
	}
	list, _ := os.ReadFile(failed)
	if got := strings.Join(uniqueLines(string(list)), ","); got != locked {
		t.Errorf("failed list = %q, want %q", got, locked)
	}
}

// failedListPath reads the failed-list path out of a FixPermissionsScript.
var failedListLine = regexp.MustCompile(`(?m)^failed='([^']*)'$`)

// When some entries cannot be applied, setup prints the real paths in commands that can be
// pasted, after a blank line: no placeholders, and not a re-run of the walk that failed.
func TestSetupNamesTheFailingPathsWhenTheACLWalkFails(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	d.RunBash = func(script string) int {
		m := failedListLine.FindStringSubmatch(script)
		if m == nil {
			t.Fatalf("no failed list in the script:\n%s", script)
		}
		_ = os.WriteFile(m[1], []byte("/Users/Shared/yolo/p/a\n/Users/Shared/yolo/p/b c\n"+
			"/Users/Shared/yolo/p/a\n"), 0o644)
		return 1
	}
	AccountCreate(d)
	out := buf.String()
	for _, want := range []string{
		"⚠ 2 paths under /Users/Shared/yolo did not get the shared-group ACL.\n\n",
		"  ls -ledO /Users/Shared/yolo/p/a '/Users/Shared/yolo/p/b c'\n",
		"  sudo chown matt /Users/Shared/yolo/p/a '/Users/Shared/yolo/p/b c'\n",
		"  yolo yolo-account share /Users/Shared/yolo\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("ACL warning lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "<file>") || strings.Contains(out, "to see which") {
		t.Errorf("ACL warning still has a placeholder or a re-run to see which:\n%s", out)
	}
}

// A walk that failed with no path listed (find itself stopped) says so and names the re-run.
func TestSetupSaysTheACLWalkStoppedWhenNoPathIsListed(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec)
	d.Out = &buf
	d.RunBash = func(string) int { return 1 }
	AccountCreate(d)
	out := buf.String()
	if !strings.Contains(out, "⚠ The shared-group ACL walk under /Users/Shared/yolo failed "+
		"(its error is above).\n\n  yolo yolo-account share /Users/Shared/yolo\n") {
		t.Errorf("ACL warning:\n%s", out)
	}
}

func uniqueLines(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

// A `find` blocker first on PATH (the guardrails pack's, in a `yolo host --` session that runs
// yolo) must not refuse the walk: both scripts run /usr/bin/find, or the system default PATH's find where that is missing. Measured on a Mac
// on 2026-10-23, where every launch fixture's yolo-account share failed on the blocker.
func TestThePermissionScriptsRunUnderAFindBlocker(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "shared")
	if err := os.MkdirAll(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	blocker := "#!/bin/sh\necho 'find is blocked' >&2\nexit 127\n"
	if err := os.WriteFile(filepath.Join(bin, "find"), []byte(blocker), 0o755); err != nil {
		t.Fatal(err)
	}
	// chmod is stubbed so the test changes no real access list.
	if err := os.WriteFile(filepath.Join(bin, "chmod"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{
		"fix":   FixPermissionsScript(root, "", filepath.Join(tmp, "failed")),
		"strip": WorkspaceACLStripScript(root),
	} {
		cmd := exec.Command(bash, "-c", script)
		cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		if out, err := cmd.CombinedOutput(); err != nil || strings.Contains(string(out), "blocked") {
			t.Errorf("%s: the script met the find blocker (%v):\n%s", name, err, out)
		}
	}
}
