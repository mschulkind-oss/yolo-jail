package packs_test

// releasedecode_test.go decodes the packs this tree SHIPS with the LAST RELEASE's in-jail
// reader, exactly as a jail that release launched would read them at boot, and fails on any
// break it does not already know about.
//
// WHY. A running jail keeps the binaries it launched with, and its boot reads whatever pack
// tree it is handed: packdecl.DecodeTolerant under packload.TolerateSkew, and then
// LoadJailPacks treats ANY problem LoadDir reports as fatal (internal/entrypoint/packsurfaces.go).
// Its surface render goes on to decode each pack's config surfaces strictly and to collect the
// whole set's config-overlays, and a problem in either stops the boot too (genStep, then
// genFailuresError). So the probe runs all three passes, the way that boot does.
// Tolerance covers an unknown kind or field. It does not cover a known field whose TYPE grew —
// claude's `api_key_env_name` became a list (providers.md, OQ-CN1) — or a new
// value in a closed set, such as a hook name. Either one stops an older jail's boot. So a manifest
// change the current tree reads happily can still stop every jail an older yolo launched from
// booting, the moment that jail is handed the new tree
// (docs/reference/attach-skew.md, "Pack-contract skew, and the release-decode check"). This test is the
// place such a change is noticed: it compiles the last release's reader from git and runs it
// over the embed a binary built from this tree carries.
//
// A KNOWN BREAK IS LISTED WITH ITS GUARD: what keeps an old jail from ever reading the new
// manifest. An unlisted break fails. So does a listed one that no longer breaks, since its guard
// line is then describing nothing. Entries are keyed by release, so the next release tag
// retires them without an edit: they become inert and are logged as removable.
//
// A BREAK THAT STOPS THE DECODE CARRIES A REPAIR. The old reader returns at the first
// encoding/json refusal (a field whose type grew, a surface field it does not know), so every
// other line of that document goes unread, and an allowlist entry for the one break it reports
// would stand in for everything behind it. So an entry for such a break says how to put the field
// back in the shape the release reads, and the probe runs again over a copy repaired that way,
// until no decode stops: the rest of the manifest is checked like any other.
//
// WHERE IT RUNS. In the short suite, so `just check-ci` and CI's check-go job both run it;
// both CI checkouts fetch full history and tags (fetch-depth: 0). Outside CI, a clone without
// tags or git skips it, saying so. Under GitHub Actions a missing tag is a failure, because a
// silent skip there would retire the check with every run green.

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport/releasebaseline"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// knownReleaseBreak is one problem the last release's reader reports for a pack this tree
// ships, and why shipping it is safe anyway.
type knownReleaseBreak struct {
	// release is the tag whose reader reports it. An entry for any other release is inert.
	release string
	// pack is the shipped pack's directory name, which is the name an old jail's boot gives it.
	pack string
	// problem is a substring of the reader's problem line: stable across index shifts
	// (contributes[11] moves when an entry is added above it), specific enough to name one
	// break.
	problem string
	// guard says what keeps a jail that release launched from ever reading this manifest.
	guard string
	// pinnedBy names the test that fails if the guard stops holding, when one exists. It is
	// checked to exist, so a pin cannot be cited and then renamed away.
	pinnedBy string
	// repair rewrites the pack's decoded manifest into the shape the release reads, for a break
	// that stops the release's reader from decoding the rest (stopsDecode). Required for such a
	// break: without it the rest of the pack goes unchecked.
	repair func(manifest any) any
}

// stopsDecode reports whether a problem the old reader reported is encoding/json refusing a
// document: the whole manifest (DecodeTolerant returns at a failed json.Unmarshal) or a pack's
// whole surface list. Nothing after the refusal was read.
var stopsDecode = regexp.MustCompile(`json: (cannot unmarshal|unknown field)`).MatchString

// firstOfList is a repair: every value of key that is a list becomes the list's first element,
// the one-value shape a release that declared the field a string reads.
func firstOfList(key string) func(any) any {
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				if l, ok := e.([]any); ok && k == key && len(l) > 0 {
					x[k] = l[0]
					continue
				}
				x[k] = walk(e)
			}
		case []any:
			for i, e := range x {
				x[i] = walk(e)
			}
		}
		return v
	}
	return walk
}

// dropKey is a repair: every object member named key is removed, the shape of a release that
// never had the field. For a surface field that release's strict decoder refuses by name.
func dropKey(key string) func(any) any {
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			delete(x, key)
			for k, e := range x {
				x[k] = walk(e)
			}
		case []any:
			for i, e := range x {
				x[i] = walk(e)
			}
		}
		return v
	}
	return walk
}

