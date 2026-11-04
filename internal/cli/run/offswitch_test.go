package run

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
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
		// The project's spelling (docs/design/legacy-config-compat.md LC-D15): a legacy
		// workspace `enabled: false` converts to its `[requests.<pack>] off = true`, which keeps
		// the loophole off for that workspace alone, beside the user's configured profile.
		"TOML requests.aws-auth.off": func(packs []*packload.Pack) *jsonx.OrderedMap {
			awsAuthServedConfig(t, packs)
			offSwitchTOMLUser(t)
			cfg := bareConfig()
			cfg.Set("allow", newConfig("aws-auth", newConfig("profile", "yolo-unit")))
			cfg.Set("requests", newConfig("aws-auth", newConfig("off", true)))
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
	// The delegate declares platforms = ["linux"], so off Linux the plan never holds it.
	wantDelegate := runtime.GOOS == "linux"
	if got := o.plannedLoopholeNames("podman", tomlOn); !slices.Contains(got, "aws-auth") || slices.Contains(got, "cgroup-delegate") != wantDelegate {
		t.Fatalf("control: a TOML-configured aws-auth (and, on Linux only, the delegate) are planned host services, got %v", got)
	}
	tomlOff := newConfig("allow", newConfig(
		"aws-auth", newConfig("profile", "yolo-unit", "off", true),
		"cgroup-delegate", newConfig("off", true)))
	if got := o.plannedLoopholeNames("podman", tomlOff); slices.Contains(got, "aws-auth") || slices.Contains(got, "cgroup-delegate") {
		t.Errorf("allow.<pack>.off = true: planned host services = %v, want neither aws-auth nor cgroup-delegate", got)
	}
	stderr.Reset()
	o.noteLoopholesOff(tomlOff)
	if !strings.Contains(stderr.String(), "your user config sets `allow.aws-auth.off = true`") ||
		!strings.Contains(stderr.String(), "`yolo config unset allow.cgroup-delegate.off`") {
		t.Errorf("the launch does not disclose the TOML off switch by its key:\n%s", stderr.String())
	}

	// The project's spelling (LC-D15): `[requests.aws-auth] off = true` beside the user's
	// configured table keeps it out of this launch's plan, and the disclosure names the
	// workspace config and the command that turns it back on.
	projectOff := newConfig("allow", newConfig("aws-auth", newConfig("profile", "yolo-unit")),
		"requests", newConfig("aws-auth", newConfig("off", true)))
	if got := o.plannedLoopholeNames("podman", projectOff); slices.Contains(got, "aws-auth") {
		t.Errorf("requests.aws-auth.off = true: planned host services = %v, want no aws-auth", got)
	}
	stderr.Reset()
	o.noteLoopholesOff(projectOff)
	if !strings.Contains(stderr.String(), "`requests.aws-auth.off = true`") ||
		!strings.Contains(stderr.String(), "`yolo config unset requests.aws-auth.off --project`") {
		t.Errorf("the launch does not disclose the project's off switch by its key and its next step:\n%s", stderr.String())
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

// TestATOMLOffSwitchPromisesNoEndpoint: promisedEndpoints (what the assembler tells the jail it
// will find) promises aws-auth's endpoint when the TOML user config configures it, and none when
// that table says `off = true`. Its read of the launch block is what carries the switch.
func TestATOMLOffSwitchPromisesNoEndpoint(t *testing.T) {
	strictPackloadReads(t)
	packHome(t)
	t.Cleanup(loopholes.SnapshotPackModules())
	loopholes.SetPackModules(packLoopholeModules([]*packload.Pack{officialPack(t, "aws-auth")}))
	offSwitchTOMLUser(t)
	on := newConfig("allow", newConfig("aws-auth", newConfig("profile", "yolo-unit")))
	if got := promisedEndpoints("podman", on); !slices.Contains(got, "aws-auth") {
		t.Fatalf("control: a configured aws-auth is promised its endpoint, got %v", got)
	}
	off := newConfig("allow", newConfig("aws-auth", newConfig("profile", "yolo-unit", "off", true)))
	if got := promisedEndpoints("podman", off); slices.Contains(got, "aws-auth") {
		t.Errorf("allow.aws-auth.off = true: promised endpoints = %v, want no aws-auth", got)
	}
}

// TestATOMLOffSwitchSpawnsNothing: startLoopholes, the spawn itself, starts an off-switch
// loophole's daemon (a fixture standing in for aws-auth's, under its name) when the TOML user
// config does not turn it off, and starts nothing for it under `[allow.aws-auth] off = true`.
func TestATOMLOffSwitchSpawnsNothing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	const name = "aws-auth"
	socketDaemonFixture(t, name)
	offSwitchTOMLUser(t)
	spawned := func(cname string, cfg *jsonx.OrderedMap) bool {
		var buf strings.Builder
		o := &Options{}
		fillDefaults(o)
		o.Stdout, o.Stderr = &buf, &buf
		handles := o.startLoopholes(cname, "podman", cfg)
		socketsDir := hostServiceSocketsDir(cname, false)
		t.Cleanup(func() {
			o.stopLoopholes(handles, socketsDir, "", "")
			_ = os.RemoveAll(socketsDir)
		})
		for _, h := range handles {
			if h.name == name {
				return true
			}
		}
		return false
	}
	if !spawned("yolo-offswitch-on", newConfig("allow", newConfig(name, newConfig("profile", "yolo-unit")))) {
		t.Fatal("control: the fixture daemon is not started with no off switch")
	}
	if spawned("yolo-offswitch-off", newConfig("allow", newConfig(name, newConfig("profile", "yolo-unit", "off", true)))) {
		t.Error("allow.aws-auth.off = true: startLoopholes started the daemon")
	}
}
