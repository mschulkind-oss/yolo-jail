package integration

import (
	"strconv"
	"strings"
	"testing"
)

// Lib-farm ("extra packages") tests. Each nix-builds a per-workspace image (the
// `packages` config triggers an
// --impure image rebuild), so these are the slowest tests in the suite; they are
// gated by requireJail(t) like every other container test.
//
// The in-jail `python3 -c 'ctypes.CDLL(...)'` probes are kept verbatim from the
// Python era on purpose: they exercise the *jail image's* python3 + ctypes (a
// product feature of the image — the lib farm exists so image python3 and other
// consumers can dlopen user-added libraries by bare soname). They are unaffected
// by the host-side Python ejection; do not "clean" them into a Go loader.

// lastNonEmptyLine returns the last non-empty line of s, or "" — ports the
// Python `(s.strip() or "0").splitlines()[-1]` idiom used to skip leading CLI
// notices before the payload line.
func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}

// TestExtraPackageLibFarm confirms three properties of a user `packages:` lib in
// ONE jail launch (the three checks share the identical `{"packages":["zbar"]}`
// config, so they build/boot the same --impure image; merged to pay the ~12-13s
// nix-rebuild + container cold-start ONCE instead of three times). Each check
// keeps its own marker and independent assertion, so failure attribution and
// every original coverage claim are preserved:
//
//  1. LIB-FARM SYMLINK — the .so is linked into /lib + /usr/lib, resolving into
//     the nix store. zbar is the canonical split-output case (its .so lives in a
//     separate `-lib` output), guarding against a naive `${pkg}/lib` impl.
//  2. DLOPEN-BY-SONAME — the image's python3/ctypes can dlopen it by bare soname
//     (the real consumer path, e.g. pyzbar). Works via LD_LIBRARY_PATH=/lib:/usr/lib
//     (the loader does NOT read /etc/ld.so.cache here — that's the mechanism).
//  3. FHS LD.SO.CACHE — build-time ldconfig populated /etc/ld.so.cache and it is
//     NOT empty. Regression guard for the `ldconfig -r $out` bug that produced a
//     0-entry cache. (`-C /etc/ld.so.cache` because bare `ldconfig -p` reads
//     $glibc/etc/ld.so.cache.)
//
// The in-jail `python3 -c 'ctypes.CDLL(...)'` probe is kept verbatim from the
// Python era on purpose (see file header); do not "clean" it into a Go loader.
func TestExtraPackageLibFarm(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{"network": {"mode": "bridge"}, "packages": ["zbar"]}`)
	// One launch, three probes, each fenced by a marker so we assert independently.
	r := runYolo(t, dir, strings.Join([]string{
		`echo "=== SYMLINK ==="; ls -l /lib/libzbar.so.0 /usr/lib/libzbar.so.0`,
		`echo "=== DLOPEN ==="; python3 -c 'import ctypes; ctypes.CDLL("libzbar.so.0"); print("dlopen-ok")'`,
		`echo "=== LDCACHE ==="; ldconfig -C /etc/ld.so.cache -p | grep -c libzbar || true`,
	}, "\n"))
	if r.rc != 0 {
		t.Fatalf("zbar lib-farm probe script failed (rc %d)\nstdout=%q\nstderr=%q",
			r.rc, r.stdout, r.stderr)
	}

	symlink := section(r.stdout, "=== SYMLINK ===", "=== DLOPEN ===")
	dlopen := section(r.stdout, "=== DLOPEN ===", "=== LDCACHE ===")
	ldcache := section(r.stdout, "=== LDCACHE ===", "")

	// 1. Lib-farm symlink resolves into the nix store (the -lib output).
	if !strings.Contains(symlink, "libzbar.so.0") || !strings.Contains(symlink, "/nix/store") {
		t.Fatalf("libzbar.so.0 not linked into /lib //usr/lib resolving to /nix/store:\n%s", symlink)
	}
	// 2. dlopen-by-soname works (the real consumer path).
	if !strings.Contains(dlopen, "dlopen-ok") {
		t.Fatalf("ctypes.CDLL(libzbar.so.0) by bare soname failed:\n%s", dlopen)
	}
	// 3. FHS /etc/ld.so.cache has libzbar and is not empty (the -r $out regression).
	line := lastNonEmptyLine(ldcache)
	if line == "" {
		line = "0"
	}
	count, err := strconv.Atoi(line)
	if err != nil {
		t.Fatalf("could not parse libzbar count from ldcache section:\n%s", ldcache)
	}
	if count < 1 {
		t.Fatalf("libzbar not in /etc/ld.so.cache (count=%d); cache may be empty "+
			"(the -r $out regression)\nstdout=%q", count, ldcache)
	}
}

// TestDevPackageLinksRuntimeLib confirms a `.dev` request links the package's
// *runtime* .so into /lib too: .dev is the documented way to make a library
// buildable (headers + .pc), so binaries linked against it must also be able to
// load it. Regression guard for the getLib-on-output-specified no-op, which left
// the farm without the runtime lib and every freshly linked binary failing at
// startup with "libfoo.so.N: cannot open shared object file".
//
// Uses libsodium, which is NOT part of the core/chromium lib stacks the image
// links unconditionally — so any /lib/libsodium.so* must come from this request.
// (freetype would be a false fixture: it's already linked via the chromium
// graphics stack regardless of the .dev request.) dlopens the versioned soname
// via glob so nixpkgs version bumps don't invalidate the fixture.
func TestDevPackageLinksRuntimeLib(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{"network": {"mode": "bridge"}, "packages": ["libsodium.dev"]}`)
	r := runYolo(t, dir,
		`python3 -c 'import ctypes, glob; `+
			`ctypes.CDLL(sorted(glob.glob("/lib/libsodium.so.*"))[0]); `+
			`print("dlopen-ok")'`)
	if r.rc != 0 {
		t.Fatalf("loading libsodium from /lib failed — the .dev request did not link "+
			"the runtime lib into the farm (rc %d)\nstdout=%q\nstderr=%q",
			r.rc, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "dlopen-ok") {
		t.Fatalf("expected dlopen-ok in stdout, got:\n%s", r.stdout)
	}
}
