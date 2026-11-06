package cli

import (
	"strings"
	"testing"
	"time"
)

// `yolo stop` reaches a jail an earlier yolo started under the workspace's legacy jail name
// (named-setups-plan.md SUP-D10): a launch refuses while that jail runs and names this stop as the
// remedy, so the front door (runStop) must stop it as well as the selected setup's jail. The
// legacy name is asked only when something of it is left (stopLegacyJailKnown).
func TestAStopFromTheFrontDoorAlsoStopsTheLegacyJail(t *testing.T) {
	for _, known := range []bool{true, false} {
		name := "legacy jail known"
		if !known {
			name = "nothing of the legacy jail left"
		}
		t.Run(name, func(t *testing.T) {
			cname, _ := stopWorkspace(t, "podman-self")
			legacy := strings.TrimSuffix(cname, "-podman-self")
			saved := stopLegacyJailKnown
			t.Cleanup(func() { stopLegacyJailKnown = saved })
			var asked []string
			stopLegacyJailKnown = func(name string) bool {
				asked = append(asked, name)
				return known
			}
			// The selected setup's jail is absent; the legacy one runs until it is stopped.
			present := map[string]bool{cname: false, legacy: true}
			calls := map[string][][]string{}
			var order []string
			useStopExec(t, func(argv []string, _ time.Duration) (string, bool, int) {
				target := argv[len(argv)-1]
				if _, ok := present[target]; !ok {
					t.Errorf("the stop asked about %q, which is neither jail: %v", target, argv)
					return "", false, 1
				}
				order = append(order, target)
				calls[target] = append(calls[target], argv)
				switch {
				case argv[1] == "inspect" && present[target]:
					return "true\n", true, 0
				case argv[1] == "inspect":
					return "Error: no such container", true, 125
				case argv[1] == "container" && argv[2] == "exists" && present[target]:
					return "", true, 0
				case argv[1] == "container" && argv[2] == "exists":
					return "", true, 1
				case argv[1] == "stop" || argv[1] == "rm":
					present[target] = false
					return "", true, 0
				}
				t.Errorf("unexpected argv %v", argv)
				return "", false, 1
			})
			var rc int
			stdout, stderr := captureBoth(t, func() { rc = runStop([]string{"stop"}) })
			if rc != 0 {
				t.Fatalf("runStop = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
			}
			if len(asked) != 1 || asked[0] != legacy {
				t.Errorf("the stop asked whether %v is known, want only the legacy name %s", asked, legacy)
			}
			if len(order) == 0 || order[0] != cname {
				t.Errorf("the stop's first question was about %v, want the selected setup's jail %s first", order, cname)
			}
			stoppedLegacy := false
			for _, argv := range calls[legacy] {
				if len(argv) > 1 && argv[1] == "stop" {
					stoppedLegacy = true
				}
			}
			if stoppedLegacy != known {
				t.Errorf("the legacy jail was stopped = %v, want %v; calls %v\nstdout:\n%s", stoppedLegacy, known, calls[legacy], stdout)
			}
			if said := strings.Contains(stdout, "Stopping the jail an earlier yolo started for this workspace ("+legacy+")"); said != known {
				t.Errorf("the stop named the legacy jail = %v, want %v:\n%s", said, known, stdout)
			}
		})
	}
}
