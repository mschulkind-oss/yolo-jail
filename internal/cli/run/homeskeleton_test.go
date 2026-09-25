package run

// homeskeleton_test.go pins the per-jail home skeleton (homeskeleton.go;
// docs/design/base-home-legacy-state.md#8-build-order-and-done-conditions).
//
// Four halves, each answering a different way the feature could quietly stop working:
//
//   - WHAT A SKELETON HOLDS: the core entries in full, the SELECTED packs' dirs and no
//     other pack's, and the config-driven mountpoints and links.
//   - COMPLETENESS OVER THE ARGV: every -v destination under /home/agent either exists in
//     the skeleton or sits inside another bind. A bind whose mountpoint is missing inside
//     the :ro root fails at `podman run` with an opaque crun error naming neither path.
//   - THE CALL SITE: the builder runs on the fresh-launch path, under the flock, after the
//     attach decision, and its result is what the argv binds. Without that pin the whole
//     feature could be switched off by deleting one line with every other test green.
//   - WHAT IT LEAVES ALONE: an attach leaves the skeleton byte-identical, and a fresh launch
//     leaves the machine store (<state>/home) byte-identical outside the shared dirs, the
//     Claude seed and the credential migration.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// buildSkeletonForTest builds a skeleton for cname and fails the test on any error or warning.
func buildSkeletonForTest(t *testing.T, cname string, packs []*packload.Pack, cfg *jsonx.OrderedMap,
	hostFiles []config.HostFileEntry) string {
	t.Helper()
	sk, err := buildHomeSkeleton(paths.HomeSkeletonRoot(cname), packs, cfg, hostFiles)
	if err != nil {
		t.Fatalf("buildHomeSkeleton: %v", err)
	}
	if len(sk.warnings) != 0 {
		t.Fatalf("buildHomeSkeleton warned: %v", sk.warnings)
	}
	return sk.dir
}

// snapshotTree records every entry under root as rel → a string holding its type, mode and
// content hash (a file) or target (a link) — "byte-identical" in a comparable form. Mtimes
// are deliberately NOT recorded: an unrelated sibling's creation moves a directory's mtime,
// and what the rules below protect is content and shape.
func snapshotTree(t *testing.T, root string, skip func(rel string) bool) map[string]string {
	t.Helper()
	out := map[string]string{}
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return out
	}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if skip != nil && rel != "." && skip(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			out[rel] = "link -> " + target
		case info.IsDir():
			out[rel] = fmt.Sprintf("dir %v", info.Mode().Perm())
		default:
			data, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			sum := sha256.Sum256(data)
			out[rel] = fmt.Sprintf("file %v %s", info.Mode().Perm(), hex.EncodeToString(sum[:]))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return out
}

// diffSnapshots names every entry that differs between two snapshots.
func diffSnapshots(before, after map[string]string) []string {
	var out []string
	for rel, was := range before {
		if now, ok := after[rel]; !ok {
			out = append(out, "removed "+rel+" ("+was+")")
		} else if now != was {
			out = append(out, "changed "+rel+": "+was+" => "+now)
		}
	}
	for rel, now := range after {
		if _, ok := before[rel]; !ok {
			out = append(out, "added "+rel+" ("+now+")")
		}
	}
	sort.Strings(out)
	return out
}

// --- what a skeleton holds ---------------------------------------------------------------

// TestTheSkeletonHoldsTheCoreEntries pins the entries that used to come from
// storage.EnsureGlobalStorage, which moved here with them: paths.HomeSkeletonCoreDirs in full
// (it was TestEnsureGlobalStorageCreatesEveryCoreBaseHomeDir in internal/storage, over
// BaseHomeCoreDirs, which also carried the pi pack's `.pi/agent`), the single-file
// mountpoints AS FILES, and the three redirects as RELATIVE links.
func TestTheSkeletonHoldsTheCoreEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-core-entries"
	dir := buildSkeletonForTest(t, cname, nil, nil, nil)

	if got, want := filepath.Dir(dir), paths.HomeSkeletonRoot(cname); got != want {
		t.Errorf("skeleton built at %s, want a child of %s", dir, want)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("skeleton root mode = %v (err %v), want 0755 — the shared base it replaces "+
			"was 0755, and os.MkdirTemp's default is 0700", info.Mode().Perm(), err)
	}
	core := paths.HomeSkeletonCoreDirs()
	if len(core) == 0 {
		t.Fatal("paths.HomeSkeletonCoreDirs is empty")
	}
	for _, rel := range core {
		if info, err := os.Lstat(filepath.Join(dir, rel)); err != nil || !info.IsDir() {
			t.Errorf("core dir %s is not a directory in the skeleton: %v", rel, err)
		}
	}
	for _, rel := range paths.HomeFileMountpoints() {
		info, err := os.Lstat(filepath.Join(dir, rel))
		if err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
			t.Errorf("file mountpoint %s is not an empty regular file: %v", rel, err)
		}
	}
	for _, r := range paths.HomeFileRedirects() {
		target, err := os.Readlink(filepath.Join(dir, r.Name))
		if err != nil {
			t.Errorf("redirect %s is not a link: %v", r.Name, err)
			continue
		}
		if target != r.Target || filepath.IsAbs(target) {
			t.Errorf("redirect %s -> %q, want the relative %q", r.Name, target, r.Target)
		}
	}
}

