package macosuser

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestRunAsAccountIsTheSudoRule(t *testing.T) {
	got := RunAs{}.Argv(true, "-i", "/usr/bin/true")
	want := RunAsSandboxArgv([]string{"--set-home"}, "-i", "/usr/bin/true")
	if !slices.Equal(got, want) {
		t.Errorf("account crossing = %v, want %v", got, want)
	}
	if got, want := (RunAs{}).Argv(false, "/usr/bin/true"), RunAsSandboxArgv(nil, "/usr/bin/true"); !slices.Equal(got, want) {
		t.Errorf("account crossing without --set-home = %v, want %v", got, want)
	}
}

func TestRunAsSelfCrossesNoAccount(t *testing.T) {
	got := RunAs{Mode: ModeSelf}.Argv(true, "-i", "/usr/bin/true")
	if want := []string{"/usr/bin/env", "-i", "/usr/bin/true"}; !slices.Equal(got, want) {
		t.Errorf("self crossing = %v, want %v", got, want)
	}
	if len(RunAsArgvProblems("self", got)) != 0 {
		t.Errorf("a self crossing reports sudo-rule problems: %v", RunAsArgvProblems("self", got))
	}
}

func TestModeSpellings(t *testing.T) {
	if ModeAccount.String() != "account" || ModeSelf.String() != "self" {
		t.Errorf("mode spellings: %q, %q", ModeAccount, ModeSelf)
	}
}

// selfCrossingArgvs is every builder's argv in self mode, plus a whole plan built with
// PlanSession.Mode = ModeSelf: the mode must reach every crossing the plan carries.
func selfCrossingArgvs(t *testing.T) map[string][]string {
	t.Helper()
	self := RunAs{Mode: ModeSelf}
	prefix := []string{"/nix/store/000mock-yolo-noncontainer-profile/bin"}
	links := []ContextLink{
		{Dest: "/ctx/data", Source: "/Users/Shared/data", Dir: true},
		{Dest: "/ctx/out", Source: "/Users/Shared/out", Dir: true, RW: true},
	}
	relocs := []CacheRelocation{sharedReloc}
	out := map[string][]string{
		"LaunchArgvWithGuard": LaunchArgvWithGuard(self, []string{"claude"}, "/s/p.sb", "/s/env",
			"/Users/Shared/yolo/proj", "", "", prefix, SessionGuard{MemoryBytes: 1 << 28}, "/s/bin/yolo"),
		"ProvisionArgv": ProvisionArgv(self, "echo hi", "/s/p.sb", "/s/env", "/Users/Shared/yolo/proj",
			"", "", prefix, "/s/bin"),
		"DarwinBootstrapArgv":       DarwinBootstrapArgv(self, "/s/bin/yolo", "", jsonx.NewOrderedMap(), ""),
		"JailDaemonArgv":            JailDaemonArgv(self, "/s/p.sb", "/s/denv", "/s/log", "", "", prefix, "/s/bin/yolo"),
		"ProbeServicesArgv":         ProbeServicesArgv(self, "/s/bin/yolo", "/s/p.sb", "/s/env", "", "", prefix),
		"CacheRelocationWriteProbe": CacheRelocationWriteProbe(self, sharedReloc, "/s/p.sb", "s", "").Argv,
		"CaptureDriverArgv": CaptureDriverArgv(self, "/s/bin/yolo", "/c/home", "/c/out", "jq", "/s/p.sb",
			"/s/env", prefix),
		"ForkBuildDriverArgv": ForkBuildDriverArgv(self, "/s/bin/yolo", "/c/home", "/c/out", "/c/src",
			"/c/tc", "abc", "make", "/s/p.sb", "/s/env", prefix),
		"TreeBuildDriverArgv": TreeBuildDriverArgv(self, "/s/bin/yolo", "/c/home", "/c/out", "/c/src",
			"/c/tc", "abc", "make", "/c/tree", "/s/p.sb", "/s/env", prefix),
	}
	for i, p := range ContextPreflight(self, links) {
		out["ContextPreflight/"+strconv.Itoa(i)] = p.Argv
	}
	for i, p := range CacheRelocationPreflight(self, relocs) {
		out["CacheRelocationPreflight/"+strconv.Itoa(i)] = p.Argv
	}
	for i, c := range CaptureScratchGuestCommands(self, "/Users/Shared/yolo-staged/capture") {
		out["CaptureScratchGuestCommands/"+strconv.Itoa(i)] = c
	}

	plan := BuildRunPlanWithDaemons("/Users/Shared/yolo/proj", provisionCfg(), []string{"claude"},
		[]string{"claude"}, "/opt/yolo/bin/yolo", "", HomeOverlay{},
		HostContext{Links: links, Relocations: relocs}, brokerEndpointEnv(), mockDarwin(), nil,
		openAIAdapterDaemons("/opt/yolo/share/yolo-jail/bin/darwin-arm64"), FloorStage{},
		PlanSession{ID: "self", Mode: ModeSelf})
	if plan.RunAs.Mode != ModeSelf {
		t.Fatalf("PlanSession.Mode did not reach the plan's crossing: %+v", plan.RunAs)
	}
	for name, argv := range map[string][]string{
		"plan/LaunchArgv": plan.LaunchArgv, "plan/ProvisionArgv": plan.ProvisionArgv,
		"plan/BootstrapArgv": plan.BootstrapArgv, "plan/JailDaemonArgv": plan.JailDaemonArgv,
		"plan/ProbeArgv": plan.ProbeArgv,
	} {
		if len(argv) == 0 {
			t.Fatalf("the self plan carries no %s; the fixture no longer exercises it", name)
		}
		out[name] = argv
	}
	for i, p := range plan.ContextPreflight {
		out["plan/ContextPreflight/"+strconv.Itoa(i)] = p.Argv
	}
	for i, p := range append(plan.CacheRelocationPreflight, plan.CacheRelocationProbes...) {
		out["plan/CacheRelocation/"+strconv.Itoa(i)] = p.Argv
	}
	return out
}

