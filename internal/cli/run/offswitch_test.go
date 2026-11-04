package run

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// offSwitchTOMLUser makes the test home's user config TOML, the format whose `[allow.<pack>]`
// tables carry a pack's settings and its `off` (loopholes.SelectionIsTheSwitch).
func offSwitchTOMLUser(t *testing.T) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("packs = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !loopholes.SelectionIsTheSwitch() {
		t.Fatalf("the fixture's user config is not read as TOML: %s", paths.UserConfigDisplayPath())
	}
}

// awsAuthOffConfigs is the two spellings of LC-D3's off switch beside a configured profile, the
// shape of the owner's own Bedrock file: "configured, and off".
func awsAuthOffConfigs(t *testing.T) map[string]func([]*packload.Pack) *jsonx.OrderedMap {
	return map[string]func([]*packload.Pack) *jsonx.OrderedMap{
		"legacy enabled: false": func(packs []*packload.Pack) *jsonx.OrderedMap {
			cfg := bareConfig()
			served, _ := awsAuthServedConfig(t, packs).Get("loopholes")
			entry, _ := served.(*jsonx.OrderedMap).Get("aws-auth")
			entry.(*jsonx.OrderedMap).Set("enabled", false)
			cfg.Set("loopholes", served)
			return cfg
		},
		"TOML allow.aws-auth.off": func(packs []*packload.Pack) *jsonx.OrderedMap {
			awsAuthServedConfig(t, packs) // records the selection's modules
			offSwitchTOMLUser(t)
			cfg := bareConfig()
			cfg.Set("allow", newConfig("aws-auth", newConfig("profile", "yolo-unit", "off", true)))
			return cfg
		},
	}
}

// TestOffAWSAuthServesNoPointer: aws-auth with a profile serves its credentials pointer to an
// agent on Bedrock (the control), and with the owner's `enabled: false`, or its TOML spelling,
// the same launch serves none (docs/design/legacy-config-compat.md LC-D3). The pointer is
// composed by the production served-set path (jailDaemonsFor's NewHostSet), so deleting the
// LaunchBlock read there, or Discover's honoring of the switch, fails this.
func TestOffAWSAuthServesNoPointer(t *testing.T) {
	packs := []string{"claude", "codex", "aws-auth", "bedrock"}
	tune := func(o *Options) { o.UseProfiles = map[string]string{"codex": "bedrock"} }
	control, _, _ := launchGateJail(t, packs, tune)
	if control.agentEnv("codex")["AWS_CONTAINER_CREDENTIALS_FULL_URI"] == "" {
		t.Fatal("control: codex on bedrock with aws-auth configured must receive the pointer")
	}
	for name, cfg := range awsAuthOffConfigs(t) {
		t.Run(name, func(t *testing.T) {
			jail, _, _ := launchGateJailWith(t, packs, cfg, gateCredentials(), tune)
			requireAbsent(t, "codex", jail.agentEnv("codex"), "AWS_CONTAINER_CREDENTIALS_FULL_URI")
		})
	}
}

// TestOffSwitchStartsNoHostService: the launch's host-service plan (plannedLoopholeNames, what the
// spawn and the keeper start) leaves out an off-switch loophole the config keeps off, aws-auth's
// minter and the cgroup delegate alike, and discloses each as off.
func TestOffSwitchStartsNoHostService(t *testing.T) {
	strictPackloadReads(t)
	home := packHome(t)
	var selected []*packload.Pack
	for _, name := range []string{"aws-auth", "cgroup-delegate"} {
		selected = append(selected, officialPack(t, name))
	}
	t.Cleanup(loopholes.SnapshotPackModules())
	loopholes.SetPackModules(packLoopholeModules(selected))
	profile := newConfig("loopholes", newConfig("aws-auth", newConfig("settings", newConfig("profile", "yolo-unit"))))

	o := goldenOptions(t.TempDir(), home)
	var stderr bytes.Buffer
	o.Stderr = &stderr
	if got := o.plannedLoopholeNames("podman", profile); !slices.Contains(got, "aws-auth") {
		t.Fatalf("control: a configured aws-auth is a planned host service, got %v", got)
	}

	off := newConfig("loopholes", newConfig(
		"aws-auth", newConfig("enabled", false, "settings", newConfig("profile", "yolo-unit")),
		"cgroup-delegate", newConfig("enabled", false)))
	if got := o.plannedLoopholeNames("podman", off); slices.Contains(got, "aws-auth") || slices.Contains(got, "cgroup-delegate") {
		t.Errorf("enabled: false: planned host services = %v, want neither aws-auth nor cgroup-delegate", got)
	}
	o.noteLoopholesOff(off)
	for _, name := range []string{"aws-auth", "cgroup-delegate"} {
		if !strings.Contains(stderr.String(), "loophole "+name+" is off for this launch: your config sets `loopholes."+name+".enabled: false`") {
			t.Errorf("the launch does not disclose %s as off:\n%s", name, stderr.String())
		}
	}

	// The TOML spelling, through the same plan: the control first, then `off = true`.
	offSwitchTOMLUser(t)
	tomlOn := newConfig("allow", newConfig("aws-auth", newConfig("profile", "yolo-unit")))
	if got := o.plannedLoopholeNames("podman", tomlOn); !slices.Contains(got, "aws-auth") || !slices.Contains(got, "cgroup-delegate") {
		t.Fatalf("control: a TOML-configured aws-auth and the delegate are planned host services, got %v", got)
	}
	tomlOff := newConfig("allow", newConfig(
		"aws-auth", newConfig("profile", "yolo-unit", "off", true),
		"cgroup-delegate", newConfig("off", true)))
	if got := o.plannedLoopholeNames("podman", tomlOff); slices.Contains(got, "aws-auth") || slices.Contains(got, "cgroup-delegate") {
		t.Errorf("allow.<pack>.off = true: planned host services = %v, want neither aws-auth nor cgroup-delegate", got)
	}
	stderr.Reset()
	o.noteLoopholesOff(tomlOff)
	if !strings.Contains(stderr.String(), "your config sets `allow.aws-auth.off = true`") ||
		!strings.Contains(stderr.String(), "`yolo config unset allow.cgroup-delegate.off`") {
		t.Errorf("the launch does not disclose the TOML off switch by its key:\n%s", stderr.String())
	}
}

// TestBothFreshLaunchArmsDiscloseLoopholesOff: the container arm (runContainer) and the macos-user
// arm (inside Run) each print the off-switch disclosure once, beside the empty-packs notice.
// noteLoopholesOff's own test above pins what it prints; this pins that a launch calls it, so
// deleting either call fails here.
func TestBothFreshLaunchArmsDiscloseLoopholesOff(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "run.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Run", "runContainer"} {
		var decl *ast.FuncDecl
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == name {
				decl = fd
			}
		}
		if decl == nil {
			t.Fatalf("run.go has no %s", name)
		}
		calls := 0
		ast.Inspect(decl, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "noteLoopholesOff" {
					calls++
				}
			}
			return true
		})
		if calls != 1 {
			t.Errorf("%s calls noteLoopholesOff %d times, want once: a launch discloses each loophole its config keeps off", name, calls)
		}
	}
}
