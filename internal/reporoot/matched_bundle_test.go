package reporoot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

var matchedLinuxBinaries = []string{"yolo", "yolo-entrypoint", "yolo-jaild", "yolo-ps", "yolo-cglimit", "yolo-journalctl", "yolo-serial", "yolo-log"}
var matchedDarwinBinaries = []string{"yolo", "yolo-jaild", "yolo-serial", "yolo-ps", "yolo-log"}

const snapshotA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const snapshotB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

type matchedStamp struct {
	Version          string `json:"version"`
	Commit           string `json:"commit"`
	SnapshotIdentity string `json:"snapshotIdentity"`
	BuildKind        string `json:"buildKind"`
}

func writeMatchedBundle(t *testing.T, root, identity, kind string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"flake.nix": "{}", "flake.lock": "{}"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stamp, err := json.Marshal(matchedStamp{Version: "0.1.0", Commit: "abc123", SnapshotIdentity: identity, BuildKind: kind})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "yolo-build-stamp.json"), stamp, 0o644); err != nil {
		t.Fatal(err)
	}
	linux := filepath.Join(root, "bin", "linux-"+runtime.GOARCH)
	for _, name := range matchedLinuxBinaries {
		writeBundleFile(t, linux, name)
	}
	if bundleGOOS == "darwin" {
		darwin := filepath.Join(root, "bin", "darwin-"+runtime.GOARCH)
		for _, name := range matchedDarwinBinaries {
			writeBundleFile(t, darwin, name)
		}
	}
}

func writeBundleFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestResolveCallsProductionResolverForMatchingInstalledBundle(t *testing.T) {
	home := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	exe := filepath.Join(canonicalTempDir(t), "bin", "yolo")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	oldExecutable, oldInstalled := resolveExecutable, resolveInstalledBundle
	oldIdentity, oldKind := version.SnapshotIdentity, version.BuildKind
	t.Cleanup(func() {
		resolveExecutable, resolveInstalledBundle = oldExecutable, oldInstalled
		version.SnapshotIdentity, version.BuildKind = oldIdentity, oldKind
	})
	resolveExecutable = func() (string, error) { return exe, nil }
	resolveInstalledBundle = paths.FlakeBundleDir
	version.SnapshotIdentity, version.BuildKind = snapshotA, "installed"
	bundle := paths.FlakeBundleDir()
	writeMatchedBundle(t, bundle, snapshotA, "installed")

	got, ok := Resolve(func(string) string { return "" })
	if !ok || got.Error != nil || got.Source != FromInstalledBundle || got.Root != mustAbs(t, bundle) {
		t.Fatalf("Resolve() = (%+v,%v), want production installed-bundle path %s", got, ok, bundle)
	}
}

func TestResolveMatchedAdjacentBundlesAreAuthoritativeAcrossLayouts(t *testing.T) {
	for _, layout := range []string{"system", "release", "sealed"} {
		t.Run(layout, func(t *testing.T) {
			base := canonicalTempDir(t)
			exeDir, bundle := "", ""
			switch layout {
			case "system":
				exeDir = filepath.Join(base, "prefix", "bin")
				bundle = filepath.Join(base, "prefix", "share", "yolo-jail")
			case "release":
				exeDir = base
				bundle = filepath.Join(base, "share", "yolo-jail")
			case "sealed":
				exeDir = filepath.Join(base, "sealed", "bin")
				bundle = filepath.Join(base, "sealed", "share", "yolo-jail")
			}
			if err := os.MkdirAll(exeDir, 0o755); err != nil {
				t.Fatal(err)
			}
			writeMatchedBundle(t, bundle, snapshotA, "development")
			installed := filepath.Join(base, "installed")
			writeMatchedBundle(t, installed, snapshotA, "installed")
			got := resolveWith(filepath.Join(exeDir, "yolo"), installed, func(string) string { return "" }, snapshotA, "development")
			if got.Error != nil || got.Source != FromBesideBinary || got.Root != mustAbs(t, bundle) {
				t.Fatalf("resolution = %+v, want adjacent complete bundle %s", got, bundle)
			}
		})
	}
}

