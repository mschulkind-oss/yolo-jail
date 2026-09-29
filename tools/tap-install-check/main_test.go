package main

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const testFormula = "mschulkind-oss/tap/yolo-jail"

// stubMachine is a Homebrew prefix on disk the way `brew install` leaves one — a
// Cellar keg holding bin/yolo and share/yolo-jail, reached through prefix/bin,
// prefix/share and prefix/opt symlinks — plus stub `brew` and `yolo` executables
// answering the queries run() makes. The stub yolo's check report is the one the REAL
// check.Check produces under a bare Mac's conditions, and the stub records the
// directory and environment it was run in.
type stubMachine struct {
	root, keg, bundle, log string

	// What the stubs answer; a test edits these before install.
	checkJSON []byte
	checkRC   int
	version   string
	linkedKeg string
}

func newStubMachine(t *testing.T) *stubMachine {
	t.Helper()
	root := resolvedTempDir(t)
	m := &stubMachine{
		root:      root,
		keg:       filepath.Join(root, "prefix", "Cellar", "yolo-jail", "0.11.0"),
		log:       filepath.Join(root, "log"),
		version:   "yolo-jail 0.11.0",
		linkedKeg: `"0.11.0"`,
	}
	m.bundle = filepath.Join(m.keg, "share", "yolo-jail")
	writeFile(t, filepath.Join(m.bundle, "flake.nix"), "{}", 0o644)
	writeFile(t, filepath.Join(m.bundle, "flake.lock"), "{}", 0o644)
	writeFile(t, filepath.Join(m.bundle, "bin/linux-amd64/yolo-entrypoint"), fakeELF(t, elf.EM_X86_64), 0o755)
	writeFile(t, filepath.Join(m.bundle, "bin/linux-arm64/yolo-entrypoint"), fakeELF(t, elf.EM_AARCH64), 0o755)
	writeFile(t, filepath.Join(m.bundle, "bin/darwin-arm64/yolo-jaild"), fakeMachO(t, macho.CpuArm64), 0o755)
	for link, target := range map[string]string{
		"prefix/bin/yolo":        "../Cellar/yolo-jail/0.11.0/bin/yolo",
		"prefix/share/yolo-jail": "../Cellar/yolo-jail/0.11.0/share/yolo-jail",
		"prefix/opt/yolo-jail":   "../Cellar/yolo-jail/0.11.0",
	} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(link)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{m.log, filepath.Join(root, "tmp")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m.checkJSON, m.checkRC = bareMacCheck(t, "0.11.0", m.bundle)
	return m
}

// install writes the stubs and puts them first on PATH, as `brew install` leaves a
// user's shell.
func (m *stubMachine) install(t *testing.T) {
	t.Helper()
	info := `{"formulae":[{"name":"yolo-jail","full_name":"` + testFormula + `",` +
		`"versions":{"stable":"0.11.0","head":null,"bottle":false},"revision":0,` +
		`"installed":[{"version":"0.11.0"}],"linked_keg":` + m.linkedKeg + `}],"casks":[]}`
	writeFile(t, filepath.Join(m.root, "brew.json"), info, 0o644)
	writeFile(t, filepath.Join(m.root, "check.json"), string(m.checkJSON), 0o644)
	writeFile(t, filepath.Join(m.root, "brewbin", "brew"), `#!/bin/sh
case "$1 $2" in
"info --json=v2") cat '`+filepath.Join(m.root, "brew.json")+`' ;;
"--prefix `+testFormula+`") echo '`+filepath.Join(m.root, "prefix", "opt", "yolo-jail")+`' ;;
*) echo "stub brew: $*" >&2; exit 1 ;;
esac
`, 0o755)
	writeFile(t, filepath.Join(m.keg, "bin", "yolo"), `#!/bin/sh
case "$*" in
"--version") echo '`+m.version+`' ;;
"check --no-build --format json")
  pwd -P > '`+filepath.Join(m.log, "pwd")+`'
  ls -A > '`+filepath.Join(m.log, "ls")+`'
  env > '`+filepath.Join(m.log, "env")+`'
  echo 'yolo-jail 0.11.0 | darwin/arm64 | host' >&2
  cat '`+filepath.Join(m.root, "check.json")+`'
  exit `+strconv.Itoa(m.checkRC)+` ;;
*) echo "stub yolo: $*" >&2; exit 2 ;;
esac
`, 0o755)
	sep := string(os.PathListSeparator)
	t.Setenv("PATH", filepath.Join(m.root, "brewbin")+sep+filepath.Join(m.root, "prefix", "bin")+sep+"/bin"+sep+"/usr/bin")
	t.Setenv("TMPDIR", filepath.Join(m.root, "tmp"))
	t.Setenv("GITHUB_ACTIONS", "")
}

func (m *stubMachine) logged(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(m.log, name))
	if err != nil {
		t.Fatalf("the stub yolo never ran `yolo check` (no %s log): %v", name, err)
	}
	return string(b)
}

