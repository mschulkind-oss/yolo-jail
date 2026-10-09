package entrypoint

// storepackages.go is the JAIL half of C4/C5 — "deliver `packages:` (and, under C5, the
// image's own bulk extras) from the MOUNTED nix store instead of baking them into the
// image" (docs/reference/image-staging-vs-baking.md, "Store-delivered packages"; shape ruled
// by its OQ-1).
//
// The HOST realized one or more `buildEnv` profiles and handed their store paths over on
// YOLO_STORE_PROFILES (the workspace's `packages:`) and YOLO_STORE_FHS_PROFILES (the image
// extras). Everything below is symlinking: each profile's `bin` into one PATH dir, its
// `lib/pkgconfig/*.pc` into one PKG_CONFIG_PATH dir, and its `lib/lib*.so*` into one
// nix-ld dir (every profile) and one LD_LIBRARY_PATH dir (`packages:` profiles only). The
// targets resolve because the launch bind-mounts /nix/store (§3.2) — which is also why the
// HOST decides eligibility and this file does not: from in here, "this host cannot share its
// store" and "the operator did not opt in" are the same observation, the same way the
// reachability witness cannot derive YOLO_HOST_LOOPBACK.
//
// WHY A FIXED DIR RATHER THAN THE PROFILE'S OWN bin ON PATH. The profile's store path
// moves whenever `packages:` moves, and five separate things have to name this location:
// BootPath, the .bashrc's second spelling of it, imageProbePath (the launcher-collision
// check), ldconfig's scan list, and LD_LIBRARY_PATH. A store hash in any of them is a
// value that must be threaded and can drift; /run/yolo/packages is a constant. The fixed
// dir also lets ONE farm hold SEVERAL profiles with first-wins precedence, which is how a
// user `packages:` entry stays ahead of the same name in the image-extras profile without
// asking nix to resolve a `buildEnv` collision it would rather abort on.
//
// /run is a tmpfs (`--tmpfs /run`, internal/cli/run/assemble_parts.go), so this dir is
// writable under the --read-only root and starts empty on every boot. That is the right
// lifetime: the farm is derived entirely from THIS launch's environment, so unlike
// ~/.yolo/bin there is no persistent anchor to preserve and nothing stale to sweep.
//
// R2, RESTATED WHERE IT IS ENFORCED: a package both baked and staged silently runs the
// BAKED copy, so the two mechanisms are per-LAUNCH alternatives, never per-package. This
// file assumes the host has already built an image WITHOUT the packages it is about to
// link; the assumption is discharged in internal/cli/run/imageload.go, which is the only
// place that can.

import (
	"os"
	"path/filepath"
	"strings"
)

// StoreProfilesEnv is the launch→boot contract: a ":"-separated, PRECEDENCE-ORDERED list
// of `buildEnv` store paths whose contents this jail delivers. Empty or absent means the
// launch did not opt in and every package is baked into the image, which is the default.
//
// ":" is safe as the separator for the same reason PATH uses it: a nix store path's name
// is drawn from [A-Za-z0-9+._?=-] and can never contain a colon.
const StoreProfilesEnv = "YOLO_STORE_PROFILES"

// StoreFHSProfilesEnv is the second half of the contract: profiles linked BEHIND
// StoreProfilesEnv's, whose libraries reach FHS binaries only (StorePackagesFHSLib, named
// in nix-ld's compiled-in path) and never LD_LIBRARY_PATH. The launcher puts the image
// extras (`.#yoloImageExtras`) here, because their chromium stack is the same newer-glibc
// class the baked image keeps in ImageFHSLib: glib needs GLIBC_2.43, so a nix program
// built on glibc 2.42 that NEEDs libglib fails to start with this glib on its
// LD_LIBRARY_PATH. Same syntax as StoreProfilesEnv.
const StoreFHSProfilesEnv = "YOLO_STORE_FHS_PROFILES"

