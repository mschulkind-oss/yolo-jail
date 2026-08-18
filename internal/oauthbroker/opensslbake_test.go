package oauthbroker

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestImageBakesOpensslForTheBrokerCA guards the one line in flake.nix that has
// no other guard anywhere, and whose absence already cost months
// (docs/design/broker-ca-and-nested-hosts.md).
//
// EnsureCAAndLeaf, three functions above this one, mints the broker CA by
// EXEC'ING openssl. Nothing in the jail image invokes openssl in its own argv, so
// the dependency is invisible to every mechanism that would normally hold it: it
// is not an import, not a `requires` entry, not a shim, and a closure audit of the
// image reads it as an unused package to drop. It was in fact absent, and the
// consequence was reported three layers from its cause — on any launch whose HOST
// IS ITSELF A JAIL the broker singleton exited at startup (2,549 times in one
// jail), which since the reachability witness became fatal surfaces as a REFUSED
// launch of exactly the nested jail AGENTS.md makes mandatory for verifying Go
// changes.
//
// A unit test is the only place this CAN be held. The image is built by nix and
// exercised by integration/, which does not run on the unit gate; nothing in
// `go test` can observe the image's PATH. So the check is textual by necessity,
// and its job is narrow: make the next `imagePkgs` cleanup fail here instead of in
// somebody's nested jail a month later.
//
// It reads the CORE list specifically. `corePackagesFromNixpkgs` ships in both the
// full and the minimal image variant, and the minimal one is a host for its
// children too — moving the package down into `fullPackages` would restore the
// original bug for exactly the CI-shaped jails that already run minimal.
func TestImageBakesOpensslForTheBrokerCA(t *testing.T) {
	// Repo root is two dirs up from this package (internal/oauthbroker → internal
	// → repo). Absent flake.nix means a shipped bundle or a trimmed checkout, not
	// a regression — the same reason internal/darwinpkg's cross-check skips.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repo := filepath.Join(filepath.Dir(thisFile), "..", "..")

	// The guard retires itself. OQ-1 of the design is to port cert minting to
	// crypto/x509, and on the day that lands the image owes this package nothing —
	// a test still demanding the package would be the tail wagging the dog. So the
	// consumer is read first, and the SKIP names what to do rather than what broke.
	cert, err := os.ReadFile(filepath.Join(repo, "internal", "oauthbroker", "cert.go"))
	if err != nil {
		t.Fatalf("reading this package's own cert.go: %v", err)
	}
	if !strings.Contains(string(cert), `exec.LookPath("openssl")`) {
		t.Skip("cert.go no longer resolves an openssl binary — if OQ-1 (the crypto/x509 " +
			"port) landed, delete this guard along with the flake.nix entry it protects")
	}

	flake, err := os.ReadFile(filepath.Join(repo, "flake.nix"))
	if err != nil {
		t.Skipf("cannot read flake.nix (%v) — skipping cross-check", err)
	}
	core, ok := nixListBody(string(flake), "corePackagesFromNixpkgs")
	if !ok {
		t.Fatal("flake.nix has no `corePackagesFromNixpkgs = [ … ];` binding — this guard " +
			"reads that list by name, so the rename has to move the check with it")
	}
	// \b so the match cannot be satisfied by `imagePkgs.openssh`, which sits in
	// fullPackages one letter away, nor by an `openssl_*` variant attribute.
	if !regexp.MustCompile(`imagePkgs\.openssl\b`).MatchString(core) {
		t.Errorf("flake.nix's corePackagesFromNixpkgs does not bake `imagePkgs.openssl`.\n" +
			"internal/oauthbroker mints the broker CA by exec'ing openssl, so without it " +
			"every launch whose host is itself a jail kills the broker singleton at " +
			"startup — silently, because the only record is a log nobody reads.\n" +
			"If it was moved to fullPackages: the minimal variant is a host for its " +
			"children too. See docs/design/broker-ca-and-nested-hosts.md §5.")
	}
}

// nixListBody returns the text between `<name> = [` and the `];` that closes it,
// matching brackets so a nested list inside the binding cannot end it early.
// Returns ok=false when the binding is absent.
func nixListBody(src, name string) (string, bool) {
	open := strings.Index(src, name+" = [")
	if open < 0 {
		return "", false
	}
	start := open + strings.Index(src[open:], "[")
	depth := 0
	for i := start; i < len(src); i++ {
		switch src[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return src[start+1 : i], true
			}
		}
	}
	return "", false
}
