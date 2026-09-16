package entrypoint

// launcherplatform_test.go is the PLATFORM axis of "why is there no launcher for this
// declared program?" (setup-support-gaps.md §7 F8): a pack `program` whose `platforms` list
// says the vendor publishes no build for this machine.
//
// SAME SHAPE RULE AS launchercollision_test.go, and for the same reason: the gate is a
// handled case rather than a structural impossibility, so the cell that matters is the one
// that fails when the CHECK is deleted. Every cell here drives GenerateAgentLaunchers — the
// function boot.go calls — and asserts on the files it wrote.
//
// `plan9/386` is the never-here platform: Go names it, so it is a legal declaration, and no
// machine this suite can run on is it. Using a made-up string instead would make the cell a
// statement about a typo rather than about the grammar.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// writePackWithPlatformedPrograms stages a one-pack YOLO_PACK_ROOT whose programs each carry
// the `platforms` list given for them. An empty list means the key is omitted entirely,
// which is the "publishes everywhere" declaration and NOT the same as `[]` (refused).
func writePackWithPlatformedPrograms(t *testing.T, name string, bins map[string][]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var entries []string
	for bin, platforms := range bins {
		entry := `{"kind":"program","bin":"` + bin + `","via":"npm","package":"` + bin + `-pkg"`
		if len(platforms) > 0 {
			entry += `,"platforms":["` + strings.Join(platforms, `","`) + `"]`
		}
		entries = append(entries, entry+"}")
	}
	manifest := `{"name":"` + name + `","contributes":[` + strings.Join(entries, ",") + `]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestNoLauncherForAProgramTheVendorDoesNotPublishHere is the load-bearing cell. Delete the
// platform check in GenerateAgentLaunchers and the launcher reappears — a lazy `npm install
// -g` for a package the vendor refuses on this machine, which is exactly what an arm64 Linux
// jail selecting `omp` used to get: yolo installed nothing and said nothing, and the vendor's
// `unsupported platform linux-arm64` arrived the first time the agent was used.
//
// The two other bins are not decoration. Without them the cell would also pass against a
// generator that wrote NO launchers at all — the other way to make the failure impossible,
// and the wrong way.
func TestNoLauncherForAProgramTheVendorDoesNotPublishHere(t *testing.T) {
	home := t.TempDir()
	here := runtime.GOOS + "/" + runtime.GOARCH
	e := NewEnv(map[string]string{
		"JAIL_HOME": home,
		"YOLO_PACK_ROOT": writePackWithPlatformedPrograms(t, "vendored", map[string][]string{
			"yolo-unpublished-bin": {"plan9/386"},
			"yolo-published-bin":   {here, "plan9/386"},
			"yolo-anywhere-bin":    nil,
		}),
	})
	var stderr strings.Builder
	e.Stderr = &stderr
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(e.LaunchDir(), "yolo-unpublished-bin")); !os.IsNotExist(err) {
		t.Errorf("a launcher was written for a program declared only for plan9/386 (err=%v) "+
			"— it can only run `npm install -g` for a package the vendor refuses here, at the "+
			"moment the agent is first used", err)
	}
	for _, bin := range []string{"yolo-published-bin", "yolo-anywhere-bin"} {
		if _, err := os.Stat(filepath.Join(e.LaunchDir(), bin)); err != nil {
			t.Errorf("%s must still get its launcher — a list naming this platform, and no "+
				"list at all, both publish here; suppressing them switches the mechanism off "+
				"instead of scoping it: %v", bin, err)
		}
	}

	// THE DISCLOSURE IS PART OF THE FIX, not a nicety: a declared program with no launcher
	// and no line is the omission that gets discovered as a tool mysteriously missing (the
	// shadow axis warns for the same reason). It must name the pack, the bin, what IS
	// published and what this machine is.
	warned := stderr.String()
	for _, want := range []string{"vendored", "yolo-unpublished-bin", "plan9/386", here} {
		if !strings.Contains(warned, want) {
			t.Errorf("the decline must name %q; stderr was:\n%s", want, warned)
		}
	}
	// And it must say the thing no `requires`-shaped message can: there is nothing to
	// install. The vendor-platform refusal misread as a missing prerequisite is the failure
	// the whole declaration exists to end.
	if !strings.Contains(warned, "nothing can be installed to fix it") {
		t.Errorf("the decline must state that nothing is missing here; stderr was:\n%s", warned)
	}
}

