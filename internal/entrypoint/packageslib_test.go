package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unsetLDLibraryPath clears LD_LIBRARY_PATH for the test and restores it afterwards.
func unsetLDLibraryPath(t *testing.T) {
	t.Helper()
	t.Setenv("LD_LIBRARY_PATH", "")
	_ = os.Unsetenv("LD_LIBRARY_PATH")
}

func TestExportImageLDLibSkipsAnEmptyOrMissingFarm(t *testing.T) {
	for name, dir := range map[string]string{
		"empty":   t.TempDir(),
		"missing": filepath.Join(t.TempDir(), "absent"),
	} {
		t.Run(name, func(t *testing.T) {
			unsetLDLibraryPath(t)
			e := NewEnv(map[string]string{})
			exportImageLDLibFrom(e, dir)
			if v, ok := e.Vars["LD_LIBRARY_PATH"]; ok {
				t.Errorf("an empty or missing farm exported LD_LIBRARY_PATH=%q", v)
			}
			if v, ok := os.LookupEnv("LD_LIBRARY_PATH"); ok {
				t.Errorf("process env LD_LIBRARY_PATH=%q, want unset", v)
			}
		})
	}
}

func TestExportImageLDLibPrependsAFilledFarmOnce(t *testing.T) {
	unsetLDLibraryPath(t)
	dir := t.TempDir()
	if err := os.Symlink("/nix/store/x-zbar-lib/lib/libzbar.so.0", filepath.Join(dir, "libzbar.so.0")); err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{})
	setEnvBoth(e, "LD_LIBRARY_PATH", "/opt/custom/lib")
	exportImageLDLibFrom(e, dir)
	exportImageLDLibFrom(e, dir) // an exec re-runs the boot
	want := dir + ":/opt/custom/lib"
	if got := e.Getenv("LD_LIBRARY_PATH"); got != want {
		t.Errorf("LD_LIBRARY_PATH = %q, want %q", got, want)
	}
	if got := os.Getenv("LD_LIBRARY_PATH"); got != want {
		t.Errorf("process LD_LIBRARY_PATH = %q, want %q", got, want)
	}
}

// THE GUARANTEE THAT /lib LEFT LD_LIBRARY_PATH FOR, KEPT: a jail whose image (or launch)
// still carries the old
// baked LD_LIBRARY_PATH ends the boot with the image LD farm on it and NOT the merged
// tree's glibc dirs. The farm's path must also survive the scrub itself, which drops every
// /usr/lib/… entry — the reason it lives under /usr/local.
func TestTheBootPutsTheImageLDFarmButNoGlibcDirOnLDLibraryPath(t *testing.T) {
	legacy := "/lib:/usr/lib:/usr/lib/x86_64-linux-gnu"
	t.Setenv("LD_LIBRARY_PATH", legacy)
	e := NewEnv(map[string]string{"LD_LIBRARY_PATH": legacy})
	scrubLegacyLDLibraryPath(e)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "libzbar.so.0"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	exportImageLDLibFrom(e, dir)

	got := e.Getenv("LD_LIBRARY_PATH")
	for _, seg := range strings.Split(got, ":") {
		if seg == "/lib" || seg == "/usr/lib" || strings.HasPrefix(seg, "/usr/lib/") {
			t.Errorf("LD_LIBRARY_PATH %q still names the merged tree's %s, which carries glibc", got, seg)
		}
	}
	if got != dir {
		t.Errorf("LD_LIBRARY_PATH = %q, want exactly the image LD farm %q", got, dir)
	}

	e2 := NewEnv(map[string]string{"LD_LIBRARY_PATH": ImageLDLib + ":/lib"})
	t.Setenv("LD_LIBRARY_PATH", ImageLDLib+":/lib")
	scrubLegacyLDLibraryPath(e2)
	if got := e2.Getenv("LD_LIBRARY_PATH"); got != ImageLDLib {
		t.Errorf("the scrub turned %q into %q; the image LD farm must survive it",
			ImageLDLib+":/lib", got)
	}
}

// The CALL SITE: the boot table runs the export, and after the store farm's, so a baked farm
// would be searched first should both ever be filled.
func TestExportPackagesLibStepIsWired(t *testing.T) {
	if !isRun(mustBootStep(t, "export_image_ld_lib"), exportImageLDLibStep) {
		t.Fatal("export_image_ld_lib no longer runs exportImageLDLibStep — a baked " +
			"`packages:` library is then not dlopen-able by bare soname from python3")
	}
	assertStepBefore(t, bootContainer, "generate_store_packages", "export_image_ld_lib",
		"the baked farm must be prepended last so it is searched first")
}

func TestIsGlibcStorePath(t *testing.T) {
	for path, want := range map[string]bool{
		"/nix/store/h4wfwic161kxrr74jlzla5lsm28hgary-glibc-2.44-25/lib/libc.so.6": true,
		"/nix/store/h4wfwic161kxrr74jlzla5lsm28hgary-glibc-2.42-51/lib/libm.so.6": true,
		"/nix/store/h4wfwic161kxrr74jlzla5lsm28hgary-glibc-locales-2.44/lib/x":    false,
		"/nix/store/h4wfwic161kxrr74jlzla5lsm28hgary-glib-2.88.3/lib/libglib.so":  false,
		"/nix/store/h4wfwic161kxrr74jlzla5lsm28hgary-zbar-0.23.93-lib/lib/a.so":   false,
		"/lib/libc.so.6": false,
	} {
		if got := isGlibcStorePath(path); got != want {
			t.Errorf("isGlibcStorePath(%q) = %v, want %v", path, got, want)
		}
	}
}

// A user `packages:` profile that carries glibc must not put it in the store farm, whose lib
// dir is on LD_LIBRARY_PATH: that is the GLIBC_PRIVATE crash by another route.
func TestTheStoreFarmLinksNoGlibc(t *testing.T) {
	profile := t.TempDir()
	lib := filepath.Join(profile, "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"libc.so.6":    "/nix/store/h4wfwic161kxrr74jlzla5lsm28hgary-glibc-2.44-25/lib/libc.so.6",
		"libzbar.so.0": "/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-zbar-0.23.93-lib/lib/libzbar.so.0",
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(lib, name)); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	for _, d := range []string{storeBinDir(root), storePkgConfigDir(root)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := linkStoreProfile(profile, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(storeLibDir(root), "libc.so.6")); err == nil {
		t.Error("the store farm linked glibc's libc.so.6 onto LD_LIBRARY_PATH")
	}
	if _, err := os.Lstat(filepath.Join(storeLibDir(root), "libzbar.so.0")); err != nil {
		t.Errorf("the store farm dropped libzbar.so.0: %v", err)
	}
}
