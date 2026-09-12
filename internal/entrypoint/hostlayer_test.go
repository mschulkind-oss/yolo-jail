package entrypoint

// hostlayer_test.go pins the FAIL-CLOSED host-layer read (OQ-CO10,
// docs/design/config-ownership-and-promotion.md §5.1.1) and the one carve-out it has.
//
// The four cases differ in ONE byte of environment and nothing else — same pack, same
// absent /ctx file, same home — which is the point: what decides whether a jail refuses is
// the launcher's own report of what it delivered, never a guess the jail makes from an
// absent file. Change hostSurfaceBytes back to `data, _ := os.ReadFile(...)` and the first
// test goes red while the other three stay green.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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
			why: "THE macos-user CARVE-OUT: that backend has no bind mounts and no /ctx, so " +
				"every host layer is missing by construction. Severity belongs to the " +
				"disposition (the reachability witness's OQ-R3, same sentence): a launch is " +
				"not refused for what yolo cannot do on that backend. The deficiency is said " +
				"— run.noteMacosUserHostByteGaps names each grant and run.backendLimits tells " +
				"the agent its config came from DEFAULTS — which is what keeps this from " +
				"being the feature-detection P5 forbids",
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
