package checkdiag

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseDryRunWillBuild(t *testing.T) {
	// Subprocess didn't run -> unknown.
	if wb, _ := ParseDryRunWillBuild(0, "", false); wb != WillBuildUnknown {
		t.Errorf("no-run => %v, want Unknown", wb)
	}
	// Everything substitutable (no header) -> No.
	subst := "these paths will be fetched (12.3 MiB download):\n  /nix/store/x\n"
	if wb, _ := ParseDryRunWillBuild(0, subst, true); wb != WillBuildNo {
		t.Errorf("substitutable => %v, want No", wb)
	}
	// Header present -> Yes + offending drv basenames.
	build := "these 2 derivations will be built:\n" +
		"  /nix/store/aaa-yolo-jail-conf.json.drv\n" +
		"  /nix/store/bbb-foo.drv\n" +
		"these paths will be fetched:\n  /nix/store/ccc\n"
	wb, off := ParseDryRunWillBuild(0, build, true)
	if wb != WillBuildYes {
		t.Fatalf("build => %v, want Yes", wb)
	}
	if !reflect.DeepEqual(off, []string{"aaa-yolo-jail-conf.json.drv", "bbb-foo.drv"}) {
		t.Errorf("offending = %v", off)
	}
	// Non-zero exit with no header (network error) -> Unknown.
	if wb, _ := ParseDryRunWillBuild(1, "error: unable to download", true); wb != WillBuildUnknown {
		t.Errorf("network-error => %v, want Unknown", wb)
	}
	// Single-derivation header form.
	if wb, off := ParseDryRunWillBuild(0, "this derivation will be built:\n  /nix/store/z-x.drv\n", true); wb != WillBuildYes || len(off) != 1 {
		t.Errorf("single-drv => %v, %v", wb, off)
	}
}

func TestDiagnoseNixBuildFailure(t *testing.T) {
	remedy := "REMEDY"
	// Explicit cross-build refusal.
	title, rem := DiagnoseNixBuildFailure([]string{"error: a 'aarch64-linux' is required to build /nix/store/x.drv"}, false, remedy)
	if title != "Image build needs a Linux builder" || rem != "Part of the image isn't in the binary cache and must be built.\nREMEDY" {
		t.Errorf("explicit cross: %q / %q", title, rem)
	}
	// Ambiguous mac (only when isMacOS).
	title, _ = DiagnoseNixBuildFailure([]string{"error: 1 dependency failed"}, true, remedy)
	if title != "Image build needs a Linux builder (or a cached package)" {
		t.Errorf("ambiguous mac title = %q", title)
	}
	// Same input on Linux -> generic fallback.
	title, rem = DiagnoseNixBuildFailure([]string{"error: 1 dependency failed"}, false, remedy)
	if title != "nix build failed" || rem != "error: 1 dependency failed" {
		t.Errorf("linux fallback: %q / %q", title, rem)
	}
	// Empty tail.
	if title, rem := DiagnoseNixBuildFailure(nil, false, remedy); title != "nix build failed" || rem != "" {
		t.Errorf("empty: %q / %q", title, rem)
	}
}

func TestHasLinuxBuilderFromConfig(t *testing.T) {
	// Inline builder with aarch64-linux + non-zero jobs.
	cfg := "builders = ssh-ng://b aarch64-linux,x86_64-linux /key 4\nother = 1\n"
	if !HasLinuxBuilderFromConfig(cfg, nil) {
		t.Error("inline aarch64-linux builder should be detected")
	}
	// max_jobs 0 -> not usable.
	if HasLinuxBuilderFromConfig("builders = ssh-ng://b aarch64-linux /key 0\n", nil) {
		t.Error("max_jobs=0 should not count")
	}
	// @machines file loaded via callback.
	loader := func(p string) ([]string, bool) {
		if p == "/etc/nix/machines" {
			return []string{"ssh-ng://vm aarch64-linux /key 2"}, true
		}
		return nil, false
	}
	if !HasLinuxBuilderFromConfig("builders = @/etc/nix/machines\n", loader) {
		t.Error("@machines aarch64-linux builder should be detected")
	}
	// No builder line at all.
	if HasLinuxBuilderFromConfig("max-jobs = auto\n", nil) {
		t.Error("no builders line => false")
	}
}

