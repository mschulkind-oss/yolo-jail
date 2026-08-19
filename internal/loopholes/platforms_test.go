package loopholes

// The HOST-SIDE half of the `platforms` declaration (loophole-packaging.md §3.1):
// the schema validates it statically, this package evaluates it against the machine
// and folds the answer into Active()/InactiveReason().
//
// The tests take (goos, goarch) explicitly wherever they can, because the suite has
// to cover the darwin answers from a linux runner — pinning the shipped behaviour to
// whatever GOOS the test happens to run on is how a platform bug survives CI.

import (
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func loadWithPlatforms(t *testing.T, name string, platforms any) *Loophole {
	t.Helper()
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, name))
	manifest := map[string]any{"name": name, "description": "x"}
	if platforms != nil {
		manifest["platforms"] = platforms
	}
	writeManifest(t, mod, manifest)
	lp, err := LoadLoophole(mod)
	if err != nil {
		t.Fatal(err)
	}
	return lp
}

// The declaration reaches the runtime record verbatim. Without this the schema
// validates a field nothing can act on — the `version` shape, which was declared by
// every bundled manifest and read by nobody.
func TestPlatformsReachTheResolvedRecord(t *testing.T) {
	lp := loadWithPlatforms(t, "plat", []any{"linux/arm64", "darwin"})
	if !lp.PlatformsSet {
		t.Error("PlatformsSet = false after loading a manifest that declares platforms")
	}
	if got := strings.Join(lp.Platforms, ","); got != "linux/arm64,darwin" {
		t.Errorf("Platforms = %q, want the author's order preserved", got)
	}
}

// An absent key means every platform, so no manifest written before the key existed
// changes meaning — including all three bundled ones.
func TestNoPlatformsDeclarationIsSupportedEverywhere(t *testing.T) {
	lp := loadWithPlatforms(t, "anywhere", nil)
	for _, pair := range [][2]string{{"linux", "amd64"}, {"darwin", "arm64"}, {"windows", "386"}} {
		if !lp.supportsPlatform(pair[0], pair[1]) {
			t.Errorf("supportsPlatform(%q, %q) = false with no declaration", pair[0], pair[1])
		}
	}
	if !lp.SupportedHere() {
		t.Error("SupportedHere() = false with no declaration")
	}
	if reason, ok := lp.UnsupportedHereReason(); ok {
		t.Errorf("UnsupportedHereReason() = %q with no declaration", reason)
	}
}

// The motivating case, driven from whatever platform CI runs on: a Linux-only
// daemon is UNSUPPORTED on darwin and that answer does not depend on the runner.
func TestPlatformMismatchIsUnsupportedNotUnmet(t *testing.T) {
	lp := loadWithPlatforms(t, "linuxd", []any{"linux"})
	if lp.supportsPlatform("darwin", "arm64") {
		t.Fatal("a linux-only loophole reports supported on darwin/arm64")
	}
	reason, ok := lp.platformUnsupportedReason("darwin", "arm64")
	if !ok {
		t.Fatal("platformUnsupportedReason returned nothing for an unsupported platform")
	}
	// R8: the message names the situation and, critically, that there is nothing to
	// install — the whole reason this is not expressed through `requires`.
	for _, want := range []string{"unsupported on darwin/arm64", "linux", "Nothing is missing"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q does not carry %q", reason, want)
		}
	}
	// And the `requires` probe is untouched by it: the two answers stay separate
	// because their fixes differ.
	if !lp.RequirementsMet() {
		t.Error("RequirementsMet() = false; the platform declaration must not leak into the probe")
	}
}

// Active() gates on the platform, so RuntimeArgsFor and the run pipeline never
// emit wiring for a loophole that cannot run here — the alternative measured in
// §3.1 is a spawn that dies five seconds later through the silent readiness path.
func TestActiveIsFalseOnAnUnsupportedPlatform(t *testing.T) {
	other := "darwin"
	if runtime.GOOS == "darwin" {
		other = "linux"
	}
	lp := loadWithPlatforms(t, "elsewhere", []any{other})
	if lp.SupportedHere() {
		t.Fatalf("a %s-only loophole reports supported on %s", other, runtime.GOOS)
	}
	if lp.Active() {
		t.Error("Active() = true for a loophole unsupported on this platform")
	}
	reason, ok := lp.InactiveReason()
	if !ok {
		t.Fatal("InactiveReason() reported nothing for an unsupported platform")
	}
	for _, want := range []string{"unsupported on " + runtime.GOOS, "Nothing is missing"} {
		if !strings.Contains(reason, want) {
			t.Errorf("InactiveReason() = %q, missing %q", reason, want)
		}
	}
}

