package check

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// ansiRe strips SGR sequences so the golden compares the semantic text.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// fakeExec builds an Exec seam that matches on the joined argv (with a prefix
// fallback so "podman info …" matches "podman info") and returns canned
// results; unmatched calls degrade as "not ran" (the missing-binary branch),
// which is the safe default for a golden fixture.
func fakeExec(cases map[string]ExecResult) func([]string, string, []string, time.Duration) ExecResult {
	return func(argv []string, dir string, env []string, timeout time.Duration) ExecResult {
		key := strings.Join(argv, " ")
		if r, ok := cases[key]; ok {
			return r
		}
		// Prefix match on the first two tokens (so "podman info" etc. match).
		for k, r := range cases {
			if strings.HasPrefix(key, k) {
				return r
			}
		}
		return ExecResult{Ran: false}
	}
}

// baseOptions returns Options wired for a deterministic in-jail no-runtime
// fixture: no binaries on PATH, no subprocesses succeed, a fixed clock, and an
// isolated temp workspace/home so filesystem probes are stable.
func baseOptions(t *testing.T, out *bytes.Buffer) Options {
	t.Helper()
	ws := t.TempDir()
	return Options{
		Build:               false,
		SkipEnsureStorage:   true,
		Version:             "9.9.9-test",
		Now:                 func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		Getenv:              func(k string) string { return "" },
		LookPath:            func(string) (string, bool) { return "", false },
		Exec:                func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: false} },
		Stdout:              out,
		Color:               false,
		IsMacOS:             false,
		Machine:             "x86_64",
		Workspace:           ws,
		RepoRoot:            func() (string, bool) { return "", false },
		PathExists:          func(string) bool { return false },
		BuilderSetupDone:    func() bool { return false },
		BuilderKeyInstalled: func() bool { return false },
		EnsureBuilder:       func(func(string)) (bool, string) { return false, "not set up" },
		BuildImage:          func(string, []any) (string, []string) { return "", nil },
		AccessRW:            func(string) bool { return false },
		NodeGID:             func(string) (int, string, bool) { return 0, "", false },
		InUserGroups:        func(int) bool { return false },
	}
}

// normHome replaces the volatile temp $HOME in the output with a stable token
// so the golden is host-independent.
func normHome(s string) string {
	home, _ := os.UserHomeDir()
	if home != "" {
		s = strings.ReplaceAll(s, home, "$HOME")
	}
	return s
}