// TestTheSkeletonCarriesOnlyTheSelectedPacksDirs is the design's DIR-BH1 ("a non-selected
// pack can never have an impact") at the level of the home root. The shared base carried
// every SHIPPED pack's dirs, so a claude-only jail had a ~/.codex, and a codex-only jail
// could read ~/.claude-shared-credentials/.credentials.json — the machine's refresh token —
// through the base.
func TestTheSkeletonCarriesOnlyTheSelectedPacksDirs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	t.Run("claude only", func(t *testing.T) {
		dir := buildSkeletonForTest(t, "yolo-claude-only", packsFixture(t, "claude"), nil, nil)
		for _, rel := range []string{".claude", ".claude-shared-credentials"} {
			if info, err := os.Lstat(filepath.Join(dir, rel)); err != nil || !info.IsDir() {
				t.Errorf("the selected claude pack's %s has no mountpoint: %v", rel, err)
			}
		}
		// `.pi` included: core's list used to carry the pi pack's `.pi/agent`, which put a
		// ~/.pi in every jail whether pi was selected or not.
		for _, rel := range []string{".codex", ".copilot", ".oh-omp", ".pi", ".pi-lens", ".gemini-shared-credentials", ".pi-shared-npm"} {
			if _, err := os.Lstat(filepath.Join(dir, rel)); err == nil {
				t.Errorf("a claude-only skeleton has %s, which no selected pack declares", rel)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, ".claude.json")); !os.IsNotExist(err) {
			// It resolves only through the .claude BIND in a container; host-side the
			// skeleton's own .claude is an empty mountpoint, so the link dangles here.
			t.Errorf("host-side, .claude.json should dangle into the empty .claude mountpoint: %v", err)
		}
	})

	t.Run("codex only", func(t *testing.T) {
		dir := buildSkeletonForTest(t, "yolo-codex-only", packsFixture(t, "codex"), nil, nil)
		if info, err := os.Lstat(filepath.Join(dir, ".codex")); err != nil || !info.IsDir() {
			t.Errorf("the selected codex pack's .codex has no mountpoint: %v", err)
		}
		for _, rel := range []string{".claude", ".claude-shared-credentials", ".copilot", ".gemini-shared-credentials", ".pi"} {
			if _, err := os.Lstat(filepath.Join(dir, rel)); err == nil {
				t.Errorf("a codex-only skeleton has %s, which no selected pack declares", rel)
			}
		}
		// The redirect is still there (core's), and in a jail without claude its target is
		// not bound, so it dangles and reads as absent.
		if _, err := os.Readlink(filepath.Join(dir, ".claude.json")); err != nil {
			t.Errorf(".claude.json is not a link: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, ".claude.json")); !os.IsNotExist(err) {
			t.Errorf(".claude.json must dangle in a jail that did not select claude: %v", err)
		}
	})

	t.Run("pi only", func(t *testing.T) {
		// The other half of dropping `.pi/agent` from core's list: a jail that DOES select
		// pi still gets its ~/.pi mountpoint, from the pack's own `.pi` state dir.
		dir := buildSkeletonForTest(t, "yolo-pi-only", packsFixture(t, "pi"), nil, nil)
		if info, err := os.Lstat(filepath.Join(dir, ".pi")); err != nil || !info.IsDir() {
			t.Errorf("the selected pi pack's .pi has no mountpoint: %v", err)
		}
	})
}

// TestCoreSkeletonDirsNameNoPacksDir is DIR-BH1 over core's own list: an entry of
// paths.HomeSkeletonCoreDirs at or under a directory some shipped pack declares would appear
// in every jail, selected or not. `.pi/agent` was that entry (under the pi pack's `.pi`).
func TestCoreSkeletonDirsNameNoPacksDir(t *testing.T) {
	loaded, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) != 0 {
		t.Fatalf("materializing official packs: %v", problems)
	}
	declared := append(packload.WritableDirs(loaded), packload.SharedDirs(loaded)...)
	if len(declared) < 5 {
		t.Fatalf("only %d pack-declared dirs across the shipped packs; the fixture has gone blind", len(declared))
	}
	for _, core := range paths.HomeSkeletonCoreDirs() {
		core = filepath.ToSlash(core)
		for _, d := range declared {
			if core == d || strings.HasPrefix(core, d+"/") {
				t.Errorf("core's skeleton dir %s is inside the pack-declared %s, so every jail gets "+
					"it whether that pack is selected or not", core, d)
			}
		}
	}
}

// TestTheSkeletonCarriesTheConfigDrivenEntries covers the mountpoints that used to be
// written into the shared base by prepareWsState and prepareHostFiles.
func TestTheSkeletonCarriesTheConfigDrivenEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := newConfig("writable_home_dirs", []any{".pi-lens", ".foo/bar"})
	hostFiles := []config.HostFileEntry{
		{Path: ".npmrc", Source: "/host/.npmrc", Codec: "raw", Mode: config.HostFileModeReadonly},
		{Path: "hf/one.json", Codec: "json", HasContent: true, Mode: config.HostFileModeOnce},
		{Path: ".config/mytool/c.json", Codec: "json", HasContent: true, Mode: config.HostFileModeOnce},
	}
	dir := buildSkeletonForTest(t, "yolo-config-driven", packsFixture(t, "claude"), cfg, hostFiles)

	for _, rel := range []string{".pi-lens", ".foo/bar", "hf"} {
		if info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil || !info.IsDir() {
			t.Errorf("mountpoint %s is not a directory: %v", rel, err)
		}
	}
	link := filepath.Join(dir, ".npmrc")
	if target, err := os.Readlink(link); err != nil || target != hostFiles[0].SymlinkTarget() {
		t.Errorf(".npmrc -> %q (err %v), want %q", target, err, hostFiles[0].SymlinkTarget())
	}
	if _, err := os.Stat(link); !os.IsNotExist(err) {
		t.Errorf("the .npmrc link resolves host-side (err=%v); it must dangle so `once` seeds", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".config", "mytool")); err == nil {
		t.Error("an entry already under the ~/.config bind got a skeleton mountpoint")
	}
}

