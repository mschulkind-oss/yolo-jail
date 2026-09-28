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

// `yolo host apply --shell-init` is REMOVED (HE-D1, docs/reference/host-agent-environment.md,
// ruled 2026-09-27: "this shell init command apperas to do nothing, and I don't th8ink it's ever
// safe so we shoud reove it"). What this file pins is what is left: the flag refuses, touches
// nothing, and hands over the line; and no text yolo prints offers it any more.

// shellInitHome builds a HOME in which an apply WOULD write, so a refusal that let the apply
// run is visible on disk: an opted-in user config whose pack installs a program, the declared
// binary stubbed so the dependency gate does not refuse first, and a user rc with content of
// its own. t.Chdir keeps the repo's own yolo-jail.jsonc out of the merged config.
func shellInitHome(t *testing.T) (home, rcPath string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	userCfg(t, home, `{"packs": ["claude"], "host_wrappers": true}`)
	stubDeclaredBins(t)
	rcPath = filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rcPath, []byte("# my own aliases\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, rcPath
}

// hostApplyArgs drives the real `yolo host` dispatch and flag parse. Calling refuseShellInit
// directly would leave the parse unpinned: deleting its `--shell-init` case must turn the
// refusal below into a bare "unexpected argument", which the first test tells apart.
func hostApplyArgs(args ...string) (stdout, stderr string, rc int) {
	var out, errw bytes.Buffer
	rc = hostMain(append([]string{"apply"}, args...), &out, &errw, false, strings.NewReader(""))
	return out.String(), errw.String(), rc
}

// TestHostApplyShellInitIsRemovedAndRefuses: every spelling that used to reach the rc write —
// bare (a dry run), with --assert, alongside --revert and alongside a JSON format — now exits 2
// with the removal named and the PATH line handed over, and writes NOTHING: the rc keeps its
// bytes, no second rc appears for another shell, and the apply it rode on never runs (no
// wrapper directory, no rendered settings file).
//
// The "was removed" assertion is what fails if the parse's `--shell-init` case is deleted: the
// default branch refuses too, with exit 2, but says only "unexpected argument".
func TestHostApplyShellInitIsRemovedAndRefuses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		shell string
		args  []string
	}{
		{"bare", "/bin/bash", []string{"--shell-init"}},
		{"asserting", "/bin/bash", []string{"--assert", "--shell-init"}},
		{"asserting under zsh", "/usr/bin/zsh", []string{"--assert", "--shell-init"}},
		{"with a value", "/bin/bash", []string{"--assert", "--shell-init=zsh"}},
		{"beside --revert", "/bin/bash", []string{"--revert", "--assert", "--shell-init"}},
		{"beside --format json", "/bin/bash", []string{"--assert", "--shell-init", "--format", "json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, rcPath := shellInitHome(t)
			t.Setenv("SHELL", tc.shell)

			stdout, stderr, rc := hostApplyArgs(tc.args...)
			if rc != 2 {
				t.Fatalf("rc = %d, want 2 (misuse)\nstdout: %s\nstderr: %s", rc, stdout, stderr)
			}
			if !strings.Contains(stderr, "--shell-init was removed") {
				t.Errorf("the refusal must name the removal, not just an unknown flag:\n%s", stderr)
			}
			if want := hostwrap.PathLine(paths.WrapDirUnder(home)); !strings.Contains(stderr, want) {
				t.Errorf("the refusal must hand over the exact line %q:\n%s", want, stderr)
			}
			if stdout != "" {
				t.Errorf("a refusal prints nothing on stdout; got:\n%s", stdout)
			}
			if body, err := os.ReadFile(rcPath); err != nil || string(body) != "# my own aliases\n" {
				t.Errorf("the refusal changed the user's rc (err=%v):\n%q", err, body)
			}
			for _, other := range []string{".zshrc", ".profile", ".bash_profile"} {
				if _, err := os.Stat(filepath.Join(home, other)); !os.IsNotExist(err) {
					t.Errorf("the refusal created %s", other)
				}
			}
			if _, err := os.Stat(paths.WrapDirUnder(home)); !os.IsNotExist(err) {
				t.Errorf("the refusal ran the apply: %s exists", paths.WrapDirUnder(home))
			}
			if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
				t.Error("the refusal ran the apply: ~/.claude/settings.json was rendered")
			}
		})
	}
}

// TestHostApplyRefusedFormatRunsNothing keeps what the removed flag's last regression test
// pinned, now that the flag is gone: [OQ-RO4]'s refusal of `--format json` at the acting
// posture is SIDE-EFFECT-FREE. It is misuse decided from argv, so it ends the COMMAND, not just
// the render — it once ended only the render, and a later stage appended the PATH line to the
// user's rc behind an exit 2 and an empty stdout (jsonRefusedForPosture). The wrapper directory
// is the instrument: it is what any stage past the refusal would create first.
func TestHostApplyRefusedFormatRunsNothing(t *testing.T) {
	home, rcPath := shellInitHome(t)
	t.Setenv("SHELL", "/bin/bash")

	stdout, stderr, rc := hostApplyArgs("--assert", "--format", "json")
	if rc != 2 {
		t.Fatalf("rc = %d, want 2 (misuse)\nstdout: %s\nstderr: %s", rc, stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("a refused format must leave stdout empty; got:\n%s", stdout)
	}
	if !strings.Contains(stderr, "--format json is the DRY RUN's") {
		t.Errorf("the refusal must say why:\n%s", stderr)
	}
	if body, err := os.ReadFile(rcPath); err != nil || string(body) != "# my own aliases\n" {
		t.Errorf("the refusal changed the user's rc (err=%v):\n%q", err, body)
	}
	if _, err := os.Stat(paths.WrapDirUnder(home)); !os.IsNotExist(err) {
		t.Errorf("the refusal ran the apply: %s exists", paths.WrapDirUnder(home))
	}
}

// TestNothingOffersShellInit: the flag is gone from every text that used to offer it — the
// `yolo host` help, `yolo config-ref`, and the completion notice an apply prints when it
// creates the wrapper directory. The apply is the real one (it writes), so the notice it
// prints is the production line, and it must still carry the PATH line it hands over.
func TestNothingOffersShellInit(t *testing.T) {
	if strings.Contains(hostUsage, "--shell-init") {
		t.Errorf("`yolo host --help` still lists --shell-init:\n%s", hostUsage)
	}
	if ref := Render(false); strings.Contains(ref, "--shell-init") {
		t.Error("`yolo config-ref` still offers --shell-init")
	}

	home, rcPath := shellInitHome(t)
	var out, errw bytes.Buffer
	if rc := applyHost(&out, &errw, false, true, strings.NewReader("")); rc != 0 {
		t.Fatalf("apply rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	if strings.Contains(out.String()+errw.String(), "shell-init") {
		t.Errorf("the apply's completion notice still offers --shell-init:\n%s", out.String())
	}
	if !strings.Contains(out.String(), hostwrap.PathLine(paths.WrapDirUnder(home))) {
		t.Errorf("the apply that created the wrapper dir must print the PATH line:\n%s", out.String())
	}
	if body, err := os.ReadFile(rcPath); err != nil || string(body) != "# my own aliases\n" {
		t.Errorf("an apply changed the user's rc (err=%v):\n%q", err, body)
	}
}
