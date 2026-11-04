package run

// macosusertreeslot_test.go pins THE TREE HALF OF THE BUILD SLOT ON macos-user
// (docs/reference/pi-extensions.md §11, docs/reference/patched-forks.md PF-D97), from Run's own
// entry point, as macosuserforkslot_test.go pins the fork half: a fresh macos-user launch hands its
// patched extensions to the build act for the sandbox's darwin platform with a copy root beside its
// pack tree, copies each delivered tree into the home overlay at `~/<into>`, hands the sandbox
// YOLO_PATCHED_TREES, honors YOLO_SKIP_EXTENSIONS, and refuses as a container launch does when a
// needed extension has no build or its newer build failed. A dry run builds nothing.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/patchrecovery"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// macosUserTreeRun is one macos-user launch through Run with what its backend was handed.
type macosUserTreeRun struct {
	rc      int
	out     string
	env     *jsonx.OrderedMap
	overlay macosuser.HomeOverlay
	// overlayHad is whether the overlay's tree held the delivered file when the backend ran.
	overlayHad bool
	ran        bool
	slot       []BuildSlotRequest
}

// treeCopyFile is the file a fake tree act writes into each per-launch copy.
const treeCopyFile = "index.js"

// deliverTreeCopyWithFile is a slot whose tree arm delivers treeKey's per-launch copy, holding
// treeCopyFile, under the request's copy root.
func deliverTreeCopyWithFile(t *testing.T) func(BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
	return func(req BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
		if req.Trees == nil {
			return nil, nil
		}
		dir := PatchedTreeCopyDir(req.Trees.CopyRoot, treeKey)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, treeCopyFile), []byte("export {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return nil, map[string]TreeDelivery{treeKey: {Dir: dir, Entry: "e1", Commit: strings.Repeat("a", 40),
			Tag: "v1.0.0", Patches: 1}}
	}
}

func runMacosUserTreeSlot(t *testing.T, mutate func(*Options)) macosUserTreeRun {
	t.Helper()
	var r macosUserTreeRun
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "macos-user", &stdout, &stderr, nil)
	o.MacosUserLaunchProbes = setUpMacProbes
	store := t.TempDir()
	o.CapturesDir = func() string { return store }
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, overlay macosuser.HomeOverlay,
		_ macosuser.HostContext, _ bool, launchEnv *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		r.env, r.overlay, r.ran = launchEnv, overlay, true
		if overlay.Tree != "" {
			_, err := os.Stat(filepath.Join(overlay.Tree, filepath.FromSlash(treeInto), treeCopyFile))
			r.overlayHad = err == nil
		}
		return 0
	}
	o.BuildSlot = deliverTreeCopyWithFile(t)
	mutate(o)
	inner := o.BuildSlot
	o.BuildSlot = func(req BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
		r.slot = append(r.slot, req)
		return inner(req)
	}
	r.rc = Run(*o)
	r.out = stdout.String() + stderr.String()
	return r
}

// treesOnMacosUser decodes the patched-extension decisions a macos-user launch handed its sandbox.
func treesOnMacosUser(t *testing.T, env *jsonx.OrderedMap) map[string]entrypoint.TreeDelivery {
	t.Helper()
	if env == nil {
		t.Fatal("the backend was handed no launch environment")
	}
	raw, ok := env.Get(entrypoint.PatchedTreesEnv)
	if !ok {
		t.Fatalf("the macos-user launch handed its sandbox no %s", entrypoint.PatchedTreesEnv)
	}
	var d map[string]entrypoint.TreeDelivery
	if err := json.Unmarshal([]byte(raw.(string)), &d); err != nil {
		t.Fatalf("%s = %q: %v", entrypoint.PatchedTreesEnv, raw, err)
	}
	return d
}

