package hostfloor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

func TestPreparePatchedKeepsBothDiagnosticSourcesAndTypedFailure(t *testing.T) {
	w, pf := patchedWorld(t)
	p := patchedProgram()
	good := pf.good(forkCommitOne, patchedRecipeOne, "v1.0.0 (11111111) + 2 patches", true)
	failure := &packsrc.PatchFailure{
		Owner: "forkpack/forkcli", Member: "0001-ten.patch", Log: "full replay failure transcript",
	}
	initial := PatchedState{
		Recipe: patchedRecipeOne, Good: good, PatchFailure: failure,
		OperationError: "initial operation diagnosis", AuthorityError: "initial authority diagnosis",
	}
	resolved := PatchedState{
		Recipe: patchedRecipeOne, Good: good, PatchFailure: failure,
		OperationError: "resolved operation diagnosis", AuthorityError: "resolved authority diagnosis",
	}
	w.floor.Patched = func(Program) PatchedState { return initial }
	w.floor.ResolvePatched = func(context.Context, Program, *Record, bool) PatchedState { return resolved }
	w.floor.AllowPatchFailures = func() bool { return true }

	preparation, err := w.floor.PreparePatched(context.Background(), p, false)
	if err == nil || preparation == nil {
		t.Fatalf("independent diagnostics were waived by a typed failure bypass: preparation=%+v err=%v", preparation, err)
	}
	if preparation.delivery != patchedPreparationInstall {
		t.Fatalf("diagnostic authority selected a delivery despite refusal: delivery=%v", preparation.delivery)
	}
	for _, want := range []string{
		"initial operation diagnosis", "resolved operation diagnosis",
		"initial authority diagnosis", "resolved authority diagnosis",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("preparation error lost %q: %v", want, err)
		}
	}
	for _, diagnostics := range []string{preparation.state.OperationError, preparation.state.AuthorityError} {
		if diagnostics == "" {
			t.Errorf("merged patched state dropped an independent diagnostic: %+v", preparation.state)
		}
	}
	var actualFailure *packsrc.PatchFailure
	if !errors.As(err, &actualFailure) || actualFailure.Member != failure.Member || actualFailure.Log != failure.Log {
		t.Fatalf("typed patch failure/log was lost from the refusal chain: failure=%+v err=%v", actualFailure, err)
	}

	deduplicated := mergePreparedPatchedState(
		PatchedState{OperationError: "same operation diagnosis", AuthorityError: "same authority diagnosis"},
		PatchedState{OperationError: "same operation diagnosis", AuthorityError: "same authority diagnosis"},
	)
	if strings.Count(deduplicated.OperationError, "same operation diagnosis") != 1 ||
		strings.Count(deduplicated.AuthorityError, "same authority diagnosis") != 1 {
		t.Fatalf("identical diagnostic text was duplicated: %+v", deduplicated)
	}
}