// StorePackagesRoot is the boot-written farm's root on the /run tmpfs.
const StorePackagesRoot = "/run/yolo/packages"

// StorePackagesBin is the farm's PATH dir. It sits immediately before /bin in BootPath —
// the position a `packages:` binary occupies today, since today it IS in /bin — so the
// blockers, the launchers and every per-project install prefix keep the precedence they
// already have over a baked tool.
func StorePackagesBin() string { return storeBinDir(StorePackagesRoot) }

// StorePackagesLib is the farm's LD_LIBRARY_PATH dir — the store-delivered twin of the
// image's ImageLDLib, so it holds the StoreProfilesEnv profiles' libraries and no image
// extras. ldconfig is handed it explicitly (see generateLdCache).
func StorePackagesLib() string { return storeLibDir(StorePackagesRoot) }

// StorePackagesFHSLib is the farm's nix-ld dir — the store-delivered twin of ImageFHSLib:
// every profile's libraries, glibc excepted, first-wins. It is never on LD_LIBRARY_PATH.
// flake.nix's nixLd names this exact path after ImageFHSLib in its compiled-in library
// path, so an FHS binary finds it even under `env -i`; on a baked launch the directory
// does not exist and the loader skips it.
func StorePackagesFHSLib() string { return storeFHSLibDir(StorePackagesRoot) }

// StorePackagesPkgConfig is the farm's PKG_CONFIG_PATH dir. It is nested under the lib dir
// on purpose: that mirrors the image's own /lib/pkgconfig, which the baked PKG_CONFIG_PATH
// already names first, so the two mechanisms have the same shape.
func StorePackagesPkgConfig() string { return storePkgConfigDir(StorePackagesRoot) }

// ImageLDLib is the image's LD_LIBRARY_PATH farm (flake.nix, mkBinPathLinks): the C++
// runtime and zlib (libstdc++, libgcc_s, libz and the rest of stdenv.cc.cc.lib) plus the lib
// outputs of the workspace's `packages:`, and nothing else. It is the ONE image directory
// that goes on LD_LIBRARY_PATH.
//
// WHY IT EXISTS. A nix-built process (the image's python3) finds a library by bare soname
// only through LD_LIBRARY_PATH or a RUNPATH, because nixpkgs' ld.so reads its cache from
// $glibc/etc/ld.so.cache in the read-only store and never /etc/ld.so.cache. So a pip
// wheel's extension (NEEDED libstdc++.so.6, libgcc_s.so.1, libz.so.1, no RUNPATH) and a
// ctypes.CDLL of a `packages:` library both need this directory.
//
// WHY NOT /lib, AND WHY NOT THE WHOLE FARM. /lib carries the merged tree's glibc, which an
// LD_LIBRARY_PATH search hands to every nix binary ahead of its own: a program linked
// against an older glibc then crashes on a GLIBC_PRIVATE lookup, which is why
// scrubLegacyLDLibraryPath strips /lib and /usr/lib. The rest of the farm is held back for a
// second reason: a library built on this image's glibc can need a newer glibc symbol version
// than an older program's glibc provides (glib needs GLIBC_2.43, and a glibc-2.42 program
// that NEEDs libglib then fails to start). The C++ runtime and zlib need at most
// GLIBC_2.38. FHS binaries get the whole farm, glibc excepted, through nix-ld's default path
// (ImageFHSLib) instead.
//
// Outside /usr/lib on purpose: scrubLegacyLDLibraryPath drops every /usr/lib/… entry.
const ImageLDLib = "/usr/local/lib/yolo-ld"

// ImageFHSLib is the image's glibc-free copy of the whole /lib farm. It is never on
// LD_LIBRARY_PATH: nix-ld's compiled-in library path names it (flake.nix, nixLd), so only
// FHS binaries search it, including under `env -i`. Named here so the boot and its tests
// share one spelling.
const ImageFHSLib = "/usr/local/lib/yolo-fhs"