// TestEachFreshLaunchGetsANewSkeleton is the maintainer's OQ-BH10 ruling: a new directory
// per fresh launch, never modified afterwards. The FIRST skeleton is byte-identical after the
// second is built — a later launch with a different config must not touch the tree a live
// jail may still hold bound.
func TestEachFreshLaunchGetsANewSkeleton(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-new-per-launch"
	dropped := []config.HostFileEntry{{Path: ".npmrc", Source: "/host/.npmrc", Codec: "raw", Mode: config.HostFileModeReadonly}}
	first := buildSkeletonForTest(t, cname, packsFixture(t, "claude"), newConfig("writable_home_dirs", []any{".old"}), dropped)
	before := snapshotTree(t, first, nil)

	second := buildSkeletonForTest(t, cname, packsFixture(t, "codex"), newConfig("writable_home_dirs", []any{".new"}), nil)
	if second == first {
		t.Fatalf("the second launch reused the first skeleton %s", first)
	}
	if d := diffSnapshots(before, snapshotTree(t, first, nil)); len(d) != 0 {
		t.Errorf("building a second skeleton modified the first:\n  %s", strings.Join(d, "\n  "))
	}
	// A DROPPED entry is gone at the next fresh launch, because nothing carries over: the
	// shared base only ever grew, so a dropped host_files link stayed in every jail for good.
	for _, rel := range []string{".old", ".npmrc", ".claude"} {
		if _, err := os.Lstat(filepath.Join(second, rel)); err == nil {
			t.Errorf("the second skeleton inherited the first launch's %s", rel)
		}
	}
}

