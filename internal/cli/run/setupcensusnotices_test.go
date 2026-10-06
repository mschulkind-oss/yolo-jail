package run

// setupcensusnotices_test.go is this package's half of the macos-user notice block reading the
// setup census (internal/setupcensus; docs/design/backend-parity.md OQ-BP-1, BP-D19). A Warned
// cell holds its launch line (setupcensus.Notice), and the printer reads it with
// setupcensus.Warning; the census's own test proves each printer makes that read. What is pinned
// here is the CALL SITE: every notice this package prints is driven through the launch path that
// prints it — Run()'s macos-user arm, or the Apple Container argv assembly — and the line that
// comes out is the census's, headline and body. Delete a printer's call, or let it print words of
// its own, and its row fails.
//
// A pack KIND's Warned cell holds no notice, because its line names the pack's own item (a
// fork's program, an extension), so those cells are routed to the printer and test that pin them
// (kindNoticeRoutes), and both are checked to exist.

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/setupcensus"
)

// runNoticeSamples drives each census notice this package prints through its call site and
// returns what the launch printed, keyed "<setup> <census path>".
var runNoticeSamples = map[string]func(t *testing.T) string{
	"macos-user/macOS devices": func(t *testing.T) string {
		return macosUserNoticeRun(t, `{"devices": [{"usb": "0403:6001"}]}`)
	},
	"macos-user/macOS gpu": func(t *testing.T) string {
		return macosUserNoticeRun(t, `{"gpu": {"enabled": true}}`)
	},
	"macos-user/macOS kvm": func(t *testing.T) string { return macosUserNoticeRun(t, `{"kvm": true}`) },
	"container/macOS cache_relocations": func(t *testing.T) string {
		home := t.TempDir()
		t.Setenv("HOME", home)
		emptyLoopholeDirs(t)
		o := goldenOptions("/ws", home)
		var buf bytes.Buffer
		o.Stderr = &buf
		o.assembleRunCmd(relocationInput(t, "container", t.TempDir(), []config.CacheRelocation{
			{Subdir: "uv", Target: "/data/relocated/uv"},
		}))
		return buf.String()
	},
}

// flat collapses a printed line's wrapping, so a check reads words rather than layout.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// TestEveryNoticePrintedHereIsTheCensusLineAtItsCallSite drives every census notice whose printer
// is in this package and expects the census's headline and body in what the launch printed.
func TestEveryNoticePrintedHereIsTheCensusLineAtItsCallSite(t *testing.T) {
	owned := map[string]setupcensus.Notice{}
	for _, p := range setupcensus.Paths() {
		e, _ := setupcensus.Find(p)
		for _, s := range setupcensus.Setups() {
			if n := e.Cell(s).Notice; strings.HasPrefix(n.By, "run.") {
				owned[s.String()+" "+p] = n
			}
		}
	}
	if len(owned) == 0 {
		t.Fatal("the census holds no notice this package prints")
	}
	keys := make([]string, 0, len(owned))
	for k := range owned {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		n := owned[k]
		sample, ok := runNoticeSamples[k]
		if !ok {
			t.Errorf("the census's %s notice is printed by %s and runNoticeSamples has no launch "+
				"that prints it: add one driving the printer's call site", k, n.By)
			continue
		}
		t.Run(k, func(t *testing.T) {
			got := flat(sample(t))
			for _, want := range []string{"Warning: " + n.Says, n.Then} {
				if !strings.Contains(got, flat(want)) {
					t.Errorf("the census's %s notice says %q and the launch printed no such words:\n%s",
						k, want, got)
				}
			}
		})
	}
	for k := range runNoticeSamples {
		if _, ok := owned[k]; !ok {
			t.Errorf("runNoticeSamples drives %s, which no census notice printed here names: drop "+
				"the sample, or give the census cell its notice", k)
		}
	}
}

