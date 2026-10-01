package run

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/darwinpkg"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/nixroots"
	"github.com/mschulkind-oss/yolo-jail/internal/nixroots/nixrootstest"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// translatedroots_test.go pins both halves of docs/design/in-jail-nix-roots.md §4 as a
// launch uses them, at their PRODUCTION call sites: the four places yolo registers its own GC
// roots (NR-D2), each driven through the function a launch calls and checked at a fake nix
// daemon, and the host path map the assembler states for the jail it starts.

// jailRootFixture is a jail in miniature: HOME is the jail's home, its .local mapped to a host
// path the way a launcher's map maps the per-workspace home overlay, a fake nix daemon on the
// socket, and a store directory holding one store path.
type jailRootFixture struct {
	home, hostLocal, store, storePath string
	daemon                            *nixrootstest.Daemon
	env                               map[string]string
}

func newJailRootFixture(t *testing.T, opts ...nixrootstest.Options) *jailRootFixture {
	t.Helper()
	// RESOLVED where it is minted: the registrar resolves a link's directory, and on darwin
	// t.TempDir() is under /var, a symlink to /private/var.
	home := resolveSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	store := resolveSymlinks(t.TempDir())
	storePath := filepath.Join(store, "0123456789abcdfghijklmnpqrsvwxyz-closure")
	if err := os.MkdirAll(storePath, 0o755); err != nil {
		t.Fatal(err)
	}
	o := nixrootstest.Options{}
	if len(opts) > 0 {
		o = opts[0]
	}
	d := nixrootstest.Start(t, o)
	hostLocal := "/home/u/code/proj/.yolo/home/local"
	m := nixroots.HostMap{
		home:                          "", // the read-only home skeleton
		filepath.Join(home, ".local"): hostLocal,
		"/workspace":                  "/home/u/code/proj",
	}
	return &jailRootFixture{
		home: home, hostLocal: hostLocal, store: store, storePath: storePath, daemon: d,
		env: map[string]string{
			"YOLO_VERSION":           "9.9.9-test",
			nixroots.MapEnv:          m.Encode(),
			"NIX_DAEMON_SOCKET_PATH": d.Socket,
			"NIX_STORE_DIR":          store,
		},
	}
}

func (f *jailRootFixture) getenv(k string) string { return f.env[k] }

// hostSpelling is what the daemon must be sent for a link under the jail's ~/.local.
func (f *jailRootFixture) hostSpelling(t *testing.T, link string) string {
	t.Helper()
	rel, err := filepath.Rel(filepath.Join(f.home, ".local"), link)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("link %s is not under the fixture's ~/.local", link)
	}
	return filepath.Join(f.hostLocal, rel)
}

// assertRooted checks the one root a call site registered: the link in the jail points at
// the fixture's store path, and the daemon was sent the host's spelling of it, nothing else.
func (f *jailRootFixture) assertRooted(t *testing.T, link string) {
	t.Helper()
	if target, err := os.Readlink(link); err != nil || target != f.storePath {
		t.Errorf("GC-root link %s = %q, %v; want a symlink to %s", link, target, err, f.storePath)
	}
	want := f.hostSpelling(t, link)
	if got := f.daemon.Roots(); !slices.Equal(got, []string{want}) {
		t.Errorf("the host daemon was sent %q, want exactly the host's spelling %q — a jail's "+
			"spelling is a root the host prunes as stale", got, want)
	}
}

// THE IMAGE ROOT, through the AutoLoadImage options the run path builds: in a jail the
// RegisterRoot it hands over registers a translated root. Delete `RegisterRoot:
// o.rootImageFn()` or the jail arm of gcRooter and this fails — the shape the image package's
// own doc called "the standing proof that shape rots quietly", nil in-jail with nothing
// pinning it.
func TestTheRunPathRootsTheImageFromAJail(t *testing.T) {
	f := newJailRootFixture(t)
	var got image.AutoLoadOptions
	o := goldenOptions(t.TempDir(), f.home)
	o.Stdout, o.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
	o.Getenv = f.getenv
	o.autoLoad = func(opts image.AutoLoadOptions) image.LoadResult {
		got = opts
		return image.LoadResult{OK: true}
	}
	o.autoLoadImage(jsonx.NewOrderedMap(), "podman", t.TempDir(), storePackagesPlan{})
	if got.RegisterRoot == nil {
		t.Fatal("a jail with a host path map got no image-root registrar")
	}
	got.RegisterRoot(f.storePath)
	f.assertRooted(t, image.ImageRootLink(f.storePath))
}

