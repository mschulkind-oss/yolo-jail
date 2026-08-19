package loopholes

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostingPlacement pins what is left of the SELF-HOSTING case a nested-jail smoke
// found and no unit test could have.
//
// # What the case was
//
// `BundledLoopholesDir` preferred the repo checkout when yolo ran from its own source
// tree, so in THIS repo's development jail every bundled loophole resolved to
// <repo>/bundled_loopholes/* — inside the very tree the launch bind-mounts :rw. Judging
// them by the placement rule refused the OAuth broker, the audio pass-through and
// host-processes on EVERY launch, with a message telling the user to "install the loophole
// outside that tree" — advice nobody can follow about content they did not install. The
// fix was an exemption for SourceBundled.
//
// # Why the exemption is gone, and why the case cannot recur through it
//
// `bundled_loopholes/` is retired (docs/design/broker-as-a-pack.md OQ-BP4) and yolo's own
// loopholes are OFFICIAL PACKS. A pack's module dir is its STAGED copy under
// paths.AgentsDir(), which is outside every workspace by construction — so the collision
// the exemption existed for is unrepresentable rather than exempted, which is the stronger
// of the two answers.
//
// # What still has to hold, and is asserted below
//
// The rule itself, for the case it was written for: a pack whose module dir sits inside
// the mounted workspace IS refused, and so is a record carrying no source label at all.
// The second one is not pedantry — SourcePack is the FAIL-SAFE default resolve() assigns
// (load.go), and the label is the only thing that ever stood between "yolo's own content"
// and "content an exemption must not cover". With no exemption left, the property to keep
// is that nothing gets one by accident.
//
// The general shape worth remembering from the original bug: a rule about how two real
// paths relate cannot be verified by a test that invents both of them.
func TestSelfHostingPlacement(t *testing.T) {
	ws := t.TempDir()

	// A locally-developed pack, checked out inside the workspace the launch mounts :rw.
	// This is the case the rule was written for, and the one that still happens.
	packed := &Loophole{
		Name:       "acme",
		Source:     SourcePack,
		Enabled:    true,
		Path:       filepath.Join(ws, "packs", "acme", "loopholes", "acme"),
		HostDaemon: &HostDaemon{Cmd: []string{"python3", "srv.py"}},
	}
	probs := packed.PlacementProblems(ws)
	if len(probs) == 0 {
		t.Fatal("a PACK-shipped loophole whose module dir is inside the mounted workspace must " +
			"be refused — an agent can swap it between launches, with none of the authority " +
			"that installed it")
	}
	if !strings.Contains(strings.Join(probs, "\n"), "acme") {
		t.Errorf("the refusal must name the loophole:\n  %s", strings.Join(probs, "\n  "))
	}

	// And an UNLABELLED record is refused too. That is the fail-safe default resolve()
	// assigns (SourcePack — see load.go); this pins that no source label is exempt now
	// that the bundled one is gone, so a record whose label was lost cannot slip through.
	unlabelled := &Loophole{
		Name:       "unlabelled",
		Enabled:    true,
		Path:       filepath.Join(ws, "loopholes", "unlabelled"),
		HostDaemon: &HostDaemon{Cmd: []string{"python3", "srv.py"}},
	}
	if probs := unlabelled.PlacementProblems(ws); len(probs) == 0 {
		t.Error("a record with no source label inside the mounted workspace must be refused — " +
			"there is no exempt source any more, and a lost label must not become one")
	}

	// The staged shape yolo's OWN packs take: a module dir outside both agent-writable
	// trees. This is what makes the deleted exemption unnecessary rather than merely
	// deleted — if it were ever refused, every launch selecting `packs: ["claude"]` would
	// lose the broker with the same unfollowable advice the original bug gave.
	staged := &Loophole{
		Name:       "claude-oauth-broker",
		Source:     SourcePack,
		Enabled:    true,
		Path:       filepath.Join(t.TempDir(), "agents", "yolo-ws", "packs", "_official", "claude", "loopholes", "claude-oauth-broker"),
		HostDaemon: &HostDaemon{Cmd: []string{"yolo", "internal", "daemon", "claude-oauth-broker"}},
	}
	if probs := staged.PlacementProblems(ws); len(probs) != 0 {
		t.Errorf("an official pack's STAGED loophole must not be refused — it lives outside "+
			"the workspace and outside the jail-home tree by construction:\n  %s",
			strings.Join(probs, "\n  "))
	}
}