// TestSkeletonFailuresNameThePath pins the fatal/best-effort split of
// docs/design/base-home-legacy-state.md#23-when-it-is-built: a core failure is FATAL and names the
// path; a config-driven one is a warning that names it.
func TestSkeletonFailuresNameThePath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	t.Run("fatal", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(blocker, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(blocker, "home")
		_, err := buildHomeSkeleton(root, nil, nil, nil)
		if err == nil {
			t.Fatal("a skeleton root that cannot be created must fail the launch")
		}
		if !strings.Contains(err.Error(), root) {
			t.Errorf("the error does not name the path it could not create: %v", err)
		}
	})

	t.Run("best effort", func(t *testing.T) {
		// A host_files home-root link on a path core already made a FILE mountpoint:
		// config validation reserves the name, so only a hand-built entry reaches here.
		entry := config.HostFileEntry{Path: ".yolo-perf.log", Source: "/host/x", Codec: "raw", Mode: config.HostFileModeReadonly}
		sk, err := buildHomeSkeleton(paths.HomeSkeletonRoot("yolo-best-effort"), nil, nil, []config.HostFileEntry{entry})
		if err != nil {
			t.Fatalf("a best-effort entry failed the launch: %v", err)
		}
		if len(sk.warnings) != 1 || !strings.Contains(sk.warnings[0], filepath.Join(sk.dir, ".yolo-perf.log")) {
			t.Errorf("want one warning naming %s, got %v", filepath.Join(sk.dir, ".yolo-perf.log"), sk.warnings)
		}
		if info, err := os.Lstat(filepath.Join(sk.dir, ".yolo-perf.log")); err != nil || !info.Mode().IsRegular() {
			t.Errorf("the core file mountpoint was replaced by the failing link: %v", err)
		}
	})

	t.Run("a pack entry on a redirect", func(t *testing.T) {
		// A selected pack whose `files` target and briefing destination are two of the
		// redirect NAMES. packload accepts both, and they are pack-driven, so each may only
		// degrade: the redirects are built before any best-effort entry, the colliding
		// entry is the one that fails, and it fails as a warning naming its path. Built the
		// other way round, the pack's file landed first and the FATAL redirect then refused
		// every fresh launch with EEXIST.
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "gitconfig"), []byte("[user]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		manifest := `{"name":"dotfiles","contributes":[
			{"kind":"files","from":"gitconfig","into":".gitconfig"},
			{"kind":"briefing","into":".bashrc"}
		]}`
		if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		p, problems := packload.LoadDir(root, "dotfiles")
		if len(problems) != 0 {
			t.Fatalf("loading pack: %v", problems)
		}
		sk, err := buildHomeSkeleton(paths.HomeSkeletonRoot("yolo-redirect-collision"), []*packload.Pack{p}, nil, nil)
		if err != nil {
			t.Fatalf("a pack-driven entry on a redirect name failed the launch: %v", err)
		}
		for _, r := range paths.HomeFileRedirects() {
			if target, err := os.Readlink(filepath.Join(sk.dir, r.Name)); err != nil || target != r.Target {
				t.Errorf("redirect %s -> %q (err %v), want %q intact", r.Name, target, err, r.Target)
			}
		}
		for _, rel := range []string{".gitconfig", ".bashrc"} {
			want := filepath.Join(sk.dir, rel)
			found := false
			for _, w := range sk.warnings {
				if strings.Contains(w, want) {
					found = true
				}
			}
			if !found {
				t.Errorf("no warning names %s; got %v", want, sk.warnings)
			}
		}
	})

	t.Run("escaping path", func(t *testing.T) {
		sk, err := buildHomeSkeleton(paths.HomeSkeletonRoot("yolo-escape"), nil,
			newConfig(), []config.HostFileEntry{{Path: "../outside", Codec: "raw", Mode: config.HostFileModeReadonly}})
		if err != nil {
			t.Fatalf("unexpected fatal error: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(filepath.Dir(sk.dir), "outside")); err == nil {
			t.Error("the builder wrote outside the skeleton")
		}
	})
}

// --- completeness over the argv ----------------------------------------------------------

// homeBindDests returns every -v destination under /home/agent in argv, and the source bound
// at /home/agent itself.
func homeBindDests(t *testing.T, argv []string) (dests []string, root string) {
	t.Helper()
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "-v" {
			continue
		}
		parts := strings.Split(argv[i+1], ":")
		if len(parts) < 2 {
			continue // an anonymous volume (`-v /tmp`)
		}
		switch dest := parts[1]; {
		case dest == "/home/agent":
			root = parts[0]
		case strings.HasPrefix(dest, "/home/agent/"):
			dests = append(dests, dest)
		}
	}
	return dests, root
}

// assertEveryHomeBindHasASkeletonEntry is the completeness rule itself.
func assertEveryHomeBindHasASkeletonEntry(t *testing.T, argv []string, skeleton string) int {
	t.Helper()
	dests, root := homeBindDests(t, argv)
	if root != skeleton {
		t.Fatalf("/home/agent is bound from %q, want this launch's skeleton %q", root, skeleton)
	}
	set := map[string]bool{}
	for _, d := range dests {
		set[d] = true
	}
	for _, d := range dests {
		inside := false
		for other := range set {
			if other != d && strings.HasPrefix(d, other+"/") {
				inside = true
				break
			}
		}
		if inside {
			continue
		}
		rel := strings.TrimPrefix(d, "/home/agent/")
		if _, err := os.Lstat(filepath.Join(skeleton, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s is bound directly under the :ro home root, and the skeleton has no "+
				"mountpoint for it (%v). crun cannot mkdirat inside a read-only bind, so this "+
				"launch fails at `podman run` with an error naming neither path.", d, err)
		}
	}
	return len(dests)
}

// TestEveryHomeBindHasASkeletonEntry builds a REAL skeleton with the production builder,
// assembles the argv from the same inputs, and checks the completeness rule over it — for
// the golden fixture and for one that exercises every config-driven kind.
func TestEveryHomeBindHasASkeletonEntry(t *testing.T) {
	t.Run("golden", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		emptyLoopholeDirs(t)
		o := goldenOptions("/ws", home)
		in := relocationInput(t, "podman", "/ws/.yolo/home", nil)
		in.homeSkeleton = buildSkeletonForTest(t, in.cname, in.packs, in.cfg, nil)
		if n := assertEveryHomeBindHasASkeletonEntry(t, o.assembleRunCmd(in), in.homeSkeleton); n < 15 {
			t.Fatalf("found only %d home binds in the golden argv; the extractor has gone blind", n)
		}
	})

	t.Run("every config-driven kind", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		emptyLoopholeDirs(t)
		ws := t.TempDir()
		o := goldenOptions(ws, home)
		o.Stdout, o.Stderr = discardBuf(), discardBuf()

		// A pack whose `files` and skills land OUTSIDE every writable dir, so their
		// mountpoints can only come from the skeleton.
		root := t.TempDir()
		for _, d := range []string{"tree", "skills"} {
			if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(root, "one.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		manifest := `{"name":"skeleton-kinds","contributes":[
			{"kind":"files","from":"tree","into":".kinds/tree"},
			{"kind":"files","from":"one.json","into":".kinds-file.json"},
			{"kind":"skills","from":"skills","into":".kinds-skills"},
			{"kind":"briefing","into":".kinds-brief/AGENTS.md"}
		]}`
		if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		custom, problems := packload.LoadDir(root, "skeleton-kinds")
		if len(problems) != 0 {
			t.Fatalf("loading pack: %v", problems)
		}
		packs := append(packsFixture(t, "claude", "codex", "pi"), custom)

		sec := jsonx.NewOrderedMap()
		sec.Set("blocked_tools", []any{})
		cfg := newConfig("security", sec, "writable_home_dirs", []any{".pi-lens", ".foo/bar"})
		hostFiles := []config.HostFileEntry{
			{Path: ".npmrc", Source: "/host/.npmrc", Codec: "raw", Mode: config.HostFileModeReadonly},
			{Path: "hf/one.json", Codec: "json", HasContent: true, Mode: config.HostFileModeOnce},
		}
		wsState := filepath.Join(ws, ".yolo", "home")
		if err := os.MkdirAll(wsState, 0o755); err != nil {
			t.Fatal(err)
		}
		in := &assembleInput{
			cfg:              cfg,
			rt:               "podman",
			cname:            "yolo-kinds",
			imageRef:         goldenImageRef,
			jailPrefix:       goldenJailPrefix,
			packs:            packs,
			agentsPath:       filepath.Join(ws, "agents"),
			wsState:          wsState,
			miseStore:        "/mise-store",
			yoloVersion:      "9.9.9-test",
			mountTargets:     map[string]struct{}{},
			writableHomeDirs: config.WritableHomeDirs(cfg, packs),
			hostFiles:        hostFiles,
			cacheRelocations: []config.CacheRelocation{{Subdir: "uv", Target: "/data/relocated/uv"}},
		}
		in.homeSkeleton = buildSkeletonForTest(t, in.cname, packs, cfg, hostFiles)
		argv := o.assembleRunCmd(in)
		assertEveryHomeBindHasASkeletonEntry(t, argv, in.homeSkeleton)

		// Non-vacuity: the kinds this subtest exists for really are on the argv.
		joined := strings.Join(argv, " ")
		for _, want := range []string{":/home/agent/.kinds/tree:ro", ":/home/agent/.kinds-file.json:ro",
			":/home/agent/.kinds-skills:ro", ":/home/agent/.kinds-brief/AGENTS.md:ro",
			":/home/agent/.pi-lens", ":/home/agent/.foo/bar", ":/home/agent/hf", ":/home/agent/.codex",
			":/home/agent/.cache/uv"} {
			if !strings.Contains(joined, want) {
				t.Errorf("the fixture no longer emits %s, so its completeness check proves nothing about it", want)
			}
		}
	})
}

// --- the call site ------------------------------------------------------------------------

// TestRunContainerBuildsTheSkeletonOnTheFreshPath pins the builder's CALL SITE, the shape
// currentimagecallsite_test.go uses for a site no unit test can reach behaviorally.
//
//   - DELETED, podman binds an empty source at /home/agent and every launch fails — or,
//     if someone "fixes" that by binding paths.GlobalHome() again, the shared base and every
//     leak it carried come back with the whole suite green.
//   - ITS RESULT DROPPED — the assignment lost, moved into a nested block, or its source
//     shadowed — the builder still runs and warns, and the argv binds "" all the same. So
//     the flow is pinned statement by statement rather than by the field's name (which is
//     all this pin checked at first, and a lost assignment passed it): `sk, err :=
//     buildHomeSkeleton(...)` and `in.homeSkeleton = sk.dir` as statements of ONE block,
//     `in` being the assembleInput runContainer declares above that block and hands to
//     assembleRunCmd below it, and nothing else writing the field or re-binding either name.
//   - MOVED ABOVE the lock or the raced re-check, or CALLED A SECOND TIME up there, two
//     launches race one skeleton root and a launch that ends up ATTACHING still builds one.
//     Moved below assembly, the argv names a directory that does not exist yet.
//   - MOVED OUT of the podman arm, Apple Container builds a tree nothing binds.
//   - CALLED FROM ANY OTHER FUNCTION of the package — attachExisting, a helper — it runs on
//     a path none of the orderings above constrain.
func TestRunContainerBuildsTheSkeletonOnTheFreshPath(t *testing.T) {
	fd := funcDecl(t, "run.go", "runContainer")
	var lockPos, lastAttachPos, hostFilesPos, assemblePos token.Pos
	var buildPos []token.Pos
	ast.Inspect(fd, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch skelCallee(call) {
		case "acquireWorkspaceLock":
			lockPos = call.Pos()
		case "attachExisting":
			if call.Pos() > lastAttachPos {
				lastAttachPos = call.Pos()
			}
		case "prepareHostFiles":
			hostFilesPos = call.Pos()
		case "buildHomeSkeleton":
			buildPos = append(buildPos, call.Pos())
		case "assembleRunCmd":
			assemblePos = call.Pos()
		}
		return true
	})

	if len(buildPos) == 0 {
		t.Fatal("runContainer no longer calls buildHomeSkeleton: podman binds an empty source at " +
			"/home/agent, or — if the bind was pointed back at paths.GlobalHome() — every jail " +
			"shares one home again, with every unselected pack's dirs and the machine's shared " +
			"credential dirs visible in it (docs/design/base-home-legacy-state.md#1-the-question-and-the-answer)")
	}
	if len(buildPos) > 1 {
		t.Errorf("runContainer calls buildHomeSkeleton %d times; one fresh launch builds ONE "+
			"skeleton, and every extra call leaves a directory behind that nothing binds", len(buildPos))
	}
	for _, c := range []struct {
		name string
		pos  token.Pos
	}{{"acquireWorkspaceLock", lockPos}, {"the last attachExisting", lastAttachPos},
		{"prepareHostFiles", hostFilesPos}, {"assembleRunCmd", assemblePos}} {
		if c.pos == token.NoPos {
			t.Fatalf("runContainer no longer calls %s — this pin's anchors moved; re-anchor it, "+
				"do not delete it", c.name)
		}
	}
	for _, bp := range buildPos {
		if !(lockPos < bp) {
			t.Error("a skeleton is built before the workspace flock is taken")
		}
		if !(lastAttachPos < bp) {
			t.Error("a skeleton is built before runContainer's last attach decision, so a launch " +
				"that ends up attaching still builds one")
		}
		if !(hostFilesPos < bp) {
			t.Error("a skeleton is built before prepareHostFiles, out of the order the fresh path provisions in")
		}
		if !(bp < assemblePos) {
			t.Error("a skeleton is built after the argv is assembled, so the argv names a directory " +
				"that does not exist yet")
		}
	}

	checkSkeletonReachesTheArgv(t, fd)

	// Only runContainer builds one, anywhere in the package's production code.
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Name.Name != "runContainer" && callsIn(fn)["buildHomeSkeleton"] {
				t.Errorf("%s: %s builds a home skeleton; only runContainer's fresh path may, after "+
					"the attach decision and under the flock (an attach keeps the one its jail booted with)",
					file, fn.Name.Name)
			}
		}
	}
}

