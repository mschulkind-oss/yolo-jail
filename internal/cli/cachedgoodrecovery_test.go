package cli

import (
	"bytes"
	encodingbinary "encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// The production build-slot caller must let an operator explicitly run the recorded Good.Entry
// after a changed desired recipe has a retained build failure. This reaches runBuildSlot and
// addForkKeys; a resolver-only test would miss the launch-delivery path that left the program out.
func TestBuildSlotRunsExactCachedGoodWithoutRepeatingFailedAdvance(t *testing.T) {
	fx, oldKey := admittedCachedGood(t)
	manifestPath := filepath.Join(fx.forkDir, "pack.json")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(manifest), `"build":"sh build.sh"`, `"build":"sh build2.sh"`, 1)
	if updated == string(manifest) {
		t.Fatal("fixture's build recipe was not found")
	}
	writeFile(t, manifestPath, updated)
	fx.rc = 2
	failed, failedOutput, _ := fx.launch(t, "podman")
	if failed.delivery.Key != "" || !failed.failed {
		t.Fatalf("fixture did not reproduce a failed current recipe: %+v\n%s", failed, failedOutput)
	}
	if len(fx.builds) != 3 {
		t.Fatalf("fixture ran %d builds, want the prior success and failed current-recipe attempts", len(fx.builds))
	}

	recordPath := patchedAdvanceStore(true).CheckRecordPath("forkpack/tool")
	beforeRecord, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	var handed []run.HandedFork
	request := run.ForkBuildRequest{Pins: []packload.ForkPin{{Fork: fx.fork(t)}},
		CachedGoodOwner: "forkpack/tool", Platform: patchedTestPlatform, Runtime: "podman",
		Workspace: t.TempDir(), Interrupt: &run.ActInterrupt{},
		Hand: func(_ string, h run.HandedFork) error { handed = append(handed, h); return nil },
	}
	got, _ := runBuildSlot(run.BuildSlotRequest{Forks: &request, MaxBuilds: 1}, &output, false)
	if got["tool"].Key != oldKey {
		t.Fatalf("explicit cached-good selection delivered %+v, want exact recorded entry %s; output:\n%s",
			got["tool"], oldKey, output.String())
	}
	if len(fx.builds) != 3 {
		t.Errorf("cached-good selection ran another build: builds are %q", fx.builds)
	}
	if len(handed) != 1 || handed[0].Key != oldKey {
		t.Errorf("delivery hand = %+v, want the old entry %s", handed, oldKey)
	}
	afterRecord, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterRecord, beforeRecord) {
		t.Errorf("cached-good selection changed the check record containing the current failure:\nbefore %s\nafter %s",
			beforeRecord, afterRecord)
	}
	if !strings.Contains(output.String(), "Cached good:") || !strings.Contains(output.String(), "Current build failure remains recorded") ||
		!strings.Contains(output.String(), "yolo capture tool") {
		t.Errorf("selection did not disclose the old good, retained failure, and repair:\n%s", output.String())
	}
}

