package cli

// frontdoornotch_test.go pins docs/plans/notch-convergence.md item 10 (row A3): the notch is
// decided once, at the front door, for every `--at host` spelling of a launch, and `yolo host`
// takes the flags a launch spelling carries there — `--at host` as a no-op, a contradicting
// notch and a run flag with no host meaning refused by name.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// hostExecThroughMain runs argv through Main with the exec replaced, from valueFlagHome's
// scratch home and unparseable workspace, and reports Main's exit code, the argv the exec was
// handed (nil when it was not reached), and stderr. A spelling that fell through to the jail
// launcher fails at that workspace's config load, so it can never start a container.
func hostExecThroughMain(t *testing.T, argv ...string) (int, []string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bin")
	writeFile(t, filepath.Join(dir, "mytool"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(dir, "mytool"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("YOLO_NO_BANNER", "1")
	var execed []string
	orig := hostSyscallExec
	hostSyscallExec = func(_ string, args, _ []string) error { execed = args; return nil }
	t.Cleanup(func() { hostSyscallExec = orig })
	var rc int
	_, errs := captureBoth(t, func() { rc = Main(append([]string{"yolo"}, argv...)) })
	return rc, execed, errs
}

// EVERY `--at host` SPELLING OF A LAUNCH RUNS THE COMMAND AT THE HOST, through Main. The
// explicit-run, run-after-flag and implicit-command spellings used to reach the jail launcher,
// which refused them; deleting routeArgv's host route sends them there again.
func TestEveryAtHostSpellingLaunchesAtTheHost(t *testing.T) {
	valueFlagHome(t, "")
	for _, spelling := range [][]string{
		{"--at", "host", "--", "mytool", "x"},
		{"--at=host", "--", "mytool", "x"},
		{"run", "--at", "host", "--", "mytool", "x"},
		{"--at", "host", "run", "--", "mytool", "x"},
		{"run", "--at", "host", "mytool", "x"},
		{"host", "--at", "host", "--", "mytool", "x"},
		{"host", "--at=host", "--", "mytool", "x"},
	} {
		rc, execed, errs := hostExecThroughMain(t, spelling...)
		if rc != 0 || !slices.Equal(execed, []string{"mytool", "x"}) {
			t.Errorf("`yolo %s`: rc=%d exec=%q, want the host exec of [mytool x]\n%s",
				strings.Join(spelling, " "), rc, execed, errs)
		}
	}
}

// A NOTCH OR A RUN FLAG THE HOST VERB CANNOT HONOR IS REFUSED BY NAME, exit 2, and nothing is
// executed. Reached from `yolo host` and from the front door's `--at host` spelling alike.
func TestHostVerbRefusesWhatItCannotHonorByName(t *testing.T) {
	valueFlagHome(t, "")
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"host", "--at", "jail", "--", "mytool"},
			"--at jail names the jail notch, and `yolo host` runs at the host notch"},
		{[]string{"--at", "jail", "host", "--", "mytool"},
			"--at jail names the jail notch"},
		{[]string{"host", "--at", "gest", "--", "mytool"},
			`--at "gest" is not a confinement level (jail|guest|host)`},
		{[]string{"host", "--timing", "--", "mytool"}, "--timing is a jail-launch flag"},
		{[]string{"--at", "host", "--timing", "--", "mytool"}, "--timing is a jail-launch flag"},
		{[]string{"run", "--at", "host", "--dry-run", "--", "mytool"}, "--dry-run is a jail-launch flag"},
		{[]string{"host", "--network", "none", "--", "mytool"}, "--network is a jail-launch flag"},
		{[]string{"host", "--network=none", "--", "mytool"}, "--network is a jail-launch flag"},
		{[]string{"host", "--accept-config-changes", "--", "mytool"},
			"--accept-config-changes is a jail-launch flag"},
	} {
		rc, execed, errs := hostExecThroughMain(t, tc.argv...)
		if rc != 2 || execed != nil || !strings.Contains(errs, "yolo host: "+tc.want) {
			t.Errorf("`yolo %s`: rc=%d exec=%q, want 2, no exec and %q\n%s",
				strings.Join(tc.argv, " "), rc, execed, tc.want, errs)
		}
		if strings.Contains(errs, "unknown flag") {
			t.Errorf("`yolo %s`: a flag yolo defines is not an unknown one:\n%s", strings.Join(tc.argv, " "), errs)
		}
	}
}

// Help among the exec flags is help, as `yolo run --help -- c` is run's.
func TestHostExecFlagsAnswerHelp(t *testing.T) {
	valueFlagHome(t, "")
	for _, argv := range [][]string{
		{"host", "--help", "--", "mytool"},
		{"run", "--at", "host", "--help"},
	} {
		var rc int
		out, errs := captureBoth(t, func() { rc = Main(append([]string{"yolo"}, argv...)) })
		if rc != 0 || !strings.Contains(out, "yolo host — configure and launch") {
			t.Errorf("`yolo %s`: rc=%d, want 0 and the host usage\nstdout: %s\nstderr: %s",
				strings.Join(argv, " "), rc, out, errs)
		}
	}
}