// A loophole supported HERE keeps every answer it had before the field existed.
func TestActiveIsUnchangedOnASupportedPlatform(t *testing.T) {
	lp := loadWithPlatforms(t, "here", []any{runtime.GOOS})
	if !lp.SupportedHere() {
		t.Fatalf("a %s loophole reports unsupported on %s", runtime.GOOS, runtime.GOOS)
	}
	if !lp.Active() {
		t.Error("Active() = false for a supported, enabled, requirement-free loophole")
	}
	if reason, ok := lp.InactiveReason(); ok {
		t.Errorf("InactiveReason() = %q for an active loophole", reason)
	}
}

// `disabled` still wins: an author who turned the loophole off does not need to hear
// about platforms, and the two messages would both be true.
func TestDisabledOutranksThePlatformReport(t *testing.T) {
	other := "darwin"
	if runtime.GOOS == "darwin" {
		other = "linux"
	}
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "off"))
	writeManifest(t, mod, map[string]any{
		"name": "off", "description": "x", "default_enabled": false, "platforms": []any{other},
	})
	lp, err := LoadLoophole(mod)
	if err != nil {
		t.Fatal(err)
	}
	reason, ok := lp.InactiveReason()
	if !ok || reason != "disabled" {
		t.Errorf("InactiveReason() = (%q, %v), want (\"disabled\", true)", reason, ok)
	}
}

// A manifest whose `platforms` is malformed is REFUSED, and discovery's contract
// applies: the loophole vanishes with a warning rather than loading half-validated.
// Pinned here because the refusal has to survive the tolerant read discovery uses —
// a bad VALUE is not version skew.
func TestMalformedPlatformsIsRefusedOnTheTolerantPath(t *testing.T) {
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "typo"))
	writeManifest(t, mod, map[string]any{
		"name": "typo", "description": "x", "platforms": []any{"darwins"},
	})
	_, err := LoadLoophole(mod)
	if err == nil {
		t.Fatal("a misspelled GOOS loaded through the tolerant reader")
	}
	if !contains(err.Error(), "not a known GOOS") {
		t.Errorf("refusal %q does not name the problem", err)
	}
}

// What each shipped loophole says about `platforms`, and the table is a decision
// record rather than a blanket rule.
//
// It USED to assert that none of them declared the field, with `audio` named as the
// interesting exception-in-waiting: Linux-only in fact, saying so through a
// `requires.file_exists` PROBE on a Linux socket path, and migrating it called "a
// behaviour change that belongs with whoever owns that migration".
//
// That migration happened on 2026-08-18, forced by the pack conversion rather than
// chosen: `requires.file_exists` is one of the two fields the pack-shipped subset
// still path-scopes, so `${XDG_RUNTIME_DIR}/pulse/native` could not come across —
// while `platforms` answers the question the probe was really asking and is not
// scoped at all.
//
// `journal` joined the same day and CHOSE the declaration rather than being forced
// into it. The bridge forwards `journalctl`, so it is Linux in the "there is nothing
// to install on darwin" sense `platforms` is for — and the run pipeline's old hand-
// written guard (the `container` runtime returning before the journal step) is not a
// thing a manifest loophole has. Note what it deliberately did NOT declare: a
// `requires.command_on_path: journalctl` probe, because a Linux host without systemd
// should hear "journalctl not found on host" per request rather than watch the
// loophole vanish from `yolo loopholes list` (R3).
func TestShippedLoopholePlatformDeclarations(t *testing.T) {
	wantPlatforms := map[string][]string{"audio": {"linux"}, "journal": {"linux"}}
	for _, s := range shippedLoopholes {
		lp, err := LoadLoophole(shippedLoopholeModule(t, s.name, s.pack))
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		want, declares := wantPlatforms[s.name]
		if lp.PlatformsSet != declares {
			t.Errorf("%s: PlatformsSet = %v (platforms=%v), want %v — this field is a "+
				"per-loophole decision, so a change here has to move a row in the table above",
				s.name, lp.PlatformsSet, lp.Platforms, declares)
			continue
		}
		if declares && !reflect.DeepEqual(lp.Platforms, want) {
			t.Errorf("%s: platforms = %v, want %v", s.name, lp.Platforms, want)
		}
		// SupportedHere is asserted only where nothing was declared: `audio` is
		// legitimately unsupported on darwin, and a test that demanded otherwise would
		// fail on the platform the declaration exists for.
		if !declares && !lp.SupportedHere() {
			t.Errorf("%s reports unsupported on %s/%s with no declaration",
				s.name, runtime.GOOS, runtime.GOARCH)
		}
	}
}
