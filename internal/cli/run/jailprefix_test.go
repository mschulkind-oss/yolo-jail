package run

import (
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// stageBundle writes a complete flake bundle at root: flake.nix, flake.lock and, when
// withBinaries, the prebuilt bin/linux-<arch>/ helper set plus the bin/darwin-<arch>/ guest
// set. It is the exact shape scripts/stage-source-bundle.sh produces, which is what makes the
// prebuilt branch below a test of the real layout rather than of a stub.
func stageBundle(t *testing.T, withBinaries bool) string {
	t.Helper()
	// Resolve where the fixture is minted, because macOS temp roots can be symlinks.
	root := resolveSymlinks(t.TempDir())
	stageBundleAt(t, root, withBinaries)
	return root
}

func stageBundleAt(t *testing.T, root string, withBinaries bool) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "flake.lock"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !withBinaries {
		return
	}
	binDir := prebuiltBinDir(root)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range requiredJailBinaries {
		body := []byte("binary")
		if name == "yolo" {
			body = yoloStandInScript()
		}
		if err := os.WriteFile(filepath.Join(binDir, name), body, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	guestBinDir := macosuser.PrebuiltGuestBinDir(root)
	if err := os.MkdirAll(guestBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range macosuser.GuestBinaries {
		if err := os.WriteFile(filepath.Join(guestBinDir, name), []byte("binary"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRequiredJailBinariesMatchPublishedSets(t *testing.T) {
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller gave no source path")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	flake, err := os.ReadFile(filepath.Join(root, "flake.nix"))
	if err != nil {
		t.Fatal(err)
	}
	bundleScript, err := os.ReadFile(filepath.Join(root, "scripts", "stage-source-bundle.sh"))
	if err != nil {
		t.Fatal(err)
	}
	flakeList := regexp.MustCompile(`(?m)^\s*shippedBinaries\s*=\s*\[([^\]]*)\]\s*;`).FindSubmatch(flake)
	bundleList := regexp.MustCompile(`(?m)^SHIPPED_BINARIES=\(([^)]*)\)`).FindSubmatch(bundleScript)
	if flakeList == nil || bundleList == nil {
		t.Fatal("could not locate shipped helper sets in flake.nix and stage-source-bundle.sh")
	}
	set := func(names []string) map[string]bool {
		out := make(map[string]bool, len(names))
		for _, name := range names {
			out[strings.Trim(name, `"`)] = true
		}
		return out
	}
	want := set(requiredJailBinaries)
	for label, names := range map[string][]string{
		"flake.nix":              strings.Fields(string(flakeList[1])),
		"stage-source-bundle.sh": strings.Fields(string(bundleList[1])),
	} {
		got := set(names)
		if len(got) != len(want) {
			t.Errorf("required helper set has %d names, %s has %d", len(want), label, len(got))
		}
		for name := range want {
			if !got[name] {
				t.Errorf("required helper %q is absent from %s", name, label)
			}
		}
		for name := range got {
			if !want[name] {
				t.Errorf("%s helper %q is not checked by the launch resolver", label, name)
			}
		}
	}
}

// prefixOptions supplies deterministic host probes for prefix resolution.
func prefixOptions(t *testing.T) *Options {
	t.Helper()
	o := &Options{
		Stdout: os.Stderr,
		Stderr: os.Stderr,
		Getenv: func(string) string { return "" },
	}
	fillDefaults(o)
	return o
}

// A flake bundle with the complete shipped Linux helper set is mounted directly.
func TestPrebuiltBundleIsMountedWithoutBuilding(t *testing.T) {
	root := stageBundle(t, true)
	o := prefixOptions(t)

	p, ok := o.resolveJailPrefix(root, "podman")
	if !ok {
		t.Fatal("resolveJailPrefix refused a bundle that ships the complete helper set")
	}
	if p.binDir != prebuiltBinDir(root) {
		t.Errorf("binDir = %q, want the bundle's own %q", p.binDir, prebuiltBinDir(root))
	}
	if p.shareDir != root {
		t.Errorf("shareDir = %q, want the flake source %q", p.shareDir, root)
	}
}

func TestJailPrefixRejectsNonExecutableHelper(t *testing.T) {
	root := stageBundle(t, true)
	entrypoint := filepath.Join(prebuiltBinDir(root), "yolo-entrypoint")
	if err := os.Chmod(entrypoint, 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	o := prefixOptions(t)
	o.Stderr = &stderr
	if _, ok := o.resolveJailPrefix(root, "podman"); ok {
		t.Fatal("a bundle with a non-executable pid1 helper was accepted")
	}
	for _, want := range []string{"yolo-entrypoint", "just build-dev-bundle", "just install"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("non-executable helper refusal lacks %q:\n%s", want, stderr.String())
		}
	}
}

// A checkout without a complete paired helper bundle refuses. It may not compile a
// replacement during launch: development goes through a printed build-only artifact, or the
// complete installed bundle.
func TestLiveCheckoutBuildsThePrefix(t *testing.T) {
	root := stageBundle(t, false)
	var stdout, stderr strings.Builder
	o := prefixOptions(t)
	o.Stdout = &stdout
	o.Stderr = &stderr

	if _, ok := o.resolveJailPrefix(root, "podman"); ok {
		t.Fatal("resolveJailPrefix accepted a source tree without paired helper binaries")
	}
	message := stderr.String()
	for _, want := range []string{"just build-dev-bundle", "printed", "launcher", "by path", "just install"} {
		if !strings.Contains(message, want) {
			t.Errorf("refusal lacks actionable %q:\n%s", want, message)
		}
	}
	if stdout.Len() != 0 {
		t.Errorf("helper refusal reached stdout, which belongs to the jailed command:\n%s", stdout.String())
	}
}

// A partial adjacent helper set is refused rather than compiled or mounted.
func TestBinDirWithoutTheEntrypointIsNotUsed(t *testing.T) {
	root := stageBundle(t, false)
	if err := os.MkdirAll(prebuiltBinDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prebuiltBinDir(root), "yolo"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	o := prefixOptions(t)
	o.Stderr = &stderr
	if _, ok := o.resolveJailPrefix(root, "podman"); ok {
		t.Fatal("a partial helper directory was mounted")
	}
	for _, want := range []string{"yolo-entrypoint", "just build-dev-bundle", "printed launcher by path", "just install"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("partial-bundle refusal lacks %q:\n%s", want, stderr.String())
		}
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
	root := resolveSymlinks(t.TempDir())
	binDir := filepath.Join(root, "bin")
	share := filepath.Join(root, "share", "yolo-jail")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stageBundleAt(t, share, true)
	// The resolver validates Darwin guest helpers on macOS as well as the Linux
	// helper set used by the jail. Keep both shipped platform sets in this fixture
	// so this test exercises the same complete bundle on every host OS.
	for _, name := range macosuser.GuestBinaries {
		path := filepath.Join(macosuser.PrebuiltGuestBinDir(share), name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("Darwin guest helper %q is missing from the complete bundle fixture: %v", name, err)
			continue
		}
		if !info.Mode().IsRegular() {
			t.Errorf("Darwin guest helper %q in the complete bundle fixture is not a regular file", name)
		}
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

// TestFlakeAndLauncherAgreeOnThePrefixLayout pins the container prefix mountpoints. The
// macos-user guest bundle's contents are pinned against its actual staging manifest by
// internal/macosuser.TestGuestBinariesMatchTheFlakeAndTheBundleScript; this test pins the
// current production resolver to that shared prebuilt bundle directory.
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
	// (docs/reference/image-staging-vs-baking.md#the-layer-plan). Same directories, same
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

	guestResolver, err := os.ReadFile("../commands.go")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`prebuilt\s*:=\s*macosuser\.PrebuiltGuestBinDir\(repoRoot\)`).Match(guestResolver) {
		t.Error("resolveGuestBinaries no longer selects the per-arch prebuilt guest set from the resolved bundle")
	}
	if got, want := macosuser.PrebuiltGuestBinDir("/bundle"), filepath.Join("/bundle", "bin", "darwin-"+goruntime.GOARCH); got != want {
		t.Errorf("macosuser.PrebuiltGuestBinDir = %q, want the bundle's current darwin-<arch> staging path %q", got, want)
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
	if !regexp.MustCompile(`jailPrefix\s*,\s*prefixOK\s*:=\s*o\.resolveJailPrefix\(repoRoot,\s*rt\)`).MatchString(src) {
		t.Error("runNormal no longer resolves the jail prefix. The install prefix is " +
			"MOUNTED, not baked, so nothing else produces the binaries the container argv names")
	}
	if !regexp.MustCompile(`jailPrefix\s*:\s*jailPrefix,`).MatchString(src) {
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
		rt      string
		isMacOS bool
		optIn   string
		refuse  bool
	}{
		{"linux never refuses — /nix is right there", store, "podman", false, "", false},
		{"darwin + a store prefix is the measured outage", store, "podman", true, "", true},
		{"darwin + the operator says the VM shares /nix", store, "podman", true, "1", false},
		{"darwin + a bundle under $HOME is what the VM does share", home, "podman", true, "", false},
		{"darwin + only the SHARE half in the store still refuses",
			jailPrefix{binDir: home.binDir, shareDir: store.shareDir}, "podman", true, "", true},
		{"darwin + only the BIN half in the store still refuses",
			jailPrefix{binDir: store.binDir, shareDir: home.shareDir}, "podman", true, "", true},
		// A directory whose NAME starts with the store's is not inside it. The
		// segment test exists for this case, so it is asserted rather than trusted.
		{"darwin + a sibling of /nix/store is not in it", jailPrefix{
			binDir:   hostNixStore + "-of-my-own/bin",
			shareDir: hostNixStore + "-of-my-own/share/yolo-jail",
		}, "podman", true, "", false},

		// ⚠ APPLE CONTAINER IS EXEMPT, AND THIS ROW IS THE GUARD ON A MEASURED FIX.
		//
		// The rule is about podman's machine VM and its one init-time share set. Apple
		// Container has no machine: each container gets its own VM and binds the host
		// paths the launch names, so a /nix/store source is just a host path.
		//
		// Refusing it anyway blocked EVERY Apple Container launch from a live checkout —
		// three parity tests on run 34871200672, 2026-09-14, the first CI run ever to
		// exercise this backend — and offered a `podman machine init` remedy that means
		// nothing there. Disproved on hardware: with the refusal bypassed, a launch whose
		// prefix WAS a store path ran its command and exited 0.
		//
		// Delete the `rt == "container"` arm and this row fails alone.
		{"apple container binds arbitrary host paths, so a store prefix is fine",
			store, "container", true, "", false},
		{"apple container is exempt on the SHARE half too",
			jailPrefix{binDir: home.binDir, shareDir: store.shareDir}, "container", true, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := prefixUnreachableFromVM(tc.p, tc.rt, tc.isMacOS, tc.optIn)
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

// TestDarwinRefusesAStorePrefix pins the production resolver's use of the
// tested macOS Podman Machine visibility guard. Complete bundles are the only
// accepted source, so this assertion covers a store-backed adjacent bundle too.
func TestDarwinRefusesAStorePrefix(t *testing.T) {
	source, err := os.ReadFile("jailprefix.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(source)
	start := strings.Index(body, "func (o *Options) resolveJailPrefix(")
	if start < 0 {
		t.Fatal("could not locate the production jail-prefix resolver")
	}
	end := strings.Index(body[start:], "func missingJailBinaries(")
	if end < 0 {
		t.Fatal("could not locate the end of the production jail-prefix resolver")
	}
	if !strings.Contains(body[start:start+end], "prefixUnreachableFromVM(p, rt, o.IsMacOS") {
		t.Error("resolveJailPrefix no longer checks whether the selected store bundle is visible to the runtime VM")
	}
}

// A complete bundle under the VM's default home share is accepted, and the
// machine's actual share list remains part of launch resolution.
func TestDarwinAcceptsAPrefixTheVMCanSee(t *testing.T) {
	root := stageBundle(t, true)
	o := prefixOptions(t)
	o.IsMacOS = true
	probes := 0
	fakePodmanMachine(t, o, append(append([]string{}, defaultMacShares...), filepath.Dir(root)), &probes)
	defer func() {
		if probes == 0 {
			t.Error("the launch never read the machine's share list, so this accepted nothing it was shown")
		}
	}()

	if _, ok := o.resolveJailPrefix(root, "podman"); !ok {
		t.Fatal("a darwin launch refused a complete prebuilt bundle under $HOME")
	}
}

// yoloStandInScript is the host yolo of a fixture bundle: a script that runs this test binary, whose
// TestMain answers `internal daemon <name>` as the real one does. A launch under test pins this file
// (pinnedexec.go) and so starts its helpers from it, as a launch starts them from the bundle's yolo.
func yoloStandInScript() []byte {
	exe, err := os.Executable()
	if err != nil {
		return []byte("binary")
	}
	return []byte("#!/bin/sh\nexec '" + exe + "' \"$@\"\n")
}
