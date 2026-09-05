package run

// packservices_test.go pins the launch call site of the service composition
// (docs/design/wire-bridge.md §2.1, §5). serviceJailDaemons' own shaping is
// trivial; this file exists for the OTHER half of the rule — the argv. A test
// that pins the helper while the call site is unpinned is not a test
// (AGENTS.md, Testing): delete the serviceJailDaemons call from assemble.go's
// loopholesRuntimeArgs line and this goes red, as does deleting the
// extra-daemon merge inside internal/loopholes' runtimeArgsFor — the env var
// is one contract and this test watches the whole path.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestServiceJailDaemonJoinsTheDaemonsEnv: a fixture pack whose manifest
// declares one kind:service contribution lands its jail_daemon in the composed
// YOLO_JAIL_DAEMONS env — the loophole JailDaemon wire shape verbatim
// ({name, cmd, restart}, restart defaulted like the loophole half defaults it),
// through the same writer the loopholes use, not a second -e of the same name.
func TestServiceJailDaemonJoinsTheDaemonsEnv(t *testing.T) {
	bridge := writePackManifest(t, "svcbridge",
		`{"name":"svcbridge","contributes":[{"kind":"service","name":"wire-bridge",
		  "jail_daemon":{"cmd":["yolo-jaild","wire-bridge"]},
		  "endpoint":"wire-bridge.endpoint"}]}`)

	argv := zaiLaunch(t, []*packload.Pack{officialPack(t, "claude"), bridge},
		bareConfig(), emptyEnv(), nil)

	vals := envArgValues(argv, "YOLO_JAIL_DAEMONS")
	if len(vals) != 1 {
		t.Fatalf("YOLO_JAIL_DAEMONS must appear exactly once on the argv — a second -e of "+
			"the same name would lose to the runtime's duplicate resolution: %q", vals)
	}
	payload := vals[0][strings.IndexByte(vals[0], '=')+1:]
	var specs []struct {
		Name    string   `json:"name"`
		Cmd     []string `json:"cmd"`
		Restart string   `json:"restart"`
	}
	if err := json.Unmarshal([]byte(payload), &specs); err != nil {
		t.Fatalf("the payload is not the supervisor's JSON list: %v\n%s", err, payload)
	}
	var found *struct {
		Name    string   `json:"name"`
		Cmd     []string `json:"cmd"`
		Restart string   `json:"restart"`
	}
	for i := range specs {
		if specs[i].Name == "wire-bridge" {
			found = &specs[i]
		}
	}
	if found == nil {
		t.Fatalf("the service daemon never reached the env: %s", payload)
	}
	if strings.Join(found.Cmd, " ") != "yolo-jaild wire-bridge" {
		t.Errorf("cmd = %v, want the manifest's argv verbatim", found.Cmd)
	}
	if found.Restart != "on-failure" {
		t.Errorf("restart = %q — the manifest omitted it, so the payload must spell the "+
			"supervisor's default (the loophole half's shape sets the key the same way)", found.Restart)
	}
}

// TestTwoServiceDaemonsAreSortedByName: two fixture packs, one launch — the
// payload order is the service name's order, not pack iteration order, because
// the env var is read in-jail verbatim and a deterministic argv is the rule the
// rest of the env block follows.
func TestTwoServiceDaemonsAreSortedByName(t *testing.T) {
	alpha := writePackManifest(t, "alpha", `{"name":"alpha","contributes":[
		{"kind":"service","name":"a-service","jail_daemon":{"cmd":["a"]}}]}`)
	zulu := writePackManifest(t, "zulu", `{"name":"zulu","contributes":[
		{"kind":"service","name":"z-service","jail_daemon":{"cmd":["z"]}}]}`)

	argv := zaiLaunch(t, []*packload.Pack{officialPack(t, "claude"), zulu, alpha},
		bareConfig(), emptyEnv(), nil)
	vals := envArgValues(argv, "YOLO_JAIL_DAEMONS")
	if len(vals) != 1 {
		t.Fatalf("YOLO_JAIL_DAEMONS must appear exactly once: %q", vals)
	}
	aPos := strings.Index(vals[0], `"a-service"`)
	zPos := strings.Index(vals[0], `"z-service"`)
	if aPos < 0 || zPos < 0 {
		t.Fatalf("both service daemons must be in the payload: %s", vals[0])
	}
	if aPos > zPos {
		t.Errorf("payload is not sorted by service name: %s", vals[0])
	}
}

// A pack with NO service contribution contributes nothing — the env var must
// not appear merely because the composition ran (a bare `yolo -- bash` launch
// has no daemons and must not grow one).
func TestNoServiceContributionEmitsNoDaemonsEnv(t *testing.T) {
	argv := zaiLaunch(t, []*packload.Pack{officialPack(t, "zai")}, bareConfig(), emptyEnv(), nil)
	if vals := envArgValues(argv, "YOLO_JAIL_DAEMONS"); len(vals) != 0 {
		t.Errorf("a launch with no jail daemon and no service must not carry the env: %q", vals)
	}
}
