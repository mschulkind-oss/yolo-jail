package cli

// frontdoornotch_test.go pins docs/plans/notch-convergence.md item 10 (row A3) under named setups
// (named-setups-plan.md slice 4a): the host route is decided once, at the front door, for every
// `--setup host-self` spelling of a launch, and `yolo host` takes the flags a launch spelling
// carries there — `--setup host-self` as a no-op, a setup with walls and a run flag with no host
// meaning refused by name, and the removed `--at` refused naming `--setup`.

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

// EVERY `--setup host-self` SPELLING OF A LAUNCH RUNS THE COMMAND AT THE HOST, through Main. The
// explicit-run, run-after-flag and implicit-command spellings used to reach the jail launcher,
// which refused them; deleting routeArgv's host route sends them there again.
func TestEveryAtHostSpellingLaunchesAtTheHost(t *testing.T) {
	valueFlagHome(t, `{"host_management":"own","host_apply_on_launch":false}`)
	for _, spelling := range [][]string{
		{"--setup", "host-self", "--", "mytool", "x"},
		{"--setup=host-self", "--", "mytool", "x"},
		{"run", "--setup", "host-self", "--", "mytool", "x"},
		{"--setup", "host-self", "run", "--", "mytool", "x"},
		{"run", "--setup", "host-self", "mytool", "x"},
		{"host", "--setup", "host-self", "--", "mytool", "x"},
		{"host", "--setup=host-self", "--", "mytool", "x"},
		// The last --setup wins, and the earlier one is not handed to the host to refuse.
		{"--setup", "podman-self", "--setup", "host-self", "--", "mytool", "x"},
	} {
		rc, execed, errs := hostExecThroughMain(t, spelling...)
		if rc != 0 || !slices.Equal(execed, []string{"mytool", "x"}) {
			t.Errorf("`yolo %s`: rc=%d exec=%q, want the host exec of [mytool x]\n%s",
				strings.Join(spelling, " "), rc, execed, errs)
		}
	}
}

// Every front-door spelling shares hostExec's ownership refusal. A command is not started under
// either explicit none or an unset host_management key.
func TestEveryAtHostSpellingRequiresOwn(t *testing.T) {
	spellings := [][]string{
		{"--setup", "host-self", "--", "mytool", "x"},
		{"--setup=host-self", "--", "mytool", "x"},
		{"run", "--setup", "host-self", "--", "mytool", "x"},
		{"--setup", "host-self", "run", "--", "mytool", "x"},
		{"run", "--setup", "host-self", "mytool", "x"},
		{"host", "--setup", "host-self", "--", "mytool", "x"},
		{"host", "--setup=host-self", "--", "mytool", "x"},
		{"--setup", "podman-self", "--setup", "host-self", "--", "mytool", "x"},
	}
	for _, mode := range []struct {
		name, config string
	}{
		{"unset", ""},
		{"none", `{"host_management":"none"}`},
	} {
		t.Run(mode.name, func(t *testing.T) {
			for _, spelling := range spellings {
				valueFlagHome(t, mode.config)
				rc, execed, errs := hostExecThroughMain(t, spelling...)
				if rc != 1 || execed != nil || !strings.Contains(errs, "host command launches require `host_management: \"own\"`") {
					t.Errorf("`yolo %s`: rc=%d exec=%q, want ownership refusal and no command\n%s",
						strings.Join(spelling, " "), rc, execed, errs)
				}
			}
		})
	}
}

// A NOTCH OR A RUN FLAG THE HOST VERB CANNOT HONOR IS REFUSED BY NAME, exit 2, and nothing is
// executed. Reached from `yolo host` and from the front door's `--setup host-self` spelling alike.
func TestHostVerbRefusesWhatItCannotHonorByName(t *testing.T) {
	valueFlagHome(t, "")
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"host", "--setup", "podman-self", "--", "mytool"},
			"setup \"podman-self\" has walls, and `yolo host` runs only a setup without them. " +
				"Run that one as a launch:\n    yolo --setup podman-self -- claude\nOr on your own machine:\n    yolo host -- claude"},
		{[]string{"--setup", "podman-self", "host", "--", "mytool"},
			"setup \"podman-self\" has walls"},
		// A Seatbelt setup is refused naming both spellings: its own launch, and the host verb.
		{[]string{"host", "--setup", "seatbelt-self", "--", "mytool"},
			"setup \"seatbelt-self\" has walls, and `yolo host` runs only a setup without them. " +
				"Run that one as a launch:\n    yolo --setup seatbelt-self -- claude\nOr on your own machine:\n    yolo host -- claude"},
		{[]string{"host", "--setup", "nope", "--", "mytool"}, `--setup: setup "nope" is not defined.`},
		{[]string{"host", "--setup", "host-yolo-account", "--", "mytool"}, `setup "host-yolo-account" is not built yet`},
		// The removed --at, refused naming --setup, at the host verb and on a launch alike.
		{[]string{"host", "--at", "gest", "--", "mytool"}, "`--at` is removed. Use `--setup <name>`"},
		{[]string{"host", "--at", "host", "--", "mytool"}, "`--at` is removed. Use `--setup <name>`"},
		{[]string{"run", "--setup", "host-self", "--dry-run", "--", "mytool"}, "--dry-run is a jail-launch flag"},
		{[]string{"host", "--network", "none", "--", "mytool"}, "--network is a jail-launch flag"},
		{[]string{"host", "--network=none", "--", "mytool"}, "--network is a jail-launch flag"},
		{[]string{"host", "--accept-config-changes", "--", "mytool"},
			"--accept-config-changes is a jail-launch flag"},
		{[]string{"--setup", "host-self", "--dry-run", "--", "mytool"}, "--dry-run is a jail-launch flag"},
		// WITH NO `--` the exec flags are still exec flags (notch-convergence.md items 9 and 10):
		// these used to fall to hostMain's verb switch and exit 1 as `unknown verb "-p"`.
		{[]string{"--setup", "host-self", "--profile="}, "--profile needs a value"},
		{[]string{"host", "--profile="}, "--profile needs a value"},
		{[]string{"--setup", "host-self", "-p"}, "-p needs a value"},
		// --timing is a host flag now (perf-logging.md D18), so with no `--` it is refused for
		// naming no command, as -p is.
		{[]string{"--setup", "host-self", "--timing"}, "--timing names no command to run"},
		{[]string{"host", "--timing"}, "--timing names no command to run"},
		{[]string{"--setup", "host-self", "--frob"}, `unknown flag "--frob"`},
		{[]string{"--setup", "host-self", "-p", "zai"}, "-p zai names no command to run"},
		{[]string{"host", "--with-credentials", "all"}, "--with-credentials all names no command to run"},
	} {
		rc, execed, errs := hostExecThroughMain(t, tc.argv...)
		if rc != 2 || execed != nil || !strings.Contains(errs, "yolo host: "+tc.want) {
			t.Errorf("`yolo %s`: rc=%d exec=%q, want 2, no exec and %q\n%s",
				strings.Join(tc.argv, " "), rc, execed, tc.want, errs)
		}
		if strings.Contains(errs, "unknown verb") {
			t.Errorf("`yolo %s`: an exec flag is not a verb:\n%s", strings.Join(tc.argv, " "), errs)
		}
		if strings.Contains(errs, "unknown flag") && !strings.Contains(tc.want, "unknown flag") {
			t.Errorf("`yolo %s`: a flag yolo defines is not an unknown one:\n%s", strings.Join(tc.argv, " "), errs)
		}
	}
}

