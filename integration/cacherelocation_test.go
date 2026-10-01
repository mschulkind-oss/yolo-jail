package integration

// cacherelocation_test.go is the podman end-to-end check of `cache_relocations`
// (docs/plans/cache-relocation.md#test-plan): a cache subdir relocated in the USER config is a
// read-write bind nested inside the `~/.cache` bind, so a file the host put at the target is
// readable in the jail and a file the jail writes lands at the target, not in the machine
// cache's own copy of that subdir.
//
// The plan omitted this test while the harness ran every launch against the developer's real
// HOME, since the key is read from the user config alone. isolateHome now writes that config
// into a temp home, so the key can be placed without touching anyone's own file.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestACacheRelocationReceivesTheJailsWrites(t *testing.T) {
	requireJail(t)
	if rt := detectRuntime(); rt != "podman" {
		// Apple Container skips the key with a warning and macos-user cannot mount at all;
		// each has its own test of what it says instead.
		t.Skipf("cache_relocations is mounted on podman only (runtime %q)", rt)
	}

	// The subdir is per process: the machine cache is shared across runs (packHomeSharedStores),
	// and the launch creates its mountpoint there.
	subdir := fmt.Sprintf("yolo-it-relocation-%d", os.Getpid())
	target := filepath.Join(resolvedTempDir(t), "relocated")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	const seed = "seeded-on-the-host"
	if err := os.WriteFile(filepath.Join(target, "seed"), []byte(seed+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	packHome(t, fmt.Sprintf(`{"cache_relocations": {%q: %q}}`, subdir, target))
	// The mountpoint the launch creates in the machine cache, and the one file a failed
	// relocation would have let the jail write into it: nothing else, since the cache is shared.
	stub := filepath.Join(paths.GlobalCache(), subdir)
	t.Cleanup(func() {
		_ = os.Remove(filepath.Join(stub, "from-jail"))
		_ = os.Remove(stub)
	})
	dir := writeProject(t, `{}`)

	in := "~/.cache/" + subdir
	r := runYolo(t, dir, strings.Join([]string{
		`echo "=== SEED ==="`,
		`cat ` + in + `/seed`,
		`echo "=== END ==="`,
		`echo written-in-the-jail > ` + in + `/from-jail`,
	}, "; "))
	if r.rc != 0 {
		t.Fatalf("launch: rc %d\nstdout: %s\nstderr: %s", r.rc, r.stdout, r.stderr)
	}
	if got := strings.TrimSpace(section(r.stdout, "=== SEED ===", "=== END ===")); got != seed {
		t.Errorf("in the jail, %s/seed read %q, want the host target's %q: the relocated subdir "+
			"is not the target", in, got, seed)
	}
	data, err := os.ReadFile(filepath.Join(target, "from-jail"))
	if err != nil || strings.TrimSpace(string(data)) != "written-in-the-jail" {
		t.Errorf("the jail's write did not land at the relocation target %s: %q (%v)\nstdout: %s",
			target, data, err, r.stdout)
	}
	if _, err := os.Stat(filepath.Join(stub, "from-jail")); err == nil {
		t.Errorf("the jail's write landed in the machine cache's own %s, under the relocation", stub)
	}
}
