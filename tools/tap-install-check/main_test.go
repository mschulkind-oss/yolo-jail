package main

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
	// The stub's --version honors YOLO_VERSION the way the real binary does
	// (internal/version: the variable beats the linker stamp), so a YOLO_VERSION that
	// reached it would show in what it prints.
	//
	// /proc/$$/environ is the environment the stub was exec'd with. `env` is not: a POSIX
	// shell replaces an inherited PWD that does not name its working directory before
	// anything it runs can see it, so only the kernel's copy shows what run() passed.
	// Linux only; TestRunPassesAGoodInstall requires it there.
	writeFile(t, filepath.Join(m.keg, "bin", "yolo"), `#!/bin/sh
case "$*" in
"--version")
  if [ -n "${YOLO_VERSION:-}" ]; then echo "yolo-jail $YOLO_VERSION"; else echo '`+m.version+`'; fi ;;
"check --no-build --format json")
  pwd -P > '`+filepath.Join(m.log, "pwd")+`'
  ls -A > '`+filepath.Join(m.log, "ls")+`'
  env > '`+filepath.Join(m.log, "env")+`'
  if [ -r /proc/$$/environ ]; then cat /proc/$$/environ > '`+filepath.Join(m.log, "environ")+`'; fi
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
	// A developer's (or a jail's) YOLO_* must not reach the binary under test: not
	// YOLO_REPO_ROOT on the check, and not YOLO_VERSION on --version, which the real
	// binary prints verbatim in place of its stamp.
	t.Setenv("YOLO_REPO_ROOT", m.root)
	t.Setenv("YOLO_VERSION", "0.0.0-from-the-environment")
	// The directory the checker was started in, standing in for the checkout: a PWD the
	// check's own must not inherit.
	t.Setenv("PWD", m.root)

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
	// And its PWD, as exec'd, names that directory or nothing — never the checker's own
	// working directory, which os/exec leaves in place once an environment is given.
	if runtime.GOOS == "linux" {
		for _, kv := range strings.Split(m.logged(t, "environ"), "\x00") {
			if pwd, ok := strings.CutPrefix(kv, "PWD="); ok && pwd != ws {
				t.Errorf("`yolo check` ran in %s with PWD=%s", ws, pwd)
			}
		}
	} else {
		t.Logf("no /proc on %s: the PWD `yolo check` was exec'd with is not observable here", runtime.GOOS)
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
			// The real binary prints YOLO_VERSION verbatim in place of its stamp, so a
			// runner (or a jail) carrying the right value would hide a wrong stamp.
			name:   "`yolo --version` names another build, and YOLO_VERSION would hide it",
			before: func(t *testing.T, m *stubMachine) { m.version = "yolo-jail 0.11.0+3.gabc1234" },
			after:  func(t *testing.T, m *stubMachine) { t.Setenv("YOLO_VERSION", "0.11.0") },
			want:   "`yolo --version` printed \"yolo-jail 0.11.0+3.gabc1234\"",
		},
		{
			// A stale opt/ link: brew answers a prefix that is not the keg brew info
			// says is linked.
			name: "`brew --prefix` resolves to another keg",
			after: func(t *testing.T, m *stubMachine) {
				other := filepath.Join(m.root, "prefix", "Cellar", "yolo-jail", "0.10.0")
				if err := os.MkdirAll(filepath.Join(other, "bin"), 0o755); err != nil {
					t.Fatal(err)
				}
				opt := filepath.Join(m.root, "prefix", "opt", "yolo-jail")
				if err := os.Remove(opt); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../Cellar/yolo-jail/0.10.0", opt); err != nil {
					t.Fatal(err)
				}
			},
			want: filepath.Join("prefix", "Cellar", "yolo-jail", "0.10.0") + ", not the linked keg 0.11.0",
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

// TestRunReadsTrustedMainReleaseDispatch pins the production caller to the
// main-sourced Release metadata: normal releases must hold the tap to the exact
// title version, while only the explicitly labeled Homebrew backfill stays
// versionless. These exercise run() through runChecker, not only the decoder.
func TestRunReadsTrustedMainReleaseDispatch(t *testing.T) {
	m := newStubMachine(t)
	m.install(t)
	payload := filepath.Join(m.root, "event.json")
	t.Setenv("GITHUB_EVENT_NAME", "workflow_run")
	t.Setenv("GITHUB_EVENT_PATH", payload)
	sha := "0123456789abcdef0123456789abcdef01234567"
	run := func(ref, title, conclusion string, includeTitle bool) string {
		workflowRun := map[string]string{
			"name": "Release", "event": "workflow_dispatch", "head_branch": ref,
			"conclusion": conclusion,
		}
		if includeTitle {
			workflowRun["display_title"] = title
		}
		body, err := json.Marshal(map[string]any{"workflow_run": workflowRun})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	for _, tc := range []struct {
		name, payload, want string
		rc                  int
	}{
		{
			name:    "main release mismatch fails against the requested version",
			payload: run("main", "Release v0.12.0 @ "+sha+" / request 123", "success", true),
			rc:      1, want: "but this run verifies release 0.12.0",
		},
		{
			name:    "main release match passes",
			payload: run("main", "Release v0.11.0 @ "+sha+" / request 123", "success", true),
			rc:      0,
		},
		{
			name:    "main prerelease is parsed as its exact version",
			payload: run("main", "Release v0.11.0-rc.1 @ "+sha+" / request 123", "success", true),
			rc:      1, want: "but this run verifies release 0.11.0-rc.1",
		},
		{
			name:    "missing main release title is refused",
			payload: run("main", "", "success", false),
			rc:      2, want: "missing or malformed display_title",
		},
		{
			name:    "malformed main release title is refused",
			payload: run("main", "Release v0.12.0 @ short / request 123", "success", true),
			rc:      2, want: "missing or malformed display_title",
		},
		{
			name:    "failed main release is refused",
			payload: run("main", "Release v0.12.0 @ "+sha+" / request 123", "failure", true),
			rc:      2, want: `concluded "failure"`,
		},
		{
			name:    "normal release metadata on a non-main ref is refused",
			payload: run("release/0.12.0", "Release v0.12.0 @ "+sha+" / request 123", "success", true),
			rc:      2, want: `unexpected ref "release/0.12.0"`,
		},
		{
			name:    "explicitly labeled legacy Homebrew backfill stays versionless",
			payload: run("main", "Homebrew-only v0.11.0", "success", true),
			rc:      0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFile(t, payload, tc.payload, 0o644)
			rc, out := runChecker(t, "-formula", testFormula, "-expect-from-github-event")
			if rc != tc.rc || (tc.want != "" && !strings.Contains(out, tc.want)) {
				t.Errorf("runChecker = %d, want %d containing %q:\n%s", rc, tc.rc, tc.want, out)
			}
		})
	}
}

// Tag-scoped publisher dispatches must reach the production checker with the
// requested version, not silently fall back to checking the tap against itself.
func TestRunReadsTagScopedReleaseDispatch(t *testing.T) {
	m := newStubMachine(t)
	m.install(t)
	payload := filepath.Join(m.root, "event.json")
	t.Setenv("GITHUB_EVENT_NAME", "workflow_run")
	t.Setenv("GITHUB_EVENT_PATH", payload)
	writeFile(t, payload, `{"workflow_run":{"name":"Release","event":"workflow_dispatch","head_branch":"v0.12.0","conclusion":"success"}}`, 0o644)
	if rc, out := runChecker(t, "-formula", testFormula, "-expect-from-github-event"); rc != 1 || !strings.Contains(out, "but this run verifies release 0.12.0") {
		t.Errorf("dispatch must fail for the version the tap does not carry, got %d:\n%s", rc, out)
	}
	writeFile(t, payload, `{"workflow_run":{"name":"Release","event":"workflow_dispatch","head_branch":"v0.11.0","conclusion":"success"}}`, 0o644)
	if rc, out := runChecker(t, "-formula", testFormula, "-expect-from-github-event"); rc != 0 {
		t.Errorf("dispatch must pass for the matching release, got %d:\n%s", rc, out)
	}
}

// TestRunRefusesAMalformedInvocation requires each refusal to be the one its case is
// about, so a case cannot pass on another guard's refusal: the exclusivity case runs
// under a valid GitHub event, where only the exclusivity check stands between the
// flags and a run. PATH names an empty directory, so a refusal that went missing
// reaches no real brew.
func TestRunRefusesAMalformedInvocation(t *testing.T) {
	t.Setenv("PATH", resolvedTempDir(t))
	payload := filepath.Join(resolvedTempDir(t), "event.json")
	writeFile(t, payload, `{"inputs":{"version":"0.11.0"}}`, 0o644)
	validEvent := func(t *testing.T) {
		t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
		t.Setenv("GITHUB_EVENT_PATH", payload)
	}
	noEvent := func(t *testing.T) {
		t.Setenv("GITHUB_EVENT_NAME", "")
		t.Setenv("GITHUB_EVENT_PATH", "")
	}
	for _, tc := range []struct {
		name string
		env  func(t *testing.T)
		args []string
		want string
	}{
		{"no formula", noEvent, nil, "usage: tap-install-check"},
		{"a stray positional", noEvent, []string{"-formula", testFormula, "extra"}, "usage: tap-install-check"},
		{"a version that is not one", noEvent, []string{"-formula", testFormula, "-expect-version", "latest"}, `"latest" is not a release version`},
		{"both expectations, under a valid event", validEvent,
			[]string{"-formula", testFormula, "-expect-version", "0.11.0", "-expect-from-github-event"},
			"-expect-version and -expect-from-github-event are exclusive"},
		{"the event's expectation outside GitHub Actions", noEvent,
			[]string{"-formula", testFormula, "-expect-from-github-event"}, "only a GitHub Actions run sets"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.env(t)
			rc, out := runChecker(t, tc.args...)
			if rc != 2 || !strings.Contains(out, tc.want) {
				t.Errorf("run(%q) = %d, want 2 saying %q:\n%s", tc.args, rc, tc.want, out)
			}
		})
	}
}
