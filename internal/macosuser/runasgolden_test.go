package macosuser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// runAsGoldenFile pins, word for word, every argv that crosses into the sandbox in ACCOUNT mode
// (the second account, today's only mode). It was recorded BEFORE the crossings were routed
// through RunAs (docs/design/macos-user-model-plan.md, slice 3 and MMP-D4) and must not change
// when they are: the refactor is proven behavior-preserving by this file staying byte-identical.
// Regenerate only for a deliberate change to an account-mode argv, with
// YOLO_UPDATE_RUNAS_GOLDEN=1, and say why in the commit. Regenerated once so far, for slice 12a
// (MMP-D25): the whole-plan entries moved (HOME is the workspace's own home, the bootstrap is
// told the homes and runs confined); every builder's own entry stayed byte-identical.
const runAsGoldenFile = "testdata/runas_account_argvs.golden.json"

// accountCrossingArgvs builds every sandbox crossing this package emits, through every builder
// and through a whole plan that carries each optional stage, all in account mode.
func accountCrossingArgvs(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	put := func(name string, argv []string) {
		if _, dup := out[name]; dup {
			t.Fatalf("golden entry %q recorded twice", name)
		}
		out[name] = argv
	}

	prefix := []string{"/nix/store/000mock-yolo-noncontainer-profile/bin"}
	ctxLinks := []ContextLink{
		{Dest: "/ctx/data", Source: "/Users/Shared/data", Dir: true},
		{Dest: "/ctx/out", Source: "/Users/Shared/out", Dir: true, RW: true},
		{Dest: "/ctx/notes.txt", Source: "/Users/Shared/notes.txt"},
	}
	relocs := []CacheRelocation{sharedReloc, volumeReloc}
	bootEnv := jsonx.NewOrderedMap()
	bootEnv.Set("YOLO_DARWIN_WORKSPACE", "/Users/Shared/yolo/proj")
	bootEnv.Set("HOME", "/ignored")

	// The builders, called directly.
	put("LaunchArgv", LaunchArgv([]string{"claude", "--x"}, "/s/p.sb", "/s/env",
		"/Users/Shared/yolo/proj", "", "", prefix))
	put("LaunchArgvWithGuard", LaunchArgvWithGuard(RunAs{}, []string{"claude"}, "/s/p.sb", "/s/env",
		"/Users/Shared/yolo/proj", "", "", prefix, SessionGuard{MemoryBytes: 256 << 20}, "/s/bin/yolo"))
	put("ProvisionArgv", ProvisionArgv(RunAs{}, "echo hi", "/s/p.sb", "/s/env", "/Users/Shared/yolo/proj",
		"", "", prefix, "/s/bin"))
	put("DarwinBootstrapArgv", DarwinBootstrapArgv(RunAs{}, "/s/bin/yolo", "", bootEnv, ""))
	put("JailDaemonArgv", JailDaemonArgv(RunAs{}, "/s/p.sb", "/s/denv", "/s/sup.log", "", "", prefix, "/s/bin/yolo"))
	put("ProbeServicesArgv", ProbeServicesArgv(RunAs{}, "/s/bin/yolo", "/s/p.sb", "/s/env", "", "", prefix))
	for i, p := range ContextPreflight(RunAs{}, ctxLinks) {
		put("ContextPreflight/"+strconv.Itoa(i)+"/"+p.Access, p.Argv)
	}
	for i, p := range CacheRelocationPreflight(RunAs{}, relocs) {
		put("CacheRelocationPreflight/"+strconv.Itoa(i)+"/"+p.Access, p.Argv)
	}
	put("CacheRelocationWriteProbe", CacheRelocationWriteProbe(RunAs{}, sharedReloc, "/s/p.sb", "sess", "").Argv)
	for i, c := range CaptureScratchGuestCommands(RunAs{}, "/Users/Shared/yolo-staged/capture") {
		put("CaptureScratchGuestCommands/"+strconv.Itoa(i), c)
	}
	put("CaptureDriverArgv", CaptureDriverArgv(RunAs{}, "/s/bin/yolo", "/c/home", "/c/out", "jq", "/s/p.sb",
		"/s/env", prefix))
	put("ForkBuildDriverArgv", ForkBuildDriverArgv(RunAs{}, "/s/bin/yolo", "/c/home", "/c/out", "/c/src",
		"/c/toolchain", "abc123", "npm run build", "/s/p.sb", "/s/env", prefix))
	put("TreeBuildDriverArgv", TreeBuildDriverArgv(RunAs{}, "/s/bin/yolo", "/c/home", "/c/out", "/c/src",
		"/c/toolchain", "abc123", "npm run build", "/c/tree", "/s/p.sb", "/s/env", prefix))

	// A whole plan carrying every optional crossing: the provisioning stage (mise_tools), the
	// jail daemons, the service probe (a published endpoint), context mounts and relocations.
	env := brokerEndpointEnv()
	plan := BuildRunPlanWithDaemons("/Users/Shared/yolo/proj", provisionCfg(), []string{"claude"},
		[]string{"claude"}, "/opt/yolo/bin/yolo", "", HomeOverlay{},
		HostContext{Links: ctxLinks, Relocations: relocs}, env, mockDarwin(), nil,
		openAIAdapterDaemons("/opt/yolo/share/yolo-jail/bin/darwin-arm64"), FloorStage{},
		PlanSession{ID: "golden"})
	for name, argv := range map[string][]string{
		"plan/LaunchArgv":     plan.LaunchArgv,
		"plan/ProvisionArgv":  plan.ProvisionArgv,
		"plan/BootstrapArgv":  plan.BootstrapArgv,
		"plan/JailDaemonArgv": plan.JailDaemonArgv,
		"plan/ProbeArgv":      plan.ProbeArgv,
	} {
		if len(argv) == 0 {
			t.Fatalf("the golden plan carries no %s; the fixture no longer exercises it", name)
		}
		put(name, argv)
	}
	for i, p := range plan.ContextPreflight {
		put("plan/ContextPreflight/"+strconv.Itoa(i)+"/"+p.Access, p.Argv)
	}
	for i, p := range plan.CacheRelocationPreflight {
		put("plan/CacheRelocationPreflight/"+strconv.Itoa(i)+"/"+p.Access, p.Argv)
	}
	for i, p := range plan.CacheRelocationProbes {
		put("plan/CacheRelocationProbes/"+strconv.Itoa(i)+"/"+p.Access, p.Argv)
	}
	return out
}