// ExportImageLDLib prepends ImageLDLib to LD_LIBRARY_PATH when the image's farm holds
// anything. An image older than the farm has no such directory and exports nothing.
func ExportImageLDLib(e *Env) {
	exportImageLDLibFrom(e, ImageLDLib)
}

// exportImageLDLibStep is the boot step table's body for export_image_ld_lib, named so a
// test can pin that the table really runs it.
func exportImageLDLibStep(b *bootRun) { ExportImageLDLib(b.e) }

func exportImageLDLibFrom(e *Env, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return
	}
	prependPathVar(e, "LD_LIBRARY_PATH", dir)
}

// The three dirs, derived from a root. Parameterized on the root ONLY so the farm builder
// is testable off a t.TempDir(): /run is a container tmpfs, and a unit test that writes to
// the real path either needs root or fails on a CI runner. Production has exactly one root
// and the three exported accessors above are the only spelling of it.
func storeBinDir(root string) string       { return filepath.Join(root, "bin") }
func storeLibDir(root string) string       { return filepath.Join(root, "lib") }
func storePkgConfigDir(root string) string { return filepath.Join(storeLibDir(root), "pkgconfig") }
func storeFHSLibDir(root string) string    { return filepath.Join(root, "fhs-lib") }

// StoreProfiles is every store-delivered profile in precedence order: StoreProfilesEnv's,
// then StoreFHSProfilesEnv's. A nil result is the authoritative "this launch bakes its
// packages" answer, and it is what every consumer (imageProbePath, the ldconfig extra dir)
// reads rather than probing for the dirs — the declaration is the claim; a directory is
// only its consequence.
func StoreProfiles(e *Env) []string {
	ld, fhs := storeProfileLists(e)
	return append(ld, fhs...)
}

// storeProfileLists is the two halves of the contract, parsed: the profiles whose libraries
// go on LD_LIBRARY_PATH, and those whose libraries reach FHS binaries only.
func storeProfileLists(e *Env) (ld, fhsOnly []string) {
	return parseProfileList(e.Getenv(StoreProfilesEnv)), parseProfileList(e.Getenv(StoreFHSProfilesEnv))
}

func parseProfileList(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ":") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// GenerateStorePackages builds the farm from StoreProfilesEnv and exports the two
// variables it is useless without. A genStep, so a failure is FATAL and collected with
// every other generator's — which is the polarity this needs: an opt-in launch built an
// image with the packages left OUT, so a farm that half-materializes yields a jail missing
// tools the workspace declared, with no other copy to fall back on.
func GenerateStorePackages(e *Env) error {
	return generateStorePackagesIn(e, StorePackagesRoot, fontsRoot, imageFontsConf)
}

// generateStorePackagesIn is the whole of the generator, parameterized on the three
// locations it writes to or reads so a test can exercise it off a t.TempDir(). Production
// has exactly one of each, named by the constants above.
//
// The env exports come AFTER the farm and only on success: a jail whose LD_LIBRARY_PATH
// names a directory that was never built is a worse diagnosis than one whose boot refused.
func generateStorePackagesIn(e *Env, root, fontsDir, imageConf string) error {
	ld, fhsOnly := storeProfileLists(e)
	if err := buildStorePackageFarm(e, root, ld, fhsOnly); err != nil {
		return err
	}
	profiles := append(append([]string(nil), ld...), fhsOnly...)
	if len(profiles) == 0 {
		return nil
	}
	// Prepended rather than replaced: the image bakes PKG_CONFIG_PATH=/lib/pkgconfig:…, and
	// the /lib farm that names still carries the image's own .pc files. LD_LIBRARY_PATH is
	// normally unset by now (scrubLegacyLDLibraryPath), but a user's own entries survive.
	//
	// LD_LIBRARY_PATH only when a `packages:` profile was delivered: the lib dir holds only
	// those profiles' libraries, and the fhs-lib dir is never exported (nix-ld names it).
	if len(ld) > 0 {
		prependPathVar(e, "LD_LIBRARY_PATH", storeLibDir(root))
	}
	prependPathVar(e, "PKG_CONFIG_PATH", storePkgConfigDir(root))
	configureStoreFontconfig(e, profiles, fontsDir, imageConf)
	return nil
}

