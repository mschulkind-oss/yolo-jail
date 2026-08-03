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
	// Both arches: nix names the system IT wants, so an x86_64 host sees x86_64-linux
	// here. Keying the classifier on aarch64-linux dropped that host to the generic
	// stderr-dump branch instead of the Linux-builder remedy (BACKLOG E8 class).
	for _, sys := range []string{"aarch64-linux", "x86_64-linux", "riscv64-linux"} {
		title, rem := DiagnoseNixBuildFailure(
			[]string{"error: a '" + sys + "' is required to build /nix/store/x.drv"}, false, remedy)
		if title != "Image build needs a Linux builder" || rem != "Part of the image isn't in the binary cache and must be built.\nREMEDY" {
			t.Errorf("explicit cross (%s): %q / %q", sys, title, rem)
		}
	}
	// "cannot build" is the other phrasing, and it must still need a system mention:
	// a bare "cannot build" with no <arch>-linux in it is not a cross-build refusal.
	if title, _ := DiagnoseNixBuildFailure([]string{"error: cannot build derivation"}, false, remedy); title != "nix build failed" {
		t.Errorf("cannot-build without a system mention should fall through, got %q", title)
	}
	// Ambiguous mac (only when isMacOS).
	if title, _ := DiagnoseNixBuildFailure([]string{"error: 1 dependency failed"}, true, remedy); title != "Image build needs a Linux builder (or a cached package)" {
		t.Errorf("ambiguous mac title = %q", title)
	}
	// Same input on Linux -> generic fallback.
	if title, rem := DiagnoseNixBuildFailure([]string{"error: 1 dependency failed"}, false, remedy); title != "nix build failed" || rem != "error: 1 dependency failed" {
		t.Errorf("linux fallback: %q / %q", title, rem)
	}
	// Empty tail.
	if title, rem := DiagnoseNixBuildFailure(nil, false, remedy); title != "nix build failed" || rem != "" {
		t.Errorf("empty: %q / %q", title, rem)
	}
}

func TestHasLinuxBuilderFromConfig(t *testing.T) {
	// Inline builder serving BOTH arches: usable from either host.
	cfg := "builders = ssh-ng://b aarch64-linux,x86_64-linux /key 4\nother = 1\n"
	for _, want := range []string{"aarch64-linux", "x86_64-linux"} {
		if !HasLinuxBuilderFromConfig(cfg, want, nil) {
			t.Errorf("multi-arch builder should satisfy %s", want)
		}
	}
	// THE E8 CASE: a builder serving only arm64 must NOT satisfy an x86_64 host.
	// Reporting "builder present" here is what let a run proceed to a build nix then
	// refused to offload.
	armOnly := "builders = ssh-ng://b aarch64-linux /key 4\n"
	if HasLinuxBuilderFromConfig(armOnly, "x86_64-linux", nil) {
		t.Error("an aarch64-only builder must not count as an x86_64-linux builder")
	}
	if !HasLinuxBuilderFromConfig(armOnly, "aarch64-linux", nil) {
		t.Error("an aarch64-only builder should satisfy an aarch64 host")
	}
	// Empty wantSystem = "any linux builder".
	if !HasLinuxBuilderFromConfig(armOnly, "", nil) {
		t.Error("empty wantSystem should accept any -linux builder")
	}
	// …but not a non-linux one, or "any" would mean "anything".
	if HasLinuxBuilderFromConfig("builders = ssh-ng://b aarch64-darwin /key 4\n", "", nil) {
		t.Error("empty wantSystem must still require a -linux system")
	}
	// max_jobs 0 -> not usable, whatever the system.
	if HasLinuxBuilderFromConfig("builders = ssh-ng://b aarch64-linux /key 0\n", "aarch64-linux", nil) {
		t.Error("max_jobs=0 should not count")
	}
	// @machines file loaded via callback.
	loader := func(p string) ([]string, bool) {
		if p == "/etc/nix/machines" {
			return []string{"ssh-ng://vm x86_64-linux /key 2"}, true
		}
		return nil, false
	}
	if !HasLinuxBuilderFromConfig("builders = @/etc/nix/machines\n", "x86_64-linux", loader) {
		t.Error("@machines builder should be detected")
	}
	// No builder line at all.
	if HasLinuxBuilderFromConfig("max-jobs = auto\n", "aarch64-linux", nil) {
		t.Error("no builders line => false")
	}
}

func TestMinFreeFromConfig(t *testing.T) {
	// Real `nix config show` shape (values on their own lines).
	cfg := "max-free = 9223372036854775807\nmin-free = 0\nmin-free-check-interval = 5\n"
	if n, ok := MinFreeFromConfig(cfg); !ok || n != 0 {
		t.Errorf("min-free=0 → (%d,%v), want (0,true)", n, ok)
	}
	// A configured floor.
	if n, ok := MinFreeFromConfig("min-free = 53687091200\n"); !ok || n != 53687091200 {
		t.Errorf("min-free=50GiB → (%d,%v), want (53687091200,true)", n, ok)
	}
	// Absent key.
	if _, ok := MinFreeFromConfig("max-jobs = auto\n"); ok {
		t.Error("absent min-free must yield ok=false")
	}
	// Non-integer value (defensive).
	if _, ok := MinFreeFromConfig("min-free = auto\n"); ok {
		t.Error("non-integer min-free must yield ok=false")
	}
	// The prefix must not match min-free-check-interval when min-free is absent.
	if _, ok := MinFreeFromConfig("min-free-check-interval = 5\n"); ok {
		t.Error("min-free-check-interval must not be parsed as min-free")
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