// guardNoRestageOnAttach is the guard for every pack-contract break an old jail could meet:
// OQ-PK2's per-launch pack trees (docs/reference/pack-system.md#oq-pk2), built. Every launch
// stages a tree of its own and an attach writes into none, so a jail keeps the tree it booted
// with; a jail launched before the change binds the one shared tree every launch used to
// re-stage, and nothing writes that tree any more (internal/cli/run/packtree.go). Nor does an
// attach compose from this tree's packs in that jail's place: where this build cannot read the
// jail's tree it takes the attach-skew disposition instead.
// pinAttachLeavesAnOlderJailsTree pins exactly that case, on the last release's real packs, the
// case a jail a release launched is in; TestAnAttachWritesNothingIntoTheRunningJailsPackTree pins
// the first half for a jail this tree launched.
const guardNoRestageOnAttach = "an attach never re-stages (one immutable pack tree per launch, " +
	"pack-system.md OQ-PK2), so a jail an older yolo launched never reads this tree's packs; " +
	"only a fresh launch does, and a fresh launch mounts this tree's binaries"

// pinAttachLeavesAnOlderJailsTree is the test that fails if the guard stops holding: an attach by
// this tree to a jail the last release launched, its shared tree holding that release's own
// packs, must leave that tree byte-identical and never compose from this tree's packs instead.
const pinAttachLeavesAnOlderJailsTree = "TestAnAttachToAJailTheLastReleaseLaunchedNeverRidesAlong"

