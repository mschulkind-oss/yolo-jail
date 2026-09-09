package run

import (
	"bytes"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// stageBundle writes a flake bundle at root: flake.nix plus, when withBinaries,
// the prebuilt bin/linux-<arch>/ every SHIPPED bundle carries. It is the exact
// shape scripts/stage-source-bundle.sh produces, which is what makes the
// prebuilt branch below a test of the real layout rather than of a stub.
func stageBundle(t *testing.T, withBinaries bool) string {
	t.Helper()
	// RESOLVED, because resolveJailPrefix resolves symlinks (resolveSymlinks in
	// jailprefix.go) and the caller compares its output to this path. On macOS
	// t.TempDir() hands back /var/folders/..., which IS a symlink to
	// /private/var/folders/..., so the unresolved form fails every comparison here
	// while passing on Linux — a darwin-only break the in-jail gate cannot see.
	root := resolveSymlinks(t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if withBinaries {
		binDir := prebuiltBinDir(root)
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(binDir, "yolo-entrypoint"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// prefixOptions is an Options whose only live seam is the prefix build, so a
// test can tell "took the prebuilt branch" from "ran a build" by whether the
// stub fired.
func prefixOptions(t *testing.T, build func(string) (string, []string)) (*Options, *int) {
	t.Helper()
	calls := 0
	o := &Options{
		Stdout: os.Stderr,
		Stderr: os.Stderr,
		BuildJailPrefix: func(root string) (string, []string) {
			calls++
			return build(root)
		},
	}
	// fillDefaults supplies PathExists (the prebuilt probe) and leaves an
	// already-set BuildJailPrefix alone — the seam under test stays ours.
	fillDefaults(o)
	return o, &calls
}

// A flake source that SHIPS prebuilt binaries — every installed bundle, and the
// mounted prefix a nested jail inherits — is mounted straight through. This is
// the branch that must never build: it is the default on every real user's
// machine, and a `nix build` there would put a Go toolchain on the launch path
// of a checkout-less install.
func TestPrebuiltBundleIsMountedWithoutBuilding(t *testing.T) {
	root := stageBundle(t, true)
	o, calls := prefixOptions(t, func(string) (string, []string) {
		t.Fatal("a bundle with prebuilt binaries must not trigger a build")
		return "", nil
	})

	p, ok := o.resolveJailPrefix(root)
	if !ok {
		t.Fatal("resolveJailPrefix refused a bundle that ships binaries")
	}
	if *calls != 0 {
		t.Errorf("the prefix build ran %d times for a prebuilt bundle", *calls)
	}
	if p.binDir != prebuiltBinDir(root) {
		t.Errorf("binDir = %q, want the bundle's own %q", p.binDir, prebuiltBinDir(root))
	}
	if p.shareDir != root {
		t.Errorf("shareDir = %q, want the flake source %q", p.shareDir, root)
	}
	if p.built {
		t.Error("built=true for a prefix nothing built")
	}
}

// A LIVE CHECKOUT ships no bin/linux-<arch> (the repo never commits one, and nix
// only ever sees tracked files anyway), so the binaries have to be built. This
// is the developer loop — `YOLO_REPO_ROOT=/workspace yolo` — and the one path
// where the image no longer carrying the Go build means someone else must.
func TestLiveCheckoutBuildsThePrefix(t *testing.T) {
	root := stageBundle(t, false)
	store := t.TempDir()
	o, calls := prefixOptions(t, func(got string) (string, []string) {
		if got != root {
			t.Errorf("built the prefix from %q, want the resolved flake source %q", got, root)
		}
		return store, nil
	})

	p, ok := o.resolveJailPrefix(root)
	if !ok {
		t.Fatal("resolveJailPrefix refused a checkout whose build succeeded")
	}
	if *calls != 1 {
		t.Errorf("the prefix build ran %d times, want exactly 1", *calls)
	}
	wantBin := filepath.Join(store, image.JailPrefixSubdir, "bin")
	if p.binDir != wantBin {
		t.Errorf("binDir = %q, want %q — the bin/ inside the built prefix", p.binDir, wantBin)
	}
	// THE SHARE HALF IS THE BUILT BUNDLE, NOT THE CHECKOUT. Mounting the checkout
	// would resolve too (it has a flake.nix), which is why this is asserted rather
	// than left to the reader: it would put the whole working tree inside the jail
	// at a second path, and hand the in-jail yolo a flake whose Go code can already
	// be newer than the binaries beside it.
	wantShare := filepath.Join(store, image.JailPrefixSubdir, "share", "yolo-jail")
	if p.shareDir != wantShare {
		t.Errorf("shareDir = %q, want the built bundle %q (never the checkout %q)",
			p.shareDir, wantShare, root)
	}
	if !p.built {
		t.Error("built=false for a prefix that was built — the launch would report the wrong provenance")
	}
}

// A HALF-STAGED bundle — the directory exists but yolo-entrypoint does not — must
// take the build branch, not mount an empty dir. Mounting it would produce a
// container that starts and dies at exec, which is the failure mode the whole
// "name the entrypoint absolutely" decision exists to make legible; catching it
// here is better still.
func TestBinDirWithoutTheEntrypointIsNotUsed(t *testing.T) {
	root := stageBundle(t, false)
	if err := os.MkdirAll(prebuiltBinDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prebuiltBinDir(root), "yolo"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := t.TempDir()
	o, calls := prefixOptions(t, func(string) (string, []string) { return store, nil })

	if _, ok := o.resolveJailPrefix(root); !ok {
		t.Fatal("resolveJailPrefix refused")
	}
	if *calls != 1 {
		t.Errorf("a bin dir with no yolo-entrypoint was mounted as-is (build ran %d times)", *calls)
	}
}

// A FAILED prefix build REFUSES the launch. There is nothing to fall back on:
// the image no longer carries a yolo-entrypoint, so "proceed anyway" means a
// container with no pid1. This is the same ruling OQ-2 made for the image build
// (a build that ran and failed is fatal), applied to the half that moved out.
func TestFailedPrefixBuildRefusesTheLaunch(t *testing.T) {
	root := stageBundle(t, false)
	var stderr strings.Builder
	o, _ := prefixOptions(t, func(string) (string, []string) {
		return "", []string{"error: builder for '/nix/store/…-yolo-jail-go.drv' failed"}
	})
	o.Stderr = &stderr

	if _, ok := o.resolveJailPrefix(root); ok {
		t.Fatal("a failed prefix build let the launch continue — the jail would have no yolo-entrypoint")
	}
	if !strings.Contains(stderr.String(), "installPrefix") {
		t.Errorf("the refusal does not name what failed:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "yolo-jail-go.drv") {
		t.Errorf("nix's own stderr was dropped, so the reader has no cause:\n%s", stderr.String())
	}
}

// THE LAYOUT CONTRACT. The in-jail `yolo` finds its flake bundle exe-relative
// (reporoot.BundledSourceDirFrom: <exeDir>/../share/yolo-jail), and the two
// mount destinations are what make that resolve. They are separate constants
// spelled separately, so this asserts the relationship between them rather than
// re-typing the strings — rename either and the jail silently loses its ability
// to build an image from inside itself, which no container test would notice
// because nothing fails until someone runs `yolo` in there.
func TestMountDestinationsSatisfyTheExeRelativeResolver(t *testing.T) {
	resolved := filepath.Clean(filepath.Join(JailPrefixBinDir, "..", "share", "yolo-jail"))
	if resolved != JailPrefixShareDir {
		t.Errorf("<JailPrefixBinDir>/../share/yolo-jail = %q, want JailPrefixShareDir %q — "+
			"reporoot.BundledSourceDirFrom would not find the bundle", resolved, JailPrefixShareDir)
	}
	if filepath.Dir(JailEntrypointPath) != JailPrefixBinDir {
		t.Errorf("the container argv names %q, which is not inside the mounted bin dir %q",
			JailEntrypointPath, JailPrefixBinDir)
	}
	// And the resolver really does accept the layout, run against a real tree
	// laid out at those relative offsets (the constants are absolute in-jail
	// paths, so the test builds the same SHAPE under a temp root).
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	share := filepath.Join(root, "share", "yolo-jail")
	for _, d := range []string{binDir, share} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(share, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := reporoot.BundledSourceDirFrom(binDir); !ok || got != share {
		t.Errorf("BundledSourceDirFrom(%q) = (%q, %v), want (%q, true)", binDir, got, ok, share)
	}
}

// flakeSource reads flake.nix from the checkout this test file was compiled
// from. It FAILS rather than skips: the agreement it checks is between two
// languages, so a skip turns a silent cross-language drift into silent
// non-coverage — the same shape of bug one level up.
func flakeSource(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller gave no source path — cannot locate the repo root")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	body, err := os.ReadFile(filepath.Join(root, "flake.nix"))
	if err != nil {
		t.Fatalf("read flake.nix at the resolved repo root %s: %v", root, err)
	}
	return string(body)
}

// THE CROSS-LANGUAGE PIN, and the reason this test exists at all: the install
// prefix is now split across two files that cannot see each other. flake.nix
// bakes /bin/<name> symlinks pointing at a hardcoded directory and pre-creates
// the mountpoints; this package mounts the host content there and names the
// entrypoint by absolute path. Nothing but agreement on that ONE string makes
// the jail bootable, and every way of breaking it is silent at build time:
//
//   - a flake that links /bin/<name> somewhere else leaves the jail's `yolo`
//     dangling while pid1 (named absolutely) still works — so the jail boots and
//     the CLI inside it is simply missing;
//   - a flake that pre-creates the wrong mountpoints leaves the runtime to
//     invent them, which podman does and Apple Container is not verified to;
//   - a Go constant that moves leaves both.
//
// This is the guarantee that replaced "shippedBinaries decides what the image
// contains" (internal/entrypoint/shippedclients_test.go still pins the NAME set
// across the flake and the bundle script; what moved is that the names now
// describe a mount, not image content).
func TestFlakeAndLauncherAgreeOnThePrefixLayout(t *testing.T) {
	flake := flakeSource(t)

	if !strings.Contains(flake, `jailPrefixDir = "`+JailPrefixDir+`";`) {
		t.Errorf("flake.nix does not declare jailPrefixDir = %q. The /bin/<name> symlinks it "+
			"bakes would point somewhere this launcher never mounts, so the jail's own "+
			"`yolo` would be a dangling link", JailPrefixDir)
	}
	// The mountpoints, both levels, on a --read-only rootfs.
	//
	// `$out/` and not `./`: the mkdirs moved from `streamLayeredImage`'s
	// fakeRootCommands (which ran in the tar's own cwd) into the top tier's
	// symlinkJoin postBuild when C9 replaced the generator
	// (docs/design/layer-aware-image-delivery.md). Same directories, same
	// consequence for pid1, one prefix.
	for _, dest := range []string{JailPrefixBinDir, JailPrefixShareDir} {
		if !strings.Contains(flake, "$out"+dest) {
			t.Errorf("flake.nix's image root tree does not pre-create %q; a --read-only "+
				"rootfs cannot grow a mountpoint, and this is the mount whose absence "+
				"means no pid1", dest)
		}
	}
	// And installPrefix must lay the content down at the offset image.JailPrefixSubdir
	// names, or a built prefix mounts an empty directory.
	if !strings.Contains(flake, "$out/"+image.JailPrefixSubdir+"/bin") {
		t.Errorf("flake.nix's installPrefix no longer writes $out/%s/bin, which is where "+
			"image.JailPrefixSubdir says the built prefix is", image.JailPrefixSubdir)
	}
	// The image must NOT bake the prefix any more — that is the whole change, and
	// re-adding installPrefix to the image silently restores goSrc as an image
	// input (every Go commit reminting the image) while every test here stays green.
	if strings.Contains(flake, "corePackages = [ installPrefix ]") {
		t.Error("installPrefix is back in the image's corePackages: goSrc is an image input " +
			"again, so every cmd/ or internal/ commit costs an image rebuild and a load")
	}
}

// The two mounts, in the order and the spelling the argv carries.
func TestJailPrefixMountArgs(t *testing.T) {
	got := jailPrefixMountArgs(jailPrefix{binDir: "/host/bin", shareDir: "/host/share"})
	want := []string{
		"-v", "/host/bin:/opt/yolo-jail/bin:ro",
		"-v", "/host/share:/opt/yolo-jail/share/yolo-jail:ro",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// prebuiltBinDir must name the LINUX directory for this machine's arch on every
// host — the jail is Linux whatever the launcher runs on, and a darwin host's
// bundle carries Linux binaries for exactly that reason. A GOOS-derived spelling
// here would make every macOS launch miss its own bundle and compile.
func TestPrebuiltBinDirIsAlwaysTheLinuxArchDir(t *testing.T) {
	got := prebuiltBinDir("/root")
	want := filepath.Join("/root", "bin", "linux-"+goruntime.GOARCH)
	if got != want {
		t.Errorf("prebuiltBinDir = %q, want %q", got, want)
	}
	if strings.Contains(got, goruntime.GOOS) && goruntime.GOOS != "linux" {
		t.Errorf("prebuiltBinDir names the HOST os (%q); the jail is always linux", got)
	}
}

// TestRunNormalResolvesAndThreadsTheJailPrefix is the CALL-SITE pin, in the same
// SOURCE-assertion shape as TestRunNormalThreadsTheLoadedRefIntoAssembly and for
// the same reason: the wiring lives inside runNormal, which needs a real
// container to reach.
//
// Delete either line and the whole unit suite stays green while every launch
// mounts nothing at /opt/yolo-jail — the argv still names
// /opt/yolo-jail/bin/yolo-entrypoint, the image still has the empty mountpoint,
// and the container dies at exec with no yolo code having run. The integration
// suite would catch it (nothing boots), but only after a full image build.
func TestRunNormalResolvesAndThreadsTheJailPrefix(t *testing.T) {
	src := runSource(t)
	if !strings.Contains(src, "jailPrefix, prefixOK := o.resolveJailPrefix(repoRoot)") {
		t.Error("runNormal no longer resolves the jail prefix. The install prefix is " +
			"MOUNTED, not baked, so nothing else produces the binaries the container argv names")
	}
	if !strings.Contains(src, "jailPrefix:       jailPrefix,") {
		t.Error("runNormal no longer threads the resolved prefix into assembleInput, so the " +
			"argv would carry an empty mount source and the jail would have no pid1")
	}
}

// The prefix must be resolved BEFORE the image is built and loaded. Both are
// nix builds; the prefix one is small and the image one streams gigabytes, so
// ordering the cheap failure first is what keeps a broken checkout from costing
// a full image build before it refuses.
func TestPrefixIsResolvedBeforeTheImageLoad(t *testing.T) {
	src := runSource(t)
	// Matched on the call NAME, not on a full argument list. Pinning the argv
	// spelling makes this fail for reasons that have nothing to do with the
	// order it exists to protect: C5 added a storePackagesPlan argument to
	// autoLoadImage and broke this pin at merge time while the ordering it
	// asserts was never in question.
	prefixAt := strings.Index(src, "o.resolveJailPrefix(")
	imageAt := strings.Index(src, "o.autoLoadImage(")
	if prefixAt < 0 || imageAt < 0 {
		t.Fatalf("could not locate both call sites (prefix=%d image=%d)", prefixAt, imageAt)
	}
	if prefixAt > imageAt {
		t.Error("the prefix is resolved AFTER the image load: a checkout that cannot build " +
			"its own binaries would pay for a whole image build before the launch refuses")
	}
}

// The Apple Container arm emits the same two mounts. It has its own base-mount
// function (the single-writable-/home/agent shape), which is exactly how a mount
// added to the podman arm alone goes missing on one backend — and this is the
// mount whose absence is not a degradation but a dead container.
func TestAppleContainerAlsoMountsTheJailPrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})

	argv := o.assembleRunCmd(&assembleInput{
		cfg:          newConfig("agents", []any{"claude"}, "security", sec),
		rt:           "container",
		cname:        "yolo-ws-abcd1234",
		imageRef:     goldenImageRef,
		jailPrefix:   goldenJailPrefix,
		packs:        claudePackFixture(t),
		agentsPath:   filepath.Join(home, "agents"),
		wsState:      filepath.Join(home, "wsstate"),
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})

	joined := strings.Join(argv, " ")
	for _, want := range []string{
		goldenJailPrefix.binDir + ":" + JailPrefixBinDir + ":ro",
		goldenJailPrefix.shareDir + ":" + JailPrefixShareDir + ":ro",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the Apple Container argv is missing %q:\n%s", want, joined)
		}
	}
	if argv[len(argv)-1] != JailEntrypointPath {
		t.Errorf("the Apple Container argv ends with %q, want %q", argv[len(argv)-1], JailEntrypointPath)
	}
}

// TestPrefixBuildNoticeGoesToStderr pins the STREAM of the one notice the prefix
// step prints, for the reason TestAssembleNoticesGoToStderr states in full:
// stdout belongs to the jailed command, so a launch notice printed there is
// swallowed by the user's redirect or corrupts what they pipe.
//
// THIS IS THE SECOND TIME THIS EXACT OUTAGE SHIPPED, through a different door.
// 6580186c moved assembleRunCmd's notices to stderr on 2026-09-06, after the
// host-loopback note had spent two days prepending itself to every integration
// test that asserts a jailed command's exact output. C8 then reintroduced it:
// image.BuildJailPrefix printed "Building yolo's own binaries…" to whatever
// writer the run path handed it, and the run path handed it o.Stdout — so
// TestHostComposedBriefingIsNotDeliveredTwice read its count "1" back as
// "Building yolo's own binaries… 1", and TestProvidersRenderInTheAgentsOwn-
// Vocabulary declared a byte-correct provider env wrong, on both architectures
// (CI run 34079261711 — the same two tests as the first time).
//
// A unit test is the only cheap proof. The leak needs a flake source with no
// prebuilt binaries — a live checkout, which is every CI runner and no user with
// an installed bundle — and it only becomes visible in tests that assert a
// jailed command's stdout, i.e. after a full image build.
//
// All three halves matter. Delete the print and the stderr half fails; move it
// back to o.Stdout and the stdout half fails; hoist it above the prebuilt probe
// and the silence half fails.
func TestPrefixBuildNoticeGoesToStderr(t *testing.T) {
	// A LIVE CHECKOUT ships no bin/linux-<arch>, so this is the branch that builds.
	root := stageBundle(t, false)
	store := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := &Options{
		Stdout:          &stdout,
		Stderr:          &stderr,
		BuildJailPrefix: func(string) (string, []string) { return store, nil },
	}
	fillDefaults(o)

	if _, ok := o.resolveJailPrefix(root); !ok {
		t.Fatal("resolveJailPrefix refused a checkout whose build succeeded")
	}
	if stdout.Len() != 0 {
		t.Errorf("the prefix build notice reached STDOUT, which belongs to the jailed "+
			"command — every integration test that asserts exact command output breaks "+
			"on every runner that has to build the prefix:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Building yolo's own binaries") {
		t.Errorf("the build notice was not printed at all. A launch that stops to run "+
			"`nix build .#installPrefix` must say why it paused; stderr was:\n%s",
			stderr.String())
	}

	// AND THE PREBUILT BRANCH SAYS NOTHING, on either stream. That is the default
	// on every installed machine and it builds nothing, so announcing a build here
	// would be false — and it is exactly what hoisting the print to the top of
	// resolveJailPrefix would do.
	var quietOut, quietErr bytes.Buffer
	q := &Options{
		Stdout: &quietOut,
		Stderr: &quietErr,
		BuildJailPrefix: func(string) (string, []string) {
			t.Fatal("a bundle with prebuilt binaries must not trigger a build")
			return "", nil
		},
	}
	fillDefaults(q)
	if _, ok := q.resolveJailPrefix(stageBundle(t, true)); !ok {
		t.Fatal("resolveJailPrefix refused a prebuilt bundle")
	}
	if quietOut.Len() != 0 || quietErr.Len() != 0 {
		t.Errorf("mounting prebuilt binaries announced a build it never ran:\n%s%s",
			quietOut.String(), quietErr.String())
	}
}

// TestPrefixNixProgressGoesToStderr pins the OTHER writer in the same decision:
// image.BuildJailPrefix streams nix's own progress summaries to the writer it is
// given, so handing it o.Stdout leaks build chatter onto the jailed command's
// stream even now that the notice sentence has moved out.
//
// An EXPRESSION assertion, in the shape TestPrefixIsResolvedBeforeTheImageLoad
// and TestRunNormalResolvesAndThreadsTheJailPrefix already use, and for the same
// reason: the wiring is inside fillDefaults' closure, and driving it runs a real
// `nix build`. Weaker than driving it, and the strongest thing available here.
func TestPrefixNixProgressGoesToStderr(t *testing.T) {
	body, err := os.ReadFile("runcmd.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "image.BuildJailPrefix(repoRoot, o.Stderr)") {
		t.Error("fillDefaults no longer hands image.BuildJailPrefix o.Stderr. Nix's " +
			"progress summaries would land on stdout, which belongs to the jailed " +
			"command — the leak CI run 34079261711 was made of")
	}
}

// TestPrefixUnreachableFromVMTable is the darwin gate's decision, arm by arm.
// The store paths are spelled with hostNixStore rather than a literal so the two
// cannot drift apart — the whole predicate is "is this the tree the VM does not
// share".
func TestPrefixUnreachableFromVMTable(t *testing.T) {
	store := jailPrefix{
		binDir:   filepath.Join(hostNixStore, "abc-yolo-jail-install-prefix", "opt", "yolo-jail", "bin"),
		shareDir: filepath.Join(hostNixStore, "abc-yolo-jail-install-prefix", "opt", "yolo-jail", "share", "yolo-jail"),
	}
	home := jailPrefix{
		binDir:   "/Users/me/.local/share/yolo-jail/flake-bundle/bin/linux-arm64",
		shareDir: "/Users/me/.local/share/yolo-jail/flake-bundle",
	}
	cases := []struct {
		name    string
		p       jailPrefix
		isMacOS bool
		optIn   string
		refuse  bool
	}{
		{"linux never refuses — /nix is right there", store, false, "", false},
		{"darwin + a store prefix is the measured outage", store, true, "", true},
		{"darwin + the operator says the VM shares /nix", store, true, "1", false},
		{"darwin + a bundle under $HOME is what the VM does share", home, true, "", false},
		{"darwin + only the SHARE half in the store still refuses",
			jailPrefix{binDir: home.binDir, shareDir: store.shareDir}, true, "", true},
		{"darwin + only the BIN half in the store still refuses",
			jailPrefix{binDir: store.binDir, shareDir: home.shareDir}, true, "", true},
		// A directory whose NAME starts with the store's is not inside it. The
		// segment test exists for this case, so it is asserted rather than trusted.
		{"darwin + a sibling of /nix/store is not in it", jailPrefix{
			binDir:   hostNixStore + "-of-my-own/bin",
			shareDir: hostNixStore + "-of-my-own/share/yolo-jail",
		}, true, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := prefixUnreachableFromVM(tc.p, tc.isMacOS, tc.optIn)
			if tc.refuse != (msg != "") {
				t.Fatalf("refuse=%v, want %v (message: %q)", msg != "", tc.refuse, msg)
			}
			if !tc.refuse {
				return
			}
			// The refusal has to carry both fixes, because a message that only
			// names the cause leaves the reader with a broken Mac and no next
			// step — which is what the raw `statfs` error already did.
			for _, want := range []string{"podman machine init -v /nix:/nix", "YOLO_NIX_HOST_DAEMON=1", "just"} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal does not mention %q:\n%s", want, msg)
				}
			}
		})
	}
}

// TestDarwinRefusesAStorePrefix is the CALL-SITE pin: it fails if the gate stops
// being consulted by resolveJailPrefix, which is the only way the check can be
// switched off wholesale with the table above still green (AGENTS.md names that
// shape). It drives the LIVE-CHECKOUT arm, because building is what puts the
// prefix in the store in the first place.
func TestDarwinRefusesAStorePrefix(t *testing.T) {
	root := stageBundle(t, false)
	store := filepath.Join(hostNixStore, "abc-yolo-jail-install-prefix")
	o, calls := prefixOptions(t, func(string) (string, []string) { return store, nil })
	o.IsMacOS = true
	var buf bytes.Buffer
	o.Stderr = &buf

	if _, ok := o.resolveJailPrefix(root); ok {
		t.Fatal("a darwin launch accepted a /nix/store prefix its VM cannot see — " +
			"podman fails that with `statfs …: no such file or directory` and rc 125, " +
			"which is the 2026-09-07 nightly (run 34117863296) in full")
	}
	if *calls != 1 {
		t.Errorf("the prefix build ran %d times, want 1 — the refusal is about where the "+
			"result LANDED, so it must come after the build, not instead of it", *calls)
	}
	if !strings.Contains(buf.String(), "runtime VM does not share") {
		t.Errorf("refusal did not reach stderr:\n%s", buf.String())
	}
}

// TestDarwinAcceptsAPrefixTheVMCanSee is the other half of the pin: the gate must
// not refuse the arm every Homebrew and `just install` user is on. A bundle under
// $HOME ships prebuilt binaries and never touches the store, and a darwin launch
// that refused it would be a total outage rather than a diagnosis.
func TestDarwinAcceptsAPrefixTheVMCanSee(t *testing.T) {
	root := stageBundle(t, true)
	o, _ := prefixOptions(t, func(string) (string, []string) {
		t.Fatal("a prebuilt bundle must not build")
		return "", nil
	})
	o.IsMacOS = true

	if _, ok := o.resolveJailPrefix(root); !ok {
		t.Fatal("a darwin launch refused a prefix under $HOME (a t.TempDir(), which is " +
			"not in the nix store) — the gate is meant to catch the store, not macOS")
	}
}