// TestCheckNoRuntimeGolden pins the ANSI-stripped full output for the
// no-runtime, not-in-jail, no-repo-root host state.
func TestCheckNoRuntimeGolden(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	// Not in jail; no runtime; repo root unresolved.
	exit := Check(opts)

	got := normHome(stripANSI(out.String()))
	want := noRuntimeGolden
	if got != want {
		t.Errorf("golden mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	// No runtime + no repo root => at least the two FAILs (runtime, repo root),
	// so exit must be 1.
	if exit != 1 {
		t.Errorf("exit = %d, want 1", exit)
	}
}

// TestColorStripsToPlain asserts the golden invariant: the ANSI-styled output,
// stripped of SGR, is byte-identical to the Color=false output. This is what
// lets the golden pin Color=false while the CLI runs Color=true.
func TestColorStripsToPlain(t *testing.T) {
	var plain, colored bytes.Buffer

	// Share one HOME + workspace so the storage-path lines are identical between
	// the two runs; only the Color flag differs.
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()

	mk := func(out *bytes.Buffer, color bool) Options {
		o := baseOptions(t, out)
		o.Workspace = ws
		o.Color = color
		return o
	}
	Check(mk(&plain, false))
	Check(mk(&colored, true))

	if got := stripANSI(colored.String()); got != plain.String() {
		t.Errorf("stripped colored output != plain output\n--- stripped ---\n%s\n--- plain ---\n%s", got, plain.String())
	}
	if !strings.Contains(colored.String(), "\x1b[") {
		t.Error("colored output has no ANSI sequences")
	}
}

// TestExitCodePolarity checks the 0/1 exit contract on a clean fixture (a live
// podman + resolvable repo + valid config => no failures => exit 0 is hard to
// fabricate without a real repo; instead assert the no-fail path returns 0 by
// stubbing every section to pass). We approximate: a fixture with a live
// podman, a repo root with flake.nix + entrypoint, --no-build, in-jail (so the
// in-jail skips dominate) yields 0.
func TestExitCodeCleanInJail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	opts := baseOptions(t, &out)

	// Build a fake repo root with flake.nix + go.mod (the checkout marker).
	repo := t.TempDir()
	must(t, os.WriteFile(filepath.Join(repo, "flake.nix"), []byte("{}"), 0o644))
	must(t, os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module test\n"), 0o644))
	opts.RepoRoot = func() (string, bool) { return repo, true }
	opts.PathExists = func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}
	// In jail: YOLO_VERSION set => most host-side sections skip with PASS.
	opts.Getenv = func(k string) string {
		if k == "YOLO_VERSION" {
			return "0.1.0-test"
		}
		return ""
	}
	// Live podman + nix present.
	opts.LookPath = func(name string) (string, bool) {
		switch name {
		case "podman", "nix", "python3":
			return "/usr/bin/" + name, true
		}
		return "", false
	}
	opts.Exec = fakeExec(map[string]ExecResult{
		"podman --version": {Stdout: "podman version 5.0.0", Ran: true, RC: 0},
		"podman info":      {Stdout: "host: {}", Ran: true, RC: 0},
		"nix --version":    {Stdout: "nix (Nix) 2.30.0", Ran: true, RC: 0},
		"nix config show":  {Stdout: "", Ran: true, RC: 0},
		// Entrypoint dry-run: succeed (exit 0, prints ok).
		"python3":       {Stdout: "ok", Ran: true, RC: 0},
		"podman images": {Stdout: "", Ran: true, RC: 0},
		"podman ps":     {Stdout: "", Ran: true, RC: 0},
	})

	exit := Check(opts)
	got := stripANSI(out.String())
	if strings.Contains(got, "[FAIL]") {
		t.Errorf("unexpected FAIL in clean in-jail run:\n%s", got)
	}
	if exit != 0 {
		t.Errorf("exit = %d, want 0\n%s", exit, got)
	}
}

// TestCheckAccumulatedFailEarlyExit is the regression for the re-audit §C gap:
// when config is VALID (passes the Merged-Configuration validation gate) but a
// prior non-validation failure occurred (repo-root unresolved), Check() must
// short-circuit right after Merged Configuration instead of proceeding into the
// Entrypoint Dry-Run and the Image section's real `nix build`. Before the fix,
// check ran those sections on an unhealthy host.
func TestCheckAccumulatedFailEarlyExit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	opts := baseOptions(t, &out)

	// Valid config path: a live podman so the Merged-Configuration validation
	// produces NO errors (no "runtime not found" fail)...
	opts.LookPath = func(name string) (string, bool) {
		if name == "podman" || name == "nix" {
			return "/usr/bin/" + name, true
		}
		return "", false
	}
	opts.Exec = fakeExec(map[string]ExecResult{
		"podman --version": {Stdout: "podman version 5.0.0", Ran: true, RC: 0},
		"podman info":      {Stdout: "host: {}", Ran: true, RC: 0},
	})
	// ...but repo root is UNRESOLVED → a non-validation FAIL accumulates before
	// the gate. (baseOptions already sets RepoRoot -> ("", false).)

	exit := Check(opts)
	got := stripANSI(out.String())

	if exit != 1 {
		t.Errorf("exit = %d, want 1 (accumulated fail)", exit)
	}
	// The repo-root fail must be present...
	if !strings.Contains(got, "Could not resolve the yolo-jail repo root") {
		t.Errorf("expected the repo-root FAIL in output:\n%s", got)
	}
	// ...and the run must STOP at the Summary right after Merged Configuration —
	// never reaching the Entrypoint Dry-Run or the Image/nix-build sections.
	for _, forbidden := range []string{"Entrypoint Dry-Run", "Image", "nix build"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("check reached %q section — accumulated-fail gate did not short-circuit:\n%s", forbidden, got)
		}
	}
	// The tail must be the fail summary.
	if !strings.Contains(got, "Summary") || !strings.Contains(got, "failed") {
		t.Errorf("expected a fail Summary at the tail:\n%s", got)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// ensure jsonx import stays used (config-shaped helpers referenced in other
// tests may drop it; keep a trivial reference).
var _ = jsonx.NewOrderedMap