func TestFmtDuration(t *testing.T) {
	cases := map[int]string{-1: "?", 0: "0m", 59: "0m", 60: "1m", 3599: "59m", 3600: "1h0m", 7320: "2h2m"}
	for in, want := range cases {
		if got := FmtDuration(in); got != want {
			t.Errorf("FmtDuration(%d) = %q, want %q", in, got, want)
		}
	}
}

// TestParityVsLivePython cross-checks the classifier, dry-run parse, and remedy
// against live check_cmd.py. Skips without Python.
func TestParityVsLivePython(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}
	script := `
import sys; sys.path.insert(0, 'src')
import json
from cli import check_cmd as c
# Parity fixtures (Linux host: IS_MACOS is False, so the ambiguous branch is off).
d_cross = c._diagnose_nix_build_failure(["error: a 'aarch64-linux' is required to build /nix/store/x.drv"])
d_generic = c._diagnose_nix_build_failure(["boom", "kaboom"])
# _fmt is nested; reproduce via the same integer arithmetic isn't ideal, so
# instead grab the remedy + regexp behavior which ARE module-level.
build_err = "these 2 derivations will be built:\n  /nix/store/aaa-conf.json.drv\n  /nix/store/bbb-foo.drv\nthese paths will be fetched:\n  /nix/store/ccc\n"
will = bool(c._WILL_BUILD_RE.search(build_err))
out = {
  "remedy": c._linux_builder_remedy(),
  "cross_title": d_cross[0],
  "cross_rem": d_cross[1],
  "generic_title": d_generic[0],
  "generic_rem": d_generic[1],
  "will_build_matches": will,
}
print(json.dumps(out))
`
	outBytes, err := py("-c", script).Output()
	if err != nil {
		t.Skipf("python check_cmd import failed: %v", err)
	}
	var want struct {
		Remedy       string `json:"remedy"`
		CrossTitle   string `json:"cross_title"`
		CrossRem     string `json:"cross_rem"`
		GenericTitle string `json:"generic_title"`
		GenericRem   string `json:"generic_rem"`
		WillBuild    bool   `json:"will_build_matches"`
	}
	if err := json.Unmarshal(outBytes, &want); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The remedy label is resolved by Python's _detect_nix_daemon_label(); on
	// Linux that returns None -> "org.nixos.nix-daemon". Feed the same label.
	goRemedy := LinuxBuilderRemedy("org.nixos.nix-daemon")
	if goRemedy != want.Remedy {
		t.Errorf("remedy mismatch:\n go: %q\n py: %q", goRemedy, want.Remedy)
	}
	// Diagnose parity (Linux: isMacOS=false), feeding Python's remedy.
	gt, gr := DiagnoseNixBuildFailure([]string{"error: a 'aarch64-linux' is required to build /nix/store/x.drv"}, false, want.Remedy)
	if gt != want.CrossTitle || gr != want.CrossRem {
		t.Errorf("cross diag:\n go: %q/%q\n py: %q/%q", gt, gr, want.CrossTitle, want.CrossRem)
	}
	gt, gr = DiagnoseNixBuildFailure([]string{"boom", "kaboom"}, false, want.Remedy)
	if gt != want.GenericTitle || gr != want.GenericRem {
		t.Errorf("generic diag:\n go: %q/%q\n py: %q/%q", gt, gr, want.GenericTitle, want.GenericRem)
	}
	if wb, _ := ParseDryRunWillBuild(0, "these 2 derivations will be built:\n  /nix/store/aaa-conf.json.drv\n", true); (wb == WillBuildYes) != want.WillBuild {
		t.Errorf("will-build detection go=%v py=%v", wb == WillBuildYes, want.WillBuild)
	}
}

func pythonRunner(t *testing.T) func(args ...string) *exec.Cmd {
	t.Helper()
	root := repoRoot(t)
	if _, err := exec.LookPath("uv"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("uv", append([]string{"run", "python"}, args...)...)
			c.Dir = root
			return c
		}
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("python3", args...)
			c.Dir = root
			return c
		}
	}
	return nil
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