// THE IMAGE COPIER, the out-link nix writes for yolo's own skopeo build: on the host that
// out-link is the root, and in a jail nix registers it under the jail's spelling, which the
// host prunes, so the run path hands AutoLoadImage the translated root for it. Delete
// `RootCopier: o.rootCopierFn()` and this fails.
func TestTheRunPathRootsTheImageCopierFromAJail(t *testing.T) {
	f := newJailRootFixture(t)
	repo := t.TempDir()
	loadWith := func(getenv func(string) string) image.AutoLoadOptions {
		var got image.AutoLoadOptions
		o := goldenOptions(t.TempDir(), f.home)
		o.Stdout, o.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
		o.Getenv = getenv
		o.autoLoad = func(opts image.AutoLoadOptions) image.LoadResult {
			got = opts
			return image.LoadResult{OK: true}
		}
		o.autoLoadImage(jsonx.NewOrderedMap(), "podman", repo, storePackagesPlan{})
		return got
	}

	got := loadWith(f.getenv)
	if got.RootCopier == nil {
		t.Fatal("a jail with a host path map got no image-copier root")
	}
	link := image.ImageCopierOutLink(repo)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil { // BuildImageCopier makes it
		t.Fatal(err)
	}
	got.RootCopier(link, f.storePath)
	f.assertRooted(t, link)

	if loadWith(func(string) string { return "" }).RootCopier != nil {
		t.Error("a host launch was handed a copier root beside nix's own out-link")
	}
	noMap := func(k string) string {
		if k == "YOLO_VERSION" {
			return "9.9.9"
		}
		return ""
	}
	if loadWith(noMap).RootCopier != nil {
		t.Error("a jail with no host path map was handed a copier root it cannot register")
	}
}