// THE TREE HALF RUNS ON macos-user, for darwin: the extension goes to BuildSlot with the macos-user
// runtime, the sandbox's platform and a copy root beside the pack tree; the copy lands in the home
// overlay at `~/<into>`, a destination the profile write-protects; and the sandbox is handed its
// build. Red with the slot's tree half skipped on macos-user, the plan's platform back to the
// container's, the overlay built without the launch's tree dirs, or the wire left unset.
func TestAMacosUserLaunchBuildsAPatchedExtensionForDarwinAndOverlaysIt(t *testing.T) {
	treeLaunchHome(t, true)
	r := runMacosUserTreeSlot(t, func(*Options) {})
	if r.rc != 0 || !r.ran {
		t.Fatalf("Run() = %d (backend ran %v):\n%s", r.rc, r.ran, r.out)
	}
	var req *TreeBuildRequest
	for _, s := range r.slot {
		if s.Trees != nil {
			req = s.Trees
		}
	}
	if req == nil {
		t.Fatalf("the macos-user launch asked the slot for no tree build (%+v):\n%s", r.slot, r.out)
	}
	if req.Runtime != "macos-user" || req.Platform != macosUserJailPlatform() {
		t.Errorf("the tree request is for runtime %q platform %q, want macos-user and %s", req.Runtime, req.Platform,
			macosUserJailPlatform())
	}
	// THE STABLE ROOT, reused (TreeBuildRequest.ReuseCopies): an unchanged relaunch is handed the
	// copy the last one made instead of copying the tree out of the store again. Red with the
	// per-launch root a container takes.
	if want := paths.TreeCopyRoot(yoloruntime.FromWorkspace(req.Workspace)); req.CopyRoot != want || !req.ReuseCopies {
		t.Errorf("the tree request's copy root is %q (reused %v), want the workspace's stable %q, reused",
			req.CopyRoot, req.ReuseCopies, want)
	}
	if len(req.Trees) != 1 || req.Trees[0].Key() != treeKey {
		t.Errorf("the tree request carries %+v, want %s", req.Trees, treeKey)
	}
	if !r.overlayHad {
		t.Errorf("the home overlay %q has no copy of the extension at %s", r.overlay.Tree, treeInto)
	}
	if !slices.Contains(r.overlay.Dests, treeInto) {
		t.Errorf("the overlay's destinations %v do not include %s, so the profile does not protect it", r.overlay.Dests, treeInto)
	}
	d := treesOnMacosUser(t, r.env)[treeKey]
	if d.Build != "e1" || !strings.Contains(d.Label, "v1.0.0") || d.Stop || d.Skipped {
		t.Errorf("the sandbox is handed %+v for %s, want its build and label", d, treeKey)
	}
	if strings.Contains(r.out, "is not delivered on macos-user") || strings.Contains(r.out, "no effect on macos-user") {
		t.Errorf("a launch that delivered the extension says it does not:\n%s", r.out)
	}
}

// A TREE WITH NO COPY IS NO OVERLAY DESTINATION: an empty directory at `into` is one an agent may
// try to load. With the missing-program hatch the launch starts, and nothing lands at `into`.
func TestAMacosUserLaunchOverlaysNoUndeliveredTree(t *testing.T) {
	treeLaunchHome(t, true)
	r := runMacosUserTreeSlot(t, func(o *Options) {
		withEnv(o, "YOLO_ALLOW_MISSING_PROGRAMS", "1")
		o.BuildSlot = func(BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			return nil, deliverNoTree(TreeBuildRequest{})
		}
	})
	if r.rc != 0 || !r.ran {
		t.Fatalf("Run() = %d (backend ran %v):\n%s", r.rc, r.ran, r.out)
	}
	if slices.Contains(r.overlay.Dests, treeInto) || r.overlayHad {
		t.Errorf("an undelivered tree is an overlay destination: %v", r.overlay.Dests)
	}
	if d := treesOnMacosUser(t, r.env)[treeKey]; d.Build != "" || d.Reason == "" {
		t.Errorf("the sandbox is handed %+v, want the reason there is no build", d)
	}
}

// YOLO_SKIP_EXTENSIONS TAKES EFFECT on macos-user (pi-extensions.md §9.1): the skipped extension is
// not handed to the act, the wire marks it skipped so the sandbox's boot drops its list entries,
// nothing lands in the overlay, and no line says the hatch has no effect.
func TestAMacosUserLaunchSkipsAnExtension(t *testing.T) {
	treeLaunchHome(t, true)
	r := runMacosUserTreeSlot(t, func(o *Options) { withEnv(o, patchrecovery.SkipExtensionsEnv, treeKey) })
	if r.rc != 0 || !r.ran {
		t.Fatalf("Run() = %d (backend ran %v):\n%s", r.rc, r.ran, r.out)
	}
	for _, s := range r.slot {
		if s.Trees != nil {
			t.Errorf("a skipped extension was handed to the act: %+v", s.Trees)
		}
	}
	if d := treesOnMacosUser(t, r.env)[treeKey]; !d.Skipped || d.Stop || d.Build != "" {
		t.Errorf("the sandbox is handed %+v, want the extension marked skipped", d)
	}
	if slices.Contains(r.overlay.Dests, treeInto) {
		t.Errorf("a skipped extension is an overlay destination: %v", r.overlay.Dests)
	}
	if strings.Contains(r.out, "no effect") {
		t.Errorf("the macos-user launch says the hatch it acted on has no effect:\n%s", r.out)
	}
	if !strings.Contains(r.out, "extension "+treeKey+": skipped (YOLO_SKIP_EXTENSIONS)") {
		t.Errorf("the launch does not say it skipped the extension:\n%s", r.out)
	}
}