// knownReleaseBreaks is the allowlist. Measured against v0.10.0 on 2026-09-26: claude's bedrock
// provider declares api_key_env_name as a list where v0.10.0's packdecl declares a string, and pi
// declares two hooks v0.10.0 does not know.
var knownReleaseBreaks = []knownReleaseBreak{
	{release: "v0.10.0", pack: "claude", problem: "api_key_env_name",
		guard: guardNoRestageOnAttach, pinnedBy: pinAttachLeavesAnOlderJailsTree,
		repair: firstOfList("api_key_env_name")},
	{release: "v0.10.0", pack: "pi", problem: `unknown hook "shared_directory"`,
		guard: guardNoRestageOnAttach, pinnedBy: pinAttachLeavesAnOlderJailsTree},
	{release: "v0.10.0", pack: "pi", problem: `unknown hook "unshare_directory"`,
		guard: guardNoRestageOnAttach, pinnedBy: pinAttachLeavesAnOlderJailsTree},
	// Measured against v0.11.0 on 2026-09-28: pi's surfaces carry the three surface fields of
	// the maintainer's two MCP rulings (docs/design/agent-directory-map.md AM-R1, AM-R2), which
	// v0.11.0's strict surface decoder refuses by name, one per decode.
	{release: "v0.11.0", pack: "pi", problem: `unknown field "retireIfMatchesRender"`,
		guard: guardNoRestageOnAttach, pinnedBy: pinAttachLeavesAnOlderJailsTree,
		repair: dropKey("retireIfMatchesRender")},
	{release: "v0.11.0", pack: "pi", problem: `unknown field "notAtHost"`,
		guard: guardNoRestageOnAttach, pinnedBy: pinAttachLeavesAnOlderJailsTree,
		repair: dropKey("notAtHost")},
	{release: "v0.11.0", pack: "pi", problem: `unknown field "whenListed"`,
		guard: guardNoRestageOnAttach, pinnedBy: pinAttachLeavesAnOlderJailsTree,
		repair: dropKey("whenListed")},
	// Measured against v0.11.0 on 2026-09-30: codex's model-list surface, the list its launcher
	// writes codex's model menu from (docs/design/model-lists-and-pickers.md MM-D22), carries
	// notAtHost, which v0.11.0's strict surface decoder refuses by name, as it does pi's.
	{release: "v0.11.0", pack: "codex", problem: `unknown field "notAtHost"`,
		guard: guardNoRestageOnAttach, pinnedBy: pinAttachLeavesAnOlderJailsTree,
		repair: dropKey("notAtHost")},
	// Measured against v0.15.1 on 2026-10-23: chrome-devtools declares the boot_check hook
	// (docs/design/mcp-presets-removal.md OQ-MP9, MP-D11), which v0.15.1's hook set lacks, as pi's
	// two hooks were unknown to v0.10.0.
	{release: "v0.15.1", pack: "chrome-devtools", problem: `unknown hook "boot_check"`,
		guard: guardNoRestageOnAttach, pinnedBy: pinAttachLeavesAnOlderJailsTree},
	// Measured against v0.15.1: Claude's program now declares `sandbox_tmpdir_env`, which that
	// reader does not know. Its fresh program loses only this native temp adaptation; an existing
	// v0.15.1 jail keeps its own staged pack tree because attach never re-stages it.
	{release: "v0.15.1", pack: "claude", problem: `unknown field "sandbox_tmpdir_env"`,
		guard: guardNoRestageOnAttach, pinnedBy: pinAttachLeavesAnOlderJailsTree,
		repair: dropKey("sandbox_tmpdir_env")},
	// Measured against v0.15.1 on 2026-10-23: guardrails' two `requires` entries became
	// `program` with no recipe (docs/design/provisioner-sets.md OQ-PS11, ruled with no
	// transition), which v0.15.1's reader refuses as a program missing its "via". It is a
	// validation problem, not an encoding/json refusal, so the rest of the manifest is read.
	{release: "v0.15.1", pack: "guardrails", problem: `program needs "via"`,
		guard: guardNoRestageOnAttach, pinnedBy: pinAttachLeavesAnOlderJailsTree},
	// Measured against v0.15.1 on 2026-10-29: the provider packs of
	// docs/design/provider-enablement.md declare `formerly` (PE-D7, the renamed packs' old names)
	// and `pinned_endpoint` on their provider (PE-D4), which that reader does not know. It skips
	// the field (`formerly`, pack-wide) or the provider contribution; neither stops the decode.
	// The repair drops the field, so the rest is decoded. Those pack directories did not exist under
	// these names in v0.15.1, so none of its jails
	// staged them, and a fresh launch mounts this tree's binaries.
	{release: "v0.15.1", pack: "codex-auth", problem: `unknown field "formerly"`,
		guard: guardNoRestageOnAttach, pinnedBy: pinAttachLeavesAnOlderJailsTree,
		repair: dropKey("formerly")},
	{release: "v0.15.1", pack: "copilot-auth", problem: `unknown field "formerly"`,
		guard: guardNoRestageOnAttach, pinnedBy: pinAttachLeavesAnOlderJailsTree,
		repair: dropKey("formerly")},
	{release: "v0.15.1", pack: "codex-auth", problem: `unknown field "pinned_endpoint"`,
		guard: guardNoRestageOnAttach, pinnedBy: pinAttachLeavesAnOlderJailsTree,
		repair: dropKey("pinned_endpoint")},
	{release: "v0.15.1", pack: "copilot-auth", problem: `unknown field "pinned_endpoint"`,
		guard: guardNoRestageOnAttach, pinnedBy: pinAttachLeavesAnOlderJailsTree,
		repair: dropKey("pinned_endpoint")},
	{release: "v0.15.1", pack: "claude-auth", problem: `unknown field "pinned_endpoint"`,
		guard: guardNoRestageOnAttach, pinnedBy: pinAttachLeavesAnOlderJailsTree,
		repair: dropKey("pinned_endpoint")},
	// Measured against v0.15.1 on 2026-11-02: cgroup-delegate declares `selected_by_default`
	// (docs/reference/loophole-grants.md PD-13), a pack-wide field that reader skips
	// without failing its boot. The field is read on the host, by config.LoadPacks, and never by
	// a jail's boot, so an old jail handed the tree loses nothing.
	{release: "v0.15.1", pack: "cgroup-delegate", problem: `unknown field "selected_by_default"`,
		guard: guardNoRestageOnAttach, pinnedBy: pinAttachLeavesAnOlderJailsTree,
		repair: dropKey("selected_by_default")},
}

// releaseDecodeProbe is the program compiled INSIDE the last release's tree: that release's
// reader, called the way its jail boot calls it — LoadDir under TolerateSkew, then each pack's
// surfaces under both postures (SurfacesForReport, which the boot's ConfigurePackSurfaces
// calls), then the whole set's config-overlays (packoverlay.Collect). It may use only what every
// release since v0.9.0 exports with these signatures; TestReleaseDecodeProbeAPIIsStable, in
// internal/packload and in internal/packoverlay, keeps this tree's copy of them fixed, since
// this tree is the next release.
const releaseDecodeProbe = `package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
)

func main() {
	packload.TolerateSkew()
	entries, err := os.ReadDir(os.Args[1])
	if err != nil {
		panic(err)
	}
	out := map[string]map[string][]string{}
	add := func(name, problem string) {
		res := out[name]
		if res == nil {
			res = map[string][]string{}
			out[name] = res
		}
		for _, have := range res["problems"] {
			if have == problem {
				return
			}
		}
		res["problems"] = append(res["problems"], problem)
	}
	names := []string{}
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var loaded []*packload.Pack
	for _, name := range names {
		p, problems := packload.LoadDir(filepath.Join(os.Args[1], name), name)
		out[name] = map[string][]string{"problems": {}, "skipped": {}}
		for _, prob := range problems {
			add(name, prob)
		}
		if p == nil {
			continue
		}
		out[name]["skipped"] = append(out[name]["skipped"], p.SkewNotes...)
		for _, install := range p.Decl.InstallContributions() {
			out[name]["programs"] = append(out[name]["programs"], install.Bin)
		}
		// The boot's surface pass, under both postures a render can select.
		for _, autonomy := range []bool{true, false} {
			_, surfaceProblems, _ := p.SurfacesForReport(autonomy)
			for _, prob := range surfaceProblems {
				add(name, prob)
			}
		}
		loaded = append(loaded, p)
	}
	// The boot's config-overlay pass, over the whole set, as the boot collects it. Each problem
	// is "pack <name>: ...", and is filed under that pack.
	for _, autonomy := range []bool{true, false} {
		for _, prob := range packoverlay.Collect(loaded, autonomy, nil).Problems {
			name := ""
			if rest, ok := strings.CutPrefix(prob, "pack "); ok {
				name, _, _ = strings.Cut(rest, ": ")
			}
			add(name, prob)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		panic(err)
	}
}
`

