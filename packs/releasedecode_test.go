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
// claude's `api_key_env_name` became a list (provider-credential-scope.md, OQ-CN1) — or a new
// value in a closed set, such as a hook name. Either one stops an older jail's boot. So a manifest
// change the current tree reads happily can still stop every jail an older yolo launched from
// booting, the moment that jail is handed the new tree
// (docs/design/attach-skew-and-contract-guardrails.md, "Findings since filing"). This test is the
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

// repairManifest applies repair to the manifest in dir, in place: the materialized copy, never
// the source tree.
func repairManifest(t *testing.T, dir string, repair func(any) any) {
	t.Helper()
	path := filepath.Join(dir, "pack.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("repairing %s: %v", dir, err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("repairing %s: the manifest is not plain JSON: %v", path, err)
	}
	fixed, err := json.Marshal(repair(doc))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fixed, 0o644); err != nil {
		t.Fatal(err)
	}
}

// lastRelease finds the repository root and the newest release tag before this commit: the
// release a jail still running on this machine was most plausibly launched by. HEAD^, not HEAD,
// so the release commit itself is compared against the release before it rather than against
// its own reader, which would compare nothing.
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
	// The MODULE root, found from this package's directory, which is where `go test` runs it —
	// not `git rev-parse --show-toplevel`. Inside a git hook (a hook that runs this test)
	// git exports GIT_DIR without GIT_WORK_TREE, and --show-toplevel then answers the current
	// directory, packs/, so every pinnedBy lookup below searched packs/internal and found nothing.
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
	out, err := exec.Command(git, "-C", root, "describe", "--tags", "--abbrev=0", "--match", "v[0-9]*", "HEAD^").Output()
	if err != nil {
		unavailable("no release tag is reachable from HEAD^ (a shallow clone, or no tags fetched)")
	}
	return root, strings.TrimSpace(string(out))
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
	tr := tar.NewReader(stdout)
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
	if err := cmd.Wait(); err != nil {
		t.Fatalf("git archive %s: %v\n%s", tag, err, stderr.String())
	}
}

// materialize copies the embed a binary built from this tree carries into dest, one directory
// per pack: the shape a host stages into a jail's _official tree.
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
