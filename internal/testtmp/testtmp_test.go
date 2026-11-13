package testtmp

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

const helperEnv = "TESTTMP_TEST_HELPER"

// TestIsolateRemovesWhatAChildLeftAndWhatIsReadOnly: a child of the binary inherits the private
// TMPDIR, makes nothing of its own, and exits from inside its test without any cleanup — the
// shape that leaked one TestMain directory per helper re-exec. The parent's release removes
// what the child left, and a read-only tree a test left too.
func TestIsolateRemovesWhatAChildLeftAndWhatIsReadOnly(t *testing.T) {
	if os.Getenv(helperEnv) != "" {
		t.Skip("helper process")
	}
	base := t.TempDir()
	t.Setenv("TMPDIR", base)
	t.Setenv(rootEnv, "")
	release := Isolate()
	root := os.TempDir()
	if filepath.Dir(root) != base || IsolatedRoot() != root {
		t.Fatalf("TMPDIR is %q (root %q), want a fresh directory directly in %q", root, IsolatedRoot(), base)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestIsolateHelperProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper: %v\n%s", err, out)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || entries[0].Name() != "left-by-child" {
		t.Fatalf("the child made its own TMPDIR or left nothing; %s holds %v", root, entries)
	}

	ro := filepath.Join(root, "modcache", "pkg")
	if err := os.MkdirAll(ro, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ro, "f"), nil, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}

	if code := release(0); code != 0 {
		t.Errorf("release(0) = %d, want 0", code)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("the private TMPDIR %s is still there after the release (%v)", root, err)
	}
	if os.TempDir() != base {
		t.Errorf("TMPDIR after the release is %q, want %q back", os.TempDir(), base)
	}
}