// A NEEDED EXTENSION WITH NO BUILD REFUSES the macos-user launch as a podman launch does (PPX-D40),
// naming the capture, the container backends and YOLO_SKIP_EXTENSIONS.
func TestAMacosUserLaunchRefusesAPatchedExtensionWithNoBuild(t *testing.T) {
	treeLaunchHome(t, true)
	r := runMacosUserTreeSlot(t, func(o *Options) {
		o.BuildSlot = func(BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			return nil, deliverNoTree(TreeBuildRequest{})
		}
	})
	if r.rc == 0 || r.ran {
		t.Fatalf("Run() = %d, backend ran %v: a needed extension with no build did not refuse:\n%s", r.rc, r.ran, r.out)
	}
	for _, w := range []string{"ERROR: 1 patched extension pack agentpack loads has no build on this machine",
		"yolo capture " + treeKey,
		"\nStart anyway:\n",
		"YOLO_SETUP=apple-container-self yolo", "YOLO_SETUP=podman-self yolo",
		patchrecovery.SkipExtensionsEnv + "=" + treeKey + " yolo"} {
		if !strings.Contains(r.out, w) {
			t.Errorf("the refusal lacks %q:\n%s", w, r.out)
		}
	}
}

// A FAILED NEWER BUILD BESIDE A GOOD ONE STOPS the macos-user launch too (OQ-PPX3, PD-NF1: no
// silent fallback), and YOLO_ALLOW_PATCH_FAILURES runs the good build, overlaid.
func TestAMacosUserLaunchRefusesAFailedNewerExtensionBuild(t *testing.T) {
	treeLaunchHome(t, true)
	failing := func(o *Options) {
		good := deliverTreeCopyWithFile(t)
		o.BuildSlot = func(req BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			_, trees := good(req)
			d := trees[treeKey]
			d.PatchFailure = &packsrc.PatchFailure{Owner: treeKey, Kind: packsrc.PatchFailureBuild,
				Target: packsrc.ListEntry{Commit: pfCommit, Tag: "v1.2.0"}, Detail: "the capture jail exited 2",
				Retry: "yolo capture " + treeKey}
			trees[treeKey] = d
			return nil, trees
		}
	}
	r := runMacosUserTreeSlot(t, failing)
	if r.rc == 0 || r.ran {
		t.Fatalf("Run() = %d, backend ran %v: a failed newer build ran the older one silently:\n%s", r.rc, r.ran, r.out)
	}
	if !strings.Contains(r.out, "the build failed at upstream v1.2.0") {
		t.Errorf("the refusal does not name the failed build:\n%s", r.out)
	}
	r = runMacosUserTreeSlot(t, func(o *Options) { failing(o); withEnv(o, patchrecovery.AllowPatchFailuresEnv, "1") })
	if r.rc != 0 || !r.ran || !r.overlayHad {
		t.Fatalf("with %s=1: Run() = %d, backend ran %v, overlaid %v:\n%s", patchrecovery.AllowPatchFailuresEnv,
			r.rc, r.ran, r.overlayHad, r.out)
	}
}

// A DRY RUN BUILDS NOTHING: no tree request, and its line says a launch without --dry-run builds it.
func TestAMacosUserDryRunBuildsNoExtension(t *testing.T) {
	treeLaunchHome(t, true)
	r := runMacosUserTreeSlot(t, func(o *Options) { o.DryRun = true })
	for _, s := range r.slot {
		if s.Trees != nil {
			t.Fatalf("a dry run asked for a tree build: %+v", s.Trees)
		}
	}
	if !strings.Contains(r.out, "a dry run builds no extension; a launch without --dry-run builds extension "+
		treeKey+" for macOS") {
		t.Errorf("the dry run does not say what the tree arm would do:\n%s", r.out)
	}
}

