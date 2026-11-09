package cli

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// `yolo stop` reaches a jail an earlier yolo started under the workspace's legacy jail name
// (named-setups-plan.md SUP-D10): a launch refuses while that jail runs and names this stop as the
// remedy, so the front door (runStop) must end it as well as the selected setup's jail. The legacy
// name is asked when something of it is left (stopLegacyJailKnown), and otherwise its container is
// looked for (stopLeftoverContainer): an older yolo's container that exited without being removed is
// known to nothing else of yolo's, and still holds the name (review M2).
func TestAStopFromTheFrontDoorAlsoStopsTheLegacyJail(t *testing.T) {
	for _, tc := range []struct {
		name      string
		known     bool // a keeper record, session or tracking file of the legacy jail is left
		container string
		wantEnded bool
	}{
		{name: "legacy jail known and running", known: true, container: "running", wantEnded: true},
		{name: "only a stopped legacy container left", container: "exited", wantEnded: true},
		{name: "only a running legacy container left", container: "running", wantEnded: true},
		{name: "nothing of the legacy jail left", wantEnded: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cname, _ := stopWorkspace(t, "podman-self")
			legacy := strings.TrimSuffix(cname, "-podman-self")
			savedKnown, savedLook := stopLegacyJailKnown, stopLookPath
			t.Cleanup(func() { stopLegacyJailKnown, stopLookPath = savedKnown, savedLook })
			stopLeftoverContainerFind = stopLeftoverContainer // the real lookup (stopWorkspace stubs it)
			var asked []string
			stopLegacyJailKnown = func(name string) bool {
				asked = append(asked, name)
				return tc.known
			}
			// Only the selected runtime (podman) is installed, so the stop asks no other.
			stopLookPath = func(string) (string, error) { return "", errors.New("not installed") }
			// The selected setup's jail is absent; the legacy container is in tc.container's state.
			state := map[string]string{cname: "", legacy: tc.container}
			calls := map[string][][]string{}
			var order []string
			useStopExec(t, func(argv []string, _ time.Duration) (string, bool, int) {
				target := argv[len(argv)-1]
				if _, ok := state[target]; !ok {
					t.Errorf("the stop asked about %q, which is neither jail: %v", target, argv)
					return "", false, 1
				}
				order = append(order, target)
				calls[target] = append(calls[target], argv)
				switch {
				case argv[1] == "inspect" && state[target] == "":
					return "Error: no such container", true, 125
				case argv[1] == "inspect" && strings.Contains(strings.Join(argv, " "), "{{.State.Running}}"):
					return map[bool]string{true: "true\n", false: "false\n"}[state[target] == "running"], true, 0
				case argv[1] == "inspect":
					return state[target] + "\n", true, 0
				case argv[1] == "container" && argv[2] == "exists" && state[target] != "":
					return "", true, 0
				case argv[1] == "container" && argv[2] == "exists":
					return "", true, 1
				case argv[1] == "stop" || argv[1] == "rm":
					state[target] = ""
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
			if ended := state[legacy] == "" && tc.container != ""; ended != tc.wantEnded {
				t.Errorf("the legacy container was ended = %v, want %v; calls %v\nstdout:\n%s\nstderr:\n%s",
					ended, tc.wantEnded, calls[legacy], stdout, stderr)
			}
			if said := strings.Contains(stdout, "Stopping the jail an earlier yolo started for this workspace ("+legacy+")"); said != tc.wantEnded {
				t.Errorf("the stop named the legacy jail = %v, want %v:\n%s", said, tc.wantEnded, stdout)
			}
		})
	}
}