// The image root's one reportable failure goes to the LAUNCH STREAM, stderr, like every other
// launch line: stdout is the jailed command's, and `yolo -- <cmd>` passes it through untouched
// (TestTheRunPathSendsImageDisclosuresToStderr has the incident).
func TestAnImageRootRefusalGoesToTheLaunchStream(t *testing.T) {
	f := newJailRootFixture(t, nixrootstest.Options{Reject: "no roots for you"})
	var got image.AutoLoadOptions
	var stdout, stderr bytes.Buffer
	o := goldenOptions(t.TempDir(), f.home)
	o.Stdout, o.Stderr = &stdout, &stderr
	o.Getenv = f.getenv
	o.autoLoad = func(opts image.AutoLoadOptions) image.LoadResult {
		got = opts
		return image.LoadResult{OK: true}
	}
	o.autoLoadImage(jsonx.NewOrderedMap(), "podman", t.TempDir(), storePackagesPlan{})
	got.RegisterRoot(f.storePath)
	if !strings.Contains(stderr.String(), "no roots for you") {
		t.Errorf("the daemon's refusal is not on the launch stream:\nstderr=%q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("the image root wrote to the jailed command's stdout: %q", stdout.String())
	}
}

// A jail whose launcher stated no map — every jail an older launcher started, and macos-user
// — keeps today's behavior: no registrar at all.
func TestAJailWithNoHostPathMapGetsNoImageRootRegistrar(t *testing.T) {
	o := goldenOptions(t.TempDir(), t.TempDir())
	for name, env := range map[string]map[string]string{
		"no map":       {"YOLO_VERSION": "9.9.9"},
		"a broken map": {"YOLO_VERSION": "9.9.9", nixroots.MapEnv: "not json"},
		"only masks":   {"YOLO_VERSION": "9.9.9", nixroots.MapEnv: `{"/home/agent":""}`},
	} {
		o.Getenv = func(k string) string { return env[k] }
		if o.rootImageFn() != nil {
			t.Errorf("%s: a jail got an image-root registrar it cannot register honestly", name)
		}
		if o.gcRooter() != nil {
			t.Errorf("%s: gcRooter is not nil", name)
		}
	}
}

// On the host nothing changes: the Rooter is nix-store's.
func TestTheHostRootsWithNixStore(t *testing.T) {
	o := goldenOptions(t.TempDir(), t.TempDir())
	if r := o.gcRooter(); r == nil || reflect.ValueOf(r).Pointer() != reflect.ValueOf(image.AddRoot).Pointer() {
		t.Error("a host launch does not register its roots with image.AddRoot")
	}
}

// THE IMAGE EXTRAS (C5), through buildImageExtras — the launch's own closure — with only the
// nix build faked.
func TestAJailRootsTheImageExtrasUnderTheHostsSpelling(t *testing.T) {
	f := newJailRootFixture(t)
	prev := buildImageExtrasProfile
	buildImageExtrasProfile = func(string) (string, error) { return f.storePath, nil }
	t.Cleanup(func() { buildImageExtrasProfile = prev })
	o, _, _ := storeOptions(t, f.env)

	profile, err := o.buildImageExtras()(t.TempDir())
	if err != nil || profile != f.storePath {
		t.Fatalf("buildImageExtras = %q, %v", profile, err)
	}
	f.assertRooted(t, extrasProfileRootLink(f.storePath))
}

// THE STORE-DELIVERED PACKAGES (C4), the fourth in-jail skip: on the host the build's own
// out-link is the root; in a jail the build takes none (nix would register it under the
// jail's spelling) and the same link is registered after it, translated.
func TestStorePackagesAreRootedAsATranslatedRootInAJail(t *testing.T) {
	f := newJailRootFixture(t)
	var outLinks []string
	prev := materializeProfileAt
	materializeProfileAt = func(_ string, _ []any, _, outLink string, _ io.Writer) (*darwinpkg.DarwinPackages, error) {
		outLinks = append(outLinks, outLink)
		return &darwinpkg.DarwinPackages{ProfilePath: f.storePath}, nil
	}
	t.Cleanup(func() { materializeProfileAt = prev })
	pkgs := []any{"zbar"}

	jail, _, _ := storeOptions(t, f.env)
	if _, _, err := jail.materializeStorePackages()(t.TempDir(), pkgs); err != nil {
		t.Fatal(err)
	}
	f.assertRooted(t, storeProfileRootLink(pkgs))

	host, _, _ := storeOptions(t, nil)
	if _, _, err := host.materializeStorePackages()(t.TempDir(), pkgs); err != nil {
		t.Fatal(err)
	}
	if want := []string{"", storeProfileRootLink(pkgs)}; !slices.Equal(outLinks, want) {
		t.Errorf("out-links handed to the build = %q, want none in the jail and the content-keyed "+
			"root on the host, %q", outLinks, want)
	}
	if n := len(f.daemon.Roots()); n != 1 {
		t.Errorf("the host launch sent the daemon a translated root too (%d roots)", n)
	}
}

// A JAIL'S PROFILE ROOT IS BEST-EFFORT, as every in-jail root is (§4, "Failure"): the build
// takes no out-link there, so a roots directory that cannot be made costs the root and never
// the packages. And a jail whose launcher stated no map makes nothing at all, as it did before
// translated roots. Only the host's build needs the directory before it runs, because there
// nix itself writes the out-link into it.
func TestAJailsStoreProfileRootNeverFailsItsPackages(t *testing.T) {
	f := newJailRootFixture(t)
	prev := materializeProfileAt
	materializeProfileAt = func(string, []any, string, string, io.Writer) (*darwinpkg.DarwinPackages, error) {
		return &darwinpkg.DarwinPackages{ProfilePath: f.storePath}, nil
	}
	t.Cleanup(func() { materializeProfileAt = prev })
	pkgs := []any{"zbar"}

	// No map: the old in-jail state, which created no directory.
	noMap := map[string]string{"YOLO_VERSION": f.env["YOLO_VERSION"]}
	o, _, _ := storeOptions(t, noMap)
	if _, _, err := o.materializeStorePackages()(t.TempDir(), pkgs); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(paths.PackageRootsDir()); !os.IsNotExist(err) {
		t.Errorf("a jail with no host path map made %s (%v)", paths.PackageRootsDir(), err)
	}

	// A build dir that is a file: no roots directory can be made under it, whoever runs this.
	if err := os.RemoveAll(paths.BuildDir()); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.BuildDir()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.BuildDir(), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	o, _, _ = storeOptions(t, f.env)
	profile, _, err := o.materializeStorePackages()(t.TempDir(), pkgs)
	if err != nil || profile != f.storePath {
		t.Errorf("a jail whose roots dir cannot be made lost its packages: %q, %v", profile, err)
	}
	if n := len(f.daemon.Roots()); n != 0 {
		t.Errorf("the daemon was sent %d roots for a link that could not be made", n)
	}
}

// THE ONE FAILURE A LAUNCH SAYS: the host's daemon refusing the root (§4, "Failure"), on the
// launch stream, naming what is left unprotected. Every other failure is silent.
func TestADaemonRefusalIsTheOneFailureALaunchSays(t *testing.T) {
	f := newJailRootFixture(t, nixrootstest.Options{Reject: "roots are not allowed here"})
	prev := buildImageExtrasProfile
	buildImageExtrasProfile = func(string) (string, error) { return f.storePath, nil }
	t.Cleanup(func() { buildImageExtrasProfile = prev })
	o, stdout, stderr := storeOptions(t, f.env)
	if _, err := o.buildImageExtras()(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"store-delivered image extras", "refused", "roots are not allowed here"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the refusal warning lacks %q:\n%s", want, stderr)
		}
	}
	if stdout.Len() != 0 {
		t.Errorf("the warning reached the jailed command's stdout: %q", stdout)
	}

	// No daemon at all is today's state, and says nothing.
	f.env["NIX_DAEMON_SOCKET_PATH"] = filepath.Join(filepath.Dir(f.daemon.Socket), "absent")
	o, _, stderr = storeOptions(t, f.env)
	if _, err := o.buildImageExtras()(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Errorf("an unreachable daemon was reported: %q", stderr)
	}
}