func TestPreparePatchedRejectsDamagedInstalledCopyForLiteralBypass(t *testing.T) {
	for _, damage := range []string{"launcher-mode", "launcher-directory", "node-mode", "direct-entry-mode", "script-directory", "escape-symlink", "complete-marker", "declared-path"} {
		t.Run(damage, func(t *testing.T) {
			p := patchedProgram()
			extra := ".npm-global/lib/node_modules/forkcli/generated.json"
			if damage == "declared-path" {
				p.Install.Produces = append(p.Install.Produces, extra)
			}
			w, pf, rec, _ := installPatchedPreparationCopy(t, p)
			if damage == "declared-path" {
				path := filepath.Join(rec.Dir, "home", filepath.FromSlash(extra))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("declared artifact\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			switch damage {
			case "launcher-mode":
				if err := os.Chmod(w.floor.Launcher(p.Bin()), 0o600); err != nil {
					t.Fatal(err)
				}
			case "launcher-directory":
				if err := os.Remove(w.floor.Launcher(p.Bin())); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(w.floor.Launcher(p.Bin()), 0o700); err != nil {
					t.Fatal(err)
				}
			case "node-mode":
				if err := os.Chmod(rec.Exec[0], 0o600); err != nil {
					t.Fatal(err)
				}
			case "direct-entry-mode":
				rec = directExecutablePreparationRecord(t, w, rec)
				if err := os.Chmod(rec.Entry, 0o644); err != nil {
					t.Fatal(err)
				}
			case "script-directory":
				if err := os.Remove(rec.Entry); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(rec.Entry, 0o700); err != nil {
					t.Fatal(err)
				}
			case "escape-symlink":
				outside := filepath.Join(w.root, "outside-script.js")
				if err := os.WriteFile(outside, []byte("console.log('outside')\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(rec.Entry); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, rec.Entry); err != nil {
					t.Fatal(err)
				}
			case "complete-marker":
				if err := os.Remove(filepath.Join(rec.Dir, completeMarker)); err != nil {
					t.Fatal(err)
				}
			case "declared-path":
				if err := os.Remove(filepath.Join(rec.Dir, "home", filepath.FromSlash(extra))); err != nil {
					t.Fatal(err)
				}
			}

			failure := preparationPatchFailure()
			pf.state = PatchedState{Recipe: patchedRecipeOne, PatchFailure: failure,
				Reason: "the selected good build is unavailable"}
			pf.advances = 0
			w.floor.AllowPatchFailures = func() bool { return true }
			prepared, err := w.floor.PreparePatched(context.Background(), p, false)
			var got *packsrc.PatchFailure
			if err == nil || prepared == nil || prepared.delivery == patchedPreparationKeepInstalled ||
				!errors.As(err, &got) || !reflect.DeepEqual(got, failure) || got.Log != failure.Log {
				t.Fatalf("damaged installed copy was accepted or lost its failure: delivery=%v prepared=%+v failure=%+v err=%v",
					prepared.delivery, prepared, got, err)
			}
			if pf.advances != 0 {
				t.Errorf("refusing damaged installed copy advanced %d times", pf.advances)
			}
		})
	}
}

func TestEnsurePreparedRevalidatesKeepInstalledCopy(t *testing.T) {
	p := patchedProgram()
	w, pf, rec, _ := installPatchedPreparationCopy(t, p)
	failure := preparationPatchFailure()
	pf.state = PatchedState{Recipe: patchedRecipeOne, PatchFailure: failure,
		Reason: "the selected good build is unavailable"}
	w.floor.AllowPatchFailures = func() bool { return true }
	prepared, err := w.floor.PreparePatched(context.Background(), p, false)
	if err != nil || prepared.delivery != patchedPreparationKeepInstalled {
		t.Fatalf("valid installed copy was not prepared for keep: delivery=%v err=%v", prepared.delivery, err)
	}
	if err := os.Remove(filepath.Join(rec.Dir, completeMarker)); err != nil {
		t.Fatal(err)
	}
	pf.advances = 0
	beforeLauncher, err := os.ReadFile(w.floor.Launcher(p.Bin()))
	if err != nil {
		t.Fatal(err)
	}
	beforeRecord, err := os.ReadFile(w.floor.recordPath(p.Bin()))
	if err != nil {
		t.Fatal(err)
	}
	st, outcome, err := w.floor.EnsurePrepared(context.Background(), p, prepared)
	var got *packsrc.PatchFailure
	if err == nil || outcome != "" || !errors.As(err, &got) || !reflect.DeepEqual(got, failure) || got.Log != failure.Log {
		t.Fatalf("changed installed copy was kept or lost its failure: status=%+v outcome=%q failure=%+v err=%v", st, outcome, got, err)
	}
	if pf.advances != 0 {
		t.Errorf("keep revalidation advanced %d times", pf.advances)
	}
	afterLauncher, launcherErr := os.ReadFile(w.floor.Launcher(p.Bin()))
	afterRecord, recordErr := os.ReadFile(w.floor.recordPath(p.Bin()))
	if launcherErr != nil || recordErr != nil || !reflect.DeepEqual(beforeLauncher, afterLauncher) || !reflect.DeepEqual(beforeRecord, afterRecord) {
		t.Errorf("failed keep revalidation published floor metadata: launcher err=%v record err=%v", launcherErr, recordErr)
	}
}

func TestPreparePatchedDoesNotHandDamagedInstalledCopyToResolver(t *testing.T) {
	p := patchedProgram()
	w, pf, _, _ := installPatchedPreparationCopy(t, p)
	if err := os.Chmod(w.floor.Launcher(p.Bin()), 0o600); err != nil {
		t.Fatal(err)
	}
	failure := preparationPatchFailure()
	pf.state = PatchedState{Recipe: patchedRecipeOne, PatchFailure: failure}
	w.floor.AllowPatchFailures = func() bool { return true }
	called := false
	w.floor.ResolvePatched = func(_ context.Context, _ Program, installed *Record, _ bool) PatchedState {
		called = true
		if installed != nil {
			t.Errorf("resolver received a damaged installed copy: %+v", installed)
		}
		return pf.state
	}
	prepared, err := w.floor.PreparePatched(context.Background(), p, false)
	var got *packsrc.PatchFailure
	if !called || err == nil || prepared == nil || prepared.delivery == patchedPreparationKeepInstalled ||
		!errors.As(err, &got) || !reflect.DeepEqual(got, failure) {
		t.Fatalf("damaged installed copy entered resolution or bypass: called=%v delivery=%v failure=%+v err=%v",
			called, prepared.delivery, got, err)
	}
}

func TestPreparePatchedKeepsReadableNonExecutableInterpretedScript(t *testing.T) {
	p := patchedProgram()
	w, pf, rec, _ := installPatchedPreparationCopy(t, p)
	script, err := filepath.EvalSymlinks(rec.Entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(script, 0o644); err != nil {
		t.Fatal(err)
	}
	failure := preparationPatchFailure()
	pf.state = PatchedState{Recipe: patchedRecipeOne, PatchFailure: failure}
	w.floor.AllowPatchFailures = func() bool { return true }
	prepared, err := w.floor.PreparePatched(context.Background(), p, false)
	if err != nil || prepared.delivery != patchedPreparationKeepInstalled {
		t.Fatalf("readable Node script was rejected for lacking its own execute bit: delivery=%v err=%v", prepared.delivery, err)
	}
}

func TestPreparePatchedRejectsUnreadableInterpretedScript(t *testing.T) {
	p := patchedProgram()
	w, pf, rec, _ := installPatchedPreparationCopy(t, p)
	failure := preparationPatchFailure()
	pf.state = PatchedState{Recipe: patchedRecipeOne, PatchFailure: failure}
	w.floor.AllowPatchFailures = func() bool { return true }
	resolved, err := filepath.EvalSymlinks(rec.Entry)
	if err != nil {
		t.Fatal(err)
	}
	originalOpen := openPreparedInstalledFile
	called := false
	openPreparedInstalledFile = func(path string) (*os.File, error) {
		if path == resolved {
			called = true
			return nil, errors.New("script read denied by fixture")
		}
		return originalOpen(path)
	}
	t.Cleanup(func() { openPreparedInstalledFile = originalOpen })
	prepared, err := w.floor.PreparePatched(context.Background(), p, false)
	var got *packsrc.PatchFailure
	if !called || err == nil || prepared == nil || prepared.delivery == patchedPreparationKeepInstalled ||
		!errors.As(err, &got) || !reflect.DeepEqual(got, failure) || got.Log != failure.Log {
		t.Fatalf("unreadable interpreted script was accepted or lost its failure: called=%v delivery=%v failure=%+v err=%v",
			called, prepared.delivery, got, err)
	}
}

func TestAliasedFloorRootKeepsPrunedInstalledCopyAcrossResolverAndEnsure(t *testing.T) {
	p := patchedProgram()
	w, pf := patchedWorld(t)
	realFloor := w.floor.Dir
	if err := os.MkdirAll(realFloor, 0o700); err != nil {
		t.Fatal(err)
	}
	aliasedFloor := filepath.Join(w.root, "host-floor-alias")
	if err := os.Symlink(realFloor, aliasedFloor); err != nil {
		t.Fatal(err)
	}
	w.floor.Dir = aliasedFloor
	good := pf.good(forkCommitOne, patchedRecipeOne, "v1.0.0 (11111111) + 2 patches", true)
	pf.state = PatchedState{Recipe: patchedRecipeOne, Good: good}
	st, outcome, err := w.floor.Ensure(context.Background(), p)
	if err != nil || outcome != Installed || st.Record == nil || !strings.HasPrefix(st.Record.Dir, aliasedFloor+string(filepath.Separator)) {
		t.Fatalf("install through floor alias: status=%+v outcome=%q err=%v", st, outcome, err)
	}

	failure := preparationPatchFailure()
	pf.state = PatchedState{Recipe: patchedRecipeOne, Good: good, PatchFailure: failure}
	w.floor.AllowPatchFailures = func() bool { return true }
	if err := os.RemoveAll(good.Entry.Root); err != nil {
		t.Fatal(err)
	}
	pf.advances = 0
	var resolverRecord *Record
	resolverCalled := false
	w.floor.ResolvePatched = func(_ context.Context, _ Program, installed *Record, allowAdvance bool) PatchedState {
		resolverCalled = true
		if allowAdvance {
			t.Error("literal failure preparation authorized an advance")
		}
		resolverRecord = cloneRecord(installed)
		return pf.state
	}
	prepared, err := w.floor.PreparePatched(context.Background(), p, false)
	if err != nil || prepared == nil || prepared.delivery != patchedPreparationKeepInstalled {
		t.Fatalf("aliased installed copy did not satisfy the pruned-store bypass: preparation=%+v err=%v", prepared, err)
	}
	if !resolverCalled || resolverRecord == nil || resolverRecord.Entry != st.Record.Entry ||
		!strings.HasPrefix(resolverRecord.Entry, aliasedFloor+string(filepath.Separator)) {
		t.Fatalf("resolver did not receive the original aliased Record path: called=%v received=%+v original=%+v",
			resolverCalled, resolverRecord, st.Record)
	}
	if _, err := os.Stat(good.Entry.Root); !os.IsNotExist(err) {
		t.Fatalf("positive did not retain a pruned-store state: %v", err)
	}
	kept, outcome, err := w.floor.EnsurePrepared(context.Background(), p, prepared)
	if err != nil || outcome != Current || kept.Record == nil {
		t.Fatalf("aliased keep consumption failed: status=%+v outcome=%q err=%v", kept, outcome, err)
	}
	if pf.advances != 0 {
		t.Errorf("aliased pruned-copy preparation ran %d new advances", pf.advances)
	}
}

func TestEnsurePreparedMaterializationRefusalPreservesCauseAndFailure(t *testing.T) {
	w, pf := patchedWorld(t)
	p := patchedProgram()
	good := patchedBuildWithBinaryHomeReference(t, pf, p)
	failure := preparationPatchFailure()
	pf.state = PatchedState{Recipe: patchedRecipeOne, Good: good, PatchFailure: failure}
	w.floor.AllowPatchFailures = func() bool { return true }
	prepared, err := w.floor.PreparePatched(context.Background(), p, false)
	if err != nil || prepared == nil || prepared.delivery != patchedPreparationInstallGood ||
		prepared.selectedGood == nil || prepared.selectedGood.Entry.Key != good.Entry.Key {
		t.Fatalf("binary-reference build was not selected for literal bypass: preparation=%+v err=%v", prepared, err)
	}
	pf.advances = 0
	st, outcome, err := w.floor.EnsurePrepared(context.Background(), p, prepared)
	var got *packsrc.PatchFailure
	if err == nil || outcome != "" || st.Disposition != NoEntry || !errors.As(err, &got) || !reflect.DeepEqual(got, failure) || got.Log != failure.Log {
		t.Fatalf("materialization refusal became success or lost the original failure: status=%+v outcome=%q failure=%+v err=%v",
			st, outcome, got, err)
	}
	if !errors.Is(err, capture.ErrNotRelocatable) || !errors.Is(err, ErrNoEntry) {
		t.Errorf("materialization refusal lost its independent sentinels: ErrNotRelocatable=%v ErrNoEntry=%v err=%v",
			errors.Is(err, capture.ErrNotRelocatable), errors.Is(err, ErrNoEntry), err)
	}
	if pf.advances != 0 {
		t.Errorf("materialization refusal ran %d advances", pf.advances)
	}
	for _, path := range []string{w.floor.Launcher(p.Bin()), w.floor.recordPath(p.Bin())} {
		if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
			t.Errorf("materialization refusal published %s: %v", path, statErr)
		}
	}
}

func patchedBuildWithBinaryHomeReference(t *testing.T, pf *patchedFake, p Program) *PatchedBuild {
	t.Helper()
	staged, err := pf.bs.store.Stage("forkcli-binary-refusal")
	if err != nil {
		t.Fatal(err)
	}
	tree := capture.TreeDir(staged)
	binDir := filepath.Join(tree, ".npm-global", "bin")
	pkgDir := filepath.Join(tree, ".npm-global", "lib", "node_modules", p.Bin())
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(pkgDir, "cli.js")
	script := []byte("#!/usr/bin/env node\nconsole.log('" + p.Bin() + "')\n")
	if err := os.WriteFile(scriptPath, script, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(pkgDir, "config.json")
	configRel := ".npm-global/lib/node_modules/" + p.Bin() + "/config.json"
	config := []byte(`{"root":"/home/agent/.npm-global/lib/node_modules/` + p.Bin() + `"}` + "\x00")
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatal(err)
	}
	linkRel := ".npm-global/bin/" + p.Bin()
	if err := os.Symlink("../lib/node_modules/"+p.Bin()+"/cli.js", filepath.Join(tree, filepath.FromSlash(linkRel))); err != nil {
		t.Fatal(err)
	}
	manifest := &capture.Manifest{
		Schema: capture.ManifestSchema, Home: "/home/agent", Platform: capture.Platform(),
		Surfaces: []string{".npm-global", ".local", "go"}, Excluded: []string{},
		Entries: []capture.ManifestEntry{
			{Path: ".npm-global", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".npm-global/bin", Kind: capture.KindDir, Mode: "0755"},
			{Path: linkRel, Kind: capture.KindSymlink, Target: "../lib/node_modules/" + p.Bin() + "/cli.js"},
			{Path: ".npm-global/lib", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".npm-global/lib/node_modules", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".npm-global/lib/node_modules/" + p.Bin(), Kind: capture.KindDir, Mode: "0755"},
			{Path: ".npm-global/lib/node_modules/" + p.Bin() + "/cli.js", Kind: capture.KindFile, Mode: "0755", Size: int64(len(script))},
			{Path: configRel, Kind: capture.KindFile, Mode: "0644", Size: int64(len(config))},
		},
		AbsoluteRefs: []capture.AbsoluteRef{{Path: configRel, Kind: capture.RefFileContent, Value: "/home/agent"}},
		RefScan:      capture.RefScanFull, Relocatable: true,
	}
	if err := capture.WriteManifest(staged, manifest); err != nil {
		t.Fatal(err)
	}
	entry, err := pf.bs.store.AdmitEntry(staged)
	if err != nil {
		t.Fatal(err)
	}
	return &PatchedBuild{Commit: forkCommitOne, Recipe: patchedRecipeOne,
		Label: "v1.0.0 (11111111) + 2 patches", Entry: entry}
}

func TestEnsurePreparedRetainsFailureForReapedSelectedStoreEntry(t *testing.T) {
	w, pf := patchedWorld(t)
	p := patchedProgram()
	good := pf.good(forkCommitOne, patchedRecipeOne, "v1.0.0 (11111111) + 2 patches", true)
	failure := preparationPatchFailure()
	pf.state = PatchedState{Recipe: patchedRecipeOne, Good: good, PatchFailure: failure}
	w.floor.AllowPatchFailures = func() bool { return true }
	prepared, err := w.floor.PreparePatched(context.Background(), p, false)
	if err != nil || prepared.delivery != patchedPreparationInstallGood {
		t.Fatalf("store-only literal bypass was not prepared: delivery=%v err=%v", prepared.delivery, err)
	}
	pf.advances = 0
	if err := os.RemoveAll(good.Entry.Root); err != nil {
		t.Fatal(err)
	}
	st, outcome, err := w.floor.EnsurePrepared(context.Background(), p, prepared)
	var got *packsrc.PatchFailure
	if err == nil || outcome != "" || !errors.As(err, &got) || !reflect.DeepEqual(got, failure) || got.Log != failure.Log {
		t.Fatalf("reaped selected store copy became success or lost the original failure: status=%+v outcome=%q failure=%+v err=%v",
			st, outcome, got, err)
	}
	if !errors.Is(err, capture.ErrNotCaptured) {
		t.Errorf("selected-entry operational sentinel was not retained: %v", err)
	}
	if pf.advances != 0 {
		t.Errorf("reaped selected entry caused %d advances", pf.advances)
	}
	for _, path := range []string{w.floor.Launcher(p.Bin()), w.floor.recordPath(p.Bin())} {
		if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
			t.Errorf("failed consumption published %s: %v", path, statErr)
		}
	}
	if _, statErr := os.Stat(w.floor.Dir); !os.IsNotExist(statErr) {
		t.Errorf("reaped selection wrote the floor before refusal: %v", statErr)
	}
}

func TestEnsurePreparedRetainsFailureAndIndependentInstallErrors(t *testing.T) {
	for _, kind := range []string{"directory", "lock", "node-install"} {
		t.Run(kind, func(t *testing.T) {
			w, pf := patchedWorld(t)
			p := patchedProgram()
			good := pf.good(forkCommitOne, patchedRecipeOne, "v1.0.0 (11111111) + 2 patches", true)
			failure := preparationPatchFailure()
			pf.state = PatchedState{Recipe: patchedRecipeOne, Good: good, PatchFailure: failure}
			w.floor.AllowPatchFailures = func() bool { return true }
			prepared, err := w.floor.PreparePatched(context.Background(), p, false)
			if err != nil || prepared.delivery != patchedPreparationInstallGood {
				t.Fatalf("store-only literal bypass was not prepared: delivery=%v err=%v", prepared.delivery, err)
			}
			pf.advances = 0
			switch kind {
			case "directory":
				if err := os.MkdirAll(w.floor.Dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(w.floor.Dir, "bin"), []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "lock":
				if err := os.MkdirAll(filepath.Join(w.floor.Dir, "locks"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(w.floor.lockPath(p.Bin()), 0o700); err != nil {
					t.Fatal(err)
				}
			case "node-install":
				platform, ok := nodePlatform(w.floor.GOOS, w.floor.GOARCH)
				if !ok {
					t.Fatalf("no Node fixture for %s/%s", w.floor.GOOS, w.floor.GOARCH)
				}
				w.floor.Node.Pinned[platform] = strings.Repeat("0", 64)
			}
			st, outcome, err := w.floor.EnsurePrepared(context.Background(), p, prepared)
			var got *packsrc.PatchFailure
			if err == nil || outcome != "" || !errors.As(err, &got) || !reflect.DeepEqual(got, failure) || got.Log != failure.Log {
				t.Fatalf("%s failure became success or lost the preparation failure: status=%+v outcome=%q failure=%+v err=%v",
					kind, st, outcome, got, err)
			}
			if pf.advances != 0 {
				t.Errorf("%s failure caused %d advances", kind, pf.advances)
			}
			switch kind {
			case "directory":
				if !errors.Is(err, syscall.ENOTDIR) {
					t.Errorf("directory error chain lost ENOTDIR: %v", err)
				}
			case "lock":
				if !errors.Is(err, syscall.EISDIR) {
					t.Errorf("lock error chain lost EISDIR: %v", err)
				}
			case "node-install":
				if !strings.Contains(err.Error(), "sha256") {
					t.Errorf("Node install failure was not retained as the independent diagnosis: %v", err)
				}
			}
			for _, path := range []string{w.floor.Launcher(p.Bin()), w.floor.recordPath(p.Bin())} {
				if kind == "directory" && path == w.floor.Launcher(p.Bin()) {
					binDir, statErr := os.Lstat(filepath.Join(w.floor.Dir, "bin"))
					if statErr != nil || !binDir.Mode().IsRegular() {
						t.Errorf("directory obstruction did not prevent launcher publication: info=%v err=%v", binDir, statErr)
					}
					continue
				}
				if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
					t.Errorf("failed %s consumption published %s: %v", kind, path, statErr)
				}
			}
		})
	}
}

func TestEnsurePreparedRetainsFailureForChangedHostPlatform(t *testing.T) {
	for _, change := range []string{"GOOS", "GOARCH"} {
		t.Run(change, func(t *testing.T) {
			w, pf := patchedWorld(t)
			p := patchedProgram()
			good := pf.good(forkCommitOne, patchedRecipeOne, "v1.0.0 (11111111) + 2 patches", true)
			failure := preparationPatchFailure()
			pf.state = PatchedState{Recipe: patchedRecipeOne, Good: good, PatchFailure: failure}
			w.floor.AllowPatchFailures = func() bool { return true }
			prepared, err := w.floor.PreparePatched(context.Background(), p, false)
			if err != nil {
				t.Fatalf("prepare selected store delivery: %v", err)
			}
			pf.advances = 0
			if change == "GOOS" {
				w.floor.GOOS = "darwin"
			} else {
				w.floor.GOARCH = "arm64"
			}
			_, outcome, err := w.floor.EnsurePrepared(context.Background(), p, prepared)
			var got *packsrc.PatchFailure
			if err == nil || outcome != "" || !errors.As(err, &got) || !reflect.DeepEqual(got, failure) || got.Log != failure.Log {
				t.Fatalf("changed %s binding was accepted or lost its originating failure: failure=%+v outcome=%q err=%v", change, got, outcome, err)
			}
			if pf.advances != 0 {
				t.Errorf("changed %s binding caused %d advances", change, pf.advances)
			}
			if _, statErr := os.Stat(w.floor.Dir); !os.IsNotExist(statErr) {
				t.Errorf("changed %s binding wrote floor artifacts before refusal: %v", change, statErr)
			}
		})
	}
}

func TestEnsurePreparedDoesNotInventPatchFailureForIndependentError(t *testing.T) {
	w, pf := patchedWorld(t)
	p := patchedProgram()
	pf.state = PatchedState{Recipe: patchedRecipeOne, Good: pf.good(forkCommitOne, patchedRecipeOne, "v1.0.0 (11111111) + 2 patches", true)}
	prepared, err := w.floor.PreparePatched(context.Background(), p, false)
	if err != nil {
		t.Fatalf("prepare clean selected build: %v", err)
	}
	if err := os.MkdirAll(w.floor.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.floor.Dir, "bin"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, outcome, err := w.floor.EnsurePrepared(context.Background(), p, prepared)
	var failure *packsrc.PatchFailure
	if err == nil || outcome != "" || errors.As(err, &failure) {
		t.Fatalf("ordinary directory failure fabricated a PatchFailure or success: outcome=%q failure=%+v err=%v", outcome, failure, err)
	}
}

func TestPreparePatchedKeepsReadableDirectExecutableCopy(t *testing.T) {
	p := patchedProgram()
	w, pf, rec, _ := installPatchedPreparationCopy(t, p)
	_ = directExecutablePreparationRecord(t, w, rec)
	failure := preparationPatchFailure()
	pf.state = PatchedState{Recipe: patchedRecipeOne, PatchFailure: failure}
	w.floor.AllowPatchFailures = func() bool { return true }
	prepared, err := w.floor.PreparePatched(context.Background(), p, false)
	if err != nil || prepared.delivery != patchedPreparationKeepInstalled {
		t.Fatalf("valid directly-executed installed copy was rejected: delivery=%v err=%v", prepared.delivery, err)
	}
}

func directExecutablePreparationRecord(t *testing.T, w *world, original *Record) *Record {
	t.Helper()
	// Replace the entry rather than write through it: where the install hardlinked it out of the
	// capture store (any filesystem without reflink, CI's ext4 among them), the entry IS the
	// store's sealed read-only inode, which a non-root writer cannot open and root would corrupt.
	if err := os.Remove(original.Entry); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original.Entry, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := cloneRecord(original)
	rec.Node = ""
	rec.Exec = []string{rec.Entry}
	if err := w.floor.writeRecord(rec); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(w.floor.Launcher(rec.Bin), []byte(launcherScript(rec)), 0o700); err != nil {
		t.Fatal(err)
	}
	return rec
}

func installPatchedPreparationCopy(t *testing.T, p Program) (*world, *patchedFake, *Record, *PatchedBuild) {
	t.Helper()
	w, pf := patchedWorld(t)
	entry := pf.bs.addFor(p, forkCommitOne, true)
	good := &PatchedBuild{Commit: forkCommitOne, Recipe: patchedRecipeOne,
		Label: "v1.0.0 (11111111) + 2 patches", Entry: entry}
	pf.state = PatchedState{Recipe: patchedRecipeOne, Good: good}
	st, outcome, err := w.floor.Ensure(context.Background(), p)
	if err != nil || outcome != Installed || st.Record == nil {
		t.Fatalf("install initial patched copy: status=%+v outcome=%q err=%v", st, outcome, err)
	}
	return w, pf, st.Record, good
}

func preparationPatchFailure() *packsrc.PatchFailure {
	return &packsrc.PatchFailure{
		Owner: "forkpack/forkcli", Series: patchedRecipeOne,
		Target: packsrc.ListEntry{Commit: "3333333333333333333333333333333333333333", Tag: "v1.2.3"},
		Kind:   "conflict", Member: "0001-conflict.patch", Paths: []string{"src/cli.go"},
		Log: "original full replay transcript",
	}
}
