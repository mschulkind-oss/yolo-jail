package run

// packservices_test.go pins the launch call site of the service composition
// (docs/reference/wire-bridge.md §2.1, §5). serviceJailDaemons' own shaping is
// trivial; this file exists for the OTHER half of the rule — the argv. A test
// that pins the helper while the call site is unpinned is not a test
// (AGENTS.md, Testing).
//
// The path is two hops: run.go's Run composes the payload above the backend
// dispatch (jailDaemonsFor, in packservices.go — that is where serviceJailDaemons
// is called, and internal/loopholes' jailDaemonSpecs appends its result to the
// loopholes' own entries), and assemble.go serializes it onto the argv
// (loopholesRuntimeArgs, handed in.jailDaemons). zaiLaunch composes the payload with
// jailDaemonsFor exactly as Run does, so this goes red if the serviceJailDaemons
// call inside jailDaemonsFor is deleted, if jailDaemonSpecs stops appending the
// extra entries, or if assemble.go stops handing in.jailDaemons to
// loopholesRuntimeArgs — each measured by mutation. ⚠ It stays GREEN if run.go's
// own jailDaemonsFor call is deleted, because the harness does not enter through
// Run; that call is pinned in macosuserjaildaemon_test.go
// (TestTheJailDaemonPayloadIsComposedAboveTheBackendDispatch).

import (
	"bytes"
	"encoding/json"
	"go/ast"
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

// TestADuplicatedServiceNameStartsOnlyTheLaterPacksDaemon: two selected packs declare the
// wire-bridge SERVICE, the shipped pack and a later one. A service name is a sole-owned claim,
// so the LATER pack in the pack order holds it (packload.laterWins, notch-convergence NC-D59):
// the payload carries ONE daemon for the name, the later pack's, and the endpoint variable the
// witness waits on names that same pack's file, so the daemon that runs is the one whose
// endpoint the jail is pointed at. Both used to reach the payload, two daemons racing for one
// endpoint file. The launch says which declaration it set aside, by pack name.
func TestADuplicatedServiceNameStartsOnlyTheLaterPacksDaemon(t *testing.T) {
	fork := writePackManifest(t, "bridge-fork", `{"name":"bridge-fork","contributes":[
		{"kind":"service","name":"wire-bridge","endpoint":"fork.endpoint",
		 "jail_daemon":{"cmd":["yolo-jaild","wire-bridge","--fork"]}}]}`)
	packs := []*packload.Pack{
		officialPack(t, "claude"), officialPack(t, "cerebras"), officialPack(t, "wire-bridge"), fork,
	}
	var stderr bytes.Buffer
	la := zaiLaunchAssembled(t, packs, bareConfig(), cerebrasKey(), func(o *Options) {
		o.ProfileName = "cerebras"
		o.Stderr = &stderr
	})

	vals := envArgValues(la.argv, "YOLO_JAIL_DAEMONS")
	if len(vals) != 1 {
		t.Fatalf("YOLO_JAIL_DAEMONS must appear exactly once: %q", vals)
	}
	var specs []struct {
		Name string   `json:"name"`
		Cmd  []string `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(vals[0][strings.IndexByte(vals[0], '=')+1:]), &specs); err != nil {
		t.Fatalf("the payload is not the supervisor's JSON list: %v\n%s", err, vals[0])
	}
	var bridges [][]string
	for _, s := range specs {
		if s.Name == "wire-bridge" {
			bridges = append(bridges, s.Cmd)
		}
	}
	if len(bridges) != 1 {
		t.Fatalf("one service name must start exactly one daemon, got %d: %v", len(bridges), bridges)
	}
	if got := strings.Join(bridges[0], " "); got != "yolo-jaild wire-bridge --fork" {
		t.Errorf("the daemon that runs is %q, want the LATER pack's (bridge-fork)", got)
	}
	if v := envArgValues(la.argv, "YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT"); len(v) != 1 ||
		v[0] != "YOLO_SERVICE_WIRE_BRIDGE_ENDPOINT=/run/yolo-services/fork.endpoint" {
		t.Errorf("the endpoint variable must name the file the running daemon publishes: %q", v)
	}

	// The disclosure reads what the payload composition recorded (jailDaemonsFor), so it
	// names the declaration the payload actually set aside.
	la.o.noteShadowedServices()
	said := stderr.String()
	for _, want := range []string{`"wire-bridge"`, "pack wire-bridge", "pack bridge-fork", "NC-D59"} {
		if !strings.Contains(said, want) {
			t.Errorf("the launch must disclose the shadowed declaration (%q missing):\n%s", want, said)
		}
	}
}

// One declaration per service name says nothing: the disclosure is about a shadowed
// declaration, and the ordinary bridged launch has none.
func TestAnUnduplicatedServiceDisclosesNothing(t *testing.T) {
	var stderr bytes.Buffer
	la := zaiLaunchAssembled(t, []*packload.Pack{
		officialPack(t, "claude"), officialPack(t, "cerebras"), officialPack(t, "wire-bridge"),
	}, bareConfig(), cerebrasKey(), func(o *Options) {
		o.ProfileName = "cerebras"
		o.Stderr = &stderr
	})
	la.o.noteShadowedServices()
	if s := stderr.String(); strings.Contains(s, "shadowed") {
		t.Errorf("no service name is duplicated, so nothing is shadowed:\n%s", s)
	}
}

// The shadowed-service disclosure has a call site on each arm that composes a fresh launch's
// daemon payload, the container path and the macos-user arm, so deleting either fails here
// (the call graph is the witness, as in TestTheUnstartedDaemonDisclosureIsPrintedByEachFreshArm).
func TestTheShadowedServiceDisclosureIsPrintedByEachFreshArm(t *testing.T) {
	for _, fn := range []string{"runContainer", "Run"} {
		t.Run(fn, func(t *testing.T) {
			found := false
			ast.Inspect(funcDeclIn(t, "run.go", fn), func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "noteShadowedServices" {
						found = true
					}
				}
				return true
			})
			if !found {
				t.Errorf("%s never prints noteShadowedServices: a shadowed service would go unsaid", fn)
			}
		})
	}
}
