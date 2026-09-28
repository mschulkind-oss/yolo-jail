package cli

// hostonlyflags_test.go pins the JAIL half of the grant's ruling
// (docs/design/credential-sources-separation.md OQ-ES5, ruled for the host 2026-09-27, jail half
// open): a jail launch given `--with-credentials` REFUSES, naming that the flag is host-only and
// the host spelling that takes it — never an "unknown flag", which would hide that the flag
// exists, and never a quiet launch.

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

// Through runRun, the call site a user reaches: exit 2, the host-only sentence and the host
// spelling with the value that was typed. Deleting the runRun call leaves the generic
// unknown-flag refusal, which this cell tells apart.
func TestJailLaunchRefusesWithCredentialsAsHostOnly(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		value string
	}{
		{[]string{"--with-credentials", "zai", "run", "--", "bash"}, "zai"},
		{[]string{"--with-credentials=all", "run", "--", "bash"}, "all"},
		{[]string{"run", "-p", "zai", "--with-credentials", "zai,cerebras", "--", "claude"}, "zai,cerebras"},
	} {
		var rc int
		errs := captureStderr(t, func() { rc = runRun(tc.args) })
		if rc != 2 {
			t.Errorf("runRun(%q) = %d, want 2 (misuse)\n%s", tc.args, rc, errs)
		}
		for _, want := range []string{"--with-credentials is HOST-ONLY", "OQ-ES5",
			"`yolo host --with-credentials " + tc.value + " -- <command>`",
			"`eval \"$(yolo host env --with-credentials " + tc.value + ")\"`"} {
			if !strings.Contains(errs, want) {
				t.Errorf("runRun(%q): the refusal must say %q:\n%s", tc.args, want, errs)
			}
		}
		if strings.Contains(errs, "unknown flag") {
			t.Errorf("runRun(%q): a host-only flag is not an unknown one:\n%s", tc.args, errs)
		}
	}
}

// The front door routes a jail launch carrying the flag to `run`, where the refusal is, even
// when its value spells a subcommand: the value is skipped as every value-taking flag's is.
// Without the skip, `yolo --with-credentials zai -- bash` answered `unknown command "zai"`.
func TestFrontDoorRoutesWithCredentialsToTheRunRefusal(t *testing.T) {
	for _, argv := range [][]string{
		{"--with-credentials", "zai", "--", "bash"},
		{"--with-credentials", "check", "--", "bash"},
	} {
		if got := routeDecision(argv); got != "dispatch:run" {
			t.Errorf("routeDecision(%q) = %q, want dispatch:run", argv, got)
		}
	}
	// `--at host` is the systematic spelling of the host verb, and carries the flag there.
	got := RewriteArgv([]string{"--at", "host", "--with-credentials", "all", "--", "bash"})
	if want := []string{"host", "--with-credentials", "all", "--", "bash"}; !slices.Equal(got, want) {
		t.Errorf("RewriteArgv(--at host --with-credentials all -- bash) = %q, want %q", got, want)
	}
}

// Only yolo's own tokens are asked: a wrapped program's `--with-credentials`, after `--` or
// after an implicit command start, is its own argv.
func TestAWrappedProgramsWithCredentialsIsNotYolos(t *testing.T) {
	for _, argv := range []string{
		"run -- tool --with-credentials zai",
		"run bash --with-credentials zai",
		"run --network host tool --with-credentials=all",
	} {
		var opts run.Options
		args := strings.Fields(argv)
		parsed := parseRunArgs(args, &opts)
		var errw bytes.Buffer
		if refuseHostOnlyFlags(parsed, &errw) {
			t.Errorf("`yolo %s` refused the wrapped program's own flag: %s", argv, errw.String())
		}
	}
}