// mapLaunch assembles a podman launch with the host nix daemon mounted, and returns the map
// its argv states (nil when it states none).
func mapLaunch(t *testing.T, getenv func(string) string, sealed bool) (nixroots.HostMap, *assembleInput) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/home/u/code/proj", home)
	o.PathExists = func(p string) bool { return p == hostNixSocket || p == hostNixStore }
	o.Getenv = getenv
	in := &assembleInput{
		cfg:          newConfig("security", newConfig("blocked_tools", []any{})),
		rt:           "podman",
		cname:        "yolo-proj-abcd1234",
		imageRef:     goldenImageRef,
		jailPrefix:   goldenJailPrefix,
		packs:        claudePackFixture(t),
		agentsPath:   "/home/u/.local/share/yolo-jail/agents/yolo-proj-abcd1234",
		homeSkeleton: goldenHomeSkeleton,
		wsState:      "/home/u/code/proj/.yolo/home",
		miseStore:    "/home/u/.local/share/yolo-jail/mise",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
		sealed:       sealed,
	}
	argv := o.assembleRunCmd(in)
	img := slices.Index(argv, goldenImageRef)
	if img < 0 {
		t.Fatal("no image ref in the argv")
	}
	var m nixroots.HostMap
	for i := 0; i+1 < img; i++ {
		if argv[i] == "-e" && strings.HasPrefix(argv[i+1], nixroots.MapEnv+"=") {
			if m != nil {
				t.Fatalf("the argv states %s twice", nixroots.MapEnv)
			}
			var err error
			if m, err = nixroots.ParseHostMap(strings.TrimPrefix(argv[i+1], nixroots.MapEnv+"=")); err != nil {
				t.Fatalf("the argv's map does not parse: %v", err)
			}
		}
	}
	return m, in
}

// THE HOST HALF, and the link between the halves: a host launch with the nix daemon mounted
// states a map in which the in-jail build dir — where all four of yolo's own roots live —
// translates to the workspace's home overlay on the host, while the read-only home skeleton
// and the scratch /tmp stay masks.
func TestAHostLaunchStatesTheMapItsJailRootsThrough(t *testing.T) {
	m, in := mapLaunch(t, func(string) string { return "" }, false)
	if m == nil {
		t.Fatalf("a launch mounting the host nix daemon stated no %s", nixroots.MapEnv)
	}
	cases := []struct {
		jail, want string
		ok         bool
	}{
		{"/workspace/result", "/home/u/code/proj/result", true},
		// The jail's paths.BuildDir(), under ~/.local: where the image, prefix and package
		// roots live (NR-D2's "a mapped bind").
		{"/home/agent/.local/share/yolo-jail/build/prefix-roots/0123456789abcdef",
			in.wsState + "/local/share/yolo-jail/build/prefix-roots/0123456789abcdef", true},
		{"/home/agent/.bashrc", "", false},
		{"/tmp/result", "", false},
		{"/nix/store/abc-x", "", false},
	}
	for _, c := range cases {
		got, ok := m.Translate(c.jail)
		if got != c.want || ok != c.ok {
			t.Errorf("the launch's map translates %s to %q, %v; want %q, %v", c.jail, got, ok, c.want, c.ok)
		}
	}
}

