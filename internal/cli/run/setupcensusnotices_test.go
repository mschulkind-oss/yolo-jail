package run

// setupcensusnotices_test.go is how the macos-user notice block reads the setup census
// (internal/setupcensus; docs/design/backend-parity.md OQ-BP-1 and BP-D19). A Warned cell asserts a
// launch line, so every path the census marks Warned on macos-user is routed here to the printer
// that says it, and the arm's own printers are driven through Run(), which pins their call site
// rather than the functions alone (macosuserkeynotices_test.go's header says why that matters).
// The other direction holds too: a line the arm prints naming a key must name one the census
// marks Warned.

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/setupcensus"
)

// macosUserWarnedRoute says where a census-Warned macos-user cell's launch line comes from.
type macosUserWarnedRoute struct {
	// arm is a workspace config declaring the path, for a line Run's macos-user arm prints; the
	// test launches it and expects says on stderr.
	arm  string
	says string
	// elsewhere names the printer outside this arm and the test that pins its line, for a line
	// printed by the orchestrator, the darwin bootstrap or a fixture this package's dry run
	// cannot reach.
	elsewhere string
}

var macosUserWarnedRoutes = map[string]macosUserWarnedRoute{
	"devices": {arm: `{"devices": ["/dev/ttyUSB0"]}`, says: "`devices` is not read on macos-user"},
	"gpu":     {arm: `{"gpu": {"enabled": true}}`, says: "`gpu.enabled` is not read on macos-user"},
	"kvm":     {arm: `{"kvm": true}`, says: "`kvm` is not read on macos-user"},
	"network.ports": {arm: `{"network": {"ports": ["3000:3000"]}}`,
		says: "`network.ports` is not honored on macos-user"},
	"network.forward_host_ports": {arm: `{"network": {"forward_host_ports": [5432]}}`,
		says: "`network.forward_host_ports` is not honored on macos-user"},

	"resources": {elsewhere: "macosuser buildPlan, " +
		"TestTheOrchestratorWarnsWhatTheCensusWarns (internal/macosuser)"},
	"resources.pids_limit": {elsewhere: "macosuser buildPlan, " +
		"TestTheOrchestratorWarnsWhatTheCensusWarns (internal/macosuser)"},
	"resources.io": {elsewhere: "macosuser buildPlan, " +
		"TestTheOrchestratorWarnsWhatTheCensusWarns (internal/macosuser)"},
	"per_side_paths": {elsewhere: "macosuser buildPlan, " +
		"TestTheOrchestratorWarnsWhatTheCensusWarns (internal/macosuser)"},
	"cache_relocations": {elsewhere: "macosuser buildPlan, " +
		"TestTheOrchestratorWarnsWhatTheCensusWarns (internal/macosuser)"},
	"mcp_presets": {elsewhere: "the darwin bootstrap's mcp_presets_declined step, " +
		"TestDarwinBootstrapSkipsLinuxMCPWrappers (internal/entrypoint)"},
	"host_files.directory_source": {elsewhere: "noteMacosUserHostByteGaps, " +
		"TestMacosUserDirHostFileWarningQualifiesTheAppleContainerFloor"},
	"kind:program.patches": {elsewhere: "noteMacosUserForks, macosuserforks_test.go"},
	"kind:files.patches":   {elsewhere: "noteMacosUserTrees, patchedtrees_test.go"},
}

// macosUserWarnedPaths is every census path whose macos-user cell is Warned, sorted.
func macosUserWarnedPaths() []string {
	var out []string
	for _, p := range setupcensus.Paths() {
		if e, _ := setupcensus.Find(p); e.MacosUser.Disposition == setupcensus.Warned {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// TestEveryMacosUserWarnedCellHasAPrinter: a census cell may say Warned only where some
// printer says it, so a new Warned cell must be routed, and a route the census no longer
// marks Warned is a line the census disowns.
func TestEveryMacosUserWarnedCellHasAPrinter(t *testing.T) {
	warned := macosUserWarnedPaths()
	for _, p := range warned {
		if _, ok := macosUserWarnedRoutes[p]; !ok {
			e, _ := setupcensus.Find(p)
			t.Errorf("the census marks %s Warned on macos-user (%s) and macosUserWarnedRoutes "+
				"names no printer for it: add a sample the arm prints, or the printer and test "+
				"that pin its line elsewhere", p, e.MacosUser.Reason)
		}
	}
	for p := range macosUserWarnedRoutes {
		if e, _ := setupcensus.Find(p); e.MacosUser.Disposition != setupcensus.Warned {
			t.Errorf("macosUserWarnedRoutes routes %s, which the census marks %s on macos-user: "+
				"drop the route, or fix the census cell", p, e.MacosUser.Disposition)
		}
	}
}

// TestTheMacosUserArmPrintsEveryCensusWarnedKeyItOwns drives each of the arm's routes through
// Run() and expects its line.
func TestTheMacosUserArmPrintsEveryCensusWarnedKeyItOwns(t *testing.T) {
	for _, p := range macosUserWarnedPaths() {
		r := macosUserWarnedRoutes[p]
		if r.arm == "" {
			continue
		}
		t.Run(p, func(t *testing.T) {
			if got := macosUserNoticeRun(t, r.arm); !strings.Contains(got, r.says) {
				t.Errorf("the census marks %s Warned on macos-user and the launch printed no %q:\n%s",
					p, r.says, got)
			}
		})
	}
}

// notOnMacosUser matches the arm's per-key notice shape and captures the key it names.
var notOnMacosUser = regexp.MustCompile("`([a-z_.]+)` is not (?:read|honored) on macos-user")

// TestTheMacosUserArmWarnsOnlyWhereTheCensusDoes is the reverse: with every arm sample declared
// at once, each key a notice names is one the census marks Warned on macos-user.
func TestTheMacosUserArmWarnsOnlyWhereTheCensusDoes(t *testing.T) {
	cfg := `{"devices": ["/dev/ttyUSB0"], "gpu": {"enabled": true}, "kvm": true,
	  "network": {"ports": ["3000:3000"], "forward_host_ports": [5432]}}`
	got := macosUserNoticeRun(t, cfg)
	named := notOnMacosUser.FindAllStringSubmatch(got, -1)
	if len(named) == 0 {
		t.Fatalf("fixture bug: the arm printed no per-key notice:\n%s", got)
	}
	for _, m := range named {
		path := strings.TrimSuffix(m[1], ".enabled")
		if e, ok := setupcensus.Find(path); !ok || e.MacosUser.Disposition != setupcensus.Warned {
			t.Errorf("the macos-user launch warns that %s is not honored, and the census marks it "+
				"%s there: one of them is wrong", m[1], e.MacosUser.Disposition)
		}
	}
}