func runChecker(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	rc := run(args, &out, &errOut)
	return rc, out.String() + errOut.String()
}

// withoutFlakeLine is the real report with its "flake.nix found" finding removed: what
// a binary that cannot find its bundle would print.
func withoutFlakeLine(t *testing.T, report []byte) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(report, &doc); err != nil {
		t.Fatal(err)
	}
	var kept []any
	for _, f := range doc["findings"].([]any) {
		if !strings.HasPrefix(f.(map[string]any)["message"].(string), "flake.nix found: ") {
			kept = append(kept, f)
		}
	}
	doc["findings"] = kept
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestRunPassesAGoodInstall drives main.go end to end over the stub machine: every
// query it makes, in the environment and directory it makes them in.
func TestRunPassesAGoodInstall(t *testing.T) {
	m := newStubMachine(t)
	m.install(t)
	// A developer's (or a jail's) YOLO_* must not reach the binary under test.
	t.Setenv("YOLO_REPO_ROOT", m.root)

	rc, out := runChecker(t, "-formula", testFormula, "-expect-version", "v0.11.0")
	if rc != 0 {
		t.Fatalf("run = %d over a good install:\n%s", rc, out)
	}
	for _, want := range []string{
		"ok    " + testFormula + " is installed and linked at 0.11.0",
		"ok    the tap carries release 0.11.0",
		"ok    `yolo --version` prints \"yolo-jail 0.11.0\"",
		"found the flake bundle beside the binary, at " + m.bundle,
		"ok    the bundle holds flake.nix, flake.lock and prebuilt executables of the right format",
		"(expected: this runner has no container runtime and no Nix)",
		"The tap install works.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}

	// `yolo check` ran in a fresh, empty directory under TMPDIR, never the checkout,
	// with no YOLO_* in its environment, and the directory is gone afterwards.
	ws := strings.TrimSpace(m.logged(t, "pwd"))
	if filepath.Dir(ws) != filepath.Join(m.root, "tmp") || !strings.HasPrefix(filepath.Base(ws), "tap-install-check-workspace-") {
		t.Errorf("`yolo check` ran in %s, want a fresh directory under TMPDIR", ws)
	}
	if ls := strings.TrimSpace(m.logged(t, "ls")); ls != "" {
		t.Errorf("`yolo check`'s workspace was not empty: %q", ls)
	}
	for _, kv := range strings.Split(m.logged(t, "env"), "\n") {
		if strings.HasPrefix(kv, "YOLO_") {
			t.Errorf("the binary under test inherited %s", kv)
		}
	}
	if left, _ := os.ReadDir(filepath.Join(m.root, "tmp")); len(left) != 0 {
		t.Errorf("run left %v in TMPDIR", left)
	}
}

// TestRunFailsEachBrokenInstall breaks the stub machine one way at a time. Each case
// is the reason one of run()'s calls into judge.go exists, so deleting that call
// turns the case green-when-it-should-be-red and fails it here.
func TestRunFailsEachBrokenInstall(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before func(t *testing.T, m *stubMachine) // before the stubs are written
		after  func(t *testing.T, m *stubMachine) // after, to reshape the disk
		args   []string
		want   string
	}{
		{
			name: "the release's formula push did not land",
			args: []string{"-expect-version", "0.12.0"},
			want: "the tap carries " + testFormula + " 0.11.0, but this run verifies release 0.12.0",
		},
		{
			name:   "the formula is not linked",
			before: func(t *testing.T, m *stubMachine) { m.linkedKeg = "null" },
			want:   "is not installed and linked",
		},
		{
			name:   "`yolo --version` names another build",
			before: func(t *testing.T, m *stubMachine) { m.version = "yolo-jail 0.11.0+3.gabc1234" },
			want:   "`yolo --version` printed \"yolo-jail 0.11.0+3.gabc1234\"",
		},
		{
			name:   "`yolo check` does not find the bundle",
			before: func(t *testing.T, m *stubMachine) { m.checkJSON = withoutFlakeLine(t, m.checkJSON) },
			want:   "the installed binary did not find its flake bundle",
		},
		{
			name: "the bundle lost its lock file",
			after: func(t *testing.T, m *stubMachine) {
				if err := os.Remove(filepath.Join(m.bundle, "flake.lock")); err != nil {
					t.Fatal(err)
				}
			},
			want: "the bundle has no flake.lock",
		},
		{
			name: "another yolo shadows the keg's on PATH",
			after: func(t *testing.T, m *stubMachine) {
				shadow := filepath.Join(m.root, "shadow")
				writeFile(t, filepath.Join(shadow, "yolo"), "#!/bin/sh\necho 'yolo-jail 0.11.0'\n", 0o755)
				t.Setenv("PATH", shadow+string(os.PathListSeparator)+os.Getenv("PATH"))
			},
			want: "the yolo on PATH is not this install's",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newStubMachine(t)
			if tc.before != nil {
				tc.before(t, m)
			}
			m.install(t)
			if tc.after != nil {
				tc.after(t, m)
			}
			rc, out := runChecker(t, append([]string{"-formula", testFormula}, tc.args...)...)
			if rc != 1 {
				t.Errorf("run = %d, want 1", rc)
			}
			if !strings.Contains(out, "FAIL  ") || !strings.Contains(out, tc.want) {
				t.Errorf("the report does not FAIL with %q:\n%s", tc.want, out)
			}
		})
	}
}