// checkSkeletonReachesTheArgv pins the data flow from buildHomeSkeleton's result to the
// assembleInput that assembleRunCmd reads, statement by statement (see the caller's list).
func checkSkeletonReachesTheArgv(t *testing.T, fd *ast.FuncDecl) {
	t.Helper()
	top := fd.Body.List

	// The podman arm holding the builder, as a statement of runContainer's own body.
	armIdx := -1
	for i, st := range top {
		if ifs, ok := st.(*ast.IfStmt); ok && skelIsPodmanArm(ifs.Cond) && callsIn(ifs.Body)["buildHomeSkeleton"] {
			armIdx = i
			break
		}
	}
	if armIdx < 0 {
		t.Fatal("buildHomeSkeleton is not inside an `if rt != \"container\"` arm that is a statement " +
			"of runContainer's own body: only podman binds a skeleton, and Apple Container binds " +
			"wsState whole")
	}
	arm := top[armIdx].(*ast.IfStmt).Body.List

	// In the arm: `R, err := buildHomeSkeleton(paths.HomeSkeletonRoot(cname), ...)`, then,
	// later in the SAME block, `V.homeSkeleton = R.dir`, with nothing re-binding R between.
	result, input := "", ""
	buildIdx, flowIdx := -1, -1
	for i, st := range arm {
		as, ok := st.(*ast.AssignStmt)
		if !ok || len(as.Lhs) == 0 || len(as.Rhs) != 1 {
			continue
		}
		if call, ok := as.Rhs[0].(*ast.CallExpr); ok && skelCallee(call) == "buildHomeSkeleton" {
			buildIdx, result = i, skelIdent(as.Lhs[0])
			if as.Tok != token.DEFINE || result == "" || result == "_" {
				t.Errorf("the podman arm does not bind buildHomeSkeleton's result to a new name " +
					"(`sk, err := buildHomeSkeleton(...)`), so nothing below can be traced to it")
			}
			root := ""
			if len(call.Args) > 0 {
				if c, ok := call.Args[0].(*ast.CallExpr); ok {
					root = skelCallee(c)
				}
			}
			if root != "HomeSkeletonRoot" {
				t.Errorf("buildHomeSkeleton is handed a root from %q, want paths.HomeSkeletonRoot(cname): "+
					"the reaper removes skeletons only because they live under AGENTS_DIR/<cname>", root)
			}
			continue
		}
		sel, ok := as.Lhs[0].(*ast.SelectorExpr)
		if buildIdx < 0 || !ok || sel.Sel.Name != "homeSkeleton" {
			continue
		}
		flowIdx, input = i, skelIdent(sel.X)
		rhs, ok := as.Rhs[0].(*ast.SelectorExpr)
		if as.Tok != token.ASSIGN || !ok || rhs.Sel.Name != "dir" || skelIdent(rhs.X) != result {
			t.Errorf("the podman arm writes homeSkeleton from something other than %s.dir, the "+
				"result it just built", result)
		}
	}
	if buildIdx < 0 {
		t.Fatal("buildHomeSkeleton is not a direct statement of the podman arm " +
			"(`sk, err := buildHomeSkeleton(...)`), so its result cannot be traced")
	}
	if flowIdx < 0 || input == "" {
		t.Fatalf("the podman arm builds a skeleton and never writes %s.dir into the assembly "+
			"input (`in.homeSkeleton = %s.dir`, a statement of the same block): the builder "+
			"runs, and the argv binds \"\" at /home/agent", result, result)
	}
	for _, st := range arm[buildIdx+1 : flowIdx] {
		if skelRebinds(st, result) {
			t.Errorf("%s is re-bound between the build and the assignment, so the argv may not "+
				"get the skeleton that was built", result)
		}
	}
	for _, st := range arm {
		if skelRebinds(st, input) {
			t.Errorf("the podman arm re-binds %s, so the homeSkeleton it sets is not the input "+
				"assembleRunCmd reads", input)
		}
	}

	// `input` is the assembleInput declared in runContainer's body ABOVE the arm and handed
	// to assembleRunCmd BELOW it, and no statement in between re-binds it.
	declIdx, useIdx := -1, -1
	for i, st := range top {
		as, ok := st.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			continue
		}
		if i < armIdx && as.Tok == token.DEFINE && skelIdent(as.Lhs[0]) == input {
			if u, ok := as.Rhs[0].(*ast.UnaryExpr); ok && u.Op == token.AND {
				if cl, ok := u.X.(*ast.CompositeLit); ok && skelIdent(cl.Type) == "assembleInput" {
					declIdx = i
				}
			}
		}
		if call, ok := as.Rhs[0].(*ast.CallExpr); ok && i > armIdx && useIdx < 0 &&
			skelCallee(call) == "assembleRunCmd" && len(call.Args) == 1 && skelIdent(call.Args[0]) == input {
			useIdx = i
		}
	}
	if declIdx < 0 {
		t.Errorf("%s is not the `%s := &assembleInput{...}` of runContainer's own body above the "+
			"podman arm", input, input)
	}
	if useIdx < 0 {
		t.Errorf("runContainer does not hand %s to assembleRunCmd after the podman arm, so the "+
			"skeleton it carries is not what the argv binds", input)
	}
	if declIdx >= 0 && useIdx >= 0 {
		for i := declIdx + 1; i < useIdx; i++ {
			if i != armIdx && skelRebinds(top[i], input) {
				t.Errorf("runContainer re-binds %s between the podman arm and assembleRunCmd", input)
			}
		}
	}

	// And the field is written in exactly one place: that assignment.
	writes := 0
	ast.Inspect(fd, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.KeyValueExpr:
			if skelIdent(x.Key) == "homeSkeleton" {
				writes++
			}
		case *ast.AssignStmt:
			for _, l := range x.Lhs {
				if sel, ok := l.(*ast.SelectorExpr); ok && sel.Sel.Name == "homeSkeleton" {
					writes++
				}
			}
		}
		return true
	})
	if writes != 1 {
		t.Errorf("runContainer writes homeSkeleton in %d places; want exactly the one after the "+
			"builder, or a later write can replace the skeleton the argv binds", writes)
	}
}

