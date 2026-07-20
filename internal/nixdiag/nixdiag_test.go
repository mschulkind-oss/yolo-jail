package nixdiag

import (
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
