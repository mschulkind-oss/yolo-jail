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
	ws := t.TempDir()
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