// TestShippedPacksDecodeUnderTheLastRelease is the check.
func TestShippedPacksDecodeUnderTheLastRelease(t *testing.T) {
	root, release := lastRelease(t)
	for _, b := range knownReleaseBreaks {
		if b.guard == "" {
			t.Errorf("the known break %s/%q names no guard; an allowlist entry is a claim that it is "+
				"safe, and the guard is the claim", b.pack, b.problem)
		}
		if b.pinnedBy != "" && !testFuncExists(t, root, b.pinnedBy) {
			t.Errorf("the known break %s/%q cites %s as its pin, and no test of that name exists",
				b.pack, b.problem, b.pinnedBy)
		}
	}

	old := t.TempDir()
	extractRelease(t, root, release, old)
	probeDir := filepath.Join(old, "cmd", "yolo-release-decode-probe")
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(probeDir, "main.go"), []byte(releaseDecodeProbe), 0o644); err != nil {
		t.Fatal(err)
	}
	shipped := filepath.Join(t.TempDir(), "packs")
	materialize(t, shipped)

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("no go toolchain on PATH to build %s's reader with", release)
	}
	probe := buildReleaseProbe(t, goBin, old, release)

	// Every problem each pack produced, over the shipped tree and then over each repaired copy.
	seen := map[string]map[string]bool{}
	var order []string
	skipped := map[string]bool{}
	repaired := map[int]bool{}
	for round := 0; ; round++ {
		got := runReleaseProbe(t, probe, shipped, release)
		var repairs []int
		for pack, res := range got {

			for _, note := range res["skipped"] {
				if !skipped[note] {
					skipped[note] = true
					t.Logf("%s skips, without failing its boot: %s", release, note)
				}
				i := knownSkippedBreakFor(release, pack, note)
				if i < 0 {
					t.Errorf("%s skipped an unlisted contribution in pack %q: %s", release, pack, note)
					continue
				}
				if seen[pack] == nil {
					seen[pack] = map[string]bool{}
				}
				if !seen[pack][note] {
					seen[pack][note] = true
					order = append(order, pack+"\x00"+note)
				}
				b := knownReleaseBreaks[i]
				switch {
				case repaired[i]:
					t.Errorf("the repair for the known skipped field %s/%q did not take: %s's reader still skips it",
						b.pack, b.problem, release)
				case b.repair == nil:
					t.Errorf("the known skipped field %s/%q has no repair for %s's reader", b.pack, b.problem, release)
				default:
					repaired[i] = true
					repairs = append(repairs, i)
				}
			}
			for _, problem := range res["problems"] {
				if seen[pack] == nil {
					seen[pack] = map[string]bool{}
				}
				if !seen[pack][problem] {
					seen[pack][problem] = true
					order = append(order, pack+"\x00"+problem)
				}
				i := knownBreakFor(release, pack, problem)
				if i < 0 || !stopsDecode(problem) {
					continue
				}
				b := knownReleaseBreaks[i]
				switch {
				case repaired[i]:
					t.Errorf("the repair for the known break %s/%q did not take: %s's reader still "+
						"stops there, so the rest of pack %s is unchecked", b.pack, b.problem, release, pack)
				case b.repair == nil:
					t.Errorf("the known break %s/%q stops %s's reader from decoding the rest of pack "+
						"%s, so nothing behind it is checked: give the entry a repair that puts the "+
						"field back in the shape %s reads", b.pack, b.problem, release, pack, release)
				default:
					repaired[i] = true
					repairs = append(repairs, i)
				}
			}
		}
		if len(repairs) == 0 {
			break
		}
		for _, i := range repairs {
			repairManifest(t, filepath.Join(shipped, knownReleaseBreaks[i].pack), knownReleaseBreaks[i].repair)
		}
		t.Logf("round %d: repaired %d decode-stopping break(s) and decoding again", round+1, len(repairs))
	}

	used := make([]bool, len(knownReleaseBreaks))
	for _, key := range order {
		pack, problem, _ := strings.Cut(key, "\x00")
		matched := false
		for i, b := range knownReleaseBreaks {
			if b.release == release && b.pack == pack && strings.Contains(problem, b.problem) {
				used[i], matched = true, true
			}
		}
		if !matched {
			t.Errorf("a jail %s launched cannot boot on pack %q as this tree ships it:\n\t%s\n"+
				"Its boot treats any decode problem as fatal. Either keep the manifest readable by "+
				"%s's reader, or add a knownReleaseBreaks entry whose guard says what keeps such "+
				"a jail from ever reading it.", release, pack, problem, release)
		}
	}
	for i, b := range knownReleaseBreaks {
		switch {
		case b.release != release:
			t.Logf("inert: the known break %s/%q is for %s, not the last release %s — removable",
				b.pack, b.problem, b.release, release)
		case !used[i]:
			t.Errorf("the known break %s/%q no longer occurs under %s's reader; remove the entry, "+
				"whose guard now describes nothing", b.pack, b.problem, release)
		}
	}
	// Check the new metadata against the same real reader without it. A successful
	// boot can still have silently lost a program, so compare identities explicitly.
	withMetadata := runReleaseProbe(t, probe, shipped, release)
	manifests := map[string]*packdecl.Manifest{}
	for pack := range withMetadata {
		manifest := shippedManifest(t, pack)
		manifests[pack] = manifest
		if manifest.ModelListChecks != nil {
			repairManifest(t, filepath.Join(shipped, pack), func(v any) any {
				delete(v.(map[string]any), "model_list_check")
				return v
			})
		}
	}
	baseline := runReleaseProbe(t, probe, shipped, release)
	piBaselineGap := false
	for pack, manifest := range manifests {
		before, after := baseline[pack]["programs"], withMetadata[pack]["programs"]
		if strings.Join(before, "\x00") != strings.Join(after, "\x00") {
			t.Errorf("%s's program identities changed with metadata for %s: before=%v after=%v", release, pack, before, after)
		}
		for _, install := range manifest.InstallContributions() {
			retained := false
			for _, bin := range after {
				retained = retained || bin == install.Bin
			}
			if retained {
				continue
			}
			// Pi already carried a nested flag requirement the v0.12.1 reader
			// cannot read. This is not a compatibility green for that program.
			if release == "v0.12.1" && pack == "pi" && install.Bin == "pi" &&
				strings.Contains(strings.Join(baseline[pack]["skipped"], "\n"), `unknown field "requires"`) {
				piBaselineGap = true
				t.Log("baseline residual: v0.12.1 drops Pi's unchanged launch_selection.requires both before and after metadata; five changed programs retained directly")
			} else {
				t.Errorf("%s lost pack %q's program %q (skips: %v)", release, pack, install.Bin, withMetadata[pack]["skipped"])
			}
		}
	}
	// Restore only the metadata we removed. Isolate Pi's existing requires
	// incompatibility in this fixture, not its production manifest or flag policy.
	for pack, manifest := range manifests {
		if manifest.ModelListChecks != nil {
			checks := manifest.ModelListChecks
			repairManifest(t, filepath.Join(shipped, pack), func(v any) any {
				v.(map[string]any)["model_list_check"] = checks
				return v
			})
		}
	}
	if piBaselineGap {
		repairManifest(t, filepath.Join(shipped, "pi"), func(v any) any {
			top := v.(map[string]any)
			for _, c := range top["contributes"].([]any) {
				entry := c.(map[string]any)
				if entry["kind"] != "program" || entry["bin"] != "pi" {
					continue
				}
				selection := entry["launch_selection"].(map[string]any)
				for _, flag := range selection["flags"].([]any) {
					delete(flag.(map[string]any), "requires")
				}
			}
			return v
		})
	}
	isolated := runReleaseProbe(t, probe, shipped, release)
	for pack, manifest := range manifests {
		for _, install := range manifest.InstallContributions() {
			retained := false
			for _, bin := range isolated[pack]["programs"] {
				retained = retained || bin == install.Bin
			}
			if !retained {
				t.Errorf("isolated %s metadata fixture lost %s/%s: %v", release, pack, install.Bin, isolated[pack])
			}
		}
	}
	if piBaselineGap {
		t.Log("fixture isolation: all six changed program identities retained with metadata after removing only Pi's unsupported nested flag requires")
	}
}