// notOnMacosUser matches the arm's per-key notice headline.
var notOnMacosUser = regexp.MustCompile("`[a-z_.]+` is not (?:read|honored) on macos-user")

// TestTheMacosUserArmWarnsOnlyWhereTheCensusDoes is the reverse: with every arm sample declared
// at once, each per-key headline the launch prints is one a census notice holds.
func TestTheMacosUserArmWarnsOnlyWhereTheCensusDoes(t *testing.T) {
	says := map[string]bool{}
	for _, p := range setupcensus.Paths() {
		if e, _ := setupcensus.Find(p); e.MacosUser.Notice.Says != "" {
			says[e.MacosUser.Notice.Says] = true
		}
	}
	cfg := `{"devices": ["/dev/ttyUSB0"], "gpu": {"enabled": true}, "kvm": true,
	  "network": {"ports": ["3000:3000"], "forward_host_ports": [5432]}}`
	got := macosUserNoticeRun(t, cfg)
	named := notOnMacosUser.FindAllString(got, -1)
	if len(named) == 0 {
		t.Fatalf("fixture bug: the arm printed no per-key notice:\n%s", got)
	}
	for _, headline := range named {
		if !says[headline] {
			t.Errorf("the macos-user launch printed %q, which no census notice holds: one of them "+
				"is wrong", headline)
		}
	}
}

// kindNoticeRoutes names, for each pack kind the census marks Warned on macos-user, the printer
// in this package that says so and the test that drives it through Run.
var kindNoticeRoutes = map[string]struct{ printer, test string }{
	"kind:program.patches": {"noteMacosUserForks", "TestAMacosUserLaunchNamesWhatRunsAPatchedFork"},
	"kind:files.patches":   {"noteMacosUserTrees", "TestAMacosUserLaunchSaysItDeliversNoTree"},
}

// TestEveryMacosUserWarnedKindIsRoutedToAPrinterThatSaysIt: every kind cell the census marks
// Warned on macos-user has a route, every route is such a cell, and each route's printer and
// test exist in this package, the printer saying the item is not delivered.
func TestEveryMacosUserWarnedKindIsRoutedToAPrinterThatSaysIt(t *testing.T) {
	for _, p := range setupcensus.Paths() {
		e, _ := setupcensus.Find(p)
		if !strings.HasPrefix(p, setupcensus.KindPathPrefix) || e.MacosUser.Disposition != setupcensus.Warned {
			continue
		}
		if _, ok := kindNoticeRoutes[p]; !ok {
			t.Errorf("the census marks %s Warned on macos-user (%s) and kindNoticeRoutes names no "+
				"printer for it", p, e.MacosUser.Reason)
		}
	}
	funcs, tests := packageFuncs(t)
	for p, r := range kindNoticeRoutes {
		if e, _ := setupcensus.Find(p); e.MacosUser.Disposition != setupcensus.Warned {
			t.Errorf("kindNoticeRoutes routes %s, which the census marks %s on macos-user", p,
				e.MacosUser.Disposition)
		}
		body, ok := funcs[r.printer]
		if !ok {
			t.Errorf("%s's printer %s is not defined in this package", p, r.printer)
		} else if !strings.Contains(body, "is not delivered on macos-user") {
			t.Errorf("%s's printer %s does not say the item is not delivered on macos-user", p, r.printer)
		}
		if !tests[r.test] {
			t.Errorf("%s's pinning test %s does not exist in this package", p, r.test)
		}
	}
}

// packageFuncs maps each function this package defines to its source text, and lists its tests.
func packageFuncs(t *testing.T) (map[string]string, map[string]bool) {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	funcs, tests := map[string]string{}, map[string]bool{}
	for _, ent := range entries {
		name := ent.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if strings.HasSuffix(name, "_test.go") {
				tests[fd.Name.Name] = true
				continue
			}
			funcs[fd.Name.Name] = string(src[fset.Position(fd.Pos()).Offset:fset.Position(fd.End()).Offset])
		}
	}
	return funcs, tests
}