// TestUnpublishedProgramsStillDeclineWhenTheImageProvidesTheName pins the ORDER of the two
// axes. Both apply to `sh` here — /bin/sh exists everywhere this test can run, and the
// declaration excludes this platform — and the line a reader needs is the actionable one:
// the tool is present, so "the image provides /bin/sh" is the answer, not "your vendor has no
// build for this machine" (run.loopholeinert's "BACKEND BEATS PLATFORM" reasoning, one
// mechanism, one rendering).
func TestUnpublishedProgramsStillDeclineWhenTheImageProvidesTheName(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME": home,
		"YOLO_PACK_ROOT": writePackWithPlatformedPrograms(t, "bothaxes", map[string][]string{
			"sh": {"plan9/386"},
		}),
	})
	var stderr strings.Builder
	e.Stderr = &stderr
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.LaunchDir(), "sh")); !os.IsNotExist(err) {
		t.Errorf("a launcher was written for `sh` (err=%v)", err)
	}
	if warned := stderr.String(); !strings.Contains(warned, "the image provides") {
		t.Errorf("the shadow axis must answer first — it is the line the reader can act "+
			"on; stderr was:\n%s", warned)
	}
}

// TestProgramPlatformMatcherAgreesWithLoopholedecl is the coupling packdecl cannot hold
// itself. One grammar, two schemas: `platforms` is validated for its VOCABULARY in
// internal/loopholedecl (whose closed GOOS/GOARCH lists are the authority) and matched for
// `program` by internal/packdecl, which imports none of the repo's world — so the predicate
// is written twice, and the two must not drift. Both ends of one grammar have to agree, or a
// list that makes a loophole inert leaves a program installing anyway.
//
// This is packload.TestCapabilityNameRulesAgree's mechanism, in the package that imports
// both: internal/entrypoint is the CONSUMER of packdecl's half, which is what makes it the
// right home rather than a convenient one.
func TestProgramPlatformMatcherAgreesWithLoopholedecl(t *testing.T) {
	lists := [][]string{
		nil,
		{"linux"},
		{"linux/amd64"},
		{"darwin/arm64", "linux/amd64"},
		{"plan9/386"},
		{"windows", "js/wasm"},
	}
	pairs := [][2]string{
		{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "arm64"}, {"darwin", "amd64"},
		{"plan9", "386"}, {"windows", "amd64"}, {"js", "wasm"}, {"linux", "riscv64"},
	}
	for _, list := range lists {
		inst := packdecl.Install{Bin: "b", Platforms: list}
		lh := &loopholedecl.Manifest{Platforms: list, PlatformsSet: len(list) > 0}
		for _, p := range pairs {
			got, want := inst.SupportsPlatform(p[0], p[1]), lh.SupportsPlatform(p[0], p[1])
			if got != want {
				t.Errorf("platforms %v on %s/%s: packdecl says %v, loopholedecl says %v — "+
					"the two spellings of one grammar have drifted", list, p[0], p[1], got, want)
			}
		}
		// The rendering the two reports share, too: a sorted list is what a message prints,
		// and one side sorting differently is a second way to disagree.
		if strings.Join(inst.PlatformsDeclared(), ",") != strings.Join(lh.PlatformsDeclared(), ",") {
			t.Errorf("platforms %v: PlatformsDeclared() disagrees (%v vs %v)", list,
				inst.PlatformsDeclared(), lh.PlatformsDeclared())
		}
	}
}

// TestShippedProgramsDeclareTheirVendorsPlatforms pins the one real case: omp's vendor
// publishes darwin-arm64 and linux-x64 only (its own refusal names them), so the pack must
// SAY so — the declaration is the whole point of the field, and a pack that carries the fact
// nowhere is back to meeting the vendor's error in the agent's first run.
//
// Only omp is asserted by name. Every other shipped program's vendor publishes both arches
// of both platforms yolo runs on, and a cell demanding a list from all of them would refuse
// the correct absent-means-everywhere declaration.
func TestShippedProgramsDeclareTheirVendorsPlatforms(t *testing.T) {
	for _, p := range packload.Embedded() {
		if p.Name != "omp" {
			continue
		}
		installs := p.Decl.InstallContributions()
		if len(installs) != 1 {
			t.Fatalf("omp declares %d programs, want 1", len(installs))
		}
		in := installs[0]
		if in.SupportsPlatform("linux", "arm64") {
			t.Errorf("omp claims to publish for linux/arm64, but the vendor refuses there "+
				"(`oh-omp: unsupported platform linux-arm64`) — declared %v", in.Platforms)
		}
		for _, p := range [][2]string{{"linux", "amd64"}, {"darwin", "arm64"}} {
			if !in.SupportsPlatform(p[0], p[1]) {
				t.Errorf("omp must still publish for %s/%s — declared %v",
					p[0], p[1], in.Platforms)
			}
		}
		return
	}
	t.Fatal("the omp pack is no longer shipped; move or drop this pin")
}
