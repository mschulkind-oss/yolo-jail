package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// packMayAccessHost is the launch-time gate that replaces "fetched => no host
// access" with "fetched => host access iff the lockfile approves the current
// claims". These cases pin the whole trust model.
func TestPackMayAccessHostGate(t *testing.T) {
	// A staged fetched pack that declares a mount (a host read).
	dir := t.TempDir()
	writePack(t, dir, `{"contributes":[{"kind":"mount","host":"refs","into":"refs"}]}`)
	claim := "mount refs -> /ctx/refs"

	fetched := config.PackEntry{Name: "acme", Source: "git+ssh://h/o/r//p?ref=v1"}
	local := config.PackEntry{Name: "acme", Source: "file:///tmp/acme"}

	// Local origin: always permitted, no lock needed.
	if !packMayAccessHost(local, dir, nil) {
		t.Error("a local pack must always get host access (origin permits)")
	}

	// Fetched, no lock: refused (fail-closed).
	if packMayAccessHost(fetched, dir, nil) {
		t.Error("a fetched pack with no lockfile must be refused host access")
	}

	// Fetched, lock present but no approval for this pack: refused.
	empty := &packsrc.Lock{Schema: packsrc.LockSchema, Packs: map[string]packsrc.LockEntry{}}
	if packMayAccessHost(fetched, dir, empty) {
		t.Error("a fetched pack absent from the lock must be refused")
	}

	// Fetched, lock approves the exact claim: granted.
	approved := &packsrc.Lock{Schema: packsrc.LockSchema, Packs: map[string]packsrc.LockEntry{
		"acme": {Name: "acme", ApprovedHostAccess: []string{claim}},
	}}
	if !packMayAccessHost(fetched, dir, approved) {
		t.Error("a fetched pack whose claim is approved must be granted host access")
	}

	// Fetched, lock approves a DIFFERENT claim (pin moved + gained access): refused.
	stale := &packsrc.Lock{Schema: packsrc.LockSchema, Packs: map[string]packsrc.LockEntry{
		"acme": {Name: "acme", ApprovedHostAccess: []string{"mount OTHER -> /ctx/other"}},
	}}
	if packMayAccessHost(fetched, dir, stale) {
		t.Error("a fetched pack whose current claim is NOT in the approved set must be refused")
	}
}

// A fetched pack that reads nothing from the host needs no approval — the gate is
// moot, so it is permitted (its non-host contributions must not be blocked on a
// consent step that has nothing to consent to).
func TestPackMayAccessHostNoClaimsNeedsNoApproval(t *testing.T) {
	dir := t.TempDir()
	writePack(t, dir, `{"contributes":[{"kind":"env","vars":{"X":"1"}},{"kind":"skills","from":"skills","into":".x/skills"}]}`)
	fetched := config.PackEntry{Name: "acme", Source: "git+ssh://h/o/r//p?ref=v1"}
	if !packMayAccessHost(fetched, dir, nil) {
		t.Error("a fetched pack reading nothing from the host needs no approval and must load")
	}
}

func writePack(t *testing.T, dir, manifest string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}
