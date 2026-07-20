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

// TestExtraPackageLibInLibFarm confirms a package added to `packages` for its
// shared library is symlinked into /lib and /usr/lib so it is dlopen-able.
//
// zbar is the canonical split-output case: its .so lives in a separate `-lib`
// output (the default output has binaries, no lib/), so this also guards against
// a naive `${pkg}/lib` implementation that would miss it.
func TestExtraPackageLibInLibFarm(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{"network": {"mode": "bridge"}, "packages": ["zbar"]}`)
	r := runYolo(t, dir, "ls -l /lib/libzbar.so.0 /usr/lib/libzbar.so.0")
	if r.rc != 0 {
		t.Fatalf("libzbar.so.0 not linked into /lib or /usr/lib (rc %d)\nstdout=%q\nstderr=%q",
			r.rc, r.stdout, r.stderr)
	}
	// The symlink must resolve into the nix store (the -lib output).
	if !strings.Contains(r.stdout, "/nix/store") {
		t.Fatalf("expected /lib symlink to resolve into /nix/store, got:\n%s", r.stdout)
	}
}

// TestExtraPackageLibDlopenByName confirms a user `packages:` lib is dlopen-able
// by bare soname — the real consumer path (e.g. pyzbar's
// ctypes.CDLL("libzbar.so.0")).
//
// This works via LD_LIBRARY_PATH=/lib:/usr/lib (set in the image env + entrypoint),
// which is how the nixpkgs glibc loader finds the symlinked libs. The loader does
// NOT consult /etc/ld.so.cache in this image (it reads its cache from
// $glibc/etc/ld.so.cache, a read-only store path), so LD_LIBRARY_PATH is the
// mechanism under test here.
func TestExtraPackageLibDlopenByName(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{"network": {"mode": "bridge"}, "packages": ["zbar"]}`)
	r := runYolo(t, dir,
		`python3 -c 'import ctypes; ctypes.CDLL("libzbar.so.0"); print("dlopen-ok")'`)
	if r.rc != 0 {
		t.Fatalf("ctypes.CDLL(libzbar.so.0) failed (rc %d)\nstdout=%q\nstderr=%q",
			r.rc, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "dlopen-ok") {
		t.Fatalf("expected dlopen-ok in stdout, got:\n%s", r.stdout)
	}
}

// TestExtraPackageLibInFhsLdcache confirms the build-time ldconfig populates
// /etc/ld.so.cache (the FHS path) so tools that read it explicitly see the lib —
// and, critically, that the cache is NOT empty. Regression guard for the prior
// `ldconfig -r $out` bug, which chrooted into $out where the farm symlinks' store
// targets didn't resolve, producing a 0-entry cache (no libc, nothing).
//
// Note: bare `ldconfig -p` reads $glibc/etc/ld.so.cache, not the FHS path, so we
// point -C at /etc/ld.so.cache explicitly.
func TestExtraPackageLibInFhsLdcache(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{"network": {"mode": "bridge"}, "packages": ["zbar"]}`)
	r := runYolo(t, dir, "ldconfig -C /etc/ld.so.cache -p | grep -c libzbar || true")
	if r.rc != 0 {
		t.Fatalf("ldconfig probe failed (rc %d): %s", r.rc, r.stderr)
	}
	line := lastNonEmptyLine(r.stdout)
	if line == "" {
		line = "0"
	}
	count, err := strconv.Atoi(line)
	if err != nil {
		t.Fatalf("could not parse libzbar count from stdout:\n%s", r.stdout)
	}
	if count < 1 {
		t.Fatalf("libzbar not in /etc/ld.so.cache (count=%d); the cache may be empty "+
			"(the -r $out regression)\nstdout=%q", count, r.stdout)
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