// Help among the exec flags is help, as `yolo run --help -- c` is run's.
func TestHostExecFlagsAnswerHelp(t *testing.T) {
	valueFlagHome(t, "")
	for _, argv := range [][]string{
		{"host", "--help", "--", "mytool"},
		{"run", "--setup", "host-self", "--help"},
	} {
		var rc int
		out, errs := captureBoth(t, func() { rc = Main(append([]string{"yolo"}, argv...)) })
		if rc != 0 || !strings.Contains(out, "yolo host — configure and launch") {
			t.Errorf("`yolo %s`: rc=%d, want 0 and the host usage\nstdout: %s\nstderr: %s",
				strings.Join(argv, " "), rc, out, errs)
		}
	}
}

// THE LAST `--setup` WINS IN BOTH ORDERS (NC-D20): `--setup host-self --setup podman-self` is a jail launch, never
// the host exec, just as `--setup podman-self --setup host-self` is the host exec (TestEveryAtHostSpelling…).
func TestLastAtWinsTowardTheJail(t *testing.T) {
	valueFlagHome(t, "")
	rc, execed, errs := hostExecThroughMain(t, "--setup", "host-self", "--setup", "podman-self", "--", "mytool", "x")
	if execed != nil || rc == 0 || strings.Contains(errs, "yolo host:") {
		t.Errorf("`yolo --setup host-self --setup podman-self -- mytool x`: rc=%d exec=%q, want the jail launcher, "+
			"never the host\n%s", rc, execed, errs)
	}
}

// --timing IS THE HOST VERB'S TOO (perf-logging.md D18): every spelling that carries it to the host
// exec times the launch and runs the command, instead of refusing it as a jail-launch flag.
func TestTheHostVerbTakesTiming(t *testing.T) {
	valueFlagHome(t, `{"host_management":"own","host_apply_on_launch":false}`)
	for _, spelling := range [][]string{
		{"host", "--timing", "--", "mytool", "x"},
		{"--setup", "host-self", "--timing", "--", "mytool", "x"},
		{"--timing", "host", "--", "mytool", "x"},
		{"run", "--setup", "host-self", "--timing", "--", "mytool", "x"},
	} {
		rc, execed, errs := hostExecThroughMain(t, spelling...)
		if rc != 0 || !slices.Equal(execed, []string{"mytool", "x"}) {
			t.Errorf("`yolo %s`: rc=%d exec=%q, want the host exec of [mytool x]\n%s",
				strings.Join(spelling, " "), rc, execed, errs)
		}
		if !strings.Contains(errs, "yolo host timing (to the hand-over):") {
			t.Errorf("`yolo %s` printed no timing table:\n%s", strings.Join(spelling, " "), errs)
		}
	}
}

// THE REMOVED `--at` ON A LAUNCH IS REFUSED, never rerouted: `yolo --at host -- c` used to be the
// host verb, and is now a misuse the launch refuses (exit 2) naming `yolo host`, with nothing run.
func TestALaunchsRemovedAtIsRefusedNamingTheHostVerb(t *testing.T) {
	valueFlagHome(t, `{"host_management":"own","host_apply_on_launch":false}`)
	for _, spelling := range [][]string{
		{"--at", "host", "--", "mytool", "x"},
		{"run", "--at=host", "--", "mytool", "x"},
	} {
		rc, execed, errs := hostExecThroughMain(t, spelling...)
		if rc != 2 || execed != nil || !strings.Contains(errs, "yolo run: `--at` is removed. Use `--setup <name>`") ||
			!strings.Contains(errs, "yolo host -- <cmd>") {
			t.Errorf("`yolo %s`: rc=%d exec=%q, want 2, no exec and the removal naming `yolo host`\n%s",
				strings.Join(spelling, " "), rc, execed, errs)
		}
	}
}