func TestResolveInstalledGOBINDoesNotTreatStateDirectoryAsAdjacentBundle(t *testing.T) {
	home := canonicalTempDir(t)
	binDir := filepath.Join(home, ".local", "bin")
	stateDir := filepath.Join(home, ".local", "share", "yolo-jail")
	generation := filepath.Join(stateDir, "flake-bundles", "generation")
	active := filepath.Join(stateDir, "flake-bundle")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMatchedBundle(t, generation, snapshotA, "installed")
	if err := os.Symlink(generation, active); err != nil {
		t.Fatal(err)
	}
	got := resolveWith(filepath.Join(binDir, "yolo"), active, func(string) string { return "" }, snapshotA, "installed")
	if got.Error != nil || got.Source != FromInstalledBundle || got.Root != mustAbs(t, generation) {
		t.Fatalf("resolution from installed GOBIN = %+v, want the active installed generation %s", got, generation)
	}
}

func TestResolveRejectsIncompleteAdjacentInsteadOfInstalledFallback(t *testing.T) {
	base := canonicalTempDir(t)
	exeDir := filepath.Join(base, "prefix", "bin")
	bundle := filepath.Join(base, "prefix", "share", "yolo-jail")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(base, "installed")
	writeMatchedBundle(t, installed, snapshotA, "installed")
	got := resolveWith(filepath.Join(exeDir, "yolo"), installed, func(string) string { return "" }, "", "")
	if got.Error == nil || got.Root != "" || got.Source != "" {
		t.Fatalf("incomplete adjacent bundle fell through: %+v", got)
	}
	if !containsAll(got.Error.Error(), "incomplete", "just build-dev-bundle", "just install") {
		t.Errorf("incomplete-bundle error is not actionable: %v", got.Error)
	}
}

func TestResolveRejectsEmptyRecognizedAdjacentBeforeInstalledFallback(t *testing.T) {
	base := canonicalTempDir(t)
	exeDir := filepath.Join(base, "prefix", "bin")
	bundle := filepath.Join(base, "prefix", "share", "yolo-jail")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(base, "installed")
	writeMatchedBundle(t, installed, snapshotA, "installed")
	got := resolveWith(filepath.Join(exeDir, "yolo"), installed, func(string) string { return "" }, snapshotA, "installed")
	if got.Error == nil || got.Root != "" {
		t.Fatalf("empty but recognized adjacent bundle fell through: %+v", got)
	}
	if !containsAll(got.Error.Error(), "adjacent", "incomplete", "just install") {
		t.Errorf("empty-adjacent refusal lacks actionable detail: %v", got.Error)
	}
}

func TestResolveRejectsNonExecutableLinuxHelperInAdjacentBundle(t *testing.T) {
	base := canonicalTempDir(t)
	exeDir := filepath.Join(base, "prefix", "bin")
	bundle := filepath.Join(base, "prefix", "share", "yolo-jail")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMatchedBundle(t, bundle, snapshotA, "development")
	helper := filepath.Join(bundle, "bin", "linux-"+runtime.GOARCH, matchedLinuxBinaries[0])
	if err := os.Chmod(helper, 0o644); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(base, "installed")
	writeMatchedBundle(t, installed, snapshotA, "installed")
	got := resolveWith(filepath.Join(exeDir, "yolo"), installed, func(string) string { return "" }, snapshotA, "development")
	if got.Error == nil || got.Root != "" || !strings.Contains(got.Error.Error(), "not executable") {
		t.Fatalf("non-executable adjacent helper was accepted or not diagnosed: %+v", got)
	}
}

func TestResolveRejectsAdjacentIdentityCrossPair(t *testing.T) {
	base := canonicalTempDir(t)
	exeDir := filepath.Join(base, "bin")
	bundle := filepath.Join(base, "share", "yolo-jail")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMatchedBundle(t, bundle, snapshotB, "development")
	installed := filepath.Join(base, "installed")
	writeMatchedBundle(t, installed, snapshotA, "installed")
	got := resolveWith(filepath.Join(exeDir, "yolo"), installed, func(string) string { return "" }, snapshotA, "development")
	if got.Error == nil || got.Root != "" {
		t.Fatalf("cross-paired adjacent identity was accepted: %+v", got)
	}
	if !containsAll(got.Error.Error(), "identity", "just build-dev-bundle") {
		t.Errorf("identity refusal lacks remedy: %v", got.Error)
	}
}

