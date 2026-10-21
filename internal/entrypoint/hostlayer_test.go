package entrypoint

// hostlayer_test.go pins the FAIL-CLOSED host-layer read (OQ-CO10,
// docs/reference/config-ownership.md#the-read-in-host-layer) and the one carve-out it has.
//
// The four cases differ in ONE byte of environment and nothing else — same pack, same
// absent /ctx file, same home — which is the point: what decides whether a jail refuses is
// the launcher's own report of what it delivered, never a guess the jail makes from an
// absent file. Change hostSurfaceBytes back to `data, _ := os.ReadFile(...)` and the first
// test goes red while the other three stay green.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// claudeHostLayerDelivered is the report a launcher emits when it really did put the user's
// ~/.claude/settings.json in the jail. The path is packload.CtxPath's, spelled out so this
// fixture fails if the destination the two halves agree on ever moves.
const claudeHostLayerDelivered = `{"delivery":"supported","delivered":["/ctx/host-claude/settings.json"]}`

// THE REFUSAL. The launcher says it delivered the user's own settings.json and the jail
// cannot read it there: the bytes were meant to arrive and did not, which is the class that
// shipped as a silent wrong composition (a pack whose staged directory name was escaped was
// mounted at one /ctx path and read at another, 2026-09-05) and the whole reason the read
// stopped being fail-open.
//
// What it must NOT do is compose: a settings.json missing the user's own keys is a config
// file an agent cannot tell from a correct one, which is why this is a refusal rather than
// a warning.
func TestHostLayerRefusesWhenTheLaunchDeliveredItAndTheJailCannotReadIt(t *testing.T) {
	e, ctx := newClaudePrismEnv(t, map[string]string{
		packload.HostLayerEnvVar: claudeHostLayerDelivered,
	})

	err := ConfigurePackByName(e, "claude")
	if err == nil {
		t.Fatal("the boot composed claude/settings without a host layer the launch says it " +
			"delivered — that is the silent wrong composition OQ-CO10 closed")
	}
	// The path it really opened, which in a jail IS the /ctx one and under Apple
	// Container's YOLO_CTX_ROOT relocation is not — a refusal naming the unremapped
	// string would send a user to a path their backend never used.
	for _, want := range []string{"claude/settings", filepath.Join(ctx, "settings.json")} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q, so a user cannot act on it:\n%v", want, err)
		}
	}
}

// The three dispositions that compose without a host layer and refuse nothing. Each has its
// own reason, and none of them is a delivery fault.
func TestHostLayerComposesWithoutRefusingWhenNothingWasDelivered(t *testing.T) {
	cases := []struct {
		name string
		wire string
		why  string
	}{
		{
			name: "the user has no such file",
			wire: `{"delivery":"supported"}`,
			why: "the overwhelmingly common case — most users have never written one — and " +
				"refusing a launch for it would be a worse bug than the one being fixed",
		},
		{
			name: "the backend delivers no host layers",
			wire: `{"delivery":"unsupported"}`,
			why: "A LAUNCH THAT DELIVERED NOTHING, which since DP-L1 (2026-09-13) is no " +
				"longer a claim about a BACKEND. macos-user used to report this " +
				"unconditionally because its host bytes crossed on a /ctx mount it does not " +
				"have; they now cross by copy into a root-owned tree, so it reports " +
				"`supported` whenever it staged one (macosuser.hostLayerWire) and this wire " +
				"is what a caller that staged NO tree emits — the install capture is the " +
				"shipped one. Severity still belongs to the disposition (the reachability " +
				"witness's OQ-R3, same sentence): a launch is not refused for a delivery " +
				"nobody attempted",
		},
		{
			name: "a garbled report",
			wire: "not json at all",
			why: "an unparseable value is UNKNOWN, not an empty delivery list — reading it " +
				"as 'nothing was delivered' would refuse every host layer on the machine, " +
				"which is the opposite of the conservative answer",
		},
		{
			name: "no report at all",
			wire: "",
			why: "a launcher older than the variable. The two halves deploy on different " +
				"cadences, so absence must mean only that — never 'nothing was delivered', " +
				"which would refuse every host layer on the machine",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vars := map[string]string{}
			if tc.wire != "" {
				vars[packload.HostLayerEnvVar] = tc.wire
			}
			// "" is the absent variable, which is its own case above.
			e, _ := newClaudePrismEnv(t, vars)

			if err := ConfigurePackByName(e, "claude"); err != nil {
				t.Fatalf("the boot refused: %v\nIt must not — %s", err, tc.why)
			}
			// And it really did render: a refusal is not the only way to get this wrong.
			if _, err := os.Stat(filepath.Join(e.ClaudeDir(), "settings.json")); err != nil {
				t.Errorf("settings.json was not written: %v", err)
			}
		})
	}
}