// SELF MODE CROSSES INTO NO ACCOUNT: no argv names sudo or the second account, and each starts
// with /usr/bin/env, the crossing's own first word.
func TestSelfModeArgvsCarryNoSudoOrAccount(t *testing.T) {
	for name, argv := range selfCrossingArgvs(t) {
		if len(argv) == 0 || argv[0] != envBin {
			t.Errorf("%s: self crossing does not start with %s: %q", name, envBin, argv)
		}
		for _, w := range argv {
			if strings.Contains(w, "sudo") || strings.Contains(w, "--user=") || w == "--set-home" {
				t.Errorf("%s: self-mode argv carries %q: %q", name, w, argv)
			}
		}
	}
}

// THE CALL-SITE PIN: no file but privilege.go and mode.go spells the sudo crossing. Two checks,
// so neither a direct RunAsSandboxArgv call nor a hand-built sudo argv slips past:
//
//   - a call to RunAsSandboxArgv, found in the parsed source (comments do not count);
//   - a string literal or constant naming sudo's `--user=` flag, or a `sudoBin` used as an
//     argv's first word, outside setup's root commands (commands.go: rootArgv and the sudo
//     probes there run as root or as you, never into the sandbox).
func TestEveryCrossingGoesThroughRunAs(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "privilege.go" || f == "mode.go" {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "RunAsSandboxArgv" {
					t.Errorf("%s calls RunAsSandboxArgv directly; cross through RunAs",
						fset.Position(x.Pos()))
				}
			case *ast.BasicLit:
				if x.Kind == token.STRING {
					if s, _ := strconv.Unquote(x.Value); strings.HasPrefix(s, "--user=") {
						t.Errorf("%s spells sudo's --user= flag; cross through RunAs",
							fset.Position(x.Pos()))
					}
				}
			case *ast.CompositeLit:
				if f == "commands.go" || len(x.Elts) == 0 {
					return true
				}
				if id, ok := x.Elts[0].(*ast.Ident); ok && id.Name == "sudoBin" {
					t.Errorf("%s builds a sudo argv by hand; cross through RunAs",
						fset.Position(x.Pos()))
				}
			}
			return true
		})
	}
	// The pin must be able to fail: privilege.go really does both.
	b, err := os.ReadFile("privilege.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"--user="`) || !strings.Contains(string(b), "func RunAsSandboxArgv(") {
		t.Fatal("privilege.go no longer holds the crossing; this pin checks nothing")
	}
}