// Only a jail with the host's nix daemon reads the map, and a sealed build is withheld the
// daemon (seal.go), so neither states one.
func TestNoMapWithoutTheHostNixDaemon(t *testing.T) {
	if m, _ := mapLaunch(t, func(string) string { return "" }, true); m != nil {
		t.Errorf("a sealed launch stated a host path map: %v", m)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := goldenOptions("/ws", home)
	if args := o.hostPathMapEnvArgs("podman", false, []string{"-v", "/ws:/workspace"}); args != nil {
		t.Errorf("a launch without the host nix daemon stated a map: %q", args)
	}
}

// A NESTED LAUNCH COMPOSES (§4): its sources are paths in its own jail, so each goes through
// the map that jail was given. The documented nested workspace lives under /tmp and translates
// to nothing; a source under the parent's mapped ~/.local translates to the parent's host path.
// With no parent map nothing translates, and no map is stated at all.
func TestANestedLaunchComposesThroughItsParentsMap(t *testing.T) {
	parent := nixroots.HostMap{
		"/home/agent/.local": "/home/u/code/proj/.yolo/home/local",
		"/tmp":               "",
	}
	o := goldenOptions("/tmp/yolo-nested", t.TempDir())
	o.PathExists = func(p string) bool { return p == hostNixSocket || p == hostNixStore }
	env := map[string]string{"YOLO_VERSION": "9.9.9", nixroots.MapEnv: parent.Encode()}
	o.Getenv = func(k string) string { return env[k] }
	argv := []string{
		"-v", "/tmp/yolo-nested:/workspace",
		"-v", "/home/agent/.local/share/yolo-jail/cache:/home/agent/.cache",
		"-v", "/home/agent/.local/share/yolo-jail/skel:/home/agent:ro",
	}
	args := o.hostPathMapEnvArgs("podman", false, argv)
	if len(args) != 2 || !strings.HasPrefix(args[1], nixroots.MapEnv+"=") {
		t.Fatalf("a nested launch with a parent map stated %q", args)
	}
	m, err := nixroots.ParseHostMap(strings.TrimPrefix(args[1], nixroots.MapEnv+"="))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := m.Translate("/home/agent/.cache/nix/x"); !ok ||
		got != "/home/u/code/proj/.yolo/home/local/share/yolo-jail/cache/nix/x" {
		t.Errorf("the nested map translates ~/.cache to %q, %v", got, ok)
	}
	if got, ok := m.Translate("/workspace/result"); ok {
		t.Errorf("a nested workspace under /tmp translated to %q", got)
	}

	delete(env, nixroots.MapEnv)
	if args := o.hostPathMapEnvArgs("podman", false, argv); args != nil {
		t.Errorf("a nested launch with no parent map stated %q", args)
	}
}

// The argv reader sees every mount form the assembler, a pack or a loophole can emit, and only
// a read-write bind of an absolute source is writable.
func TestArgvMountsReadsEveryMountForm(t *testing.T) {
	got := argvMounts([]string{
		"podman", "run", "--rm",
		"-v", "/h/ws:/workspace",
		"-v", "/h/skel:/home/agent:ro",
		"-v", "/h/x:/x:rw,z",
		"-v", "/h/o:/o:O",
		"-v", "yolo-mise:/mise",
		"-v", "/anon",
		"--volume=/h/v:/v",
		"--tmpfs", "/tmp:exec,mode=1777",
		"--mount", "type=bind,source=/h/m,target=/m",
		"--mount", "type=bind,src=/h/r,dst=/r,readonly",
		"--mount", "type=volume,source=vol,target=/vol",
		"-e", "X=-v",
	})
	want := []nixroots.Mount{
		{Dest: "/workspace", Source: "/h/ws", Writable: true},
		{Dest: "/home/agent", Source: "/h/skel"},
		{Dest: "/x", Source: "/h/x", Writable: true},
		{Dest: "/o", Source: "/h/o"},
		{Dest: "/mise", Source: "yolo-mise"},
		{Dest: "/anon"},
		{Dest: "/v", Source: "/h/v", Writable: true},
		{Dest: "/tmp"},
		{Dest: "/m", Source: "/h/m", Writable: true},
		{Dest: "/r", Source: "/h/r"},
		{Dest: "/vol", Source: "vol"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("argvMounts =\n  %v\nwant\n  %v", got, want)
	}
}

// paths.BuildDir is where every one of these roots lives; the fixtures above assume it is
// under HOME's .local, which is also what makes NR-D2's "a mapped bind" true in a jail.
func TestTheBuildDirIsUnderTheHomesLocal(t *testing.T) {
	home := resolveSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	if !strings.HasPrefix(paths.BuildDir(), filepath.Join(home, ".local")+"/") {
		t.Errorf("paths.BuildDir() = %s is not under %s/.local", paths.BuildDir(), home)
	}
}
