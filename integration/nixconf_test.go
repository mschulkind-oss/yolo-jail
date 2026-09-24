package integration

import (
	"strings"
	"testing"
)

// TestInJailNixHasTheNewCLIEnabledWithNoFlags pins that the container image carries a
// /etc/nix/nix.conf enabling `nix-command` and `flakes`, so a human or agent typing plain
// `nix shell nixpkgs#hello`, `nix build` or `nix eval` in a jail is not refused with
// "experimental Nix feature 'nix-command' is disabled".
//
// yolo's own nix calls never noticed the gap, because every one passes
// `--extra-experimental-features` itself; this test deliberately passes NO flag and sets no
// NIX_CONFIG, which is the invocation a user makes.
//
// THREE HALVES, because the image variant and the host decide which can run:
//
//   - The FILE is asserted on every image: flake.nix writes it in mkBinPathLinks, which every
//     variant gets.
//   - `nix config show` needs the new CLI but no store, so it answers whether the config
//     enables the features — but only where the image HAS a `nix`. `.#ociImageMinimal`, the
//     image CI's integration job runs, drops fullPackages and with it `nix`, so there the
//     half is skipped with a log naming why rather than failing on "command not found".
//   - `nix eval` needs a store. In a container jail that is the HOST daemon behind the
//     mounted socket (NIX_REMOTE=daemon), so it runs only when that socket is present.
//     Without it there is no working store at all — the jail root is --read-only, and nix
//     refuses with 'remounting "/nix/store" writable: Operation not permitted' (measured
//     2026-09-24 with `podman run --read-only` on the full image) — which is a different
//     gap (docs/plans/setup-support-gaps.md G21) from the one this test is about.
func TestInJailNixHasTheNewCLIEnabledWithNoFlags(t *testing.T) {
	requireJail(t)
	if rt := detectRuntime(); rt == "macos-user" {
		t.Skip("macos-user runs no image, so the image's nix.conf does not apply")
	}
	dir := writeProject(t, `{}`)

	script := `unset NIX_CONFIG
echo "CONF: $(cat /etc/nix/nix.conf 2>&1 | tr '\n' ' ')"
if ! command -v nix >/dev/null 2>&1; then
  echo "NIX: absent"
  exit 0
fi
echo "NIX: $(command -v nix)"
echo "FEATURES: $(nix config show experimental-features 2>/dev/null)"
if [ -S /nix/var/nix/daemon-socket/socket ]; then
  echo "EVAL: $(nix eval --expr 1+1 2>&1)"
else
  echo "EVAL: skipped-no-daemon-socket"
fi`
	r := runYolo(t, dir, script)
	if r.rc != 0 {
		t.Fatalf("jail run failed (rc %d):\nstdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
	}

	conf := lineWithPrefix(r.stdout, "CONF: ")
	if !strings.Contains(conf, "experimental-features = nix-command flakes") {
		t.Errorf("/etc/nix/nix.conf does not enable nix-command and flakes: %q", conf)
	}

	if lineWithPrefix(r.stdout, "NIX: ") == "absent" {
		t.Skip("this image has no `nix` (the minimal variant drops fullPackages); only the " +
			"nix.conf file half was exercised")
	}

	// WHOLE FIELDS of stdout alone, stderr discarded: a refusal reads as EMPTY rather than as
	// text that happens to contain "nix-command" — nix's own error names the feature it refuses.
	features := map[string]bool{}
	for _, f := range strings.Fields(lineWithPrefix(r.stdout, "FEATURES: ")) {
		features[f] = true
	}
	for _, want := range []string{"nix-command", "flakes"} {
		if !features[want] {
			t.Errorf("`nix config show experimental-features` with no flags does not report %q — "+
				"the image's /etc/nix/nix.conf is missing or does not enable it.\nfeatures: %q\nconf: %q",
				want, lineWithPrefix(r.stdout, "FEATURES: "), conf)
		}
	}

	switch eval := lineWithPrefix(r.stdout, "EVAL: "); eval {
	case "skipped-no-daemon-socket":
		t.Log("no nix daemon socket mounted: the eval half was not exercised on this host")
	case "2":
	default:
		t.Errorf("`nix eval --expr 1+1` with no flags = %q, want \"2\"", eval)
	}
}

// lineWithPrefix returns the rest of the first line of out starting with prefix, or "".
func lineWithPrefix(out, prefix string) string {
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}