// skelCallee names a call's function: `f(...)` → f, `x.f(...)` → f.
func skelCallee(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

// skelIdent is e's name when e is a bare identifier, else "".
func skelIdent(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// skelIsPodmanArm reports whether cond is `rt != "container"`.
func skelIsPodmanArm(cond ast.Expr) bool {
	be, ok := cond.(*ast.BinaryExpr)
	if !ok || be.Op != token.NEQ || skelIdent(be.X) != "rt" {
		return false
	}
	lit, ok := be.Y.(*ast.BasicLit)
	return ok && lit.Value == `"container"`
}

// skelRebinds reports whether anything inside st assigns to, re-declares or shadows name.
func skelRebinds(st ast.Stmt, name string) bool {
	found := false
	ast.Inspect(st, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for _, l := range x.Lhs {
				if skelIdent(l) == name {
					found = true
				}
			}
		case *ast.ValueSpec:
			for _, id := range x.Names {
				if id.Name == name {
					found = true
				}
			}
		case *ast.RangeStmt:
			if skelIdent(x.Key) == name || skelIdent(x.Value) == name {
				found = true
			}
		}
		return true
	})
	return found
}

// funcDecl returns the top-level function or method named name in file.
func funcDecl(t *testing.T, file, name string) *ast.FuncDecl {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == name {
			return fd
		}
	}
	t.Fatalf("%s has no function %s", file, name)
	return nil
}

// callsIn returns the set of called function/method names inside n.
func callsIn(n ast.Node) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(n, func(m ast.Node) bool {
		call, ok := m.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			out[fn.Name] = true
		case *ast.SelectorExpr:
			out[fn.Sel.Name] = true
		}
		return true
	})
	return out
}

// TestRunCallsEnsureStorage pins the call site of the storage provisioning and the v2 layout
// migration.
//
// ⚠ IF THIS FAILS BECAUSE THE CALL MOVED rather than because it is gone, MOVE THIS CHECK
// WITH IT — do not delete it. Deleting `o.ensureStorage()` from Run stops the machine store's
// shared dirs being provisioned (bind sources the argv names) and stops the v2 layout
// migration, which then looks like nothing at all: internal/storage's own test calls
// MigrateStorageLayout directly with insideJail=true. It lived in basehomedisclosure_test.go
// until that file went with the legacy base-home refusal.
func TestRunCallsEnsureStorage(t *testing.T) {
	if !callsIn(funcDecl(t, "run.go", "Run"))["ensureStorage"] {
		t.Fatal("Run no longer calls o.ensureStorage(): the machine store's shared dirs and the v2 " +
			"layout migration both stop running, silently. If the call MOVED, move this pin with it.")
	}
}

