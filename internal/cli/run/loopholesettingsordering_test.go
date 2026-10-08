package run

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func sourceFunction(t *testing.T, path, name string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, data, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name || fn.Body == nil {
			continue
		}
		start := fset.Position(fn.Body.Pos()).Offset
		end := fset.Position(fn.Body.End()).Offset
		return string(data[start:end])
	}
	t.Fatalf("%s has no function %s", path, name)
	return ""
}

func assertCallOrder(t *testing.T, body string, before, after string) {
	t.Helper()
	b, a := strings.Index(body, before), strings.Index(body, after)
	if b < 0 || a < 0 || b >= a {
		t.Fatalf("expected %q before %q in production call site (positions %d, %d)", before, after, b, a)
	}
}

// The settings validator is executable pack code, so its host-execution disclosure and the
// settings check must precede provisioning. Keep one assertion at each production boundary: a
// helper-only test stays green if a caller stops using the preflight.
func TestSettingsValidatorDisclosurePrecedesPackCodeExecution(t *testing.T) {
	prepare := sourceFunction(t, "loopholesettings.go", "prepareLoopholeSettings")
	assertCallOrder(t, prepare, "discloseLoopholeSettingsResolved", "RunSettingsCheck")
}

func TestRunSettingsPreflightPrecedesExpensiveFreshLaunchWork(t *testing.T) {
	run := sourceFunction(t, "run.go", "Run")
	assertCallOrder(t, run, "discloseSettingsCheckHostExec", "prepareLoopholeSettingsForStart")
	assertCallOrder(t, run, "prepareLoopholeSettingsForStart", "autoCaptureMacosUser")

	container := sourceFunction(t, "run.go", "runContainer")
	assertCallOrder(t, container, "discloseSettingsCheckHostExec", "prepareLoopholeSettingsForStart")
	for _, expensive := range []string{"autoCaptureInstallerPrograms", "runForkBuildSlot", "autoLoadImage"} {
		assertCallOrder(t, container, "prepareLoopholeSettingsForStart", expensive)
	}
}

func TestHostDoorwaySettingsPreflightPrecedesServiceStart(t *testing.T) {
	start := sourceFunction(t, "hostdoorways.go", "Start")
	assertCallOrder(t, start, "discloseSettingsCheckHostExec", "prepareLoopholeSettingsForStart")
	assertCallOrder(t, start, "prepareLoopholeSettingsForStart", "startLoopholesMatching")
}

func TestKeeperConsumesTheLaunchFrozenSettingsPlan(t *testing.T) {
	spawn := sourceFunction(t, "keeperspawn.go", "keeperPlanFor")
	if !strings.Contains(spawn, "Settings: o.frozenLoopholeSettings()") ||
		!strings.Contains(spawn, "SettingsPrepared: o.settingsPrepared") {
		t.Fatal("keeper plan does not carry the prepared settings bytes and state")
	}
	newKeeper := sourceFunction(t, "keeper.go", "newKeeper")
	assertCallOrder(t, newKeeper, "o.settingsPrepared = plan.SettingsPrepared", "o.settingsFrozen = plan.Settings")
	assertCallOrder(t, newKeeper, "o.settingsFrozen = plan.Settings", "k.o = &o")
}