// YOLO_USE_CACHED_GOOD NAMING AN EXTENSION on macos-user reaches the darwin tree request, where it
// used to match nothing there. Red with selectCachedGood passing over the trees on macos-user.
func TestAMacosUserLaunchServesTheCachedGoodExtension(t *testing.T) {
	treeLaunchHome(t, true)
	r := runMacosUserTreeSlot(t, func(o *Options) { withEnv(o, CachedGoodEnv, treeKey) })
	if r.rc != 0 || !r.ran {
		t.Fatalf("Run() = %d, backend ran %v:\n%s", r.rc, r.ran, r.out)
	}
	var req *TreeBuildRequest
	for _, s := range r.slot {
		if s.Trees != nil {
			req = s.Trees
		}
	}
	if req == nil || !slices.Contains(req.CachedGood, treeKey) || req.Platform != macosUserJailPlatform() {
		t.Errorf("the tree request is %+v, want %s's cached good build at darwin:\n%s", req, treeKey, r.out)
	}
}

// A RECOVERY HATCH NAMING A PATCHED EXTENSION REACHES IT ON macos-user (pi-extensions.md PPX-D45): the
// launch matches the hatch against its extensions as well as its forks, so it neither warns that the
// name matches no patched build nor says the hatch does nothing for an extension there. Red with
// noteRecoveryHatches matching a macos-user launch's hatches against its forks alone.
func TestAMacosUserLaunchMatchesAHatchAgainstItsExtensions(t *testing.T) {
	treeLaunchHome(t, true)
	r := runMacosUserTreeSlot(t, func(o *Options) {
		o.BuildSlot = deliverTreeCopyWithFile(t)
		withEnv(o, patchrecovery.AllowOlderUpstreamEnv, treeKey)
	})
	if r.rc != 0 || !r.ran {
		t.Fatalf("Run() = %d (backend ran %v):\n%s", r.rc, r.ran, r.out)
	}
	for _, bad := range []string{"matches no patched build", "has no effect on a patched extension"} {
		if strings.Contains(r.out, bad) {
			t.Errorf("the macos-user launch says %q of a hatch naming its extension:\n%s", bad, r.out)
		}
	}
}

// A DELIVERED TREE'S LINKS CROSS AS LINKS on macos-user (pi-extensions.md §7.1, PPX-D45), as they do
// through a container's read-only mount: a link to a directory (an npm workspace), a relative file
// link (node_modules/.bin) and an absolute link out of the tree each arrive in the home overlay as the
// same link, and the host process never reads through one — an absolute link to a host file left by
// an upstream must not put that file's bytes into the overlay the sandbox account is handed. Red with
// the overlay copying a tree's files by reading them.
func TestAMacosUserLaunchOverlaysATreesLinksAsLinks(t *testing.T) {
	treeLaunchHome(t, true)
	secret := filepath.Join(t.TempDir(), "id_host")
	if err := os.WriteFile(secret, []byte("HOST-SECRET-BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{"pkgs": "sub", filepath.Join("bin", "x"): "../" + treeCopyFile, "secret": secret}
	var checked bool
	r := runMacosUserTreeSlot(t, func(o *Options) {
		deliver := deliverTreeCopyWithFile(t)
		o.BuildSlot = func(req BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			forks, trees := deliver(req)
			if d, ok := trees[treeKey]; ok {
				for _, dir := range []string{"sub", "bin"} {
					if err := os.MkdirAll(filepath.Join(d.Dir, dir), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				for rel, target := range links {
					if err := os.Symlink(target, filepath.Join(d.Dir, rel)); err != nil {
						t.Fatal(err)
					}
				}
			}
			return forks, trees
		}
		backend := o.MacosUserRun
		o.MacosUserRun = func(cfg *jsonx.OrderedMap, a string, b, c []string, d, e string, overlay macosuser.HomeOverlay,
			hc macosuser.HostContext, dry bool, env *jsonx.OrderedMap, bt []packload.BlockedTool, jd macosuser.JailDaemons) int {
			checked = true
			root := filepath.Join(overlay.Tree, filepath.FromSlash(treeInto))
			for rel, target := range links {
				got, err := os.Readlink(filepath.Join(root, rel))
				if err != nil || got != target {
					t.Errorf("~/%s/%s reached the overlay as %q (%v), want the link to %q", treeInto, rel, got, err, target)
				}
			}
			_ = filepath.WalkDir(overlay.Tree, func(p string, de os.DirEntry, err error) error {
				if err == nil && de.Type().IsRegular() {
					if b, _ := os.ReadFile(p); bytes.Contains(b, []byte("HOST-SECRET-BYTES")) {
						t.Errorf("the host file an absolute link named was copied into the overlay at %s", p)
					}
				}
				return nil
			})
			return backend(cfg, a, b, c, d, e, overlay, hc, dry, env, bt, jd)
		}
	})
	if r.rc != 0 || !checked {
		t.Fatalf("Run() = %d (backend ran %v):\n%s", r.rc, checked, r.out)
	}
}