func TestResolveLegacyCompleteAdjacentBundleNeedsNoLocalIdentity(t *testing.T) {
	base := canonicalTempDir(t)
	exeDir := filepath.Join(base, "bin")
	bundle := filepath.Join(base, "share", "yolo-jail")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMatchedBundle(t, bundle, "", "")
	if err := os.Remove(filepath.Join(bundle, "yolo-build-stamp.json")); err != nil {
		t.Fatal(err)
	}
	got := resolveWith(filepath.Join(exeDir, "yolo"), "", func(string) string { return "" }, "", "")
	if got.Error != nil || got.Source != FromBesideBinary || got.Root != mustAbs(t, bundle) {
		t.Fatalf("legacy complete distribution was not accepted: %+v", got)
	}
}

func TestResolveInheritedObsoleteRepoRootRefusesWithoutSelectingSource(t *testing.T) {
	base := canonicalTempDir(t)
	exeDir := filepath.Join(base, "bin")
	bundle := filepath.Join(base, "share", "yolo-jail")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMatchedBundle(t, bundle, "", "")
	got := resolveWith(filepath.Join(exeDir, "yolo"), "", func(k string) string {
		if k == "YOLO_REPO_ROOT" {
			return "/old/checkout"
		}
		return ""
	}, "", "")
	if got.Error == nil || got.Root != "" {
		t.Fatalf("obsolete environment selected a source: %+v", got)
	}
	if !containsAll(got.Error.Error(), "YOLO_REPO_ROOT", "just build-dev-bundle", "just install") {
		t.Errorf("obsolete-variable refusal lacks next step: %v", got.Error)
	}
}

func TestResolveBareFallbackRequiresInstalledMatchingIdentityAndPinsRoot(t *testing.T) {
	base := canonicalTempDir(t)
	exeDir := filepath.Join(base, "bare-bin")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(base, "flake-bundle")
	oldGeneration := filepath.Join(base, "generation-a")
	newGeneration := filepath.Join(base, "generation-b")
	writeMatchedBundle(t, oldGeneration, snapshotA, "installed")
	writeMatchedBundle(t, newGeneration, snapshotB, "installed")
	if err := os.Symlink(oldGeneration, active); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, kind, identity string
		wantOK               bool
	}{
		{"matching installed", "installed", snapshotA, true},
		{"development is not authorized", "development", snapshotA, false},
		{"unmarked is not authorized", "", snapshotA, false},
		{"identity mismatch", "installed", snapshotB, false},
		{"missing identity", "installed", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveWith(filepath.Join(exeDir, "yolo"), active, func(string) string { return "" }, tc.identity, tc.kind)
			if (got.Error == nil) != tc.wantOK {
				t.Fatalf("resolution = %+v, want success %v", got, tc.wantOK)
			}
			if tc.wantOK && got.Root != mustAbs(t, oldGeneration) {
				t.Fatalf("root = %q, want pinned canonical generation %q", got.Root, oldGeneration)
			}
		})
	}
	got := resolveWith(filepath.Join(exeDir, "yolo"), active, func(string) string { return "" }, snapshotA, "installed")
	if err := os.Remove(active); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(newGeneration, active); err != nil {
		t.Fatal(err)
	}
	if got.Error != nil || got.Root != mustAbs(t, oldGeneration) || !fileExists(filepath.Join(got.Root, "flake.nix")) {
		t.Fatalf("resolved generation changed after activation: %+v", got)
	}
}

func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}
	return true
}

// A darwin bundle without its host yolo is not a matched bundle: every host helper a Mac launch
// starts is exec'd from that copy (docs/reference/pinned-host-helpers.md, PH-D2). The OS is injected
// so this runs on Linux; the guest set alone, as older bundles shipped it, must be refused.
func TestValidateBundleRequiresTheDarwinHostYolo(t *testing.T) {
	old := bundleGOOS
	t.Cleanup(func() { bundleGOOS = old })
	bundleGOOS = "darwin"
	root := filepath.Join(canonicalTempDir(t), "bundle")
	writeMatchedBundle(t, root, snapshotA, "installed")
	if _, err := validateBundle(root); err != nil {
		t.Fatalf("a complete darwin bundle was refused: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "bin", "darwin-"+runtime.GOARCH, "yolo")); err != nil {
		t.Fatal(err)
	}
	_, err := validateBundle(root)
	if err == nil || !strings.Contains(err.Error(), "bin/darwin-"+runtime.GOARCH+"/yolo") {
		t.Fatalf("a darwin bundle with only the guest set was accepted or the error names no yolo: %v", err)
	}
}
