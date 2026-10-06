package macosuser

// setupcensus_test.go is the orchestrator's half of the macos-user notice block reading the setup
// census (internal/setupcensus; docs/design/backend-parity.md OQ-BP-1, BP-D19). buildPlan reads
// its lines from the census (setupcensus.Warning), which the census's own test proves; this pins
// the CALL SITE: every notice whose printer is this package's is driven through RunMacosUser's
// dry run, rather than buildPlan alone, so deleting the call is as visible as deleting the line,
// and what it prints is the census's headline and body. internal/cli/run's
// setupcensusnotices_test.go holds the run arm's half.

import (
	"bytes"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/setupcensus"
)

// orchestratorSample is one config key and value whose dry run prints a census notice.
type orchestratorSample struct{ key, value string }

// orchestratorNoticeSamples drive each macos-user notice this package prints, keyed by census path.
var orchestratorNoticeSamples = map[string]orchestratorSample{
	"per_side_paths":    {"per_side_paths", `["build"]`},
	"resources":         {"resources", `{"memory": "4g"}`},
	"cache_relocations": {"cache_relocations", `{"npm": "/Volumes/big/npm"}`},
}

// orchestratorAspectSamples drive each Warned aspect that leans on its parent's line: the line
// must be the parent's notice, naming the aspect among its entries.
var orchestratorAspectSamples = map[string]orchestratorSample{
	"resources.pids_limit": {"resources", `{"pids_limit": 100}`},
	"resources.io":         {"resources", `{"io": "idle"}`},
}

// dryRunWith is what RunMacosUser's dry run prints for a config holding one key.
func dryRunWith(t *testing.T, sample orchestratorSample) string {
	t.Helper()
	v, err := json5.Decode([]byte(sample.value))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	d := mockDeps(nil)
	d.IsMacOS = func() bool { return false } // a dry run works off-macOS
	d.Out = &buf
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.DryRun = true
	opts.Config = jsonx.NewOrderedMap()
	opts.Config.Set(sample.key, v)
	RunMacosUser(d, opts)
	return strings.Join(strings.Fields(buf.String()), " ")
}

func TestTheOrchestratorPrintsTheCensusLines(t *testing.T) {
	var owned []string
	for _, p := range setupcensus.Paths() {
		if e, _ := setupcensus.Find(p); strings.HasPrefix(e.MacosUser.Notice.By, "macosuser.") {
			owned = append(owned, p)
		}
	}
	sort.Strings(owned)
	if len(owned) == 0 {
		t.Fatal("the census holds no macos-user notice this package prints")
	}
	for _, p := range owned {
		n := setupcensus.Warning(setupcensus.MacosUser, p)
		sample, ok := orchestratorNoticeSamples[p]
		if !ok {
			t.Errorf("the census's %s notice is printed by %s and orchestratorNoticeSamples has no "+
				"config that prints it", p, n.By)
			continue
		}
		t.Run(p, func(t *testing.T) {
			got := dryRunWith(t, sample)
			for _, want := range []string{"Warning: " + n.Says, n.Then} {
				if !strings.Contains(got, strings.Join(strings.Fields(want), " ")) {
					t.Errorf("the census's %s notice says %q and the dry run printed no such words:\n%s",
						p, want, got)
				}
			}
		})
	}
	for p := range orchestratorNoticeSamples {
		if e, _ := setupcensus.Find(p); !strings.HasPrefix(e.MacosUser.Notice.By, "macosuser.") {
			t.Errorf("orchestratorNoticeSamples drives %s, whose census cell holds no notice printed here", p)
		}
	}
}

func TestTheOrchestratorNamesEachAspectInItsParentsLine(t *testing.T) {
	for _, p := range setupcensus.Paths() {
		parent, aspect, isAspect := strings.Cut(p, ".")
		if !isAspect || strings.HasPrefix(p, setupcensus.KindPathPrefix) {
			continue
		}
		e, _ := setupcensus.Find(p)
		pe, _ := setupcensus.Find(parent)
		if e.MacosUser.Disposition != setupcensus.Warned || e.MacosUser.Notice.Says != "" ||
			!strings.HasPrefix(pe.MacosUser.Notice.By, "macosuser.") {
			continue
		}
		sample, ok := orchestratorAspectSamples[p]
		if !ok {
			t.Errorf("the census marks %s Warned on macos-user through its parent's line, and "+
				"orchestratorAspectSamples has no config that prints it", p)
			continue
		}
		got := dryRunWith(t, sample)
		if !strings.Contains(got, "Warning: "+pe.MacosUser.Notice.Says) || !strings.Contains(got, aspect) {
			t.Errorf("the census marks %s Warned on macos-user, and the dry run printed no %q naming "+
				"%s:\n%s", p, pe.MacosUser.Notice.Says, aspect, got)
		}
	}
}
