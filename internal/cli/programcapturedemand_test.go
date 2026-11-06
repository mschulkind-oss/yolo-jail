package cli

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

func selectedNPMUser(t *testing.T, f *selectedNPMFixture, updates bool) run.ProgramCaptureRequest {
	t.Helper()
	manifest := `{"name":"fixture","contributes":[{"kind":"program","bin":"probe","via":"npm","package":"probe-pkg"}]}`
	if f.in.NodeFloor != "" {
		floor, _ := json.Marshal(f.in.NodeFloor)
		manifest = strings.Replace(manifest, `"package":"probe-pkg"`, `"package":"probe-pkg","node_floor":`+string(floor), 1)
	}
	return selectedNPMUserPack(t, f, updates, "fixture", manifest)
}

// selectedNPMUserPack selects the one pack name, whose manifest declares one npm program, through
// the user's config, as selectedNPMUser does the probe fixture.
func selectedNPMUserPack(t *testing.T, f *selectedNPMFixture, updates bool, name, manifest string) run.ProgramCaptureRequest {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_SETUP", "podman-self")
	oldResolver := resolvePreparationBundle
	resolvePreparationBundle = func() (reporoot.Resolution, bool) {
		return reporoot.Resolution{Root: f.root}, true
	}
	t.Cleanup(func() { resolvePreparationBundle = oldResolver })
	writeFile(t, filepath.Join(f.root, "flake.nix"), "{}")
	writeFile(t, filepath.Join(f.root, "flake.lock"), "{}")
	pack := t.TempDir()
	writeFile(t, filepath.Join(pack, "pack.json"), manifest)
	cfgJSON, _ := json.Marshal(map[string]any{"host_management": "own", "host_apply_on_launch": false,
		"packs": []any{map[string]string{"source": pack, "name": name}}, "agent_updates": updates, "host_floor": true})
	writeFile(t, paths.UserConfigPath(), string(cfgJSON))
	f.store.Dir = paths.CapturesDir()
	selected := selectConfiguredHostPacks()
	if len(selected.problems()) != 0 {
		t.Fatal(selected.problems())
	}
	workspace := t.TempDir()
	cfg, err := config.LoadConfig(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	vector, err := selectedOrdinaryInputs(ordinarySelectionEnvironment(cfg, selected.packs), selected.packs, entrypoint.ProgramInstallOnly, captureJailPlatform())
	if err != nil || len(vector) != 1 {
		t.Fatalf("ordinary fixture vector: %+v %v", vector, err)
	}
	f.in = vector[0]
	fingerprint, err := run.ProgramRuntimeFingerprint(f.root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	userCfg, err := config.UserScopeConfig(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	userFP, err := run.ProgramRuntimeFingerprint(f.root, userCfg)
	if err != nil || userFP != fingerprint {
		t.Fatalf("user/runtime fingerprint mismatch %s %s %v", userFP, fingerprint, err)
	}
	return run.ProgramCaptureRequest{Packs: selected.packs, Env: ordinarySelectionEnvironment(cfg, selected.packs), Runtime: "podman", Platform: captureJailPlatform(), Fingerprint: fingerprint, RepoRoot: f.root, Workspace: workspace, Act: &run.ActInterrupt{}}
}

func TestStandardReaderLegacyMiseOverrideExcludesNPMProgramFromFreshPreparation(t *testing.T) {
	f := newSelectedNPMFixture(t)
	req := selectedNPMUser(t, f, false)
	writeFile(t, filepath.Join(req.Workspace, paths.WorkspaceConfigLegacyJSONCName), `{"mise_tools":{"probe":"1.2.3"}}`)

	cfg, err := config.LoadConfig(req.Workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	install, _ := cfg.Get("install")
	installMap, ok := install.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("standard reader omitted canonical install table: %#v", install)
	}
	mise, present := installMap.Get("mise")
	miseMap, ok := mise.(*jsonx.OrderedMap)
	if !present || !ok {
		t.Fatalf("standard reader did not lower workspace mise_tools into install.mise: %#v", cfg)
	}
	pin, pinPresent := miseMap.Get("probe")
	if !pinPresent || pin != "1.2.3" {
		t.Fatalf("standard reader did not preserve the workspace mise pin: %#v", cfg)
	}
	if _, legacyPresent := cfg.Get("mise_tools"); legacyPresent {
		t.Fatalf("standard reader retained the legacy mise_tools root: %#v", cfg)
	}

	req.Env = ordinarySelectionEnvironment(cfg, req.Packs)
	req.Fresh = true
	vector, err := selectedOrdinaryInputs(req.Env, req.Packs, entrypoint.ProgramFreshRefresh, req.Platform)
	if err != nil {
		t.Fatal(err)
	}
	if len(vector) != 0 {
		t.Fatalf("fresh ordinary preparation selected program overridden by the workspace's mise tool: %+v", vector)
	}
	prepared, err := prepareOrdinaryForLaunch(req, io.Discard)
	if err != nil || len(prepared) != 0 {
		t.Fatalf("fresh launch preparation = %v, %v; want no unnecessary preparation/refusal", prepared, err)
	}
}

func selectedNPMSharedWorker(t *testing.T) *sync.WaitGroup {
	t.Helper()
	var workers sync.WaitGroup
	old := sharedPreparationSpawn
	sharedPreparationSpawn = func(_ context.Context, d preparationDemand) error {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = runSharedPreparation(ctx, d, io.Discard)
		}()
		return nil
	}
	t.Cleanup(func() { workers.Wait(); sharedPreparationSpawn = old })
	return &workers
}

func TestSelectedNPMRunFrontdoorActualSharedColdWarmFresh(t *testing.T) {
	f := newSelectedNPMFixture(t)
	req := selectedNPMUser(t, f, false)
	selectedNPMSharedWorker(t)
	old := launchRunPipeline
	t.Cleanup(func() { launchRunPipeline = old })
	var delivery entrypoint.ProgramCaptureDelivery
	launchRunPipeline = func(o run.Options) int {
		if o.PreparePrograms == nil {
			t.Fatal("public run frontdoor omitted the selected producer callback")
		}
		req.Act = &run.ActInterrupt{}
		req.Fresh = o.Update
		got, err := o.PreparePrograms(req)
		if err != nil {
			t.Fatal(err)
		}
		var ok bool
		delivery, ok = got["probe"]
		if !ok {
			t.Fatal("selected ordinary generation absent")
		}
		return 0
	}
	if rc := runRun([]string{"run", "--", "probe"}); rc != 0 {
		t.Fatal(rc)
	}
	first := delivery.Compatibility
	if f.resolves.Load() != 1 || f.installs.Load() != 1 {
		t.Fatalf("cold producer %d/%d", f.resolves.Load(), f.installs.Load())
	}
	for n := 0; n < 4; n++ {
		if rc := runRun([]string{"run", "--", "probe"}); rc != 0 {
			t.Fatal(rc)
		}
	}
	if delivery.Compatibility != first || f.resolves.Load() != 1 || f.installs.Load() != 1 {
		t.Fatal("warm public launch contacted registry/installer")
	}
	if rc := runRun([]string{"run", "--update", "--", "probe"}); rc != 0 {
		t.Fatal(rc)
	}
	if delivery.Compatibility != first || f.resolves.Load() != 2 || f.installs.Load() != 1 {
		t.Fatal("fresh public check rebuilt unchanged generation")
	}
	f.version(t, "2.0.0")
	if rc := runRun([]string{"run", "--update", "--", "probe"}); rc != 0 {
		t.Fatal(rc)
	}
	if delivery.Compatibility == first || f.resolves.Load() != 3 || f.installs.Load() != 2 {
		t.Fatal("fresh public launch did not select a new generation")
	}
	records, err := preparationSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if r.Kind == preparationOrdinaryProgramKind && r.Active {
			t.Fatal("agent_updates:false retained automatic ordinary demand")
		}
	}
}
