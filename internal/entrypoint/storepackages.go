package entrypoint

// storepackages.go is the JAIL half of C4/C5 — "deliver `packages:` (and, under C5, the
// image's own bulk extras) from the MOUNTED nix store instead of baking them into the
// image" (docs/design/image-staging-vs-baking.md §4 C4/C5, shape ruled by OQ-1).
//
// The HOST realized one or more `buildEnv` profiles and handed their store paths over on
// YOLO_STORE_PROFILES. Everything below is symlinking: each profile's `bin` into one PATH
// dir, its `lib/lib*.so*` into one LD_LIBRARY_PATH dir, its `lib/pkgconfig/*.pc` into one
// PKG_CONFIG_PATH dir. The targets resolve because the launch bind-mounts /nix/store
// (§3.2) — which is also why the HOST decides eligibility and this file does not: from in
// here, "this host cannot share its store" and "the operator did not opt in" are the same
// observation, the same way the reachability witness cannot derive YOLO_HOST_LOOPBACK.
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

// StorePackagesRoot is the boot-written farm's root on the /run tmpfs.
const StorePackagesRoot = "/run/yolo/packages"

// StorePackagesBin is the farm's PATH dir. It sits immediately before /bin in BootPath —
// the position a `packages:` binary occupies today, since today it IS in /bin — so the
// blockers, the launchers and every per-project install prefix keep the precedence they
// already have over a baked tool.
func StorePackagesBin() string { return storeBinDir(StorePackagesRoot) }

// StorePackagesLib is the farm's LD_LIBRARY_PATH dir — the store-delivered twin of the
// image's /lib farm, and the reason ldconfig is handed it explicitly (see generateLdCache).
func StorePackagesLib() string { return storeLibDir(StorePackagesRoot) }

// StorePackagesPkgConfig is the farm's PKG_CONFIG_PATH dir. It is nested under the lib dir
// on purpose: that mirrors the image's own /lib/pkgconfig, which the baked PKG_CONFIG_PATH
// already names first, so the two mechanisms have the same shape.
func StorePackagesPkgConfig() string { return storePkgConfigDir(StorePackagesRoot) }

// The three dirs, derived from a root. Parameterized on the root ONLY so the farm builder
// is testable off a t.TempDir(): /run is a container tmpfs, and a unit test that writes to
// the real path either needs root or fails on a CI runner. Production has exactly one root
// and the three exported accessors above are the only spelling of it.
func storeBinDir(root string) string       { return filepath.Join(root, "bin") }
func storeLibDir(root string) string       { return filepath.Join(root, "lib") }
func storePkgConfigDir(root string) string { return filepath.Join(storeLibDir(root), "pkgconfig") }

// StoreProfiles parses StoreProfilesEnv into the ordered profile list. A nil result is the
// authoritative "this launch bakes its packages" answer, and it is what every consumer
// (imageProbePath, the ldconfig extra dir) reads rather than probing for the dirs — the
// declaration is the claim; a directory is only its consequence.
func StoreProfiles(e *Env) []string {
	raw := e.Getenv(StoreProfilesEnv)
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
func GenerateStorePackages(e *Env) error { return generateStorePackagesIn(e, StorePackagesRoot) }

// generateStorePackagesIn is the whole of the generator, parameterized on the farm root so
// a test can exercise it off a t.TempDir(). Production has exactly one root.
//
// The env exports come AFTER the farm and only on success: a jail whose LD_LIBRARY_PATH
// names a directory that was never built is a worse diagnosis than one whose boot refused.
func generateStorePackagesIn(e *Env, root string) error {
	profiles := StoreProfiles(e)
	if err := buildStorePackageFarm(root, profiles); err != nil {
		return err
	}
	if len(profiles) == 0 {
		return nil
	}
	// Prepended rather than replaced: the image bakes LD_LIBRARY_PATH=/lib:/usr/lib:… and
	// PKG_CONFIG_PATH=/lib/pkgconfig:…, and the /lib farm those name still carries the
	// image's own libraries.
	prependPathVar(e, "LD_LIBRARY_PATH", storeLibDir(root))
	prependPathVar(e, "PKG_CONFIG_PATH", storePkgConfigDir(root))
	return nil
}

// buildStorePackageFarm is the filesystem half: clear the farm under root, then link every
// profile into it.
//
// PRECEDENCE IS FIRST-WINS, across profiles and within one, using the same `[ ! -e ]`
// idiom flake.nix's /lib farm uses for exactly the same reason: the host orders the
// profiles (user `packages:` ahead of the image extras), and a later profile may not
// silently retarget a name an earlier one already claimed.
func buildStorePackageFarm(root string, profiles []string) error {
	if len(profiles) == 0 {
		// Not opted in. Clear rather than create: an `exec` back into a live container
		// re-runs the boot, so a launch that stopped opting in must not inherit the
		// previous one's farm — and creating the dirs here would put an empty directory
		// on PATH for every jail on every backend, which is noise at best.
		for _, d := range []string{storeBinDir(root), storeLibDir(root)} {
			_ = ClearContents(d)
		}
		return nil
	}
	for _, d := range []string{storeBinDir(root), storeLibDir(root)} {
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
	for _, profile := range profiles {
		if err := linkStoreProfile(profile, root); err != nil {
			return err
		}
	}
	return nil
}

// linkStoreProfile links one profile's bin/, lib/lib*.so* and lib/pkgconfig/*.pc into the
// farm under root.
//
// A PROFILE THAT IS NOT THERE AT ALL IS AN ERROR, and that is the one check here that
// earns its place. The host resolved the path against the HOST's store; if it does not
// resolve in here, /nix/store is not mounted the way the host believed it was — and
// linking nothing would then produce a silently tool-less jail, which is the half-state
// R2 exists to make unrepresentable. A profile that exists but has no lib/ or no bin/ is
// ordinary (a bin-only or headers-only closure) and links nothing without complaint.
func linkStoreProfile(profile, root string) error {
	info, err := os.Stat(profile)
	if err != nil || !info.IsDir() {
		return &storeProfileError{profile: profile, err: err}
	}
	if err := linkDirEntries(filepath.Join(profile, "bin"), storeBinDir(root), nil); err != nil {
		return err
	}
	if err := linkDirEntries(filepath.Join(profile, "lib"), storeLibDir(root), isSharedObject); err != nil {
		return err
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
