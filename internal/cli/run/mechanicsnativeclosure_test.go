package run

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestNativeMechanicsHostFileFactsReachProductionStaging(t *testing.T) {
	home := ctxLaunchHome(t, `, "host_files": [{"path": ".npmrc", "source": "~/.npmrc"}]`)
	t.Setenv("HOME", home)
	source := filepath.Join(home, ".npmrc")
	writeHostFileAt(t, source, "registry=https://example.invalid\n", 0o600)
	workspace := t.TempDir()
	cname := "yolo-native-mechanics-host-file"
	o := goldenOptions(workspace, home)
	selectMacosUserMode(o, macosuser.ModeSelf)
	staged := stagedPacks{packs: claudePackFixture(t)}
	cfg := jsonx.NewOrderedMap()
	stagingRoot := filepath.Join(paths.AgentsDir(), cname)

	// Exercise the actual native producer: this is the resolved copy that Run hands to
	// the native plan, not a manually assembled host-file fact.
	delivery, err := o.buildMacosCtxTree(stagingRoot, staged.packs, cfg)
	if err != nil {
		t.Fatalf("buildMacosCtxTree: %v", err)
	}
	if len(delivery.ctx.HostFiles) != 1 || delivery.ctx.Tree == "" {
		t.Fatalf("native host-file delivery = %#v, want the resolved source-bearing entry and copied tree", delivery.ctx)
	}
	entry := delivery.ctx.HostFiles[0]
	backing := filepath.Join(delivery.ctx.Tree, "host-user", entry.Slug())
	if body, err := os.ReadFile(backing); err != nil || string(body) != "registry=https://example.invalid\n" {
		t.Fatalf("producer backing %q = %q, %v", backing, body, err)
	}

	facts := o.mechanicsFactSheet(cname, cfg, "macos-user", staged, storePackagesPlan{}, delivery.ctx.HostFiles)
	mechanicsNativeHostFileBackings(facts, delivery.ctx, cname)
	if _, err := o.refreshJailBriefings(cname, cfg, "macos-user", staged, ioprio.Normal, facts); err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	mapPath := findStagedMechanicsFile(t, stagingRoot, filepath.Join("jail-mechanics", "host-map.md"))
	mapBytes, err := os.ReadFile(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	nativeBacking := filepath.Join(macosuser.StagedCtxRoot(cname, ""), "host-user", entry.Slug())
	for _, want := range []string{source, nativeBacking, "per-launch context copy", "copy into native context staging at launch"} {
		if !strings.Contains(string(mapBytes), want) {
			t.Errorf("staged native host map omits producer fact %q:\n%s", want, mapBytes)
		}
	}

	// Pin the production caller: deleting the native staging handoff or moving it after
	// suite composition must fail this test even though the isolated producer above works.
	_, callerFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate native closure test")
	}
	runSource, err := os.ReadFile(filepath.Join(filepath.Dir(callerFile), "run.go"))
	if err != nil {
		t.Fatal(err)
	}
	caller := string(runSource)
	producer := strings.Index(caller, "ctxDelivery, err := o.buildMacosCtxTree")
	factsCall := strings.Index(caller, "facts := o.mechanicsFactSheet(cname, cfg, rt, staged, storePackagesPlan{}, ctxDelivery.ctx.HostFiles)")
	adapter := strings.Index(caller, "mechanicsNativeHostFileBackings(facts, ctxDelivery.ctx, cname)")
	refresh := strings.Index(caller, "o.refreshJailBriefings(cname, cfg")
	if producer < 0 || factsCall < 0 || adapter < 0 || refresh < 0 || !(producer < factsCall && factsCall < adapter && adapter < refresh) {
		t.Fatalf("native producer/facts/render order = (%d, %d, %d, %d), want buildMacosCtxTree < fact sheet < native backing adapter < suite refresh", producer, factsCall, adapter, refresh)
	}
}

func TestNativeMechanicsRestartAndUpdateUseCurrentSessionRecipe(t *testing.T) {
	home := ctxLaunchHome(t, "")
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	cname := "yolo-native-mechanics-restart"
	o := goldenOptions(workspace, home)
	selectMacosUserMode(o, macosuser.ModeSelf)
	staged := stagedPacks{packs: claudePackFixture(t)}
	cfg := jsonx.NewOrderedMap()
	facts := o.mechanicsFactSheet(cname, cfg, "macos-user", staged, storePackagesPlan{}, nil)
	if facts.RestartCommand != "yolo" {
		t.Fatalf("native producer restart command = %q, want actual fresh-launch command yolo", facts.RestartCommand)
	}
	staging, err := o.refreshJailBriefings(cname, cfg, "macos-user", staged, ioprio.Normal, facts)
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	for _, rel := range []string{filepath.Join("jail-mechanics", "recipes", "restart.md"), filepath.Join("jail-mechanics", "recipes", "yolo-update.md")} {
		path := findStagedMechanicsFile(t, staging, rel)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if !strings.Contains(text, "End this session.") || !strings.Contains(text, "Run `yolo` in `"+workspace+"`") {
			t.Errorf("native %s does not end the current session and then start one yolo:\n%s", rel, text)
		}
		if strings.Contains(strings.ToLower(text), "ends the jail's sessions") || strings.Count(text, "Run `yolo`") != 1 {
			t.Errorf("native %s falsely ends all sessions or duplicates the yolo command:\n%s", rel, text)
		}
	}
}

func findStagedMechanicsFile(t *testing.T, root, rel string) string {
	t.Helper()
	var found string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(filepath.ToSlash(path), "/"+filepath.ToSlash(rel)) {
			return nil
		}
		found = path
		return nil
	})
	if err != nil {
		t.Fatalf("walk staging %q: %v", root, err)
	}
	if found == "" {
		t.Fatalf("staged mechanics file %q not found below %q", rel, root)
	}
	return found
}
