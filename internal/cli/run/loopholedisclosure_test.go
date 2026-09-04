package run

// loopholedisclosure_test.go pins what a user is TOLD about a pack-shipped loophole, which
// since OQ-TP9 (docs/design/trust-paths.md, 2026-09-04) is the only thing standing between
// them and a daemon running on their machine.
//
// # What this file used to be
//
// loopholerefusal_test.go, pinning the REPORTING half of §4.3 G3 — "refusals printed
// per-claim". Two defects, one shape, and both are worth keeping in view because the second
// one INVERTED rather than vanished:
//
//   - a refused loophole was withheld in SILENCE: a pack the user installed, selected, and
//     whose whole purpose was a loophole, doing nothing, with no line saying why. Retired —
//     nothing is refused, so there is no silence left to break.
//   - the pre-spawn block printed a REFUSED daemon's argv under "This launch runs pack code
//     on your machine", because the footprint deliberately answers what a pack WANTS. That
//     was a lie in the one place a user reads before host code runs. It is still a lie in the
//     other direction: with nothing refused, a daemon MISSING from that block is a process
//     that starts unannounced. The test below is the same assertion with the sign flipped.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// EVERY loophole module a pack ships is honored, and nothing is refused.
//
// It was TestHonoredLoopholesGrantsAndRefusesByOrigin, whose whole subject was the split.
func TestHonoredLoopholesRefusesNothing(t *testing.T) {
	body := `{"name":"acme-proxy","transport":"none","host_daemon":{"cmd":["/bin/true"],` +
		`"publishes":"socket"},"host_devices":["/dev/snd"]}`
	p := writeRealLoopholePack(t, "acme", "acme-proxy", body)

	mods, refused := p.HonoredLoopholes()
	if len(mods) != 1 {
		t.Errorf("the pack's loophole was withheld: mods=%d", len(mods))
	}
	if len(refused) != 0 {
		t.Errorf("a loophole was REFUSED: %v\nThe origin gate is deleted; a refusal here is a "+
			"gate that came back without a ruling", refused)
	}
}

// THE PRE-SPAWN BLOCK MUST NAME THE DAEMON THAT IS ABOUT TO RUN.
//
// Its value is that every line in it is imminent — which used to mean a REFUSED daemon must
// not appear, and now means an HONORED one must. Same property, and with nothing refused the
// failure mode is worse than before: a missing line is a host process the user was never told
// about, in the one place they could have hit ctrl-c.
func TestDisclosureAnnouncesThePackDaemonBeforeItRuns(t *testing.T) {
	body := `{"name":"acme-proxy","transport":"none",` +
		`"host_daemon":{"cmd":["python3","{loophole_dir}/acme-daemon.py"],"publishes":"socket"},` +
		`"intercepts":[{"host":"api.acme.test"}]}`
	p := writeRealLoopholePack(t, "acme", "acme-proxy", body)

	execLines := renderLines(disclosedClaims([]*packload.Pack{p}, disclosureExec))
	if !strings.Contains(execLines, "acme-daemon.py") {
		t.Errorf("the pre-spawn block does not name the daemon about to run:\n%s\n"+
			"Since OQ-TP9 nothing withholds it, so an omission here is a host process that "+
			"starts with the user never told", execLines)
	}
	readLines := renderLines(disclosedClaims([]*packload.Pack{p}, disclosureRead))
	if !strings.Contains(readLines, "api.acme.test") {
		t.Errorf("the loophole's intercept — a CA every TLS client in the jail trusts — is "+
			"not disclosed:\n%s", readLines)
	}
}

// And end to end at the spawn boundary: the launch prints the heading and the argv BEFORE the
// daemon starts.
//
// It was TestSpawnBoundarySaysRefusedNotPending, asserting the absence of both. The heading
// is asserted separately from the argv because they fail differently — a block with no
// heading reads as unrelated noise, and a heading with no argv says "something runs" without
// saying what.
func TestSpawnBoundaryAnnouncesHostExecution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
	isolatePackModules(t)

	p := writeRealLoopholePack(t, "acme", "acme-proxy", `{
		"name": "acme-proxy",
		"transport": "none",
		"host_daemon": {"cmd": ["python3", "{loophole_dir}/acme-daemon.py"], "publishes": "socket"}
	}`)

	cname := "yolo-disclosed-" + t.Name()
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	var errBuf bytes.Buffer
	o := &Options{}
	fillDefaults(o)
	o.Stderr = &errBuf
	o.Stdout = &errBuf
	o.PathExists = func(string) bool { return false }
	o.startLoopholesDisclosed(cname, "podman", newConfig(), []*packload.Pack{p})

	got := errBuf.String()
	if !strings.Contains(got, "runs pack code on your machine") {
		t.Errorf("the launch spawned a pack's host daemon without the heading that says so:\n%s",
			got)
	}
	if !strings.Contains(got, "acme-daemon.py") {
		t.Errorf("the launch announced host execution without naming the argv, so the user "+
			"cannot tell WHAT is about to run:\n%s", got)
	}
}
