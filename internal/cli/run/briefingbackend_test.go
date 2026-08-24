package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// THE BRIEFING MUST NOT ADVERTISE A DAEMON THAT NEVER STARTED. briefingLoopholes selects
// via Honored() — enabled, supported here, allowed to run host code — which has no
// BACKEND term. Apple Container returns from startLoopholes before any host service
// starts and macos-user never reaches it, so on those two the unfiltered list rendered a
// section headed "host capabilities wired into this jail" describing nothing that exists.
//
// This is the second narrowing of this list for the same reason: its own comment records
// the earlier Enabled() → Active() fix. An agent reading a false capability list does not
// merely lack a feature — it plans around one it does not have.
//
// Asserted on the BRIEFING THAT GETS WRITTEN, not on briefingLoopholes, so it fails if
// the gate is bypassed anywhere between the two.
func TestBriefingOmitsLoopholesOnBackendsThatStartNone(t *testing.T) {
	for _, rt := range []string{"container", "macos-user"} {
		t.Run(rt, func(t *testing.T) {
			if body := briefingBodyFor(t, rt); strings.Contains(body, "acme-proxy") {
				t.Errorf("the %s briefing advertises a loophole that backend never starts.\n"+
					"That backend starts NO host services, so this section describes daemons "+
					"that do not exist:\n%s", rt, body)
			}
		})
	}
}

// And it must still list them where they DO run, or the gate deleted a real section
// instead of correcting it.
func TestBriefingStillListsLoopholesOnPodman(t *testing.T) {
	if body := briefingBodyFor(t, "podman"); !strings.Contains(body, "acme-proxy") {
		t.Errorf("the podman briefing lost its loophole list:\n%s", body)
	}
}

// briefingBodyFor composes a briefing for one backend with a config-declared loophole
// and returns the text actually written for the claude pack's declared destination.
func briefingBodyFor(t *testing.T, rt string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	emptyLoopholeDirs(t)

	entry := jsonx.NewOrderedMap()
	entry.Set("enabled", true)
	entry.Set("description", "acme proxy")
	entry.Set("command", []any{"/bin/true"})
	lp := jsonx.NewOrderedMap()
	lp.Set("acme-proxy", entry)

	o := goldenOptions(ws, home)
	staging, err := o.refreshJailBriefings("yolo-ws-abcd1234", newConfig("loopholes", lp), rt,
		stagedPacks{packs: claudePackFixture(t)})
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(staging, briefingStagingName("claude")))
	if err != nil {
		t.Fatalf("no briefing was written for the claude pack: %v", err)
	}
	return string(body)
}