const (
	// fontsRoot is where the boot writes a fontconfig config for store-delivered fonts.
	fontsRoot = "/run/yolo/fonts"
	// imageFontsConf is the config the IMAGE bakes (mkBinPathLinks' `withChromium` half
	// symlinks /etc/fonts at fontconfig's store path). Its presence is the whole test for
	// "this launch still has its own fonts", so a baked jail is untouched.
	imageFontsConf = "/etc/fonts/fonts.conf"
)

// configureStoreFontconfig is C5's font half, and it exists because moving `fullPackages`
// out of the image takes THREE pieces of baked content with chromium, not one:
// /usr/bin/chromium, the /etc/fonts symlink into fontconfig's store path, and the font
// dirs linked into /usr/share/fonts (flake.nix's `withChromium` block). The image bakes
// FONTCONFIG_FILE=/etc/fonts/fonts.conf and FONTCONFIG_PATH=/etc/fonts, and on a lean
// image neither exists — so fontconfig falls back to a compiled-in default that names
// paths this image does not have, and chromium renders with no fonts at all.
//
// The root filesystem is --read-only, so /etc/fonts and /usr/share/fonts cannot be
// recreated. What CAN be done is point fontconfig somewhere writable: a tiny config on the
// /run tmpfs that includes the profile's own fonts.conf (whose relative `<include>conf.d`
// then resolves inside the profile, so the upstream rule set comes along) and adds the
// profile's share/fonts as a font dir.
//
// A NO-OP WHENEVER THE IMAGE STILL HAS ITS OWN, which is every baked launch: the check is
// for /etc/fonts/fonts.conf, so a jail that bakes keeps the baked config and this function
// never writes anything. Best-effort — fonts are a rendering concern, not a boot
// invariant, so a failure here warns rather than refusing the launch that GenerateShims'
// failure would.
func configureStoreFontconfig(e *Env, profiles []string, fontsDir, imageConf string) {
	if pathExists(imageConf) {
		return
	}
	var profile string
	for _, p := range profiles {
		if pathExists(filepath.Join(p, "etc", "fonts", "fonts.conf")) {
			profile = p
			break
		}
	}
	if profile == "" {
		return
	}
	cacheDir := filepath.Join(fontsDir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		e.warn("store fontconfig: " + err.Error())
		return
	}
	conf := `<?xml version="1.0"?>
<!DOCTYPE fontconfig SYSTEM "urn:fontconfig:fonts.dtd">
<fontconfig>
  <include ignore_missing="yes">` + filepath.Join(profile, "etc", "fonts", "fonts.conf") + `</include>
  <dir>` + filepath.Join(profile, "share", "fonts") + `</dir>
  <cachedir>` + cacheDir + `</cachedir>
</fontconfig>
`
	confPath := filepath.Join(fontsDir, "fonts.conf")
	if err := os.WriteFile(confPath, []byte(conf), 0o644); err != nil {
		e.warn("store fontconfig: " + err.Error())
		return
	}
	setEnvBoth(e, "FONTCONFIG_FILE", confPath)
	setEnvBoth(e, "FONTCONFIG_PATH", fontsDir)
}

