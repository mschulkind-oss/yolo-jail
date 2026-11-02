package loopholes

import (
	"io/fs"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// ungatedShippedLoopholes is the closed list of shipped loopholes that cross to the host and
// declare no grant kind, each with the approval that stands in for a grant or the reason none is
// needed (docs/design/loophole-access-approval.md §4.3). A fifth row is a design change, not a
// test fix.
var ungatedShippedLoopholes = map[string]string{
	"claude-oauth-broker": "the login: nothing is served until a human has logged in on the host",
	"openai-auth-broker":  "the login, completed in a browser by a human",
	"aws-auth":            "a user-scope setting (allow.aws-auth.profile) and a live `aws sso login`",
	"cgroup-delegate":     "it can only narrow the jail itself and reads or changes nothing of the host's",
}

// TestNoShippedLoopholeIsUngatedWithoutAReason reads every loophole manifest in the official
// pack embed, what an installed yolo ships, and holds the ungated set closed: a manifest declares
// grants (a broker's `brokered` block is its grant declaration), or is a row of
// ungatedShippedLoopholes, or crosses nothing to the host (no host daemon, bind, device or
// intercept, as `hello-daemon`).
func TestNoShippedLoopholeIsUngatedWithoutAReason(t *testing.T) {
	packDirs, err := fs.ReadDir(packs.FS, ".")
	if err != nil {
		t.Fatalf("reading the official pack embed: %v", err)
	}
	seen := map[string]bool{}
	for _, p := range packDirs {
		if !p.IsDir() {
			continue
		}
		mods, err := fs.ReadDir(packs.FS, p.Name()+"/loopholes")
		if err != nil {
			continue // most official packs ship no loophole
		}
		for _, mod := range mods {
			if !mod.IsDir() {
				continue
			}
			name := mod.Name()
			dir := "packs/" + p.Name() + "/loopholes/" + name
			data, err := fs.ReadFile(packs.FS, p.Name()+"/loopholes/"+name+"/"+loopholedecl.ManifestTOMLName)
			if err != nil {
				t.Errorf("%s: %v", dir, err)
				continue
			}
			m, err := loopholedecl.Decode(data, dir)
			if err != nil {
				t.Errorf("%s: %v", dir, err)
				continue
			}
			seen[name] = true
			gated := len(m.Grants) > 0 || m.Brokered != nil
			crosses := m.HostDaemon != nil || len(m.HostBindMounts) > 0 || len(m.HostDevices) > 0 ||
				len(m.Intercepts) > 0
			_, reasoned := ungatedShippedLoopholes[name]
			switch {
			case gated && reasoned:
				t.Errorf("%s declares grants and is still listed as ungated; delete its row", name)
			case gated, reasoned, !crosses:
			default:
				t.Errorf("%s crosses to the host and declares no [[grants]]: declare what a project "+
					"can be granted, or, if a design ruling makes it ungated, add a row to "+
					"ungatedShippedLoopholes naming its approval", name)
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("the pack embed yielded no loophole, so this test would pass over nothing")
	}
	for name := range ungatedShippedLoopholes {
		if !seen[name] {
			t.Errorf("ungatedShippedLoopholes lists %q, which no longer ships; delete the row", name)
		}
	}
}
