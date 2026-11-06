package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestNamedSetupsHaveDistinctHomes: two setups launched in one workspace each run in their own
// home, <ws>/.yolo/setups/<name>/home, and neither touches the other's (named-setups.md §9,
// named-setups-plan.md Slice 6a-ii). The second setup is a user-defined podman one, so the test
// needs no backend beyond the suite's own.
//
// The legacy variant is the test that fails if the launch stops moving the legacy home
// (adoptLegacyHome's call site, SUP-D19): a workspace that already holds `.yolo/home` launches
// with it moved to the selected setup's home, said once, and the jail reads what it held. Both
// markers sit under ~/.local, which podman binds from <home>/local (the home root is read-only).
func TestNamedSetupsHaveDistinctHomes(t *testing.T) {
	requireJail(t)

	t.Run("two setups", func(t *testing.T) {
		packHome(t, `{"setups": {"lab": {"confinement": "podman"}}}`)
		dir := writeProject(t, `{}`)
		homes := map[string]string{}
		for _, name := range []string{"podman-self", "lab"} {
			r := runYolo(t, dir, `printf 'from-%s' "$YOLO_SETUP" > ~/.local/setup-marker; echo "HOME-OF=$YOLO_SETUP"`,
				withEnv("YOLO_SETUP="+name))
			if r.rc != 0 || !strings.Contains(r.stdout, "HOME-OF="+name) {
				t.Fatalf("the %s launch failed (rc %d):\n%s", name, r.rc, r.combined())
			}
			homes[name] = paths.SetupHomeState(dir, name)
		}
		if homes["podman-self"] == homes["lab"] {
			t.Fatalf("both setups name one home %s", homes["lab"])
		}
		for name, home := range homes {
			got, err := os.ReadFile(filepath.Join(home, "local", "setup-marker"))
			if err != nil {
				t.Errorf("the %s launch's home %s holds no marker: %v", name, home, err)
				continue
			}
			if string(got) != "from-"+name {
				t.Errorf("the %s home holds %q, want its own launch's marker: the setups share a home", name, got)
			}
		}
		if _, err := os.Lstat(paths.LegacyHomeState(dir)); !os.IsNotExist(err) {
			t.Errorf("a launch made the legacy home %s (err %v)", paths.LegacyHomeState(dir), err)
		}
	})

	t.Run("legacy home moves", func(t *testing.T) {
		packHome(t, `{}`)
		dir := writeProject(t, `{}`)
		legacy := paths.LegacyHomeState(dir)
		if err := os.MkdirAll(filepath.Join(legacy, "local"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(legacy, "local", "carried-over"), []byte("legacy-bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
		r := runYolo(t, dir, `echo "READ=$(cat ~/.local/carried-over)"`, withEnv("YOLO_SETUP=podman-self"))
		if r.rc != 0 {
			t.Fatalf("the launch failed (rc %d):\n%s", r.rc, r.combined())
		}
		if !strings.Contains(r.stdout, "READ=legacy-bytes") {
			t.Errorf("the jail does not read the legacy home's file through its HOME:\n%s", r.combined())
		}
		if n := strings.Count(r.combined(), "Moved this project's home"); n != 1 {
			t.Errorf("the move's informational line was printed %d times, want once:\n%s", n, r.combined())
		}
		if _, err := os.Lstat(legacy); !os.IsNotExist(err) {
			t.Errorf("the legacy home is still in place (err %v)", err)
		}
		if got, err := os.ReadFile(filepath.Join(paths.SetupHomeState(dir, "podman-self"), "local", "carried-over")); err != nil || string(got) != "legacy-bytes" {
			t.Errorf("the legacy home did not move to podman-self's home: %q, %v", got, err)
		}
	})
}