func TestBuildSlotRefusesCachedGoodWhenNoGoodIsRecorded(t *testing.T) {
	fx, _ := admittedCachedGood(t)
	if err := patchedAdvanceStore(true).WithCheckRecord("forkpack/tool", nil, func(r *packsrc.CheckRecord,
		_ error, _ func() error) (bool, error) {
		r.Good = nil
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	d, _ := askForCachedGood(t, fx, "forkpack/tool")
	if d.Key != "" || !strings.Contains(d.Reason, "no recorded Good build") {
		t.Errorf("a surviving store entry without a Good record was guessed: %+v", d)
	}
}

func TestBuildSlotRefusesCachedGoodWhenGoodEntryWasPruned(t *testing.T) {
	fx, key := admittedCachedGood(t)
	if err := (&capture.Store{Dir: paths.CapturesDir()}).ReapEntry(key); err != nil {
		t.Fatal(err)
	}
	d, _ := askForCachedGood(t, fx, "forkpack/tool")
	if d.Key != "" || !strings.Contains(d.Reason, "missing or pruned") {
		t.Errorf("a pruned Good.Entry was not refused: %+v", d)
	}
}

func TestBuildSlotRefusesCachedGoodWhenEntryContentsAreCorrupt(t *testing.T) {
	fx, key := admittedCachedGood(t)
	entry, err := (&capture.Store{Dir: paths.CapturesDir()}).Resolve(key)
	if err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(entry.Tree, ".local", "bin", "tool")
	if err := os.Chmod(program, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(program)
	if err != nil {
		t.Fatal(err)
	}
	body[len(body)-2] ^= 1
	if err := os.WriteFile(program, body, 0o755); err != nil {
		t.Fatal(err)
	}

	d, _ := askForCachedGood(t, fx, "forkpack/tool")
	if d.Key != "" || !strings.Contains(d.Reason, "content address") {
		t.Errorf("corrupt Good.Entry was not refused by its digest: %+v", d)
	}
}

func TestBuildSlotKeepsExactGoodCompatibleWithAdditionalCurrentOutputs(t *testing.T) {
	fx, _ := admittedCachedGood(t)
	manifestPath := filepath.Join(fx.forkDir, "pack.json")
	manifest := strings.Replace(mustRead(t, manifestPath), `"produces":[".local/bin/tool"]`,
		`"produces":[".local/bin/tool",".local/lib/required"]`, 1)
	if manifest == mustRead(t, manifestPath) {
		t.Fatal("fixture output declaration was not found")
	}
	writeFile(t, manifestPath, manifest)
	d, out := askForCachedGood(t, fx, "forkpack/tool")
	if d.Key == "" {
		t.Errorf("an additional current output declaration rejected the exact old build despite its unchanged runnable program: %+v\n%s", d, out)
	}
}

func TestBuildSlotDoesNotUseCachedGoodWithoutExplicitOwner(t *testing.T) {
	fx, oldKey := admittedCachedGood(t)
	manifestPath := filepath.Join(fx.forkDir, "pack.json")
	manifest := strings.Replace(mustRead(t, manifestPath), `"build":"sh build.sh"`, `"build":"sh build2.sh"`, 1)
	writeFile(t, manifestPath, manifest)
	fx.rc = 2
	failed, _, _ := fx.launch(t, "podman")
	if failed.delivery.Key != "" || !failed.failed {
		t.Fatalf("fixture did not leave the changed recipe unavailable: %+v", failed)
	}
	d, _ := askForCachedGood(t, fx, "")
	if d.Key == oldKey {
		t.Errorf("strict default silently served the old Good entry %s", oldKey)
	}
}

func admittedCachedGood(t *testing.T) (*patchedAdvanceFixture, string) {
	t.Helper()
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	good, _, _ := fx.launch(t, "podman")
	if good.delivery.Key == "" {
		t.Fatalf("fixture did not admit the prior good build:\n%s", good.delivery.Reason)
	}
	return fx, good.delivery.Key
}

func askForCachedGood(t *testing.T, fx *patchedAdvanceFixture, owner string) (entrypoint.ForkDelivery, string) {
	t.Helper()
	var output bytes.Buffer
	request := run.ForkBuildRequest{Pins: []packload.ForkPin{{Fork: fx.fork(t)}}, CachedGoodOwner: owner,
		Platform: patchedTestPlatform, Runtime: "podman", Workspace: t.TempDir(), Interrupt: &run.ActInterrupt{},
		Hand: func(string, run.HandedFork) error { return nil },
	}
	got, _ := runBuildSlot(run.BuildSlotRequest{Forks: &request, MaxBuilds: 1}, &output, false)
	return got["tool"], output.String()
}

func TestBuildSlotRefusesMatchingReceiptThatIsNotARecordAct(t *testing.T) {
	fx, key := admittedCachedGood(t)
	manifestPath := filepath.Join(fx.forkDir, "pack.json")
	manifest := strings.Replace(mustRead(t, manifestPath), `"build":"sh build.sh"`, `"build":"sh build2.sh"`, 1)
	writeFile(t, manifestPath, manifest)
	fx.rc = 2
	failed, _, _ := fx.launch(t, "podman")
	if !failed.failed {
		t.Fatal("the fixture did not record a new ordinary build failure")
	}
	entry, err := (&capture.Store{Dir: paths.CapturesDir()}).Resolve(key)
	if err != nil {
		t.Fatal(err)
	}
	receipts := capture.ReceiptsPath(entry.Root)
	data, err := os.ReadFile(receipts)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(data), `"act":"record"`, `"act":"materialize"`, 1)
	if changed == string(data) {
		t.Fatalf("fixture has no record act receipt: %s", data)
	}
	if err := os.WriteFile(receipts, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	d, out := askForCachedGood(t, fx, "forkpack/tool")
	if d.Key != "" || !strings.Contains(d.Reason, "no admission record") {
		t.Errorf("non-record materialization receipt admitted as original build: %+v\n%s", d, out)
	}
	if !strings.Contains(out, "Current build failure remains recorded") || !strings.Contains(out, "yolo capture tool") ||
		strings.Contains(out, "yolo pack rebase") {
		t.Errorf("refusal lost the ordinary current build failure or fabricated patch-rejection repair:\n%s", out)
	}
}

func TestBuildSlotAcceptsPersistedGoodWithOptionalLegacyAttributionOmitted(t *testing.T) {
	fx, key := admittedCachedGood(t)
	if err := patchedAdvanceStore(true).WithCheckRecord("forkpack/tool", nil, func(r *packsrc.CheckRecord,
		_ error, _ func() error) (bool, error) {
		r.Good.Series, r.Good.Recipe, r.Good.Tree = "", "", ""
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	d, out := askForCachedGood(t, fx, "forkpack/tool")
	if d.Key != key {
		t.Errorf("persisted exact Good.Entry was rejected for absent optional attribution: %+v\n%s", d, out)
	}
}

func TestCachedGoodDisclosureKeepsCurrentTypedPatchFailureOnSuccessAndRefusal(t *testing.T) {
	fx, key := admittedCachedGood(t)
	fork := fx.fork(t)
	series, err := fork.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	if err := patchedAdvanceStore(true).WithCheckRecord(fork.Key(), nil, func(r *packsrc.CheckRecord,
		_ error, _ func() error) (bool, error) {
		if r.Check == nil || len(r.Check.List) == 0 {
			return false, fmt.Errorf("fixture check has no target list")
		}
		inputs, _, _, err := fork.CheckWant(series).Inputs()
		if err != nil {
			return false, err
		}
		r.PatchFailure = &packsrc.PatchFailure{Owner: fork.Key(), Inputs: inputs, Series: series.Digest,
			Target: r.Check.List[0], Kind: "conflict", Member: "0001-example.patch", Paths: []string{"src/main.go"}}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	d, success := askForCachedGood(t, fx, fork.Key())
	if d.Key != key || !strings.Contains(success, "Current typed patch failure remains recorded") ||
		!strings.Contains(success, "yolo pack rebase forkpack/tool --onto") ||
		!strings.Contains(success, "old build does not contain current edits") {
		t.Errorf("success omitted current typed patch failure or old-build warning: delivery=%+v\n%s", d, success)
	}

	entry, err := (&capture.Store{Dir: paths.CapturesDir()}).Resolve(key)
	if err != nil {
		t.Fatal(err)
	}
	receipts := capture.ReceiptsPath(entry.Root)
	data, err := os.ReadFile(receipts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receipts, []byte(strings.Replace(string(data), `"act":"record"`, `"act":"materialize"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	d, refusal := askForCachedGood(t, fx, fork.Key())
	if d.Key != "" || !strings.Contains(refusal, "ERROR: forkpack/tool: patch application failed") ||
		!strings.Contains(refusal, "yolo pack rebase forkpack/tool --onto") ||
		!strings.Contains(refusal, "No prior build was delivered") {
		t.Errorf("validation refusal omitted current typed failure/repair: delivery=%+v\n%s", d, refusal)
	}
}
func TestBuildSlotHandRefusalRetainsCurrentPackagingFailureDisclosure(t *testing.T) {
	fx, _ := admittedCachedGood(t)
	manifestPath := filepath.Join(fx.forkDir, "pack.json")
	manifest := strings.Replace(mustRead(t, manifestPath), `"build":"sh build.sh"`, `"build":"sh build2.sh"`, 1)
	writeFile(t, manifestPath, manifest)
	fx.rc = 2
	failed, _, _ := fx.launch(t, "podman")
	if !failed.failed {
		t.Fatal("the fixture did not record the changed recipe's ordinary build failure")
	}
	var out bytes.Buffer
	request := run.ForkBuildRequest{Pins: []packload.ForkPin{{Fork: fx.fork(t)}}, CachedGoodOwner: "forkpack/tool",
		Platform: patchedTestPlatform, Runtime: "podman", Workspace: t.TempDir(), Interrupt: &run.ActInterrupt{},
		Hand: func(string, run.HandedFork) error { return fmt.Errorf("fixture hand write denied") }}
	delivered, _ := runBuildSlot(run.BuildSlotRequest{Forks: &request, MaxBuilds: 1}, &out, false)
	if delivered["tool"].Key != "" || !strings.Contains(delivered["tool"].Reason, "fixture hand write denied") ||
		!strings.Contains(out.String(), "Current build failure remains recorded") ||
		!strings.Contains(out.String(), "yolo capture tool") || strings.Contains(out.String(), "yolo pack rebase") {
		t.Errorf("hand refusal lost the ordinary failure or invented a patch rejection: %+v\n%s",
			delivered["tool"], out.String())
	}
}
func TestBuildSlotDoesNotWaitForHeldGoodRecordLock(t *testing.T) {
	fx, _ := admittedCachedGood(t)
	store := patchedAdvanceStore(true)
	locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- store.WithCheckRecord("forkpack/tool", nil, func(*packsrc.CheckRecord, error, func() error) (bool, error) {
			close(locked)
			<-release
			return false, nil
		})
	}()
	<-locked
	d, out := askForCachedGood(t, fx, "forkpack/tool")
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if d.Key != "" || !strings.Contains(d.Reason, "retry this fresh launch") ||
		!strings.Contains(out, "retry the fresh launch after that process finishes") {
		t.Errorf("held record lock blocked or gave no concrete retry: %+v\n%s", d, out)
	}
}

func TestBuildSlotRefusesIntactUnsupportedInterpreterBuilds(t *testing.T) {
	for name, body := range map[string]string{
		"unknown-absolute":            "#!/usr/bin/not-a-real-runtime\nexit 0\n",
		"env-node-unprovisioned":      "#!/usr/bin/env node\nprocess.exit(0)\n",
		"absolute-node-unprovisioned": "#!/usr/bin/node\nprocess.exit(0)\n",
	} {
		t.Run(name, func(t *testing.T) {
			fx := newPatchedAdvanceFixture(t, "")
			if f := fx.fork(t); f.NodeFloor != "" {
				t.Fatalf("fixture unexpectedly provisions Node: node_floor=%q", f.NodeFloor)
			}
			fx.programBody = body
			fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
			good, _, _ := fx.launch(t, "podman")
			if good.delivery.Key == "" {
				t.Fatalf("bad-runtime fixture was not admitted as an intact build: %s", good.delivery.Reason)
			}
			d, out := askForCachedGood(t, fx, "forkpack/tool")
			unsupported := strings.Contains(d.Reason, "unsupported") || strings.Contains(d.Reason, "unprovisioned") ||
				strings.Contains(d.Reason, "requires Node")
			if d.Key != "" || !unsupported {
				t.Errorf("intact unsupported runtime was admitted: %+v\n%s", d, out)
			}
		})
	}
}

func TestBuildSlotRefusesIntactELFForWrongTargetMachine(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	binary, err := os.ReadFile("/bin/true")
	if err != nil {
		t.Skipf("no ELF fixture at /bin/true: %v", err)
	}
	if len(binary) < 20 || string(binary[:4]) != "\x7fELF" {
		t.Skip("/bin/true is not a usable ELF fixture")
	}
	// Change only e_machine, preserving a parseable ELF file whose bytes, manifest and receipt
	// are all admitted normally. The recovery validator must refuse based on target compatibility.
	machine := encodingbinary.LittleEndian.Uint16(binary[18:20]) ^ 1
	encodingbinary.LittleEndian.PutUint16(binary[18:20], machine)
	fx.programBody = string(binary)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	good, _, _ := fx.launch(t, "podman")
	if good.delivery.Key == "" {
		t.Fatalf("wrong-machine fixture was not admitted as intact bytes: %s", good.delivery.Reason)
	}
	d, out := askForCachedGood(t, fx, "forkpack/tool")
	if d.Key != "" || !strings.Contains(d.Reason, "ELF machine") {
		t.Errorf("intact wrong-machine ELF was admitted: %+v\n%s", d, out)
	}
}

func TestBuildSlotCachedGoodRunsThroughGeneratedLauncherAndCaptureMaterializer(t *testing.T) {
	body := "#!/bin/sh\nprintf '%s\\0' \"$@\" > \"$CACHED_GOOD_ARGV\"\nprintf '%s\\n' \"$PWD\" > \"$CACHED_GOOD_CWD\"\nprintf 'old-good-stdout\\n'\nprintf 'old-good-stderr\\n' >&2\nexit 23\n"
	fx := newPatchedAdvanceFixture(t, "")
	fx.programBody = body
	fx.relocatable = true
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	good, _, _ := fx.launch(t, "podman")
	if good.delivery.Key == "" {
		t.Fatal("could not admit the original Good fixture")
	}
	manifestPath := filepath.Join(fx.forkDir, "pack.json")
	manifest := strings.Replace(mustRead(t, manifestPath), `"build":"sh build.sh"`, `"build":"sh build2.sh"`, 1)
	if manifest == mustRead(t, manifestPath) {
		t.Fatal("fixture build recipe was not changed")
	}
	writeFile(t, manifestPath, manifest)
	fx.rc = 2
	failed, _, _ := fx.launch(t, "podman")
	if !failed.failed || failed.delivery.Key != "" {
		t.Fatalf("changed recipe did not fail while old Good remained: %+v", failed)
	}

	var output bytes.Buffer
	request := run.ForkBuildRequest{Pins: []packload.ForkPin{{Fork: fx.fork(t)}}, CachedGoodOwner: "forkpack/tool",
		Platform: patchedTestPlatform, Runtime: "podman", Workspace: t.TempDir(), Interrupt: &run.ActInterrupt{},
		Hand: func(_ string, h run.HandedFork) error { return nil }}
	delivered, _ := runBuildSlot(run.BuildSlotRequest{Forks: &request, MaxBuilds: 1}, &output, false)
	key := delivered["tool"].Key
	if key != good.delivery.Key {
		t.Fatalf("resolver delivered %q, want exact admitted old Good.Entry %q\n%s", key, good.delivery.Key, output.String())
	}

	home, workspace := t.TempDir(), t.TempDir()
	argvPath, cwdPath := filepath.Join(home, "argv.bin"), filepath.Join(home, "cwd.txt")
	deliveryJSON, err := json.Marshal(map[string]map[string]string{"tool": {"key": key}})
	if err != nil {
		t.Fatal(err)
	}
	e := entrypoint.NewEnv(map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": workspace, "YOLO_PACK_ROOT": fx.packs,
		entrypoint.CapturesDirEnv: paths.CapturesDir(), entrypoint.ForkBuildsEnv: string(deliveryJSON),
	})
	e.Stderr = &bytes.Buffer{}
	// GenerateAgentLaunchers reads the staged tree through entrypoint.LoadJailPacks, which
	// switches this process to the jail's tolerant manifest decoder; restore the strict one so
	// every later test's malformed-manifest refusal still bites.
	t.Cleanup(packload.OverrideSkewTolerance(false))
	if err := entrypoint.GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(e.LaunchDir(), "tool")
	stamp := filepath.Join(home, ".local", "state", "yolo", "fork-keys", "tool")
	if err := os.MkdirAll(filepath.Dir(stamp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stamp, []byte("stale-entry\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".local", "bin", "tool"), []byte("#!/bin/sh\necho stale\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fakeBin := t.TempDir()
	wrapper := "#!/bin/sh\nexport YOLO_CACHED_GOOD_MATERIALIZER_HELPER=1\nexec " + shquote.Quote(testBinary) +
		" -test.run=^TestCachedGoodMaterializerHelperProcess$ -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "yolo"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	baseEnv := []string{"HOME=" + home, "PATH=" + fakeBin + ":" + os.Getenv("PATH"),
		"CACHED_GOOD_ARGV=" + argvPath, "CACHED_GOOD_CWD=" + cwdPath}
	readiness := exec.Command(launcher)
	readiness.Dir, readiness.Env = workspace, append(append([]string(nil), baseEnv...), entrypoint.InstallOnlyEnv+"=1")
	var readyOut, readyErr bytes.Buffer
	readiness.Stdout, readiness.Stderr = &readyOut, &readyErr
	if err := readiness.Run(); err != nil {
		t.Fatalf("generated launcher readiness materialization failed: %v\nstdout:\n%s\nstderr:\n%s", err, readyOut.String(), readyErr.String())
	}
	if readyOut.Len() != 0 || !strings.Contains(readyErr.String(), "Materialized tool from fork build "+key) {
		t.Errorf("readiness did not materialize the exact old entry via production capture-materialize: stdout=%q stderr=%q", readyOut.String(), readyErr.String())
	}
	installed, err := os.ReadFile(filepath.Join(home, ".local", "bin", "tool"))
	if err != nil || string(installed) != body {
		t.Fatalf("readiness installed bytes from elsewhere: err=%v", err)
	}
	if _, err := os.Stat(argvPath); !os.IsNotExist(err) {
		t.Errorf("readiness ran the program (argv stat: %v)", err)
	}

	command := exec.Command(launcher, "arg-one", "space argument")
	command.Dir, command.Env = workspace, baseEnv
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 23 {
		t.Fatalf("old Good program exit = %v, want 23; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if stdout.String() != "old-good-stdout\n" || stderr.String() != "old-good-stderr\n" {
		t.Errorf("old Good program streams differ: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	argv, err := os.ReadFile(argvPath)
	if err != nil || string(argv) != "arg-one\x00space argument\x00" {
		t.Errorf("old Good program argv = %q, err %v", argv, err)
	}
	cwd, err := os.ReadFile(cwdPath)
	if err != nil || strings.TrimSpace(string(cwd)) != workspace {
		t.Errorf("old Good program cwd = %q, err %v; want %s", cwd, err, workspace)
	}
}

func TestCachedGoodMaterializerHelperProcess(t *testing.T) {
	if os.Getenv("YOLO_CACHED_GOOD_MATERIALIZER_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "internal" && i+2 < len(os.Args) && os.Args[i+1] == "capture-materialize" {
			os.Exit(runCaptureMaterialize(os.Args[i+2:]))
		}
	}
	fmt.Fprintln(os.Stderr, "helper process received no capture-materialize command")
	os.Exit(2)
}
