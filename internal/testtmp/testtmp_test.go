package testtmp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
