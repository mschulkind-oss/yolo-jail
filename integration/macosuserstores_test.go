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
// entirely: the root-owned copies each launch stages under /var/yolo-jail, and the sandbox
// account's machine-tier stores in /Users/_yolojail. It now lists them, as the host user, with no
// sudo (internal/cli/stores/inventory.go, macosUserStores).
//
// WHAT THIS SETTLES. Reading the sandbox home without sudo is an INFERENCE from the account's
// layout (the home is 0750, owned by the sandbox account, and macos-setup puts the invoking user
// in its group); only a Mac can say whether the walk gets in. So the mise store must come back
// measured or partial, never unknown. The session puts a probe file in that store first, so the
// row has something to measure on a machine whose store a previous run left empty, and removes it
// before it ends. The packs row must be measured: a launch with a pack staged a copy there.

// storesJSON is the part of `yolo stores --format json` this test reads.
type storesJSON struct {
	Stores []struct {
		Key, Section, Sizing, Reason, Note string
		Bytes                              int64
	} `json:"stores"`
}

// TestMacosUserStoresListsTheSandboxAccount: with a macos-user session up, `yolo stores` lists the
// state dir's packs copies as measured, the sandbox account's mise store as measured or partial,
// and the container image store as not applicable.
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

	r := runCommand(t, ws, []string{"stores", "--format", "json", "--no-record"}, macosUserRunEnv())
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
	if sizing, _, ok := get("images.macos-user"); !ok || sizing != "absent" {
		t.Errorf("the container image store is not the not-applicable row on macos-user (%q, listed %v)",
			sizing, ok)
	}

	if res := session.release(t); res.rc != 0 {
		t.Fatalf("the held session did not end cleanly (rc %d):\n%s", res.rc, lastLines(res.combined(), 60))
	}
}
