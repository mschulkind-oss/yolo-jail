package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

func buildReceiptSnapshot(t *testing.T, store, key string) ([]entrypoint.BuildReceipt, []byte) {
	t.Helper()
	entry, err := (&capture.Store{Dir: store}).Resolve(key)
	if err != nil {
		t.Fatalf("resolving built tree entry %s: %v", key, err)
	}
	path := capture.ReceiptsPath(entry.Root)
	receipts, err := entrypoint.ReadBuildReceipts(path)
	if err != nil {
		t.Fatalf("reading tree build receipts for %s: %v", key, err)
	}
	if len(receipts) != 1 || receipts[0].Key != key || receipts[0].Act != entrypoint.ReceiptActRecord {
		t.Fatalf("tree entry %s has %d build execution receipts, want one record receipt: %+v", key, len(receipts), receipts)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading tree receipt identity for %s: %v", key, err)
	}
	return receipts, data
}

func TestHostSemanticsOptionIsChildScopedAndUsesWithEnvPrecedence(t *testing.T) {
	t.Setenv("YOLO_VERSION", "suite-context-marker")
	parentMarker := os.Getenv("YOLO_VERSION")
	physicalNesting := inContainer()
	physicalMarkers := []bool{}
	for _, marker := range []string{"/run/.containerenv", "/.dockerenv"} {
		_, err := os.Stat(marker)
		physicalMarkers = append(physicalMarkers, err == nil)
	}

	if got := launchEnvValue(launchEnvironment(runConfig{}), "YOLO_VERSION"); got != parentMarker {
		t.Fatalf("the default child marker = %q, want inherited %q", got, parentMarker)
	}
	opted := runConfig{}
	withHostSemantics()(&opted)
	if got := launchEnvValue(launchEnvironment(opted), "YOLO_VERSION"); got != "<unset>" {
		t.Fatalf("the opted-in host-semantic child marker = %q, want it unset", got)
	}
	earlier := runConfig{}
	withEnv("YOLO_VERSION=earlier")(&earlier)
	withHostSemantics()(&earlier)
	if got := launchEnvValue(launchEnvironment(earlier), "YOLO_VERSION"); got != "<unset>" {
		t.Fatalf("host semantics after an earlier withEnv = %q, want it unset", got)
	}
	if got := launchEnvValue(launchEnvironment(runConfig{env: []string{"YOLO_VERSION=caller-value"}}), "YOLO_VERSION"); got != "caller-value" {
		t.Fatalf("a call-specific withEnv marker = %q, want caller-value", got)
	}
	laterOverride := runConfig{}
	withHostSemantics()(&laterOverride)
	withEnv("YOLO_VERSION=last-option")(&laterOverride)
	if got := launchEnvValue(launchEnvironment(laterOverride), "YOLO_VERSION"); got != "last-option" {
		t.Fatalf("the later explicit withEnv option = %q, want last-option", got)
	}
	if got := launchEnvValue(launchEnvironment(runConfig{}), "YOLO_VERSION"); got != parentMarker {
		t.Fatalf("a sibling's default child marker = %q, want inherited %q", got, parentMarker)
	}
	if got := os.Getenv("YOLO_VERSION"); got != parentMarker {
		t.Fatalf("the helper changed the test process marker to %q, want %q", got, parentMarker)
	}
	if got := inContainer(); got != physicalNesting {
		t.Fatalf("the host-semantic option changed physical container detection from %v to %v", physicalNesting, got)
	}
	for i, marker := range []string{"/run/.containerenv", "/.dockerenv"} {
		_, err := os.Stat(marker)
		if (err == nil) != physicalMarkers[i] {
			t.Errorf("the option changed physical container marker %s", marker)
		}
	}
}

func launchEnvValue(env []string, key string) string {
	prefix := key + "="
	value, found := "", false
	for _, pair := range env {
		if strings.HasPrefix(pair, prefix) {
			value, found = strings.TrimPrefix(pair, prefix), true
		}
	}
	if !found {
		return "<unset>"
	}
	return value
}