// buildStorePackageFarm is the filesystem half: clear the farm under root, then link every
// profile into it — ld's first, then fhsOnly's.
//
// PRECEDENCE IS FIRST-WINS, across profiles and within one, using the same `[ ! -e ]`
// idiom flake.nix's /lib farm uses for exactly the same reason: the host orders the
// profiles (user `packages:` ahead of the image extras), and a later profile may not
// silently retarget a name an earlier one already claimed.
//
// THE LIB SPLIT mirrors the baked image's two farms. The lib dir (LD_LIBRARY_PATH, like
// ImageLDLib) gets ld's libraries only; the fhs-lib dir (nix-ld, like ImageFHSLib) gets
// every profile's. So a `packages:` library stays dlopen-able by bare soname, and the
// image extras' chromium stack — glib needs a newer glibc than an older nix program has —
// reaches FHS binaries without being handed to every nix program.
func buildStorePackageFarm(e *Env, root string, ld, fhsOnly []string) error {
	dirs := []string{storeBinDir(root), storeLibDir(root), storeFHSLibDir(root)}
	if len(ld)+len(fhsOnly) == 0 {
		// Not opted in. Clear rather than create: an `exec` back into a live container
		// re-runs the boot, so a launch that stopped opting in must not inherit the
		// previous one's farm — and creating the dirs here would put an empty directory
		// on PATH for every jail on every backend, which is noise at best.
		//
		// A FAILED CLEAR IS REPORTED, and it is not cosmetic: the farm's bin dir sits
		// IMMEDIATELY BEFORE /bin on BootPath, so a surviving symlink from the previous
		// entry's farm SHADOWS the baked binary of the same name — the exact inversion of
		// R2's "a package both baked and staged silently runs the BAKED copy". Reporting
		// rather than returning keeps the polarity right: this is a re-entry into a jail
		// that is NOT opted in, so every tool it needs is baked and present, and refusing
		// the launch over a stale symlink would be the worse outcome.
		for _, d := range dirs {
			if err := ClearContents(d); err != nil {
				e.warn("Warning: this launch does not opt into store-delivered packages, " +
					"but the previous entry's farm at " + d + " could not be cleared: " +
					err.Error() + "; it precedes /bin on PATH, so any symlink left in it " +
					"shadows the baked binary of the same name")
			}
		}
		return nil
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
		if err := ClearContents(d); err != nil {
			return err
		}
	}
	// After the lib dir is cleared, never before: pkgconfig lives inside it.
	if err := os.MkdirAll(storePkgConfigDir(root), 0o755); err != nil {
		return err
	}
	for _, profile := range ld {
		if err := linkStoreProfile(profile, root, true); err != nil {
			return err
		}
	}
	for _, profile := range fhsOnly {
		if err := linkStoreProfile(profile, root, false); err != nil {
			return err
		}
	}
	return nil
}

// linkStoreProfile links one profile's bin/, lib/lib*.so* and lib/pkgconfig/*.pc into the
// farm under root. Its libraries always go into the fhs-lib dir, and into the
// LD_LIBRARY_PATH lib dir only when onLDPath.
//
// A PROFILE THAT IS NOT THERE AT ALL IS AN ERROR, and that is the one check here that
// earns its place. The host resolved the path against the HOST's store; if it does not
// resolve in here, /nix/store is not mounted the way the host believed it was — and
// linking nothing would then produce a silently tool-less jail, which is the half-state
// R2 exists to make unrepresentable. A profile that exists but has no lib/ or no bin/ is
// ordinary (a bin-only or headers-only closure) and links nothing without complaint.
func linkStoreProfile(profile, root string, onLDPath bool) error {
	info, err := os.Stat(profile)
	if err != nil || !info.IsDir() {
		return &storeProfileError{profile: profile, err: err}
	}
	if err := linkDirEntries(filepath.Join(profile, "bin"), storeBinDir(root), nil); err != nil {
		return err
	}
	keepLib := isNonGlibcSharedObject(profile)
	if err := linkDirEntries(filepath.Join(profile, "lib"), storeFHSLibDir(root), keepLib); err != nil {
		return err
	}
	if onLDPath {
		if err := linkDirEntries(filepath.Join(profile, "lib"), storeLibDir(root), keepLib); err != nil {
			return err
		}
	}
	return linkDirEntries(filepath.Join(profile, "lib", "pkgconfig"), storePkgConfigDir(root),
		func(name string) bool { return strings.HasSuffix(name, ".pc") })
}

