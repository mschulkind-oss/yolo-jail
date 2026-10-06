package run

// capturemisetools_test.go pins FP-D19 (docs/design/forked-programs-as-packs.md, OQ-FP10 ruled
// 2026-10-05; seal.go's jailMiseTools) from Run's own entry point: the user config's `mise_tools`
// reach no capture jail — a sealed build's (a fork's, a patched extension tree's) and an installer
// program's `yolo capture` jail alike — while the pack's own toolchain, the base's `node_floor`,
// still does.
//
// "INSTALLS NONE OF THEM" IS ASKED OF THE FILE MISE INSTALLS FROM. Each cell takes the argv the
// launch handed its runtime and renders, with the entrypoint's own code, what the jail renders
// from it: the global mise config (entrypoint.ConfigureMisePrism, whose [tools] the provisioning
// stage's `mise install` installs), yolo's pnpm launcher beside MISE_DISABLE_TOOLS (the two halves
// that must agree on who delivers pnpm, misepnpm_test.go), and the bootstrap script, whose
// `_yolo_node_floor` line is the node_floor's install. The ordinary launch of the same config is
// the control, so a withholding that happens for some other reason fails there. The real-jail half
// is integration/capturemisetools_test.go.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// captureMiseFloor is the base pack's declared node_floor, the toolchain a pack declares.
const captureMiseFloor = "22.19"

