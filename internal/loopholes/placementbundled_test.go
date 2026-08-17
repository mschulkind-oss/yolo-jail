package loopholes

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestBundledLoopholeInTheWorkspaceIsNotRefused pins the SELF-HOSTING case, which a
// nested-jail smoke found and no unit test could have.
//
// BundledLoopholesDir prefers the repo checkout when yolo runs from its own source tree
// (see loopholes.go's reporoot.Resolve branch), so in THIS repo's development jail all
// three bundled loopholes resolve to <repo>/bundled_loopholes/* — inside the very tree the
// launch bind-mounts :rw. Judging them by the placement rule refused the OAuth broker, the
// audio pass-through and host-processes on every launch, with a message telling the user to
// "install the loophole outside that tree" — advice nobody can follow about content they did
// not install.
//
// No unit test caught it because every existing placement test builds its module dir under
// t.TempDir(): the one configuration where the bundled dir and the workspace coincide is the
// one nobody constructs. That is the general shape worth remembering — a rule about how two
// real paths relate cannot be verified by a test that invents both of them.
func TestBundledLoopholeInTheWorkspaceIsNotRefused(t *testing.T) {
	ws := t.TempDir()
	moduleDir := filepath.Join(ws, "bundled_loopholes", "claude-oauth-broker")

	bundled := &Loophole{
		Name:    "claude-oauth-broker",
		Source:  SourceBundled,
		Enabled: true,
		Path:    moduleDir,
		HostDaemon: &HostDaemon{
			Cmd: []string{"yolo", "internal", "daemon", "claude-oauth-broker"},
		},
	}
	if probs := bundled.PlacementProblems(ws); len(probs) != 0 {
		t.Errorf("a BUNDLED loophole inside the mounted workspace must not be refused — that is "+
			"yolo's own content, and an agent that can rewrite it has already rewritten the code "+
			"performing this check (gate-placement Test 1). In yolo's own development jail this "+
			"refusal takes out the broker, audio and host-processes on every launch:\n  %s",
			strings.Join(probs, "\n  "))
	}

	// The control, and it is what keeps the exemption from being a hole: the SAME path is
	// still refused for a PACK-shipped loophole, whose content is not yolo's.
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
			"still be refused — exempting bundled content must not exempt everything")
	}
	if !strings.Contains(strings.Join(probs, "\n"), "acme") {
		t.Errorf("the refusal must name the loophole:\n  %s", strings.Join(probs, "\n  "))
	}

	// And an UNLABELLED record is refused too. That is the fail-safe default resolve()
	// assigns (SourcePack — see load.go), which matters because the label is the only
	// thing standing between "yolo's own content" and "content the exemption must not
	// cover". It used to default to the now-retired `user` label, which was also judged;
	// this pins that the retirement did not flip the default to the exempt side.
	unlabelled := &Loophole{
		Name:       "unlabelled",
		Enabled:    true,
		Path:       filepath.Join(ws, "loopholes", "unlabelled"),
		HostDaemon: &HostDaemon{Cmd: []string{"python3", "srv.py"}},
	}
	if probs := unlabelled.PlacementProblems(ws); len(probs) == 0 {
		t.Error("a record with no source label inside the mounted workspace must still be " +
			"refused — only SourceBundled is exempt, and only because it IS yolo's own content")
	}
}
