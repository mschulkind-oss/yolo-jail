package entrypoint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// storepackages_test.go covers C4/C5's jail half: the /run/yolo/packages farm, the two env
// vars it needs, and the two ORDERING facts the boot depends on
// (docs/reference/image-staging-vs-baking.md, "Store-delivered packages").

// fakeProfile lays down a buildEnv-shaped store path: bin/<bins>, lib/<libs> and, when
// pcs is non-empty, lib/pkgconfig/<pcs>.
func fakeProfile(t *testing.T, dir string, bins, libs, pcs []string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, b := range bins {
		if err := os.WriteFile(filepath.Join(dir, "bin", b), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range libs {
		if err := os.WriteFile(filepath.Join(dir, "lib", l), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if len(pcs) > 0 {
		if err := os.MkdirAll(filepath.Join(dir, "lib", "pkgconfig"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, pc := range pcs {
			if err := os.WriteFile(filepath.Join(dir, "lib", "pkgconfig", pc), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

// linkTarget reads the symlink at <root>/<rel>, failing the test when it is not one.
func linkTarget(t *testing.T, root, rel string) string {
	t.Helper()
	got, err := os.Readlink(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("%s is not a symlink in the farm: %v", rel, err)
	}
	return got
}

// TestStoreFarmLinksBinLibAndPkgConfig is the base case: one profile, three kinds of
// content, three destinations. The lib assertion is the one that matters most — the lib
// farm is where C4 is likeliest to break, because a `packages:` entry added for its .so
// (zbar, libdmtx) has no other way to be dlopen-able once it leaves the image.
func TestStoreFarmLinksBinLibAndPkgConfig(t *testing.T) {
	profile := fakeProfile(t, filepath.Join(t.TempDir(), "profile"),
		[]string{"zbarimg"}, []string{"libzbar.so.0", "libzbar.so.0.3.0", "notalib.txt"},
		[]string{"zbar.pc"})
	root := t.TempDir()
	if err := buildStorePackageFarm(root, []string{profile}); err != nil {
		t.Fatalf("buildStorePackageFarm: %v", err)
	}

	if got, want := linkTarget(t, root, "bin/zbarimg"), filepath.Join(profile, "bin", "zbarimg"); got != want {
		t.Errorf("bin/zbarimg -> %q, want %q", got, want)
	}
	for _, lib := range []string{"libzbar.so.0", "libzbar.so.0.3.0"} {
		if got, want := linkTarget(t, root, "lib/"+lib), filepath.Join(profile, "lib", lib); got != want {
			t.Errorf("lib/%s -> %q, want %q", lib, got, want)
		}
	}
	// The farm links `lib*.so*` and nothing else, matching flake.nix's own /lib loop —
	// a package's stray lib/ data file must not land on LD_LIBRARY_PATH.
	if _, err := os.Lstat(filepath.Join(root, "lib", "notalib.txt")); err == nil {
		t.Error("a non-.so file under the profile's lib/ was linked into the farm; the " +
			"glob is lib*.so*, the same one the image's /lib farm uses")
	}
	if got, want := linkTarget(t, root, "lib/pkgconfig/zbar.pc"),
		filepath.Join(profile, "lib", "pkgconfig", "zbar.pc"); got != want {
		t.Errorf("lib/pkgconfig/zbar.pc -> %q, want %q", got, want)
	}
}

// TestStoreFarmPrecedenceIsFirstProfileWins pins the rule the host's profile ORDER relies
// on. Under C5 two profiles reach the farm — the workspace's own `packages:` and the
// image extras — and a name in both must resolve to the workspace's, or declaring a
// version of a tool yolo also ships would silently do nothing.
func TestStoreFarmPrecedenceIsFirstProfileWins(t *testing.T) {
	base := t.TempDir()
	first := fakeProfile(t, filepath.Join(base, "user"), []string{"fzf"}, []string{"libz.so.1"}, nil)
	second := fakeProfile(t, filepath.Join(base, "extras"), []string{"fzf", "bat"}, []string{"libz.so.1"}, nil)

	root := t.TempDir()
	if err := buildStorePackageFarm(root, []string{first, second}); err != nil {
		t.Fatalf("buildStorePackageFarm: %v", err)
	}
	if got, want := linkTarget(t, root, "bin/fzf"), filepath.Join(first, "bin", "fzf"); got != want {
		t.Errorf("bin/fzf -> %q, want the FIRST profile's %q — the host orders the "+
			"profiles and a later one may not retarget a claimed name", got, want)
	}
	if got, want := linkTarget(t, root, "lib/libz.so.1"), filepath.Join(first, "lib", "libz.so.1"); got != want {
		t.Errorf("lib/libz.so.1 -> %q, want the FIRST profile's %q", got, want)
	}
	// A name only the later profile has still arrives.
	if got, want := linkTarget(t, root, "bin/bat"), filepath.Join(second, "bin", "bat"); got != want {
		t.Errorf("bin/bat -> %q, want %q", got, want)
	}
}

// TestStoreFarmRefusesAnUnresolvableProfile is the R2 guard. An opt-in launch built an
// image WITHOUT these packages, so a profile that does not resolve in the jail means the
// store is not mounted the way the host believed — and linking nothing would hand the
// agent a jail quietly missing every tool the workspace declared.
func TestStoreFarmRefusesAnUnresolvableProfile(t *testing.T) {
	err := buildStorePackageFarm(t.TempDir(), []string{"/nix/store/does-not-exist-profile"})
	if err == nil {
		t.Fatal("a profile that does not resolve inside the jail must be a FATAL boot " +
			"error: there is no baked copy to fall back on, so linking nothing " +
			"silently produces a tool-less jail")
	}
	if !strings.Contains(err.Error(), "does-not-exist-profile") {
		t.Errorf("the error must name the profile it could not read, got: %v", err)
	}
}

// TestStoreFarmIsClearedWhenTheLaunchStopsOptingIn: the farm is regenerated per boot, and
// an `exec` back into a live container re-runs the boot. A launch that dropped the opt-in
// must not inherit the previous one's links.
func TestStoreFarmIsClearedWhenTheLaunchStopsOptingIn(t *testing.T) {
	profile := fakeProfile(t, filepath.Join(t.TempDir(), "profile"), []string{"jq"}, nil, nil)
	root := t.TempDir()
	if err := buildStorePackageFarm(root, []string{profile}); err != nil {
		t.Fatalf("buildStorePackageFarm: %v", err)
	}
	if err := buildStorePackageFarm(root, nil); err != nil {
		t.Fatalf("buildStorePackageFarm (no profiles): %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "bin", "jq")); err == nil {
		t.Error("a stale link survived a boot that delivers no profiles — the farm must " +
			"be cleared, or the jail keeps executing a closure this launch never asked for")
	}
}

// TestGenerateStorePackagesExportsTheSearchPaths: the links are inert without them. A
// store-delivered .so is discoverable ONLY via LD_LIBRARY_PATH (flake.nix records that
// nixpkgs' ld.so never reads /etc/ld.so.cache), and a .pc file only via PKG_CONFIG_PATH.
// Both PREPEND, because the image's own /lib and /lib/pkgconfig are still live.
func TestGenerateStorePackagesExportsTheSearchPaths(t *testing.T) {
	profile := fakeProfile(t, filepath.Join(t.TempDir(), "profile"), []string{"jq"},
		[]string{"libjq.so.1"}, []string{"jq.pc"})
	root := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME":       "/home/agent",
		StoreProfilesEnv:  profile,
		"LD_LIBRARY_PATH": "/lib:/usr/lib",
		"PKG_CONFIG_PATH": "/lib/pkgconfig",
	})
	if err := generateStorePackagesIn(e, root, filepath.Join(root, "fonts"), imageFontsConf); err != nil {
		t.Fatalf("generateStorePackagesIn: %v", err)
	}
	if got, want := e.Vars["LD_LIBRARY_PATH"], storeLibDir(root)+":/lib:/usr/lib"; got != want {
		t.Errorf("LD_LIBRARY_PATH = %q, want %q. The links are INERT without it: "+
			"flake.nix records that nixpkgs' ld.so never reads /etc/ld.so.cache, so "+
			"this variable is the whole of runtime discovery for a store-delivered .so",
			got, want)
	}
	if got, want := e.Vars["PKG_CONFIG_PATH"], storePkgConfigDir(root)+":/lib/pkgconfig"; got != want {
		t.Errorf("PKG_CONFIG_PATH = %q, want %q (PREPENDED — the image's own "+
			"/lib/pkgconfig is still live)", got, want)
	}
	// Idempotent: an `exec` back into a live container re-runs the whole boot.
	if err := generateStorePackagesIn(e, root, filepath.Join(root, "fonts"), imageFontsConf); err != nil {
		t.Fatalf("second boot: %v", err)
	}
	if got, want := e.Vars["LD_LIBRARY_PATH"], storeLibDir(root)+":/lib:/usr/lib"; got != want {
		t.Errorf("LD_LIBRARY_PATH grew a duplicate entry on a second boot: %q", got)
	}

	// A FAILED farm exports nothing: a jail whose LD_LIBRARY_PATH names a directory that
	// was never built is a worse diagnosis than one whose boot refused.
	broken := NewEnv(map[string]string{
		"JAIL_HOME":       "/home/agent",
		StoreProfilesEnv:  "/nix/store/does-not-exist-profile",
		"LD_LIBRARY_PATH": "/lib:/usr/lib",
	})
	if err := generateStorePackagesIn(broken, t.TempDir(), t.TempDir(), imageFontsConf); err == nil {
		t.Fatal("expected an unresolvable profile to fail the genStep")
	}
	if got := broken.Vars["LD_LIBRARY_PATH"]; got != "/lib:/usr/lib" {
		t.Errorf("LD_LIBRARY_PATH was rewritten by a FAILED farm build: %q", got)
	}
}

// TestStoreFontconfigOnlyActsWhenTheImageHasNoFonts is C5's font half, and it is the
// piece the design named as what chromium "drags" out of the image with it.
//
// Moving `fullPackages` out takes THREE pieces of baked content with chromium, not one:
// the /usr/bin/chromium symlink, the /etc/fonts symlink into fontconfig's store path, and
// the font dirs linked into /usr/share/fonts. The image bakes
// FONTCONFIG_FILE=/etc/fonts/fonts.conf, so on a lean image that variable names a file
// that does not exist and chromium renders with no fonts at all. The root filesystem is
// --read-only, so the fix is a config on the /run tmpfs pointing at the profile.
//
// The first half of the test is the more important one: a BAKED jail must be untouched.
func TestStoreFontconfigOnlyActsWhenTheImageHasNoFonts(t *testing.T) {
	base := t.TempDir()
	profile := filepath.Join(base, "extras")
	if err := os.MkdirAll(filepath.Join(profile, "etc", "fonts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "etc", "fonts", "fonts.conf"),
		[]byte("<fontconfig/>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	imageConf := filepath.Join(base, "image-fonts.conf")
	if err := os.WriteFile(imageConf, []byte("<fontconfig/>\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The image still has its own fonts (a baked launch): change nothing.
	baked := NewEnv(map[string]string{"JAIL_HOME": "/home/agent"})
	configureStoreFontconfig(baked, []string{profile}, filepath.Join(base, "unused"), imageConf)
	if _, set := baked.Vars["FONTCONFIG_FILE"]; set {
		t.Error("a launch whose image still bakes /etc/fonts must keep the baked config — " +
			"repointing it would change font rendering for every jail, for nothing")
	}
	if _, err := os.Stat(filepath.Join(base, "unused")); err == nil {
		t.Error("a baked launch wrote a fontconfig dir it will never read")
	}

	// The lean image: repoint at the profile.
	fontsDir := filepath.Join(base, "run-fonts")
	lean := NewEnv(map[string]string{"JAIL_HOME": "/home/agent"})
	configureStoreFontconfig(lean, []string{profile}, fontsDir, filepath.Join(base, "absent.conf"))
	conf := filepath.Join(fontsDir, "fonts.conf")
	if lean.Vars["FONTCONFIG_FILE"] != conf {
		t.Fatalf("FONTCONFIG_FILE = %q, want %q — without it a lean jail's chromium has "+
			"no fonts at all", lean.Vars["FONTCONFIG_FILE"], conf)
	}
	if lean.Vars["FONTCONFIG_PATH"] != fontsDir {
		t.Errorf("FONTCONFIG_PATH = %q, want %q", lean.Vars["FONTCONFIG_PATH"], fontsDir)
	}
	body, err := os.ReadFile(conf)
	if err != nil {
		t.Fatalf("read generated fonts.conf: %v", err)
	}
	// The profile's own fonts.conf is INCLUDED rather than replaced, so its relative
	// `<include>conf.d</include>` resolves inside the profile and the upstream rule set
	// comes with it; the profile's share/fonts is added because the lean image has no
	// /usr/share/fonts to link them into.
	for _, want := range []string{
		filepath.Join(profile, "etc", "fonts", "fonts.conf"),
		filepath.Join(profile, "share", "fonts"),
		filepath.Join(fontsDir, "cache"),
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("generated fonts.conf does not name %q:\n%s", want, body)
		}
	}

	// No profile carries fontconfig at all (C4 without C5): write nothing.
	bare := NewEnv(map[string]string{"JAIL_HOME": "/home/agent"})
	other := filepath.Join(base, "other-fonts")
	configureStoreFontconfig(bare, []string{filepath.Join(base, "no-such")}, other,
		filepath.Join(base, "absent.conf"))
	if _, set := bare.Vars["FONTCONFIG_FILE"]; set {
		t.Error("no profile provides fontconfig, so nothing should have been repointed")
	}
}

// TestStoreProfilesParsesTheWireContract covers the one thing the parser must get right:
// absent/empty is the DEFAULT (bake), not an empty farm claim.
func TestStoreProfilesParsesTheWireContract(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"", nil},
		{"  ", nil},
		{"/nix/store/a", []string{"/nix/store/a"}},
		{"/nix/store/a:/nix/store/b", []string{"/nix/store/a", "/nix/store/b"}},
		{"/nix/store/a::/nix/store/b:", []string{"/nix/store/a", "/nix/store/b"}},
	}
	for _, tc := range cases {
		e := NewEnv(map[string]string{StoreProfilesEnv: tc.raw})
		got := StoreProfiles(e)
		if len(got) != len(tc.want) {
			t.Errorf("StoreProfiles(%q) = %v, want %v", tc.raw, got, tc.want)
			continue
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("StoreProfiles(%q)[%d] = %q, want %q", tc.raw, i, got[i], tc.want[i])
			}
		}
	}
}

// TestImageProbePathCountsStoreDeliveredPackages is the SHADOWING-INVERSION guard, and it
// is the test that fails if the imageProbePath addition is deleted.
//
// Under C4/C5 a name that used to be in /bin is in the farm instead. The
// launcher-collision check exists so a pack declaring `program fzf` cannot shadow a
// working tool of that name (defect 11.1); if the check cannot see the farm, C4 hands 11.1
// straight back. The gate is the DECLARATION, not the directory — a launch that bakes must
// be unaffected.
func TestImageProbePathCountsStoreDeliveredPackages(t *testing.T) {
	baking := NewEnv(map[string]string{"JAIL_HOME": "/home/agent"})
	if strings.Contains(imageProbePath(baking), StorePackagesBin()) {
		t.Error("a launch that BAKES must not have the store farm on the collision " +
			"probe path — nothing was delivered there")
	}
	opted := NewEnv(map[string]string{
		"JAIL_HOME":      "/home/agent",
		StoreProfilesEnv: "/nix/store/aaa-profile",
	})
	segs := strings.Split(imageProbePath(opted), ":")
	if segs[0] != StorePackagesBin() {
		t.Fatalf("imageProbePath = %v; the store-delivered farm must be on it (and "+
			"first, since it precedes /bin on the real PATH). Without it a pack's "+
			"`program fzf` launcher shadows a tool the workspace declared by name — "+
			"defect 11.1, arriving through the door C4 opens", segs)
	}
	// The install prefixes stay out. This is the ⚠ in launchercollision.go's header: a
	// probe path that includes where a launcher INSTALLS stops writing the launcher after
	// its first success, and evergreen works exactly once.
	for _, seg := range segs {
		if strings.HasPrefix(seg, "/home/agent/") {
			t.Errorf("imageProbePath includes %q, under the jail home — the install "+
				"prefixes must stay out or the launcher stops being written after its "+
				"own first install", seg)
		}
	}
}

// TestStorePackagesGenStepRunsBeforeItsTwoConsumers is the CALL-SITE test, and it pins the
// two orderings the boot depends on rather than the presence of a line.
//
// Main() cannot be called from a test (it ends in execBash, which replaces the process),
// so the call site is pinned by reading the source — the same technique launcherdir_test.go
// and bootlog_test.go use. Both relations are real and both fail silently:
//
//   - generate_ld_cache scans the farm's lib dir, so a cache built first omits every
//     store-delivered library and `ldconfig -p` disagrees with what is loadable;
//   - generate_agent_launchers asks imageProbePath, which answers by stat'ing files, so a
//     launcher written before the farm exists shadows a declared tool.
func TestStorePackagesGenStepRunsBeforeItsTwoConsumers(t *testing.T) {
	path := filepath.Join(repoRoot(t), "internal", "entrypoint", "boot.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse boot.go: %v", err)
	}

	pos := map[string]int{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			if fn.Name == "genStep" && len(call.Args) >= 2 {
				lit, ok := call.Args[1].(*ast.BasicLit)
				if ok && lit.Kind == token.STRING {
					name := strings.Trim(lit.Value, `"`)
					if _, seen := pos[name]; !seen {
						pos[name] = fset.Position(call.Pos()).Offset
					}
				}
			}
			if fn.Name == "generateLdCache" {
				if _, seen := pos["generateLdCache"]; !seen {
					pos["generateLdCache"] = fset.Position(call.Pos()).Offset
					// The farm's lib dir must be what it is handed, or the extra-dir
					// parameter is dead and the cache silently omits the farm.
					if len(call.Args) != 1 {
						t.Errorf("generateLdCache is called with %d args, want 1 "+
							"(StorePackagesLib()) — without it the cache omits every "+
							"store-delivered library", len(call.Args))
					}
				}
			}
		}
		return true
	})

	farm, ok := pos["generate_store_packages"]
	if !ok {
		t.Fatal("boot.go has no genStep(\"generate_store_packages\", …) — the farm is " +
			"never built, so an opt-in launch boots a jail with neither the baked " +
			"packages nor the staged ones, and every other test in this file passes")
	}
	for _, consumer := range []string{"generateLdCache", "generate_agent_launchers"} {
		at, ok := pos[consumer]
		if !ok {
			t.Fatalf("boot.go no longer calls %s — this pin is not pinning anything; "+
				"fix the test rather than deleting it", consumer)
		}
		if farm > at {
			t.Errorf("generate_store_packages runs AFTER %s. It must run before: %s "+
				"reads the farm, and reading it before it exists fails silently", consumer, consumer)
		}
	}
}
