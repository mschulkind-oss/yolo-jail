package integration

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// nixBuildJailTimeout is the per-command budget for the two tests below, and it exists
// because their cost is REAL rather than misattributed.
//
// Each sets `packages:`, which makes the launch do a full --impure nix image build before
// the jail starts, so these are the slowest launches in the suite by a wide margin. On the
// macOS nightly TestExtraPackageLibFarm measured 683s (2026-08-20), 1059s (08-21) and then
// TIMED OUT at 1216s (08-22) against YOLO_TEST_JAIL_TIMEOUT=1200; its sibling has climbed
// 256s → 345s → 641s over the same three nights. Neither is absorbing suite warmup — that
// is a different problem with a different fix (docs/design/agent-install-in-ci.md §4.1, and
// warmJail) — so for THESE two the honest answer is the budget the harness already provides
// for a legitimately expensive launch, the same `withTimeout` the mise-venv case uses.
//
// 40 minutes is ~3.5x the worst measured run, and the job's own deadline still backstops a
// genuine hang: the macOS nightly takes ~2h against GitHub's 6h default.
const nixBuildJailTimeout = 40 * time.Minute

// Lib-farm ("extra packages") tests. Each nix-builds a per-workspace image (the
// `packages` config triggers an
// --impure image rebuild), so these are the slowest tests in the suite; they are
// gated by requireJail(t) like every other container test.
//
// THAT PER-WORKSPACE BUILD IS WHY THESE TWO TESTS LIE WHEN THEY FAIL — or did,
// until the guard below existed. Every assertion in this file is of the form
// "the library I asked for is in the image", so ANY reason the image is not the
// one this config asked for reads as a lib-farm bug. On 2026-08-15 the macOS
// nightly's build failed, the CLI silently fell back to the already-loaded
// image, and both tests reported `libzbar.so.0 not linked into /lib` for what
// was a nix error. The fix is not here: runCommand now aborts with the build's
// own output whenever the CLI reports a failed build, so the failure arrives at
// its cause (see imagebuildfailure_test.go). Keep new assertions in this file
// written as if the image is correct — checking for a stale image per-test is
// the harness's job, and duplicating it here would drift.
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
	}, "\n"), withTimeout(nixBuildJailTimeout))
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
			`print("dlopen-ok")'`, withTimeout(nixBuildJailTimeout))
	if r.rc != 0 {
		t.Fatalf("loading libsodium from /lib failed — the .dev request did not link "+
			"the runtime lib into the farm (rc %d)\nstdout=%q\nstderr=%q",
			r.rc, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "dlopen-ok") {
		t.Fatalf("expected dlopen-ok in stdout, got:\n%s", r.stdout)
	}
}