// TestModelListCheckPackWideMetadataPreservesTheLastReleasesProgram contrasts the safe
// location with the original unsafe program field, using the REAL last-release reader.
func TestModelListCheckPackWideMetadataPreservesTheLastReleasesProgram(t *testing.T) {
	root, release := lastRelease(t)
	old := t.TempDir()
	extractRelease(t, root, release, old)
	probeDir := filepath.Join(old, "cmd", "yolo-release-decode-probe")
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(probeDir, "main.go"), []byte(releaseDecodeProbe), 0o644); err != nil {
		t.Fatal(err)
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	probe := buildReleaseProbe(t, goBin, old, release)
	tree := t.TempDir()
	fixtures := map[string]string{
		"safe":   `{"model_list_check":{"synthetic":{}},"contributes":[{"kind":"program","bin":"synthetic","via":"npm","package":"example/synthetic"}]}`,
		"unsafe": `{"contributes":[{"kind":"program","bin":"synthetic","via":"npm","package":"example/synthetic","model_list_check":{}}]}`,
	}
	for name, raw := range fixtures {
		dir := filepath.Join(tree, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := runReleaseProbe(t, probe, tree, release)
	for name, res := range got {
		if len(res["problems"]) != 0 {
			t.Errorf("%s refused %s: %v", release, name, res["problems"])
		}
	}
	if programs := got["safe"]["programs"]; len(programs) != 1 || programs[0] != "synthetic" {
		t.Fatalf("%s lost the safe fixture's program: %v", release, got["safe"])
	}
	if release == "v0.12.1" && !strings.Contains(strings.Join(got["safe"]["skipped"], "\n"), `unknown field "model_list_check" is ignored`) {
		t.Fatalf("%s did not ignore the pack-wide metadata: %v", release, got["safe"])
	}
	if programs := got["unsafe"]["programs"]; len(programs) != 0 {
		t.Fatalf("%s unexpectedly retained the unsafe program-field control: %v", release, got["unsafe"])
	}
	t.Logf("%s retained synthetic under pack-wide metadata; unsafe program field dropped it", release)
}

// buildReleaseProbe compiles releaseDecodeProbe inside the release tree at old and returns the
// binary. Hermetic: the release's own vendor tree, this machine's toolchain, no network, and no
// workspace file or GOFLAGS from the environment steering the build.
func buildReleaseProbe(t *testing.T, goBin, old, release string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "yolo-release-decode-probe")
	cmd := exec.Command(goBin, "build", "-o", bin, "./cmd/yolo-release-decode-probe")
	cmd.Dir = old
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=vendor", "GOTOOLCHAIN=local", "GOWORK=off",
		"GOPROXY=off", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building %s's pack reader failed: %v\n%s\n"+
			"If %s no longer exports packload.TolerateSkew(), packload.LoadDir(root, name) (*Pack, "+
			"[]string), Pack.SkewNotes, (*Pack).SurfacesForReport(bool) and packoverlay.Collect "+
			"with OverlaySet.Problems, update releaseDecodeProbe to that release's in-jail entry "+
			"points.", release, err, out, release)
	}
	return bin
}