// TestRunReadsTheExpectationFromTheEvent pins the workflow's mode: with
// -expect-from-github-event the expected version comes from the payload, so a
// release the tap does not carry fails the run.
func TestRunReadsTheExpectationFromTheEvent(t *testing.T) {
	m := newStubMachine(t)
	m.install(t)
	payload := filepath.Join(m.root, "event.json")
	writeFile(t, payload, `{"workflow_run":{"name":"Release","event":"push","head_branch":"v0.12.0","conclusion":"success"}}`, 0o644)
	t.Setenv("GITHUB_EVENT_NAME", "workflow_run")
	t.Setenv("GITHUB_EVENT_PATH", payload)

	rc, out := runChecker(t, "-formula", testFormula, "-expect-from-github-event")
	if rc != 1 || !strings.Contains(out, "but this run verifies release 0.12.0") {
		t.Errorf("run = %d, want 1 naming the release the tap does not carry:\n%s", rc, out)
	}

	writeFile(t, payload, `{"workflow_run":{"name":"Release","event":"push","head_branch":"v0.11.0","conclusion":"success"}}`, 0o644)
	if rc, out := runChecker(t, "-formula", testFormula, "-expect-from-github-event"); rc != 0 {
		t.Errorf("run = %d for the release the tap carries:\n%s", rc, out)
	}
}

func TestRunRefusesAMalformedInvocation(t *testing.T) {
	t.Setenv("GITHUB_EVENT_NAME", "")
	t.Setenv("GITHUB_EVENT_PATH", "")
	for _, args := range [][]string{
		{},                                 // no formula
		{"-formula", testFormula, "extra"}, // a stray positional
		{"-formula", testFormula, "-expect-version", "latest"},
		{"-formula", testFormula, "-expect-version", "0.11.0", "-expect-from-github-event"},
		{"-formula", testFormula, "-expect-from-github-event"}, // outside GitHub Actions
	} {
		if rc, out := runChecker(t, args...); rc != 2 {
			t.Errorf("run(%q) = %d, want 2:\n%s", args, rc, out)
		}
	}
}
