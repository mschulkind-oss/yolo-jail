package internaldaemon

import (
	"io"
	"os"
	"slices"
	"strings"
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

// TestTheMacosLogMemberIsDispatched: packs/macos-log's manifest spells
// `yolo internal daemon macos-log`, so the name must reach the bridge rather than "unknown
// daemon". Both refuse a bare argv with 2, so the test reads which one answered.
func TestTheMacosLogMemberIsDispatched(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	rc := Run([]string{"macos-log"})
	os.Stderr = saved
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if rc != 2 || !strings.Contains(string(out), "yolo-macos-log: --socket is required") {
		t.Errorf("rc=%d stderr=%q, want the macos-log bridge's own --socket refusal", rc, out)
	}
}
