package cli

import (
	"strings"
	"testing"
)

// THE PER-ARRAY ACCOUNT FOLDS THE OWNER'S MANAGED LAYER AT THE NOTCH IT DESCRIBES
// (declaration-parity.md DP-B25). claude's AUTONOMOUS posture manages `permissions.allow: []`
// and its GUARDED one does not, so a config-list on /permissions/allow is replaced in a jail
// and assembled at the host. `config render --at host` (the host apply's own preview) already
// shows the entry landing; `config ls --at host` said the managed layer replaces it, because
// packSurfacesForAgent folded every pack at the autonomous posture beside an overlay set the
// same function collected at the guarded one. Runs the real verb over a file:// pack, so it
// fails if overlayContributionRows stops handing its notch's posture to the surface fold.
// Under `host_management: "own"`: the unset key is `none` since the `assert` retirement
// (OQ-CO14), under which the host notch renders no config surface for the entry to land in.
func TestConfigLsListReplacementFollowsTheNotchPosture(t *testing.T) {
	listWorldUnder(t, "own", func(home string) string {
		return `"claude",` + listPack(t, home, "matt", `{"kind":"config-list",`+
			`"surface":"claude/settings","path":"/permissions/allow","add":["Bash(ls:*)"]}`)
	})
	const replaced = "managed layer replaces this array"
	for _, c := range []struct {
		notch        string
		wantReplaced bool
	}{{"host", false}, {"jail", true}} {
		// Fixture guard: the render this report describes agrees with the expectation.
		rc, out, errw := runConfigVerb(t, "render", "claude/settings", "--at", c.notch)
		if rc != 0 {
			t.Fatalf("render --at %s rc=%d\n%s%s", c.notch, rc, out, errw)
		}
		if lands := strings.Contains(out, "Bash(ls:*)"); lands == c.wantReplaced {
			t.Fatalf("fixture bug: render --at %s lands the entry=%v\n%s", c.notch, lands, out)
		}
		rc, out, errw = runConfigVerb(t, "ls", "--at", c.notch)
		if rc != 0 {
			t.Fatalf("ls --at %s rc=%d\n%s%s", c.notch, rc, out, errw)
		}
		if !hasLine(out, "/permissions/allow", "matt (1 entry)") {
			t.Fatalf("config ls --at %s does not list matt's array:\n%s", c.notch, out)
		}
		if got := hasLine(out, "/permissions/allow", replaced); got != c.wantReplaced {
			t.Errorf("config ls --at %s: says the managed layer replaces /permissions/allow=%v, "+
				"want %v (config render --at %s agrees with the want)\n%s",
				c.notch, got, c.wantReplaced, c.notch, out)
		}
	}
}