// linkDirEntries symlinks every entry of src into dst that keep(name) accepts (nil accepts
// all), skipping any name dst already carries. A missing src is not an error.
func linkDirEntries(src, dst string, keep func(string) bool) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, ent := range entries {
		name := ent.Name()
		if keep != nil && !keep(name) {
			continue
		}
		link := filepath.Join(dst, name)
		if _, err := os.Lstat(link); err == nil {
			continue // the first profile to claim a name keeps it
		}
		if err := os.Symlink(filepath.Join(src, name), link); err != nil {
			return err
		}
	}
	return nil
}

// isSharedObject matches the `lib*.so*` glob the flake's own /lib farm uses, so a package
// delivered from the store lands the same set of names it lands when baked.
func isSharedObject(name string) bool {
	return strings.HasPrefix(name, "lib") && strings.Contains(name, ".so")
}

// isNonGlibcSharedObject is isSharedObject minus every library that resolves into a glibc
// store path. The farm's lib dir is on LD_LIBRARY_PATH, so a glibc linked into it would be
// handed to every nix binary ahead of the glibc its own interpreter belongs to — the
// GLIBC_PRIVATE crash scrubLegacyLDLibraryPath exists to prevent. The fhs-lib dir makes
// the same exclusion, as ImageFHSLib does: an FHS binary's glibc is nix-ld's. The baked farm makes the
// same exclusion in flake.nix; here it is by the resolved target, because a profile is
// opaque until it is read.
func isNonGlibcSharedObject(profile string) func(string) bool {
	libDir := filepath.Join(profile, "lib")
	return func(name string) bool {
		if !isSharedObject(name) {
			return false
		}
		path := filepath.Join(libDir, name)
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			// Dangling: judge it by the link it names, which is what it would load.
			if target, err = os.Readlink(path); err != nil {
				return true
			}
		}
		return !isGlibcStorePath(target)
	}
}

// isGlibcStorePath reports whether path lies in a glibc output's store path
// (/nix/store/<hash>-glibc-<version>…), the only package that ships the loader-coupled
// libc, libm, libpthread and friends.
func isGlibcStorePath(path string) bool {
	const store = "/nix/store/"
	if !strings.HasPrefix(path, store) {
		return false
	}
	rest := path[len(store):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	dash := strings.IndexByte(rest, '-')
	if dash < 0 {
		return false
	}
	name := rest[dash+1:]
	return strings.HasPrefix(name, "glibc-") && len(name) > len("glibc-") &&
		name[len("glibc-")] >= '0' && name[len("glibc-")] <= '9'
}

// storeProfileError names the profile that could not be read and says what that means —
// the message is the whole value here, because the operator meets it as a refused boot.
type storeProfileError struct {
	profile string
	err     error
}

func (e *storeProfileError) Error() string {
	msg := "store-delivered packages: " + e.profile + " is not a readable directory in " +
		"this jail. The launch opted into store delivery, so this profile is the ONLY " +
		"copy of the packages it holds — they were deliberately left out of the image. " +
		"The usual cause is that /nix/store is not bind-mounted (podman + Linux + a " +
		"running nix daemon are all required); re-launch without YOLO_STORE_PACKAGES to " +
		"go back to the baked image"
	if e.err != nil {
		msg += ": " + e.err.Error()
	}
	return msg
}

// prependPathVar puts dir at the front of a ":"-separated env var, in BOTH the process env
// and e.Vars, and does nothing when it is already present (an `exec` into a live container
// re-runs the boot, and a var that grows a duplicate entry per re-entry is a slow leak).
func prependPathVar(e *Env, key, dir string) {
	cur := e.Getenv(key)
	if cur == "" {
		setEnvBoth(e, key, dir)
		return
	}
	for _, seg := range strings.Split(cur, ":") {
		if seg == dir {
			return
		}
	}
	setEnvBoth(e, key, dir+":"+cur)
}
