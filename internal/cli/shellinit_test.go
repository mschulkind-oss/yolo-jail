package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostwrap"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// shellInitHome builds the minimal precondition for driving `yolo host apply --shell-init`
// end to end: a throwaway HOME holding an opted-in user config, so applyHost runs its real
// pipeline (creating the wrap dir whose PATH line is about to be appended) before
// runShellInit touches the rc. t.Chdir keeps the repo's own yolo-jail.jsonc out of the
// merged config, exactly as the other host apply tests do.
func shellInitHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	userCfg(t, home, `{"packs": ["claude"], "host_wrappers": true}`)
	return home
}

// hostApplyShellInit drives the real `yolo host apply` dispatch AND flag wiring. Calling
// runShellInit directly would leave the wiring unpinned — a test that pins the callee
// while the call site is unpinned is not a test, and deleting the `--shell-init` case
// from hostApply must fail every test in this file.
func hostApplyShellInit(args ...string) (stdout, stderr string, rc int) {
	var out, errw bytes.Buffer
	rc = hostMain(append([]string{"apply"}, args...), &out, &errw, false, strings.NewReader(""))
	return out.String(), errw.String(), rc
}

// TestHostApplyShellInitAppendsPathLineIdempotently pins the write contract end to end:
// after one asserting run the rc contains the PATH line exactly once AND the user's own
// content is still there (it appends, it never rewrites), and after a SECOND run the line
// is STILL there exactly once. That second half is the interesting one: the idempotency
// guard in runShellInit is the only thing standing between a user's .bashrc and one PATH
// line per apply, and nothing in the suite re-ran the append before this test.
func TestHostApplyShellInitAppendsPathLineIdempotently(t *testing.T) {
	home := shellInitHome(t)
	t.Setenv("SHELL", "/bin/bash")
	rcPath := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rcPath, []byte("# my own aliases\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wrapLine := hostwrap.PathLine(paths.WrapDirUnder(home))

	_, stderr, rc := hostApplyShellInit("--assert", "--shell-init")
	if rc != 0 {
		t.Fatalf("rc = %d, stderr = %q", rc, stderr)
	}
	body, err := os.ReadFile(rcPath)
	if err != nil {
		t.Fatalf("no rc written: %v", err)
	}
	if n := strings.Count(string(body), wrapLine); n != 1 {
		t.Errorf("after one run the PATH line appears %d times, want exactly 1:\n%s", n, body)
	}
	if !strings.Contains(string(body), "# my own aliases") {
		t.Errorf("--shell-init rewrote the rc instead of appending; user content lost:\n%s", body)
	}
	if !strings.Contains(string(body), "# yolo-jail host launch wrappers") {
		t.Errorf("the appended block is not marked as yolo's:\n%s", body)
	}

	// The second run must take the already-references-it branch, not append a second copy.
	stdout, stderr, rc := hostApplyShellInit("--assert", "--shell-init")
	if rc != 0 {
		t.Fatalf("second run rc = %d, stderr = %q", rc, stderr)
	}
	body, err = os.ReadFile(rcPath)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(body), wrapLine); n != 1 {
		t.Errorf("after a second run the PATH line appears %d times, want still exactly 1 — "+
			"the idempotency guard is not guarding:\n%s", n, body)
	}
	if !strings.Contains(stdout, "already references the wrapper dir") {
		t.Errorf("the second run did not report leaving the rc alone:\n%s", stdout)
	}
}

