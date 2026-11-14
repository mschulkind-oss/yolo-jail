package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// `yolo stores` ON A MAC THAT RUNS macos-user. The inventory used to omit that backend's storage
// entirely: the host-owned copies each launch stages under macosuser.StateDir(), and the sandbox
// account's machine-tier stores in /Users/_yolojail. It now lists them, as the host user, with no
// sudo (internal/cli/stores/inventory.go, macosUserStores).
//
// WHAT THIS SETTLES. Reading the sandbox home without sudo is an INFERENCE from the account's
// layout (the home is 0750, owned by the sandbox account, and yolo-account create puts the invoking user
// in its group); only a Mac can say whether the walk gets in. So the mise store must come back
// measured or partial, never unknown. The session puts a probe file in that store first, so the
// row has something to measure on a machine whose store a previous run left empty, and removes it
// before it ends. The packs row must be measured: a launch with a pack staged a copy there.
//
// THE CONTAINER IMAGE STORE IS A CONTAINER SOURCE'S SECTION (named-setups-plan.md SUP-D57, SUP-D58).
// There is no macos-user image row: `yolo stores` collects the image section once per container
// source that `yolo ps`'s discovery listed (Podman, Apple Container), each row tagged with that
// source's confinement and keyed `images.<row>` for Podman and `images.<row>@apple-container`
// otherwise. A Mac whose only listed source is Seatbelt has no image section at all. The Linux
// halves are internal/cli/stores TestAMacosUserInventoryHasNoContainerSectionWithoutAContainerSource
// and TestEachTechnologysSectionsAppearOncePerListedSource; this reads the same shape off a real Mac,
// whatever container runtimes it has.

// storesJSON is the part of `yolo stores --format json` this test reads.
type storesJSON struct {
	Sources []struct {
		Confinement, State string
	} `json:"sources"`
	Stores []struct {
		Key, Section, Sizing, Reason, Note, Confinement string
		Bytes                                           int64
	} `json:"stores"`
}

// TestMacosUserStoresListsTheSandboxAccount: with a macos-user session up, `yolo stores` lists the
// state dir's packs copies as measured, the sandbox account's mise store as measured or partial,
// and the container image store only as the rows of each container source that listed.
func TestMacosUserStoresListsTheSandboxAccount(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["claude"]}`)
	ws := macosUserWorkspace(t, `{}`)
	sync := macosUserSyncDir(t, ws, "stores-sync")
	probe := fmt.Sprintf(`"${MISE_DATA_DIR:?}/yolo-it-stores-probe-%d"`, os.Getpid())
	session := holdSession(t, ws, sync, heldSessionScript(sync,
		"mkdir -p "+probe+" && printf probe > "+probe+"/f",
		"rm -rf "+probe))
	session.awaitUp(t)

	r := runCommand(t, ws, []string{"stores", "--format", "json", "--no-record"}, macosUserRunEnv("account"))
	var rep storesJSON
	if r.rc != 0 || json.Unmarshal([]byte(r.stdout), &rep) != nil {
		t.Fatalf("`yolo stores --format json` rc %d did not print an inventory:\n%s", r.rc, r.combined())
	}
	rows := map[string]int{}
	for i, s := range rep.Stores {
		rows[s.Key] = i
		if s.Section == "macos-user sandbox account" {
			t.Logf("STORES ROW %s: %s %d B (%s)", s.Key, s.Sizing, s.Bytes, s.Reason)
		}
	}
	get := func(key string) (string, int64, bool) {
		i, ok := rows[key]
		if !ok {
			return "", 0, false
		}
		return rep.Stores[i].Sizing, rep.Stores[i].Bytes, true
	}
	if sizing, bytes, ok := get("macos.state.packs"); !ok || sizing != "measured" || bytes == 0 {
		t.Errorf("macos.state.packs = %q %d B (listed %v); want measured and non-empty: this "+
			"session's launch staged a pack tree under %s", sizing, bytes, ok,
			filepath.Join(macosuser.StateDir(), "packs"))
	}
	if sizing, bytes, ok := get("macos.home.mise"); !ok || (sizing != "measured" && sizing != "partial") || bytes == 0 {
		t.Errorf("macos.home.mise = %q %d B (listed %v); want measured or partial: the host user "+
			"could not read the sandbox account's mise store %s without sudo, which the inventory "+
			"assumes it can", sizing, bytes, ok, macosuser.SandboxMiseData(""))
	}
	// The sources: the held session is Seatbelt's, so that source listed; the image section is
	// each listed container source's, and nothing else's.
	listed := map[string]bool{}
	for _, src := range rep.Sources {
		if src.State == "listed" {
			listed[src.Confinement] = true
		}
	}
	if !listed["seatbelt"] {
		t.Errorf("the Seatbelt source did not list with a macos-user session up: sources %+v", rep.Sources)
	}
	if _, _, ok := get("images.macos-user"); ok {
		t.Errorf("an images.macos-user row is listed; SUP-D57 lists only each container source's image rows")
	}
	imageKeys := map[string]bool{}
	for _, s := range rep.Stores {
		if s.Section != "container image store" {
			continue
		}
		imageKeys[s.Key] = true
		if (s.Confinement != "podman" && s.Confinement != "apple-container") || !listed[s.Confinement] {
			t.Errorf("image row %s carries confinement %q, which is not a container source that listed "+
				"(sources %+v)", s.Key, s.Confinement, rep.Sources)
		}
	}
	for confinement, key := range map[string]string{
		"podman": "images.yolo", "apple-container": "images.yolo@apple-container",
	} {
		if listed[confinement] && !imageKeys[key] {
			t.Errorf("%s listed, but its image row %s is missing (image rows %v)", confinement, key, imageKeys)
		}
	}

	if res := session.release(t); res.rc != 0 {
		t.Fatalf("the held session did not end cleanly (rc %d):\n%s", res.rc, lastLines(res.combined(), 60))
	}
}
