package cli

// jailwithcredentials_test.go pins the FRONT DOOR of the grant's jail half
// (docs/design/credential-sources-separation.md OQ-ES5, the jail half ruled 2026-10-05): a jail
// launch given `--with-credentials` reads it in the host's grammar into the one Options field the
// launch resolves it from (run.Options.WithCredentials), where it used to be refused as host-only.
// What the launch then does with it is internal/cli/run's jailgrant_test.go.

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

// seenRun runs runRun with the pipeline stubbed, returning its exit code, the Options it would
// have launched (nil when it never got that far), and its stderr.
func seenRun(t *testing.T, args []string) (int, *run.Options, string) {
	t.Helper()
	var seen *run.Options
	prev := launchRunPipeline
	launchRunPipeline = func(o run.Options) int { seen = &o; return 0 }
	t.Cleanup(func() { launchRunPipeline = prev })
	var rc int
	errs := captureStderr(t, func() { rc = runRun(args) })
	return rc, seen, errs
}

// Through runRun, the call site a user reaches: the flag is taken, in every spelling the host
// takes, its lists split and merged in order, and handed to the launch. Deleting the parse's grant
// arm, or runRun's hand-off, leaves WithCredentials empty or the launch never reached.
func TestJailLaunchTakesWithCredentials(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"run", "--with-credentials", "zai", "--", "bash"}, []string{"zai"}},
		{[]string{"run", "--with-credentials=all", "--", "bash"}, []string{"all"}},
		{[]string{"run", "-p", "zai", "--with-credentials", "zai,cerebras", "--", "claude"}, []string{"zai", "cerebras"}},
		{[]string{"run", "--with-credentials=zai", "--with-credentials", "openrouter", "--", "bash"}, []string{"zai", "openrouter"}},
	} {
		rc, seen, errs := seenRun(t, tc.args)
		if rc != 0 || seen == nil {
			t.Errorf("runRun(%q) = %d, launched = %v, want the launch\n%s", tc.args, rc, seen != nil, errs)
			continue
		}
		if !slices.Equal(seen.WithCredentials, tc.want) {
			t.Errorf("runRun(%q) launched WithCredentials %q, want %q", tc.args, seen.WithCredentials, tc.want)
		}
		for _, gone := range []string{"HOST-ONLY", "host-only", "unknown flag"} {
			if strings.Contains(errs, gone) {
				t.Errorf("runRun(%q): a jail launch takes the flag, yet stderr says %q:\n%s", tc.args, gone, errs)
			}
		}
	}
}

// An empty value is a mistake, refused at the parse as the host refuses it (exit 2), and no
// launch is started: a grant of nothing would read as "the provider has no key".
func TestJailLaunchRefusesAnEmptyGrantValue(t *testing.T) {
	for _, args := range [][]string{
		{"run", "--with-credentials", "", "--", "bash"},
		{"run", "--with-credentials=zai,", "--", "bash"},
		{"run", "--with-credentials"},
	} {
		rc, seen, errs := seenRun(t, args)
		if rc != 2 || seen != nil {
			t.Errorf("runRun(%q) = %d, launched = %v, want 2 and no launch\n%s", args, rc, seen != nil, errs)
		}
		if !strings.Contains(errs, "--with-credentials") {
			t.Errorf("runRun(%q): the refusal must name the flag:\n%s", args, errs)
		}
	}
}

// NOTHING ELSE IMPLIES IT at the front door: a -p, a pair, and an environment spelling of the
// flag launch with no grant.
func TestJailLaunchGrantIsImpliedByNothingElse(t *testing.T) {
	t.Setenv("YOLO_WITH_CREDENTIALS", "all")
	t.Setenv("YOLO_ALLOW_ALL_CREDENTIALS", "1")
	for _, args := range [][]string{
		{"run", "-p", "zai", "--", "bash"},
		{"run", "-p", "claude=zai", "--", "claude"},
		{"run", "--", "bash"},
	} {
		rc, seen, errs := seenRun(t, args)
		if rc != 0 || seen == nil {
			t.Fatalf("runRun(%q) = %d\n%s", args, rc, errs)
		}
		if len(seen.WithCredentials) != 0 {
			t.Errorf("runRun(%q): no --with-credentials was typed, yet the launch carries %q", args, seen.WithCredentials)
		}
	}
}

// The front door routes a jail launch carrying the flag to `run`, even when its value spells a
// subcommand: the value is skipped as every value-taking flag's is. Without the skip,
// `yolo --with-credentials zai -- bash` answered `unknown command "zai"`.
func TestFrontDoorRoutesWithCredentialsToRun(t *testing.T) {
	for _, argv := range [][]string{
		{"--with-credentials", "zai", "--", "bash"},
		{"--with-credentials", "check", "--", "bash"},
	} {
		if got := routeDecision(argv); got != "dispatch:run" {
			t.Errorf("routeDecision(%q) = %q, want dispatch:run", argv, got)
		}
	}
	// `--at host` is the systematic spelling of the host verb, and carries the flag there.
	sub, got, _ := routeArgv([]string{"--at", "host", "--with-credentials", "all", "--", "bash"})
	if want := []string{"host", "--with-credentials", "all", "--", "bash"}; sub != "host" || !slices.Equal(got, want) {
		t.Errorf("routeArgv(--at host --with-credentials all -- bash) = %q %q, want host %q", sub, got, want)
	}
}

// Only yolo's own tokens are asked: a wrapped program's `--with-credentials`, after `--` or
// after an implicit command start, is its own argv, and grants nothing.
func TestAWrappedProgramsWithCredentialsIsNotYolos(t *testing.T) {
	for _, argv := range []string{
		"run -- tool --with-credentials zai",
		"run bash --with-credentials zai",
		"run --network host tool --with-credentials=all",
	} {
		var opts run.Options
		parsed := parseRunArgs(strings.Fields(argv), &opts)
		if len(opts.WithCredentials) != 0 || parsed.misuse != nil {
			t.Errorf("`yolo %s` read the wrapped program's own flag as yolo's: %q (misuse %v)",
				argv, opts.WithCredentials, parsed.misuse)
		}
		if !slices.Contains(opts.Args, "--with-credentials") && !slices.Contains(opts.Args, "--with-credentials=all") {
			t.Errorf("`yolo %s` dropped the wrapped program's flag from its argv: %q", argv, opts.Args)
		}
	}
}

// ONE SPELLING AT EVERY NOTCH: the jail's parser spells the flag as a literal (for
// TestRunUsageListsEveryRunFlag), the host's messages through withCredentialsFlag; and the host,
// which shares the flag, does not refuse it as a jail-launch flag.
func TestTheJailGrantFlagIsTheHostsSpelling(t *testing.T) {
	if withCredentialsFlag != "--with-credentials" {
		t.Fatalf("withCredentialsFlag = %q", withCredentialsFlag)
	}
	if !slices.Contains(runFlags, withCredentialsFlag) {
		t.Errorf("runFlags must list %s: a jail launch takes it", withCredentialsFlag)
	}
	if slices.Contains(jailOnlyRunFlags(), withCredentialsFlag) {
		t.Errorf("the host shares %s; it is no jail-only flag", withCredentialsFlag)
	}
	var errw bytes.Buffer
	if _, ok := parseHostExecFlags([]string{"--with-credentials", "zai"}, &errw); !ok {
		t.Errorf("yolo host refused its own flag: %s", errw.String())
	}
}