// runReleaseProbe runs the release's reader over a pack tree: pack name to its problems and the
// contributions it skipped.
func runReleaseProbe(t *testing.T, probe, tree, release string) map[string]map[string][]string {
	t.Helper()
	cmd := exec.Command(probe, tree)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("running %s's pack reader failed: %v\n%s", release, err, stderr.String())
	}
	var got map[string]map[string][]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the probe's output is not JSON: %v\n%s", err, raw)
	}
	if len(got) == 0 {
		t.Fatalf("%s's reader decoded no packs from %s, so this test checked nothing", release, tree)
	}
	return got
}

// knownBreakFor returns the index of the allowlist entry that covers problem, or -1.
func knownBreakFor(release, pack, problem string) int {
	for i, b := range knownReleaseBreaks {
		if b.release == release && b.pack == pack && strings.Contains(problem, b.problem) {
			return i
		}
	}
	return -1
}

// knownSkippedBreakFor recognizes only a listed unknown-field diagnostic for the exact released
// yolo and pack, on a skip that reader reports: a skipped program or provider contribution, or a
// pack-wide field it ignores. Every such entry carries a repair, so the rest of what the skip hid
// is decoded again without it. Other skips remain failures in the release-compatibility test.
func knownSkippedBreakFor(release, pack, skipped string) int {
	if !strings.Contains(skipped, "skipping the program contribution") &&
		!strings.Contains(skipped, "skipping the provider contribution") &&
		!strings.Contains(skipped, `" is ignored`) {
		return -1
	}
	for i, b := range knownReleaseBreaks {
		if b.release != release || b.pack != pack || !strings.HasPrefix(b.problem, `unknown field "`) {
			continue
		}
		if strings.Contains(skipped, b.problem) {
			return i
		}
	}
	return -1
}