// TestEnsureStorageNoLongerRefusesOnLegacyBytes is the maintainer's OQ-BH13 ruling at the
// launch: legacy bytes in <state>/home are unmounted and unread now, so the refusal and its
// YOLO_ALLOW_LEGACY_BASE_HOME hatch are gone and ensureStorage returns only the storage
// error. `yolo check` still reports the bytes (internal/cli/check/basehomedisclosure.go).
func TestEnsureStorageNoLongerRefusesOnLegacyBytes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacy := filepath.Join(paths.GlobalHome(), ".claude", "projects", "-home-someone-code-thing", "t.jsonl")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, bytes.Repeat([]byte("x"), 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	var errBuf bytes.Buffer
	o := goldenOptions(t.TempDir(), home)
	o.Stdout, o.Stderr = discardBuf(), &errBuf

	if err := o.ensureStorage(); err != nil {
		t.Fatalf("ensureStorage refused over legacy bytes nothing mounts or reads: %v", err)
	}
	if strings.Contains(errBuf.String(), "base home") || strings.Contains(errBuf.String(), "mv ") {
		t.Errorf("the launch still discloses the legacy bytes; that report is `yolo check`'s now:\n%s", errBuf.String())
	}
}

// --- what it leaves alone -----------------------------------------------------------------

// TestAttachLeavesTheSkeletonByteIdentical: an attach re-runs everything that writes under
// AGENTS_DIR/<cname> — the pack staging (Run, above the dispatch) and the skills and briefing
// refresh (runContainer, above the attach decision) — and then the attach arm itself,
// attachExisting, and must leave every skeleton under that root exactly as it was, and add
// none. A live jail's /home/agent is one of them, and a host-side rmdir of its mountpoint
// silently detaches the bind inside (rule 2 in homeskeleton.go).
//
// attachExisting is DRIVEN, not re-implemented: an earlier cut of this test replayed only the
// staging and the refresh by hand, so an attach arm that removed the skeleton root passed it.
// Its exec is the one step not reached (a real runtime would run): no runtime is on PATH, so
// the arm runs every host-side step it has and stops there.
func TestAttachLeavesTheSkeletonByteIdentical(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	emptyLoopholeDirs(t)
	jailcontent.SetPackSkillDirs(nil)
	jailcontent.SetPackSkillTargets(nil)
	t.Cleanup(func() { jailcontent.SetPackSkillDirs(nil); jailcontent.SetPackSkillTargets(nil) })

	const cname = "yolo-attach-skeleton"
	o := goldenOptions(t.TempDir(), home)
	o.Stdout, o.Stderr = discardBuf(), discardBuf()
	cfg := newConfig("writable_home_dirs", []any{".pi-lens"})

	// The fresh launch.
	staged, ok := o.stageRunPacks(cname)
	if !ok {
		t.Fatal("stageRunPacks failed")
	}
	if _, err := o.refreshJailBriefings(cname, cfg, "podman", staged); err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	buildSkeletonForTest(t, cname, staged.packs, cfg, nil)
	before := snapshotTree(t, paths.HomeSkeletonRoot(cname), nil)
	if len(before) < 20 {
		t.Fatalf("the fresh launch's skeleton has only %d entries; the snapshot is not seeing it", len(before))
	}

	// The attach, with a config that has since changed.
	changed := newConfig("writable_home_dirs", []any{".pi-lens", ".added-since"})
	staged, ok = o.stageRunPacks(cname)
	if !ok {
		t.Fatal("stageRunPacks failed on the attach")
	}
	if _, err := o.refreshJailBriefings(cname, changed, "podman", staged); err != nil {
		t.Fatalf("refreshJailBriefings on the attach: %v", err)
	}
	// The attach arm, against a jail `inspect` reports as running.
	var stdout bytes.Buffer
	o.Stdout = &stdout
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "inspect" {
			return ExecResult{Ran: true, RC: 0, Stdout: "YOLO_VERSION=9.9.9-test\n"}
		}
		return ExecResult{Ran: false}
	}
	t.Setenv("PATH", t.TempDir())
	channel := channelFor(t, o, changed, staged.packs, nil)
	rc := o.attachExisting(cname, "podman", "true", changed, staged, channel, false)
	if got := stdout.String(); rc != 1 || !strings.Contains(got, "Attaching to existing jail") ||
		!strings.Contains(got, "not found on PATH") {
		t.Fatalf("the attach arm did not run through to its exec (rc=%d), so this test proves "+
			"nothing about it:\n%s", rc, got)
	}

	if d := diffSnapshots(before, snapshotTree(t, paths.HomeSkeletonRoot(cname), nil)); len(d) != 0 {
		t.Errorf("the attach path changed the skeletons under %s:\n  %s",
			paths.HomeSkeletonRoot(cname), strings.Join(d, "\n  "))
	}
}