// captureMiseHome writes a user config selecting a base pack whose npm program declares a
// node_floor and a fork of it, and declaring two mise_tools of the user's own: a neovim nightly,
// the maintainer's case, and a bare pnpm, the key that moves MISE_DISABLE_TOOLS.
func captureMiseHome(t *testing.T) {
	t.Helper()
	home := packHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	packs := t.TempDir()
	for name, manifest := range map[string]string{
		"basepack": `{"contributes":[{"kind":"program","bin":"tool","via":"npm","package":"tool",` +
			`"node_floor":"` + captureMiseFloor + `"}]}`,
		"forkpack": `{"contributes":[{"kind":"program","bin":"tool","via":"source","fork_of":"basepack",` +
			`"source":"` + forkPinSource + `","build":"make install","produces":[".local/bin/tool"]}]}`,
	} {
		if err := os.MkdirAll(filepath.Join(packs, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(packs, name, "pack.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeUserConfig(t, home, `{
  "packs": [{"source": "file://`+filepath.Join(packs, "basepack")+`", "name": "basepack"},
            {"source": "file://`+filepath.Join(packs, "forkpack")+`", "name": "forkpack"}],
  "mise_tools": {"neovim": "nightly", "pnpm": "9.12.0"}
}
`)
}

// captureMiseUserTools are the fixture's own mise_tools keys.
var captureMiseUserTools = []string{"neovim", "pnpm"}

// jailMiseView is what the jail renders from a launch's argv: its global mise config, whether it
// wrote yolo's pnpm launcher, the tools MISE_DISABLE_TOOLS hides, and its bootstrap script.
type jailMiseView struct {
	miseConfig       string
	pnpmLauncher     bool
	miseHides        []string
	bootstrap        string
	yoloMiseTools    string
	yoloMiseToolsSet bool
}

// renderJailMise renders argv's jail the way its boot does, against throwaway homes: the pack
// tree is read from the host directory the argv binds at YOLO_PACK_ROOT, as snap holds it — a copy
// of the agents dir taken while the runtime ran, since the launch removes the tree once its
// container is gone.
func renderJailMise(t *testing.T, argv []string, snap string) jailMiseView {
	t.Helper()
	vars := envPairs(argv)
	var v jailMiseView
	v.yoloMiseTools, v.yoloMiseToolsSet = vars["YOLO_MISE_TOOLS"]
	v.miseHides = strings.Split(vars["MISE_DISABLE_TOOLS"], ",")
	t.Cleanup(packload.OverrideSkewTolerance(false))
	t.Cleanup(entrypoint.OverrideImageProbeBase(t.TempDir()))
	t.Cleanup(entrypoint.OverrideWorkspaceMisePath(filepath.Join(t.TempDir(), "mise.toml")))

	home := t.TempDir()
	jail := map[string]string{"JAIL_HOME": home, "HOME": home, "MISE_DATA_DIR": filepath.Join(home, "mise")}
	for _, k := range []string{"YOLO_MISE_TOOLS", "MISE_DISABLE_TOOLS"} {
		jail[k] = vars[k]
	}
	e := entrypoint.NewEnv(jail)
	if err := entrypoint.ConfigureMisePrism(e); err != nil {
		t.Fatalf("ConfigureMisePrism: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(home, ".config", "mise", "config.toml"))
	if err != nil {
		t.Fatalf("the jail rendered no global mise config: %v", err)
	}
	v.miseConfig = string(body)
	if err := entrypoint.GeneratePackageManagerLaunchers(e); err != nil {
		t.Fatalf("GeneratePackageManagerLaunchers: %v", err)
	}
	_, err = os.Stat(filepath.Join(e.LaunchDir(), "pnpm"))
	v.pnpmLauncher = err == nil

	root := vars["YOLO_PACK_ROOT"]
	if root == "" {
		t.Fatal("the launch hands its jail no YOLO_PACK_ROOT")
	}
	src, ok := sealedBindSources(argv)[root]
	if !ok {
		t.Fatalf("the argv binds nothing at YOLO_PACK_ROOT %s", root)
	}
	rel, err := filepath.Rel(paths.AgentsDir(), src)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("the pack tree %s is not under the agents dir %s", src, paths.AgentsDir())
	}
	vars["YOLO_PACK_ROOT"] = filepath.Join(snap, rel)
	v.bootstrap = entrypoint.BootstrapScript(&entrypoint.Env{Home: t.TempDir(), Vars: vars})
	return v
}

// nodeFloorLine is how the bootstrap's install of the fixture's node_floor begins.
const nodeFloorLine = "_yolo_node_floor " + captureMiseFloor + " "

// captureMiseLaunch runs one launch of the fixture: an ordinary one, a sealed build of the fork,
// or a plain capture (the installer capture's jail: no capture store, never sealed).
func captureMiseLaunch(t *testing.T, kind string) (jailMiseView, string) {
	t.Helper()
	captureMiseHome(t)
	snap := filepath.Join(t.TempDir(), "agents")
	onRun := "cp -a " + shquote.Quote(paths.AgentsDir()) + " " + shquote.Quote(snap)
	argv, printed := fakePodmanLaunchIn(t, t.TempDir(), onRun, func(o *Options) {
		o.NeverAttach, o.AcceptConfigChanges = true, true
		switch kind {
		case "sealed":
			o.Sealed, o.OnlyPacks = true, []string{"forkpack", "basepack"}
			o.CapturesDir = func() string { return "" }
		case "capture":
			o.CapturesDir = func() string { return "" }
		}
	})
	if argv == nil {
		t.Fatalf("the %s launch never reached the runtime:\n%s", kind, printed)
	}
	return renderJailMise(t, argv, snap), printed
}

// THE CONTROL: an ordinary launch of the fixture hands its jail every mise tool, mise lists them,
// and mise rather than yolo delivers the declared pnpm. So each withholding below is the rule's.
func TestAnOrdinaryLaunchHandsItsJailTheUsersMiseTools(t *testing.T) {
	v, printed := captureMiseLaunch(t, "ordinary")
	for _, tool := range captureMiseUserTools {
		if !strings.Contains(v.miseConfig, tool+" = ") {
			t.Errorf("an ordinary launch's jail mise config does not list %s, so the site is unexercised:\n%s",
				tool, v.miseConfig)
		}
	}
	if v.pnpmLauncher || slices.Contains(v.miseHides, "pnpm") {
		t.Errorf("an ordinary launch with a declared mise pnpm: yolo launcher %v, MISE_DISABLE_TOOLS %v; "+
			"mise should deliver it", v.pnpmLauncher, v.miseHides)
	}
	if !strings.Contains(v.bootstrap, nodeFloorLine) {
		t.Errorf("an ordinary launch's bootstrap does not install the base's node_floor:\n%s", v.bootstrap)
	}
	if strings.Contains(printed, "mise_tools") {
		t.Errorf("an ordinary launch says something about mise_tools:\n%s", printed)
	}
}

// A SEALED BUILD with user mise_tools installs none of them, still installs its base's
// node_floor, and says in its withheld line how many of the user's tools it withheld.
func TestASealedBuildInstallsNoneOfTheUsersMiseTools(t *testing.T) {
	v, printed := captureMiseLaunch(t, "sealed")
	assertCaptureJailHasNoUserMiseTools(t, "a sealed build", v)
	if !strings.Contains(v.bootstrap, nodeFloorLine) {
		t.Errorf("a sealed build's bootstrap no longer installs its base's node_floor %s, which "+
			"FP-D19 keeps:\n%s", captureMiseFloor, v.bootstrap)
	}
	const withheld = "2 of your mise_tools are withheld"
	if !strings.Contains(printed, withheld) {
		t.Errorf("a sealed build does not say %q:\n%s", withheld, printed)
	}
}

// A PLAIN CAPTURE — an installer program's `yolo capture` jail, unsealed — likewise installs none
// of the user's mise_tools (FP-D19 widens OQ-FP10 to every capture jail), and its packs'
// node_floor still installs.
func TestAPlainCaptureInstallsNoneOfTheUsersMiseTools(t *testing.T) {
	v, _ := captureMiseLaunch(t, "capture")
	assertCaptureJailHasNoUserMiseTools(t, "a plain capture", v)
	if !strings.Contains(v.bootstrap, nodeFloorLine) {
		t.Errorf("a plain capture's bootstrap does not install the base's node_floor:\n%s", v.bootstrap)
	}
}

// assertCaptureJailHasNoUserMiseTools: the jail is handed none of the user's tools, its mise
// config lists none, and the pnpm the user declared is yolo's again — its launcher written and
// mise kept off the name — because the jail no longer holds the declaration that stood both down.
func assertCaptureJailHasNoUserMiseTools(t *testing.T, what string, v jailMiseView) {
	t.Helper()
	if !v.yoloMiseToolsSet || v.yoloMiseTools != "{}" {
		t.Errorf("%s hands its jail YOLO_MISE_TOOLS=%q (set %v), want none of the user's: {}",
			what, v.yoloMiseTools, v.yoloMiseToolsSet)
	}
	for _, tool := range captureMiseUserTools {
		if strings.Contains(v.miseConfig, tool) {
			t.Errorf("%s's jail mise config lists the user's %s, so its mise install installs it:\n%s",
				what, tool, v.miseConfig)
		}
	}
	if !v.pnpmLauncher || !slices.Contains(v.miseHides, "pnpm") {
		t.Errorf("%s: yolo's pnpm launcher written %v, MISE_DISABLE_TOOLS %v; with no mise pnpm in the "+
			"jail, both halves must hand pnpm back to yolo", what, v.pnpmLauncher, v.miseHides)
	}
}
