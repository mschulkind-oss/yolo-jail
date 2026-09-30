package internaldaemon

import (
	"slices"
	"testing"
)

// TestTheKeeperMemberDispatchesToItsHook: `jail-keeper` runs what the CLI set, with the rest of the
// argv, and a binary that set nothing refuses it rather than calling a nil entry.
func TestTheKeeperMemberDispatchesToItsHook(t *testing.T) {
	saved := JailKeeper
	t.Cleanup(func() { JailKeeper = saved })
	var got []string
	JailKeeper = func(args []string) int {
		got = args
		return 7
	}
	if rc := Run([]string{"jail-keeper", "--plan", "/p"}); rc != 7 || !slices.Equal(got, []string{"--plan", "/p"}) {
		t.Errorf("rc=%d args=%q", rc, got)
	}
	JailKeeper = nil
	if rc := Run([]string{"jail-keeper"}); rc != 2 {
		t.Errorf("an unwired keeper member returned %d, want 2", rc)
	}
}