func TestKnownSkippedReleaseBreakMatchesOnlyTheListedField(t *testing.T) {
	const diagnostic = `pack claude: contributes[0]: skipping the program contribution "claude" — unknown field "sandbox_tmpdir_env", which this yolo does not know`
	want := knownBreakFor("v0.15.1", "claude", `unknown field "sandbox_tmpdir_env"`)
	if want < 0 || knownReleaseBreaks[want].repair == nil {
		t.Fatal("the v0.15.1 Claude temp follower break must be listed with a repair")
	}
	if got := knownSkippedBreakFor("v0.15.1", "claude", diagnostic); got != want {
		t.Fatalf("listed diagnostic matched %d, want %d", got, want)
	}
	for _, tc := range []struct {
		name, release, pack, diagnostic string
	}{
		{"wrong release", "v0.15.2", "claude", diagnostic},
		{"wrong pack", "v0.15.1", "pi", diagnostic},
		{"different field", "v0.15.1", "claude", `skipping the program contribution "claude" — unknown field "sandbox_tmpdir_env_typo"`},
		{"not a skipped program", "v0.15.1", "claude", `unknown field "sandbox_tmpdir_env"`},
		{"a skipped provider's other field", "v0.15.1", "claude-auth", `skipping the provider contribution "anthropic" — unknown field "pinned_endpoint_typo"`},
		{"a skipped kind the list does not name", "v0.15.1", "claude-auth", `skipping the loophole contribution "x" — unknown field "pinned_endpoint"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := knownSkippedBreakFor(tc.release, tc.pack, tc.diagnostic); got != -1 {
				t.Fatalf("unlisted skip matched break %d", got)
			}
		})
	}
}

// repairManifest applies repair to the manifest in dir, in place: the materialized copy, never
// the source tree. A pack.toml is rendered as its legacy pack.json first, the shape every repair
// edits. A manifest is JSON5 and a shipped one carries comments (each install hint's
// source is one), so it is read as the pack loader reads it and written back as plain JSON,
// which every release's reader reads; the comments it drops are nothing a reader decodes.
func repairManifest(t *testing.T, dir string, repair func(any) any) {
	t.Helper()
	path := filepath.Join(dir, "pack.json")
	if toml := filepath.Join(dir, "pack.toml"); fileExists(toml) {
		// A repair edits the legacy shape, so a pack.toml is first replaced by the pack.json the
		// same manifest means; a reader refuses a pack holding both.
		legacy := legacyManifestJSON(t, filepath.Base(dir), shippedManifest(t, filepath.Base(dir)))
		if err := os.WriteFile(path, legacy, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(toml); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("repairing %s: %v", dir, err)
	}
	parsed, err := json5.Decode(raw)
	if err != nil {
		t.Fatalf("repairing %s: the manifest does not parse: %v", path, err)
	}
	fixed, err := json.Marshal(repair(jsonx.Plain(parsed)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fixed, 0o644); err != nil {
		t.Fatal(err)
	}
}

// lastRelease finds the repository root and highest valid release tag not pointing at this
// candidate commit. Excluding the candidate preserves the previous-reader compatibility check.
func lastRelease(t *testing.T) (root, tag string) {
	t.Helper()
	inCI := os.Getenv("GITHUB_ACTIONS") == "true"
	unavailable := func(format string, args ...any) {
		t.Helper()
		if inCI {
			t.Fatalf(format+" — CI must fetch full history and tags (actions/checkout fetch-depth: 0) "+
				"for this check to run", args...)
		}
		t.Skipf(format, args...)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		unavailable("no git on PATH to read the last release from")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Dir(wd)
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("this test runs from packs/, so %s should be the module root, and it holds no go.mod: %v", root, err)
	}
	if err := exec.Command(git, "-C", root, "rev-parse", "--git-dir").Run(); err != nil {
		unavailable("not a git checkout, so there is no last release to read")
	}
	tag, err = releasebaseline.LatestBefore(root, "HEAD")
	if err != nil {
		unavailable("no valid local release tag other than the candidate HEAD: %v", err)
	}
	return root, tag
}

// extractRelease writes the tree at tag into dest, streaming `git archive` through archive/tar
// so no tar binary is needed. The whole tree, not a package list: a future release's reader may
// import anything in its module.
func extractRelease(t *testing.T, root, tag, dest string) {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "archive", "--format=tar", tag)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	untarInto(t, stdout, tag, dest)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("git archive %s: %v\n%s", tag, err, stderr.String())
	}
}

// untarInto writes the tar stream r (`git archive` of tag) into dest.
func untarInto(t *testing.T, r io.Reader, tag, dest string) {
	t.Helper()
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("reading git archive %s: %v", tag, err)
		}
		name := filepath.Clean(h.Name)
		if name == "." || strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			continue
		}
		path := filepath.Join(dest, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			_ = os.Symlink(h.Linkname, path)
		}
	}
	// Read past the end-of-archive marker to the stream's end: the writer's record padding is
	// still in the pipe, and a writer blocked on it never exits (TestUntarIntoReadsTheWholeStream).
	if _, err := io.Copy(io.Discard, r); err != nil {
		t.Fatalf("draining git archive %s: %v", tag, err)
	}
}

// shippedManifest decodes pack's shipped manifest with the CURRENT reader, from whichever name
// it ships under: pack.toml once converted, pack.json for a pack still on its legacy manifest.
func shippedManifest(t *testing.T, pack string) *packdecl.Manifest {
	t.Helper()
	if raw, err := packs.FS.ReadFile(pack + "/pack.toml"); err == nil {
		manifest, problems := packdecl.DecodeTOML(raw)
		if len(problems) != 0 {
			t.Fatalf("current reader refused %s/pack.toml: %v", pack, problems)
		}
		return manifest
	}
	raw, err := packs.FS.ReadFile(pack + "/pack.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, problems := packdecl.Decode(raw)
	if len(problems) != 0 {
		t.Fatalf("current reader refused %s/pack.json: %v", pack, problems)
	}
	return manifest
}

// materialize copies the embed a binary built from this tree carries into dest, one directory
// per pack: the shape a host stages into a jail's _official tree. Every release since v0.16.0
// reads pack.toml, so the tree is handed over as it ships; repairManifest renders a pack's
// legacy pack.json only for a pack it must edit.
func materialize(t *testing.T, dest string) {
	t.Helper()
	err := fs.WalkDir(packs.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dest, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := fs.ReadFile(packs.FS, path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// legacyManifestJSON renders manifest as the pack.json the last release reads. json.Marshal
// alone is not that rendering: an OptionDefault has a decoder but no encoder, so a provider
// option marshals as the struct ({"Defaulted":…}) where the legacy shape is the string
// default, or null for an option declared with no default. Each contribution's options are
// rewritten from the typed values, so an option a pack.toml declares with no default
// (`false`) reaches the old reader as the null it means.
func legacyManifestJSON(t *testing.T, pack string, manifest *packdecl.Manifest) []byte {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("rendering %s's pack.json for the last release's reader: %v", pack, err)
	}
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("re-reading %s's rendered pack.json: %v", pack, err)
	}
	contributes, _ := tree["contributes"].([]any)
	for i, c := range manifest.Contributes {
		if len(c.Options) == 0 {
			continue
		}
		options := make(map[string]any, len(c.Options))
		for name, value := range c.Options {
			if value.Defaulted {
				options[name] = value.Value
			} else {
				options[name] = nil
			}
		}
		contributes[i].(map[string]any)["options"] = options
	}
	out, err := json.Marshal(tree)
	if err != nil {
		t.Fatalf("rendering %s's pack.json for the last release's reader: %v", pack, err)
	}
	return out
}

// testFuncExists reports whether any _test.go file under root declares func name(.
func testFuncExists(t *testing.T, root, name string) bool {
	t.Helper()
	re := regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(name) + `\(`)
	found := false
	for _, dir := range []string{"internal", "cmd", "packs", "integration"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || found {
				return nil
			}
			if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if b, err := os.ReadFile(path); err == nil && re.Match(b) {
				found = true
			}
			return nil
		})
	}
	return found
}

// TestUntarIntoReadsTheWholeStream pins that the release extraction consumes git archive's output
// to its end, record padding included. tar's reader stops at the end-of-archive marker; a writer
// still holding padding then blocks on a full pipe while the test waits for it to exit, which on a
// loaded host (where Linux shrinks new pipes to one page past pipe-user-pages-soft) hung
// TestModelListCheckPackWideMetadataPreservesTheLastReleasesProgram for ten minutes.
func TestUntarIntoReadsTheWholeStream(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	body := []byte("package x\n")
	if err := tw.WriteHeader(&tar.Header{Name: "x/x.go", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	buf.Write(make([]byte, 20*512)) // git archive pads its last record, as tar(1) does
	r := bytes.NewReader(buf.Bytes())
	untarInto(t, r, "v0.0.0", t.TempDir())
	if r.Len() != 0 {
		t.Errorf("untarInto left %d bytes of the stream unread; a pipe writer would block on them", r.Len())
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