// TestAccountCrossingArgvsAreByteIdentical is the byte-identity proof for MMP-D4: account mode
// produces exactly the argvs recorded before RunAs existed.
func TestAccountCrossingArgvsAreByteIdentical(t *testing.T) {
	got, err := json.MarshalIndent(accountCrossingArgvs(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if os.Getenv("YOLO_UPDATE_RUNAS_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(runAsGoldenFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(runAsGoldenFile, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(runAsGoldenFile)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with YOLO_UPDATE_RUNAS_GOLDEN=1 only for a deliberate "+
			"account-mode argv change)", runAsGoldenFile, err)
	}
	if string(got) != string(want) {
		var gotM, wantM map[string][]string
		_ = json.Unmarshal(got, &gotM)
		_ = json.Unmarshal(want, &wantM)
		for k, w := range wantM {
			if g, ok := gotM[k]; !ok {
				t.Errorf("%s: no longer emitted", k)
			} else if string(mustJSON(g)) != string(mustJSON(w)) {
				t.Errorf("%s moved:\n got  %q\n want %q", k, g, w)
			}
		}
		for k := range gotM {
			if _, ok := wantM[k]; !ok {
				t.Errorf("%s: new entry not in the golden file", k)
			}
		}
		t.Errorf("an account-mode crossing argv differs from %s", runAsGoldenFile)
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
