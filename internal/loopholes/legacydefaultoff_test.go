package loopholes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// legacydefaultoff_test.go pins the launch half of the legacy manifest window
// (docs/design/legacy-config-compat.md LC-D10): a legacy manifest's `default_enabled: false` ran
// its loophole in v0.15.1 only where a config switched it on. Its host daemon is MEDIATED in shape
// (a daemon answers each request), but a daemon written for v0.15.1 reads no grant file, so nothing
// of it starts or crosses until the launch holds its grant: the switch the legacy projection makes
// that grant of.

// legacyDefaultOffSet writes a pack module whose legacy manifest.jsonc declares a host daemon, a
// bind and `default_enabled: false`, and returns the set a launch reads.
func legacyDefaultOffSet(t *testing.T) Set {
	t.Helper()
	unsetJail(t)
	module := filepath.Join(t.TempDir(), "note-relay")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "notes.txt"), []byte("# fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "name": "note-relay",
  "description": "Relay notes from the host into the jail.",
  "transport": "none",
  "default_enabled": false,
  "host_daemon": {"cmd": ["/bin/true"], "publishes": "socket"},
  "host_bind_mounts": [{"host": "{loophole_dir}/notes.txt", "container": "/run/notes.txt", "readonly": true}]
}`
	if err := os.WriteFile(filepath.Join(module, loopholedecl.ManifestName), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return NewSet(DiscoverOptions{PackModules: []PackModule{{Dir: module, Pack: "note-relay", HostExecApproved: true}}})
}

func TestALegacyDefaultOffLoopholeStartsNothingUntilGranted(t *testing.T) {
	set := legacyDefaultOffSet(t)
	lp, ok := set.Lookup("note-relay")
	if !ok || !lp.LegacyDefaultOff {
		t.Fatalf("the record does not carry the manifest's default_enabled: false: %+v", lp)
	}
	daemons := func(s Set) []string {
		specs := s.ManifestHostDaemonSpecs(s.Enabled())
		if specs == nil {
			return nil
		}
		return specs.Keys()
	}
	binds := func(s Set) bool {
		return strings.Contains(strings.Join(s.RuntimeArgsFor(s.Enabled(), "podman"), " "), "/run/notes.txt")
	}
	for _, tc := range []struct {
		name    string
		granted map[string][]string
	}{
		{"no grants handed", nil},
		{"another loophole's grant", map[string][]string{"journal": {"read"}}},
		{"the kind not granted", map[string][]string{"note-relay": nil}},
	} {
		without := set.WithGrants(tc.granted)
		if got := daemons(without); len(got) != 0 {
			t.Errorf("%s: the host daemon of a loophole v0.15.1 kept off would start: %v", tc.name, got)
		}
		if binds(without) {
			t.Errorf("%s: its bind crosses into the jail", tc.name)
		}
	}
	with := set.WithGrants(map[string][]string{"note-relay": {loopholedecl.LegacyRunGrantKind}})
	if got := daemons(with); len(got) != 1 || got[0] != "note-relay" {
		t.Errorf("granted, the host daemons are %v, want note-relay's", got)
	}
	if !binds(with) {
		t.Errorf("granted, its bind does not cross")
	}
}
