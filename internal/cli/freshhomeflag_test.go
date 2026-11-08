package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

// TestTheFreshHomeFlagIsRunsSpelling: `yolo --fresh-home` (SD-D14) parses into the launch's
// FreshHome, in the spelling every refusal and disclosure names (run.FreshHomeFlag), it is
// documented in `yolo run --help`, it does not start the command, and it is off unless typed.
func TestTheFreshHomeFlagIsRunsSpelling(t *testing.T) {
	var opts run.Options
	parsed := parseRunArgs([]string{"run", run.FreshHomeFlag, "--", "claude"}, &opts)
	if !opts.FreshHome || parsed.misuse != nil || !slices.Equal(opts.Args, []string{"claude"}) {
		t.Fatalf("parseRunArgs(%s -- claude): FreshHome=%v misuse=%v args=%q", run.FreshHomeFlag,
			opts.FreshHome, parsed.misuse, opts.Args)
	}
	if !slices.Contains(runFlags, run.FreshHomeFlag) {
		t.Errorf("runFlags lacks %s", run.FreshHomeFlag)
	}
	if !strings.Contains(runUsage, run.FreshHomeFlag) {
		t.Errorf("runUsage does not document %s", run.FreshHomeFlag)
	}
	opts = run.Options{}
	parseRunArgs([]string{"run", "--", "true"}, &opts)
	if opts.FreshHome {
		t.Error("FreshHome must default to false")
	}
	// The bare launcher's spelling reaches run, not the host verb or a subcommand.
	if sub, routed, _ := routeArgv([]string{run.FreshHomeFlag, "--", "claude"}); sub != "run" ||
		!slices.Contains(routed, run.FreshHomeFlag) {
		t.Errorf("`yolo %s -- claude` routes to %q %q, want run", run.FreshHomeFlag, sub, routed)
	}
}

// TestTheHostVerbRefusesFreshHomeNamingTheJailLaunch: `yolo host` has no jail home to replace, so
// --fresh-home is refused by name, exit 2, nothing is run, and the refusal names the jail launch
// that does take it. Reached from `yolo host` and from a host setup's launch spelling alike.
func TestTheHostVerbRefusesFreshHomeNamingTheJailLaunch(t *testing.T) {
	valueFlagHome(t, "")
	for _, argv := range [][]string{
		{"host", run.FreshHomeFlag, "--", "mytool"},
		{"--setup", "host-self", run.FreshHomeFlag, "--", "mytool"},
	} {
		rc, execed, errs := hostExecThroughMain(t, argv...)
		if rc != 2 || execed != nil || !strings.Contains(errs, run.FreshHomeFlag+" is a jail-launch flag") ||
			!strings.Contains(errs, "`yolo --fresh-home` starts this workspace's jail") {
			t.Errorf("`yolo %s`: rc=%d exec=%q, want 2, no exec and the jail launch named\n%s",
				strings.Join(argv, " "), rc, execed, errs)
		}
	}
}