// TestHostApplyShellInitPicksRCFileFromShell pins the $SHELL → rc mapping as behaviour,
// not help text: the run writes the line into exactly the file the code maps the shell to
// and creates no other rc. zsh → ~/.zshrc; bash, sh, and an unset $SHELL all fall to the
// ~/.bashrc default. Absolute SHELL paths exercise the filepath.Base dispatch.
func TestHostApplyShellInitPicksRCFileFromShell(t *testing.T) {
	for _, tc := range []struct {
		shell, wantRC string
	}{
		{"/usr/bin/zsh", ".zshrc"},
		{"/bin/bash", ".bashrc"},
		{"/bin/sh", ".bashrc"},
		{"", ".bashrc"}, // unset $SHELL defaults rather than refusing
	} {
		t.Run("shell="+tc.shell, func(t *testing.T) {
			home := shellInitHome(t)
			t.Setenv("SHELL", tc.shell)
			_, stderr, rc := hostApplyShellInit("--assert", "--shell-init")
			if rc != 0 {
				t.Fatalf("rc = %d, stderr = %q", rc, stderr)
			}
			body, err := os.ReadFile(filepath.Join(home, tc.wantRC))
			if err != nil {
				t.Fatalf("%s not written: %v", tc.wantRC, err)
			}
			if !strings.Contains(string(body), hostwrap.PathLine(paths.WrapDirUnder(home))) {
				t.Errorf("%s lacks the PATH line:\n%s", tc.wantRC, body)
			}
			for _, other := range []string{".zshrc", ".bashrc"} {
				if other == tc.wantRC {
					continue
				}
				if _, err := os.Stat(filepath.Join(home, other)); !os.IsNotExist(err) {
					t.Errorf("--shell-init for SHELL=%q also wrote %s", tc.shell, other)
				}
			}
		})
	}
}

// TestHostApplyShellInitRefusesFish pins the refusal: fish cannot source a POSIX export
// line, so --shell-init fails with the by-hand remedy — naming the REAL wrap dir — and
// never falls through to the ~/.bashrc default, where the line would sit unread.
func TestHostApplyShellInitRefusesFish(t *testing.T) {
	home := shellInitHome(t)
	t.Setenv("SHELL", "/usr/local/bin/fish")
	_, stderr, rc := hostApplyShellInit("--assert", "--shell-init")
	if rc != 1 {
		t.Errorf("fish rc = %d, want 1 (a refusal, not a write in a syntax fish cannot read)", rc)
	}
	if !strings.Contains(stderr, "does not know fish syntax yet") {
		t.Errorf("stderr does not say why fish is refused:\n%s", stderr)
	}
	dir := paths.WrapDirUnder(home)
	if !strings.Contains(stderr, "fish_add_path "+dir) {
		t.Errorf("the remedy does not name the real wrap dir %s:\n%s", dir, stderr)
	}
	for _, rcFile := range []string{".bashrc", ".zshrc"} {
		if _, err := os.Stat(filepath.Join(home, rcFile)); !os.IsNotExist(err) {
			t.Errorf("the fish refusal still wrote %s", rcFile)
		}
	}
}

// TestHostApplyShellInitObserveWritesNothing mirrors TestApplyHostWrappersObserveWritesNothing
// for the rc half of the observe/write split: --shell-init without --assert — and with
// --assert --dry-run, which forces observe — changes NOTHING, not even creating the rc
// file, while still describing the append it would make.
func TestHostApplyShellInitObserveWritesNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		hasRC bool
	}{
		{"bare, no rc yet", []string{"--shell-init"}, false},
		{"bare, existing rc", []string{"--shell-init"}, true},
		{"dry-run beats --assert", []string{"--assert", "--dry-run", "--shell-init"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := shellInitHome(t)
			t.Setenv("SHELL", "/bin/bash")
			rcPath := filepath.Join(home, ".bashrc")
			seed := "# leave me alone\n"
			if tc.hasRC {
				if err := os.WriteFile(rcPath, []byte(seed), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			stdout, stderr, rc := hostApplyShellInit(tc.args...)
			if rc != 0 {
				t.Fatalf("rc = %d, stderr = %q", rc, stderr)
			}
			if !tc.hasRC {
				if _, err := os.Stat(rcPath); !os.IsNotExist(err) {
					t.Error("an observing --shell-init created the rc file")
				}
			} else if body, err := os.ReadFile(rcPath); err != nil || string(body) != seed {
				t.Errorf("an observing --shell-init changed the rc (err=%v):\n%q", err, body)
			}
			if !strings.Contains(stdout, "would append to") {
				t.Errorf("observe did not describe the append it would make:\n%s", stdout)
			}
		})
	}
}