// The delivered file is COMPOSED, which is the half a refusal test cannot show. Together
// with the refusal above this says the report decides the FAILURE, not the reading: a
// delivered layer that is there still merges exactly as it always did.
func TestHostLayerComposesTheDeliveredFile(t *testing.T) {
	e, ctx := newClaudePrismEnv(t, map[string]string{
		packload.HostLayerEnvVar: claudeHostLayerDelivered,
	})
	if err := os.WriteFile(filepath.Join(ctx, "settings.json"),
		[]byte(`{"verbose":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ConfigurePackByName(e, "claude"); err != nil {
		t.Fatalf("ConfigurePackByName: %v", err)
	}
	got := decodeJSONFile(t, filepath.Join(e.ClaudeDir(), "settings.json"))
	if got["verbose"] != true {
		t.Errorf("verbose = %v, want true — a key no layer of yolo's declares, so only the "+
			"host layer can produce it", got["verbose"])
	}
}

// A surface that DECLARES a host layer while no loader derived where it lands is refused
// too, and this is the one case with no call site to pin it from: packload fills HostSource
// for every surface it decodes, and a `readsHost` on a path with no host twin is refused at
// decode, so the state is only reachable by building a Surface in Go — a core surface
// (manifest.BuiltinManifest), or a loader that is not packload. Tested directly rather than
// left unpinned, because the alternative to refusing is composing the file without the
// user's own settings, which is what the whole restructure is about.
func TestHostLayerRefusesADeclarationNoLoaderResolved(t *testing.T) {
	e := &Env{Home: t.TempDir(), Vars: map[string]string{}}
	_, err := hostSurfaceBytes(e, manifest.Surface{
		Agent: "zz", Name: "settings", Path: "~/.zz/settings.json",
		Codec: "json", ReadsHost: true,
	})
	if err == nil {
		t.Fatal("composed a surface whose host layer nothing located")
	}
	if !strings.Contains(err.Error(), "zz/settings") {
		t.Errorf("the refusal does not name the surface: %v", err)
	}
}

// THE CALL SITE of agentcfg.DropHostOnlyCommands, through a real claude boot: the user's host
// settings.json carries herdr's SessionStart hook at a script under the host's home, which this
// jail cannot see, beside a Stop hook it can run. The jail's copy leaves the first out, keeps the
// second, and the boot says so. Delete the call in packsurfaces.go and the first assertion fails:
// the filter's own tests stay green, so this is the test that proves the boot runs it.
func TestHostLayerLeavesOutAHookAtAHostOnlyPath(t *testing.T) {
	hostHome := filepath.Join(t.TempDir(), "Users", "someone")
	e, ctx := newClaudePrismEnv(t, map[string]string{
		packload.HostLayerEnvVar: claudeHostLayerDelivered,
		paths.HostHomeEnv:        hostHome,
	})
	var stderr strings.Builder
	e.Stderr = &stderr
	hook := "bash '" + hostHome + "/.claude/hooks/herdr-agent-state.sh' session"
	host := `{"hooks": {
	  "SessionStart": [{"hooks": [{"type": "command", "command": ` + strconv.Quote(hook) + `}]}],
	  "Stop": [{"hooks": [{"type": "command", "command": "echo done > /tmp/stop"}]}]
	}}`
	if err := os.WriteFile(filepath.Join(ctx, "settings.json"), []byte(host), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ConfigurePackByName(e, "claude"); err != nil {
		t.Fatalf("ConfigurePackByName: %v", err)
	}
	got := decodeJSONFile(t, filepath.Join(e.ClaudeDir(), "settings.json"))
	hooks, _ := got["hooks"].(map[string]any)
	if _, still := hooks["SessionStart"]; still {
		t.Errorf("the jail's settings.json still runs %q, a script only the host has — "+
			"the boot no longer filters host-only commands:\n%v", hook, hooks)
	}
	if _, kept := hooks["Stop"]; !kept {
		t.Errorf("the Stop hook names no host path and was left out anyway: %v", hooks)
	}
	for _, want := range []string{"claude/settings", "hooks.SessionStart.0.hooks.0",
		hostHome + "/.claude/hooks/herdr-agent-state.sh", "mounts, host_files"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the boot's disclosure does not say %q:\n%s", want, stderr.String())
		}
	}
}
