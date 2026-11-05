package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

type fenceResolver struct{ known map[string]LoopholeInfo }

func (r fenceResolver) Known() (map[string]LoopholeInfo, bool) { return r.known, true }

var brokeredResolver = fenceResolver{known: map[string]LoopholeInfo{
	"github-broker": {Name: "github-broker", HasHostDaemon: true, Brokered: &loopholedecl.Brokered{
		Source: "github", RemoteHost: "github.com", CredentialPaths: []string{"$GH_CONFIG_DIR", "~/.config/gh"}}},
}}

// fenceCase validates a merged config whose workspace half is wsMounts and whose user half
// is userMounts, returning the fence's errors and warnings.
func fenceCase(t *testing.T, resolver LoopholeResolver, wsMounts, userMounts []string) (errs, warns []string) {
	t.Helper()
	t.Setenv("YOLO_VERSION", "") // the fence stands down in a jail; these are host cases
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("GH_CONFIG_DIR", "")
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	quote := func(l []string) string {
		q := make([]string, len(l))
		for i, s := range l {
			q[i] = `"` + strings.ReplaceAll(s, "~", home) + `"`
		}
		return strings.Join(q, ",")
	}
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"),
		[]byte(`{"mounts": [`+quote(wsMounts)+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	merged := decode(t, `{"mounts": [`+quote(append(append([]string(nil), wsMounts...), userMounts...))+`]}`)
	e, w := &[]string{}, &[]string{}
	validateBrokerMountFence(merged, ws, resolver, e, w)
	return *e, *w
}

// §12 done criterion 14: with the pack selected, a workspace mount of the state dir, of the
// broker dir, or of the host gh config refuses; the same at user scope is disclosed.
func TestTheMountFenceRefusesAWorkspaceMountAndDisclosesAUserOne(t *testing.T) {
	for name, src := range map[string]string{
		"the state dir contains the broker dir": "~/.local/share/yolo-jail:/ctx/state",
		"inside the broker dir":                 "~/.local/share/yolo-jail/broker/github:/ctx/b",
		"the host gh config":                    "~/.config/gh:/ctx/gh",
		"the whole home contains everything":    "~:/ctx/home",
	} {
		t.Run(name, func(t *testing.T) {
			errs, warns := fenceCase(t, brokeredResolver, []string{src}, nil)
			if len(errs) != 1 || !strings.Contains(errs[0], "A workspace config may not mount it") || len(warns) != 0 {
				t.Fatalf("errs %q warns %q", errs, warns)
			}
			errs, warns = fenceCase(t, brokeredResolver, nil, []string{src})
			if len(errs) != 0 || len(warns) != 1 || !strings.Contains(warns[0], "(user config)") {
				t.Fatalf("user scope: errs %q warns %q", errs, warns)
			}
		})
	}
}

func TestTheMountFenceLeavesOtherMountsAlone(t *testing.T) {
	errs, warns := fenceCase(t, brokeredResolver, []string{"~/code/lib:/ctx/lib"}, []string{"~/.local/share/yolo-jail/logs:/ctx/logs"})
	if len(errs) != 0 || len(warns) != 0 {
		t.Fatalf("errs %q warns %q", errs, warns)
	}
}

func TestTheMountFenceCoversSelectedHostDaemonStateDirectories(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	name := "fixture-daemon"
	stateRoot, _ := filepath.EvalSymlinks(t.TempDir())
	stateDir := filepath.Join(stateRoot, "state", name)
	resolver := fenceResolver{known: map[string]LoopholeInfo{
		name: {Name: name, HasHostDaemon: true, StateDir: stateDir},
	}}
	for label, source := range map[string]string{
		"exact directory":            stateDir,
		"containing state directory": filepath.Dir(stateDir),
		"descendant":                 filepath.Join(stateDir, "credentials.json"),
	} {
		t.Run(label, func(t *testing.T) {
			mount := source + ":/ctx/credentials"
			errs, warns := fenceCase(t, resolver, []string{mount}, nil)
			if len(errs) != 1 || len(warns) != 0 {
				t.Fatalf("workspace scope: errs %q warns %q", errs, warns)
			}
			for _, want := range []string{name, "the state of the fixture-daemon host daemon, which holds credentials the jail must not read", "Remove the entry", paths.UserConfigDisplayPath()} {
				if !strings.Contains(errs[0], want) {
					t.Errorf("refusal %q does not contain %q", errs[0], want)
				}
			}
			errs, warns = fenceCase(t, resolver, nil, []string{mount})
			if len(errs) != 0 || len(warns) != 1 || !strings.Contains(warns[0], "(user config)") || !strings.Contains(warns[0], "jail can read it") || !strings.Contains(warns[0], "Remove the entry if the jail should not have this access") {
				t.Fatalf("user scope: errs %q warns %q", errs, warns)
			}
		})
	}
}

func TestTheMountFenceDoesNotCoverUnselectedHostDaemonState(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	name := "fixture-daemon"
	stateRoot, _ := filepath.EvalSymlinks(t.TempDir())
	stateDir := filepath.Join(stateRoot, "state", name)
	for label, resolver := range map[string]LoopholeResolver{
		"no selected loopholes": fenceResolver{known: map[string]LoopholeInfo{}},
		"selected loophole without host daemon": fenceResolver{known: map[string]LoopholeInfo{
			"fixture-device": {Name: "fixture-device", StateDir: stateDir},
		}},
		"different selected host daemon": fenceResolver{known: map[string]LoopholeInfo{
			"another-daemon": {Name: "another-daemon", HasHostDaemon: true, StateDir: paths.LoopholeStateDir("another-daemon")},
		}},
	} {
		t.Run(label, func(t *testing.T) {
			errs, warns := fenceCase(t, resolver, []string{stateDir + ":/ctx/credentials:ro"}, nil)
			if len(errs) != 0 || len(warns) != 0 {
				t.Fatalf("errs %q warns %q", errs, warns)
			}
		})
	}
}

// ValidateConfig is the production caller: removing its fence invocation leaves this test green
// only if the new selected-host-daemon rule remains enforced through the launch/check path.
func TestValidateConfigRunsTheHostDaemonStateFence(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	name := "fixture-daemon"
	stateDir := paths.LoopholeStateDir(name)
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	spec := stateDir + ":/ctx/credentials"
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(`{"mounts": ["`+spec+`"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := fenceResolver{known: map[string]LoopholeInfo{
		name: {Name: name, HasHostDaemon: true, StateDir: stateDir},
	}}
	errs, _ := ValidateConfig(decode(t, `{"mounts": ["`+spec+`"]}`), ws, resolver)
	if !strings.Contains(strings.Join(errs, "\n"), "A workspace config may not mount it") ||
		!strings.Contains(strings.Join(errs, "\n"), "fixture-daemon") {
		t.Fatalf("errs %q", errs)
	}
}

// Without a selected brokered pack there is nothing to fence.
func TestTheMountFenceIsArmedBySelection(t *testing.T) {
	none := fenceResolver{known: map[string]LoopholeInfo{"journal": {Name: "journal"}}}
	errs, warns := fenceCase(t, none, []string{"~/.config/gh:/ctx/gh"}, nil)
	if len(errs) != 0 || len(warns) != 0 {
		t.Fatalf("errs %q warns %q", errs, warns)
	}
}

// A symlink cannot walk around the fence.
func TestTheMountFenceFollowsSymlinks(t *testing.T) {
	t.Setenv("YOLO_VERSION", "") // the fence stands down in a jail; these are host cases
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	if err := os.MkdirAll(paths.BrokerDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "innocent")
	if err := os.Symlink(paths.BrokerDir(), link); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(`{"mounts": ["`+link+`:/ctx/x"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e, w := &[]string{}, &[]string{}
	validateBrokerMountFence(decode(t, `{"mounts": ["`+link+`:/ctx/x"]}`), ws, brokeredResolver, e, w)
	if len(*e) != 1 {
		t.Fatalf("a symlink to the broker dir passed the fence: %q %q", *e, *w)
	}
}

// ValidateConfig is the call site: the launch and `yolo check` both run it.
func TestValidateConfigRunsTheMountFence(t *testing.T) {
	t.Setenv("YOLO_VERSION", "") // the fence stands down in a jail; these are host cases
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	ws := t.TempDir()
	spec := home + "/.config/gh:/ctx/gh"
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(`{"mounts": ["`+spec+`"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	errs, _ := ValidateConfig(decode(t, `{"mounts": ["`+spec+`"]}`), ws, brokeredResolver)
	if !strings.Contains(strings.Join(errs, "\n"), "A workspace config may not mount it") {
		t.Fatalf("errs %q", errs)
	}
}

// TestValidateConfigFencesSelectedCircleCIXDGCredentials exercises the real
// selected CircleCI declaration through the production config-validation caller.
func TestValidateConfigFencesSelectedCircleCIXDGCredentials(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	xdg, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", xdg)
	module := filepath.Join("..", "..", "packs", "circleci", "loopholes", "circleci-broker")
	manifest, err := loopholedecl.LoadDir(module)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.CallBroker == nil || len(manifest.CallBroker.CredentialPaths) < 2 {
		t.Fatalf("selected CircleCI credential declaration absent: %+v", manifest.CallBroker)
	}
	resolver := fenceResolver{known: map[string]LoopholeInfo{
		"circleci-broker": {Name: "circleci-broker", HasHostDaemon: true, CallBroker: manifest.CallBroker},
	}}
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.Mkdir(filepath.Join(xdg, "circleci"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "linked-circleci")
	if err := os.Symlink(filepath.Join(xdg, "circleci"), link); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{xdg, filepath.Join(xdg, "circleci"), filepath.Join(xdg, "circleci", "config.yml"), link} {
		spec := source + ":/ctx/circleci"
		body := `{"mounts":["` + spec + `"]}`
		if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		errs, _ := ValidateConfig(decode(t, body), ws, resolver)
		if !strings.Contains(strings.Join(errs, "\n"), "A workspace config may not mount it") {
			t.Errorf("XDG credential source %s not refused: %q", source, errs)
		}
	}
	for _, source := range []string{filepath.Join(home, "cache"), filepath.Join(xdg, "other-cache")} {
		spec := source + ":/ctx/cache"
		errs, _ := ValidateConfig(decode(t, `{"mounts":["`+spec+`"]}`), ws, resolver)
		if len(errs) != 0 {
			t.Errorf("unrelated source %s refused: %q", source, errs)
		}
	}
}

// countingResolver counts Known() calls. The real resolver is not free, and not pure: its first
// answer resolves the configured packs from the store and is memoized for the process.
type countingResolver struct {
	fenceResolver
	calls *int
}

func (r countingResolver) Known() (map[string]LoopholeInfo, bool) {
	*r.calls++
	return r.fenceResolver.Known()
}

// A config with no `mounts` has nothing to fence, so the fence must not ask for the loophole
// set at all. It did, on every ValidateConfig, and the answer was not free: the real resolver's
// first call resolves the configured packs and memoizes them for the process. In
// internal/cli's suite that made `yolo check` under one test's home fix the pack loopholes that
// a later test's `yolo loopholes status` executed the doctors of, under another home.
func TestTheMountFenceAsksForTheLoopholeSetOnlyWhenThereIsAMount(t *testing.T) {
	t.Setenv("YOLO_VERSION", "") // the fence stands down in a jail; these are host cases
	os.Unsetenv("YOLO_VERSION")
	calls := 0
	r := countingResolver{brokeredResolver, &calls}
	for _, cfg := range []string{`{}`, `{"mounts": []}`, `{"packs": ["claude"]}`} {
		e, w := &[]string{}, &[]string{}
		validateBrokerMountFence(decode(t, cfg), t.TempDir(), r, e, w)
		if calls != 0 || len(*e) != 0 || len(*w) != 0 {
			t.Fatalf("%s: Known() called %d times, errs %q warns %q", cfg, calls, *e, *w)
		}
	}
	// The control: a mount does consult it, so the zero above is about the missing mount.
	fenceCase(t, r, []string{"~/code/lib:/ctx/lib"}, nil)
	if calls != 1 {
		t.Fatalf("with a mount, Known() was called %d times, want 1", calls)
	}
}