// TestOnlyTheListedFunctionsNameTheMachineStore is the call-site half of the snapshot test
// below. That test drives a hand-picked list of writers, so it pins those callees and nothing
// else: a write into <state>/home added straight into runContainer, or into a fresh-path
// helper not on its list, passed it and the whole suite (a reviewer's mutation, 2026-09-25:
// a writable_home_dirs MkdirAll loop into paths.GlobalHome() right after prepareHostFiles).
// So every production function of this package that names paths.GlobalHome() is listed
// here with what it does with it, and each one that WRITES is one the snapshot drives.
//
// It pins the accessor, not every spelling of the path: filepath.Join(paths.GlobalStorage(),
// "home") would pass it. Writers in other packages (storage.EnsureGlobalStorage) are driven
// by the snapshot through their callers here.
func TestOnlyTheListedFunctionsNameTheMachineStore(t *testing.T) {
	allowed := map[string]string{
		"prepareWsState": "writes the Claude login seed (syncClaudeJSONSeed) and runs the " +
			"stranded-credential rescue (migrateOldOverlay); the snapshot below drives it",
		"assembleRunCmd": "names podman's shared-dir bind SOURCES in the argv, and writes " +
			"nothing; the snapshot below drives it",
		"appleContainerBaseMounts": "names Apple Container's shared-dir bind SOURCES in the " +
			"argv, and writes nothing",
		"ensureSharedDirSources": "creates those SOURCES for the selected packs' machine-scope " +
			"shared dirs, and nothing else; the snapshot below drives it",
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "GlobalHome" || skelIdent(sel.X) != "paths" {
					return true
				}
				seen[fn.Name.Name] = true
				if _, ok := allowed[fn.Name.Name]; !ok {
					t.Errorf("%s: %s names paths.GlobalHome(), the machine store every jail's "+
						"shared dirs come from. A fresh launch may write there only through the "+
						"writers the snapshot below drives; if this is one, add it to that test's "+
						"writers and to this list, saying what it does there", file, fn.Name.Name)
				}
				return true
			})
		}
	}
	for name := range allowed {
		if !seen[name] {
			t.Errorf("%s no longer names paths.GlobalHome(); drop it from this list", name)
		}
	}
}

// TestAFreshLaunchLeavesTheMachineStoreByteIdentical runs every host-side writer of a fresh
// podman launch, in runContainer's order, over a machine store holding legacy bytes and an
// old base's shape, and requires <state>/home to come out byte-identical outside the three
// things that still belong there: the machine-scope shared dirs, the Claude login seed, and
// the credential migration. Before the skeleton, the same launch added this workspace's
// writable_home_dirs, host_files and pack mountpoints to the store, where every other jail
// then saw them.
func TestAFreshLaunchLeavesTheMachineStoreByteIdentical(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	emptyLoopholeDirs(t)
	jailcontent.SetPackSkillDirs(nil)
	jailcontent.SetPackSkillTargets(nil)
	t.Cleanup(func() { jailcontent.SetPackSkillDirs(nil); jailcontent.SetPackSkillTargets(nil) })

	store := paths.GlobalHome()
	for rel, body := range map[string]string{
		".copilot/config.json":                 `{"legacy": true}`,
		".claude/projects/-legacy/t.jsonl":     "transcript",
		".claude/settings.local.json":          `{"legacy": true}`,
		".codex/auth.json":                     `{"legacy": true}`,
		".yolo-perf.log":                       "",
		".foo/.keep":                           "",
		".claude-shared-credentials/.x":        "shared",
		".claude/claude.json":                  `{"hasCompletedOnboarding": true}`,
		"stale-leftover-from-another-ws/x.txt": "bytes",
	} {
		p := filepath.Join(store, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(".claude/claude.json", filepath.Join(store, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	allowed := func(rel string) bool {
		rel = filepath.ToSlash(rel)
		for _, d := range packload.EmbeddedSharedDirs() {
			if rel == d || strings.HasPrefix(rel, d+"/") {
				return true // the machine-scope shared dirs
			}
		}
		return rel == ".claude/claude.json" || // the login seed
			rel == ".claude/.credentials.json" // the credential migration's source
	}
	before := snapshotTree(t, store, allowed)

	ws := t.TempDir()
	o := goldenOptions(ws, home)
	o.Stdout, o.Stderr = discardBuf(), discardBuf()
	const cname = "yolo-fresh-store"
	cfg := newConfig("writable_home_dirs", []any{".pi-lens"})
	hostFiles := []config.HostFileEntry{
		{Path: ".npmrc", Source: "/host/.npmrc", Codec: "raw", Mode: config.HostFileModeReadonly},
		{Path: "hf/one.json", Codec: "json", HasContent: true, Mode: config.HostFileModeOnce},
	}

	// runContainer's host-side writers, in its order.
	if err := o.ensureStorage(); err != nil {
		t.Fatalf("ensureStorage: %v", err)
	}
	staged, ok := o.stageRunPacks(cname)
	if !ok {
		t.Fatal("stageRunPacks failed")
	}
	agentsPath, err := o.refreshJailBriefings(cname, cfg, "podman", staged)
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	wsState := o.prepareWsState(cfg, staged.packs, "podman")
	// A logged-in workspace, so the seed's reverse pass has something to write.
	if err := os.WriteFile(filepath.Join(wsState, "claude", "claude.json"),
		[]byte(`{"oauthAccount": {"emailAddress": "someone@example.invalid"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = o.prepareWsState(cfg, staged.packs, "podman")
	prepareHostFiles(wsState, hostFiles, staged.packs, config.WritableHomeDirs(cfg, staged.packs))
	if err := ensureSharedDirSources(staged.packs); err != nil {
		t.Fatalf("ensureSharedDirSources: %v", err)
	}
	skeleton := buildSkeletonForTest(t, cname, staged.packs, cfg, hostFiles)
	_ = o.assembleRunCmd(&assembleInput{
		cfg: cfg, rt: "podman", cname: cname, imageRef: goldenImageRef, jailPrefix: goldenJailPrefix,
		packs: staged.packs, agentsPath: agentsPath, packStaging: staged.root, homeSkeleton: skeleton,
		wsState: wsState, miseStore: "/mise-store", yoloVersion: "9.9.9-test",
		mountTargets: map[string]struct{}{}, writableHomeDirs: config.WritableHomeDirs(cfg, staged.packs),
		hostFiles: hostFiles,
	})

	if d := diffSnapshots(before, snapshotTree(t, store, allowed)); len(d) != 0 {
		t.Errorf("a fresh launch wrote into the machine store (<state>/home) outside the shared "+
			"dirs, the seed and the credential migration:\n  %s", strings.Join(d, "\n  "))
	}
	// And the seed did learn the login — the one deliberate write, still happening.
	seed, _ := os.ReadFile(filepath.Join(store, ".claude", "claude.json"))
	if !strings.Contains(string(seed), "oauthAccount") {
		t.Errorf("the logged-in workspace's login did not back-propagate into the seed: %s", seed)
	}
}