func podmanRunArgumentRecorder(t *testing.T) (runOption, string) {
	t.Helper()
	if detectRuntime() != "podman" || !inContainer() {
		return func(*runConfig) {}, ""
	}
	realPodman, err := exec.LookPath("podman")
	if err != nil {
		t.Fatalf("finding podman for the nested argv witness: %v", err)
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "run-argv.log")
	script := "#!/bin/sh\n" +
		"if [ \"${1-}\" = run ]; then\n" +
		"  { printf 'BEGIN\\n'; for arg do printf '%s\\n' \"$arg\"; done; printf 'END\\n'; } >> \"$YOLO_TEST_PODMAN_RUN_LOG\"\n" +
		"fi\n" +
		"exec \"$YOLO_TEST_REAL_PODMAN\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "podman"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing transparent podman argv recorder: %v", err)
	}
	path := dir + string(os.PathListSeparator) + os.Getenv("PATH")
	return withEnv("PATH="+path, "YOLO_TEST_REAL_PODMAN="+realPodman,
		"YOLO_TEST_PODMAN_RUN_LOG="+logPath), logPath
}

func assertPhysicalNestedPodmanControls(t *testing.T, workspace, output, logPath string) {
	t.Helper()
	if detectRuntime() != "podman" || !inContainer() {
		t.Logf("physical nested Podman controls not applicable: runtime=%q inContainer=%v", detectRuntime(), inContainer())
		return
	}
	if logPath == "" {
		t.Fatal("physical nested Podman controls lack their argv recorder")
	}
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading recorded nested Podman run argv: %v", err)
	}
	var call []string
	found := false
	for _, line := range strings.Split(string(calls), "\n") {
		switch line {
		case "BEGIN":
			call = call[:0]
		case "END":
			for i := 0; i+1 < len(call); i++ {
				if call[i] == "--name" && call[i+1] == naming.FromWorkspace(workspace) {
					found = containsArgPair(call, "--userns", "host") && containsArg(call, "--net=host")
				}
			}
		case "":
		default:
			call = append(call, line)
		}
	}
	if !found {
		t.Fatalf("the child-only semantic option changed or omitted the physical nested Podman's userns/network argv; recorded calls:\n%s", calls)
	}
	if !strings.Contains(output, "AUTO_HOST_LOOPBACK=shared") {
		t.Fatalf("the nested jail did not retain shared loopback after the child-only semantic opt-in:\n%s", output)
	}
	t.Logf("physical nested Podman controls retained: userns=host; network=host; loopback=shared")
}

func containsArg(args []string, value string) bool {
	for _, arg := range args {
		if arg == value {
			return true
		}
	}
	return false
}

func containsArgPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestPrivateFixtureYoloStoreKeepsCapturesAndPacksUnderItsHome(t *testing.T) {
	machineHome := resolvedTempDir(t)
	dir, err := makeRunStore(machineHome)
	if err != nil {
		t.Fatal(err)
	}
	savedRunStore, savedHostHome := runStore, hostHome
	runStore = struct{ machineHome, dir string }{machineHome: machineHome, dir: dir}
	hostHome = machineHome
	t.Cleanup(func() {
		tearDownRunStore()
		runStore, hostHome = savedRunStore, savedHostHome
	})

	sourceHome := resolvedTempDir(t)
	const userConfig = `{"packs": ["fixture-host-semantic"]}`
	if err := seedPackHome(sourceHome, machineHome, userConfig); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", sourceHome)
	home := withPrivateFixtureYoloStore(t)
	if got := os.Getenv("HOME"); got != home {
		t.Fatalf("fixture HOME = %q, want %q", got, home)
	}
	configBytes, err := os.ReadFile(filepath.Join(home, ".config", "yolo-jail", "config.jsonc"))
	if err != nil || string(configBytes) != userConfig {
		t.Fatalf("private fixture config = %q (%v), want %q", configBytes, err, userConfig)
	}

	state := paths.GlobalStorageUnder(home)
	info, err := os.Lstat(state)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("private yolo state %s is not a real directory: %v (%v)", state, info, err)
	}
	for _, name := range runStoreShared {
		path := filepath.Join(state, name)
		got, err := os.Readlink(path)
		if err != nil || got != filepath.Join(runStore.dir, name) {
			t.Errorf("private state shared child %s -> %q (%v), want run-store child %q", name, got, err,
				filepath.Join(runStore.dir, name))
		}
	}
	for _, name := range []string{"captures", "packs"} {
		path := filepath.Join(state, name)
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("private fixture's %s exists before a test writes it: %v", name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(runStore.dir, "captures")); !os.IsNotExist(err) {
		t.Fatalf("private fixture setup wrote captures into the run store: %v", err)
	}
	captures := paths.CapturesDirUnder(home)
	if err := os.MkdirAll(captures, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(captures, "private-fixture-marker")
	if err := os.WriteFile(marker, []byte("private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(runStore.dir, "captures", "private-fixture-marker")); !os.IsNotExist(err) {
		t.Errorf("the private capture marker escaped into the run store: %v", err)
	}
}