// TestIsolateHelperProcess is the child: it isolates as a TestMain would, leaves a file, and
// exits without releasing.
func TestIsolateHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) == "" {
		t.Skip("only runs as a helper process")
	}
	_ = Isolate()
	if err := os.WriteFile(filepath.Join(os.TempDir(), "left-by-child"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

// TestAChildWithAnEnvSpelledFromScratchLeavesNothingWithChildEnv: a test that hands its child
// a CLEAN environment (HOME and PATH, never os.Environ()) drops the private TMPDIR's marker, so
// that child isolated again, in the machine's /tmp, and exited from inside its test before
// removing it — hundreds of directories a day. Wrapped in ChildEnv, the child is a descendant:
// it makes no directory, and what it leaves lands in the parent's, which the release removes.
func TestAChildWithAnEnvSpelledFromScratchLeavesNothingWithChildEnv(t *testing.T) {
	if os.Getenv(helperEnv) != "" {
		t.Skip("helper process")
	}
	base := t.TempDir()
	t.Setenv("TMPDIR", base)
	t.Setenv(rootEnv, "")
	release := Isolate()
	root := os.TempDir()

	cmd := exec.Command(os.Args[0], "-test.run=^TestIsolateHelperProcess$", "-test.count=1")
	cmd.Env = ChildEnv([]string{"PATH=" + os.Getenv("PATH"), helperEnv + "=1"})
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper: %v\n%s", err, out)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || entries[0].Name() != "left-by-child" {
		t.Errorf("the child did not leave its file in the parent's private TMPDIR %s: it holds %v", root, entries)
	}
	if code := release(0); code != 0 {
		t.Errorf("release(0) = %d, want 0", code)
	}
	if left, _ := os.ReadDir(base); len(left) != 0 {
		t.Errorf("the run left %v in its TMPDIR %s", left, base)
	}
}

// TestChildEnvKeepsATMPDIRTheTestChose: Isolate leaves a descendant's chosen TMPDIR alone, so
// ChildEnv does too, and it adds the marker once, and nothing at all in a binary that did not
// isolate.
func TestChildEnvKeepsATMPDIRTheTestChose(t *testing.T) {
	t.Setenv(rootEnv, "")
	if got := ChildEnv([]string{"HOME=/h"}); len(got) != 1 {
		t.Errorf("ChildEnv in a binary that did not isolate = %q, want the env unchanged", got)
	}
	t.Setenv(rootEnv, "/r")
	got := strings.Join(ChildEnv([]string{"HOME=/h", "TMPDIR=/chosen", rootEnv + "=/stale"}), " ")
	if want := "HOME=/h TMPDIR=/chosen " + rootEnv + "=/r"; got != want {
		t.Errorf("ChildEnv = %q, want %q", got, want)
	}
	got = strings.Join(ChildEnv([]string{"HOME=/h"}), " ")
	if want := "HOME=/h " + rootEnv + "=/r TMPDIR=/r"; got != want {
		t.Errorf("ChildEnv = %q, want %q", got, want)
	}
}

// TestTheDirectoryNamesItsSession: the leak gate and the census find a directory a child left in
// the machine's /tmp by the session id in its name (SessionDirPrefix), so Isolate must keep
// spelling it, and short enough for ShortDir.
func TestTheDirectoryNamesItsSession(t *testing.T) {
	base := t.TempDir()
	t.Setenv("TMPDIR", base)
	t.Setenv(rootEnv, "")
	release := Isolate()
	defer release(0)
	sid, err := unix.Getsid(0)
	if err != nil {
		t.Fatal(err)
	}
	tag := strconv.FormatInt(int64(sid), 36)
	tag = strings.Repeat("0", 5-len(tag)) + tag
	name := filepath.Base(IsolatedRoot())
	if !regexp.MustCompile(`^tt[0-9a-z]{9}$`).MatchString(name) || name[2:7] != tag {
		t.Errorf("Isolate made %q, want tt, the session %d as five base-36 digits (%s), and four more", name, sid, tag)
	}
	if n := len("/tmp/" + name); n > MaxShortDir {
		t.Errorf("/tmp/%s is %d bytes, longer than MaxShortDir (%d)", name, n, MaxShortDir)
	}
}

// TestShortDirIsTMPDIROnlyWhenShort: the gate's short TMPDIR is where socket fixtures go, so it
// sees them; a long TMPDIR (macOS's /var/folders) falls back to /tmp.
func TestShortDirIsTMPDIROnlyWhenShort(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp/yt.abcdef")
	if got := ShortDir(); got != "/tmp/yt.abcdef" {
		t.Errorf("ShortDir() = %q under a short TMPDIR, want it", got)
	}
	t.Setenv("TMPDIR", "/var/folders/xy/abcdefghijklmnop/T")
	if got := ShortDir(); got != "/tmp" {
		t.Errorf("ShortDir() = %q under a long TMPDIR, want /tmp", got)
	}
}

// TestNoGoFileMakesATempDirInALiteralTmp: a directory made in a literal /tmp is outside the
// TMPDIR the leak gate (scripts/tmp-leak-gate.py) checks, so whatever it leaks is never seen.
// Spell the base testtmp.ShortDir(). A test whose subject really is the machine's /tmp says so
// with a `testtmp:literal-tmp <reason>` comment on the line above, and removes what it makes.
func TestNoGoFileMakesATempDirInALiteralTmp(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, top := range []string{"cmd", "internal", "integration", "tools", "packs"} {
		err := filepath.WalkDir(filepath.Join(repo, top), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "vendor" || strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || filepath.Dir(path) == filepath.Join(repo, "internal", "testtmp") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			checked++
			lines := strings.Split(string(b), "\n")
			for i, line := range lines {
				if !strings.Contains(line, `MkdirTemp("/tmp"`) && !strings.Contains(line, `CreateTemp("/tmp"`) {
					continue
				}
				if strings.HasPrefix(strings.TrimSpace(line), "//") {
					continue
				}
				if i > 0 && strings.Contains(lines[i-1], "testtmp:literal-tmp ") {
					continue
				}
				rel, _ := filepath.Rel(repo, path)
				t.Errorf("%s:%d makes a temporary directory in a literal /tmp, where the TMPDIR leak gate "+
					"cannot see it: spell the base testtmp.ShortDir()\n\t%s", rel, i+1, strings.TrimSpace(line))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked < 100 {
		t.Fatalf("read only %d Go files under %s; the walk no longer reaches the tree", checked, repo)
	}
}
