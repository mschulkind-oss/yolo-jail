package cli

// hostlaunchflags_test.go pins docs/plans/notch-convergence.md item 20 (row D9) at the host
// front door: `yolo host -- <bin>` runs the ONE launch-flag injector with the host notch's
// posture bit, so a pack's `guarded.launch` flag reaches the exec'd argv, its `autonomous` one
// never does, and the rewrite is disclosed. Before this the host exec'd the argv as typed and a
// guarded launch flag reached no notch.
//
// Each test drives hostMain with the exec replaced, so deleting injectHostLaunchFlags' call in
// hostExec fails them: they read the argv the exec was handed, not the injector's answer.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostLaunchFlagsRun sets up a temp HOME whose conventional local pack declares manifest,
// puts a fake `tool` first on PATH and runs `yolo host -- tool <args>` through hostMain: the
// exit code, the argv the exec was handed (nil when none) and stderr.
func hostLaunchFlagsRun(t *testing.T, manifest string, args ...string) (int, []string, string) {
	t.Helper()
	home := hostGateHome(t, `{}`, nil)
	t.Chdir(t.TempDir())
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "local", "pack.json"), manifest)

	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "tool"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var got []string
	origExec := hostSyscallExec
	hostSyscallExec = func(_ string, argv, _ []string) error {
		got = append([]string{}, argv...)
		return nil
	}
	t.Cleanup(func() { hostSyscallExec = origExec })

	var out, errw bytes.Buffer
	rc := hostMain(append([]string{"--", "tool"}, args...), &out, &errw, false, nil)
	return rc, got, errw.String()
}

const twoPostureManifest = `{"name":"acme","contributes":[{"kind":"autonomy",` +
	`"autonomous":{"launch":[{"bin":"tool","flags":["--no-prompts"]}]},` +
	`"guarded":{"launch":[{"bin":"tool","flags":["--ask-first"]}]}}]}`

func TestHostLaunchInjectsTheGuardedPosturesFlags(t *testing.T) {
	rc, argv, errs := hostLaunchFlagsRun(t, twoPostureManifest, "sub")
	if rc != 0 || argv == nil {
		t.Fatalf("yolo host -- tool sub: rc = %d, exec reached = %v\n%s", rc, argv != nil, errs)
	}
	if got := strings.Join(argv, " "); got != "tool --ask-first sub" {
		t.Errorf("the host exec'd %q, want the guarded posture's flag injected (%q)\n%s", got,
			"tool --ask-first sub", errs)
	}
	for _, want := range []string{
		"yolo host: yolo CHANGED the command you asked for:",
		"  you asked for: tool sub",
		"  yolo will run: tool --ask-first sub",
		"  added by pack local: --ask-first",
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("the host rewrite must be disclosed (%q):\n%s", want, errs)
		}
	}
}

// The autonomous posture's permission bypass never reaches the host, and a launch nothing
// rewrote prints no disclosure.
func TestHostLaunchWithholdsAnAutonomousOnlyFlag(t *testing.T) {
	rc, argv, errs := hostLaunchFlagsRun(t, `{"name":"acme","contributes":[{"kind":"autonomy",`+
		`"autonomous":{"launch":[{"bin":"tool","flags":["--yolo"]}]}}]}`, "sub")
	if rc != 0 || argv == nil {
		t.Fatalf("yolo host -- tool sub: rc = %d, exec reached = %v\n%s", rc, argv != nil, errs)
	}
	if got := strings.Join(argv, " "); got != "tool sub" {
		t.Errorf("the host exec'd %q: an autonomous-only flag must never reach the host notch", got)
	}
	if strings.Contains(errs, "CHANGED the command") {
		t.Errorf("nothing was rewritten, so nothing may be disclosed:\n%s", errs)
	}
}
