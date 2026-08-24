package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// READS-HOST ON APPLE CONTAINER: the grant is one FILE, and that backend cannot bind one
// (apple/container#1089). Left as a bind it does not error — it silently does not arrive,
// the surface composes from defaults, and the launch still prints
// "reads-host .claude/settings.json", asserting a read that did not happen. Worse than an
// omission, which is why this is a P0 rather than a parity nit.
//
// The assertion is the OUTCOME the backend owes: the bytes are reachable in the jail and
// the entrypoint is told where. Not "acMaterialize was called" — that would pass if the
// destination were somewhere the entrypoint never looks.
func TestReadsHostGrantsAreMaterializedOnAppleContainer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	emptyLoopholeDirs(t)

	// The real claude pack declares reads-host .claude/settings.json; give the host one.
	hostSettings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(hostSettings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hostSettings, []byte(`{"model":"from-host"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	wsState := filepath.Join(ws, ".yolo", "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	o := goldenOptions(ws, home)
	o.IsMacOS = true
	o.IsLinux = false
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})

	argv := o.assembleRunCmd(&assembleInput{
		cfg:          newConfig("security", sec),
		rt:           "container",
		cname:        "yolo-ws-abcd1234",
		packs:        claudePackFixture(t),
		agentsPath:   filepath.Join(ws, "agents"),
		wsState:      wsState,
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})

	// 1. The bytes are in the home the backend binds.
	got, err := os.ReadFile(filepath.Join(wsState, acCtxDirRel, "host-claude", "settings.json"))
	if err != nil {
		t.Fatalf("the host settings grant was not materialized for Apple Container: %v\n"+
			"That backend cannot bind a single file, so a -v of one arrives as nothing and the "+
			"surface silently composes without the user's host layer.", err)
	}
	if string(got) != `{"model":"from-host"}` {
		t.Errorf("materialized content = %q, want the host file's bytes", got)
	}

	// 2. The entrypoint is TOLD where, or the copy is unreachable and the fix is cosmetic.
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "YOLO_CTX_ROOT=/home/agent/"+acCtxDirRel) {
		t.Errorf("argv does not carry YOLO_CTX_ROOT, so the entrypoint still reads /ctx and "+
			"finds nothing:\n%s", joined)
	}

	// 3. No single-file bind of the grant survives — that is the thing AC cannot do.
	if strings.Contains(joined, hostSettings+":/ctx/") {
		t.Errorf("a single-file /ctx bind is still emitted on Apple Container:\n%s", joined)
	}
}

// The podman path must be untouched: it binds, and it must NOT gain the env var, or every
// jail's argv changes for a backend that never needed it.
func TestReadsHostStillBindsOnPodman(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	emptyLoopholeDirs(t)

	hostSettings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(hostSettings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hostSettings, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	wsState := filepath.Join(ws, ".yolo", "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	o := goldenOptions(ws, home)
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})

	argv := o.assembleRunCmd(&assembleInput{
		cfg: newConfig("security", sec), rt: "podman", cname: "yolo-ws-abcd1234",
		packs: claudePackFixture(t), agentsPath: filepath.Join(ws, "agents"),
		wsState: wsState, miseStore: "/mise-store", yoloVersion: "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, ":/ctx/host-claude/settings.json:ro") {
		t.Errorf("podman no longer binds the host grant into /ctx:\n%s", joined)
	}
	if strings.Contains(joined, "YOLO_CTX_ROOT") {
		t.Errorf("podman argv gained YOLO_CTX_ROOT; it mounts at /ctx and must not be remapped:\n%s", joined)
	}
}
