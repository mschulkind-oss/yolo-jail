package check

import (
	"path/filepath"
	"strings"
	"time"
)

// sectionNixLD runs the "FHS loader (nix-ld)" block — an in-jail-only smoke test
// that the FHS ELF interpreter wiring still resolves an FHS binary under a fully
// scrubbed environment. This is the baseline-drift tripwire the nix-ld design
// asks for (docs/design/mise-node-dynamic-linking.md step 8): before nix-ld a
// mise-installed node crashed with `libstdc++.so.6: cannot open` whenever a
// launcher scrubbed LD_LIBRARY_PATH; nix-ld (the /lib64 interpreter) makes it
// work env-free. If a future nixpkgs bump or flake change regresses that, this
// surfaces a clear message here instead of a cryptic MCP-spawn failure deep in
// an agent.
//
// Skipped entirely on the host (the mise node + /lib64 wiring are jail state)
// and when no mise node is installed (nothing to probe — not a failure).
func (o *Options) sectionNixLD(r *reporter) {
	if !o.inJail() {
		return // jail-only wiring; nothing to probe on the host
	}
	node := o.MiseNode()
	if node == "" {
		return // no mise node installed — nothing this tripwire covers
	}
	r.section("FHS loader (nix-ld)")
	// `env -i` in the argv (NOT via the Exec env slice, which appends to
	// os.Environ and so cannot scrub): an empty environment is the exact case
	// the MCP wrappers used to guard with LD_LIBRARY_PATH and that nix-ld now
	// covers structurally. Scrubbing in the command guarantees no ambient
	// LD_LIBRARY_PATH masks a regression, regardless of how Exec builds its env.
	res := o.Exec([]string{"env", "-i", node, "--version"}, "", nil, 15*time.Second)
	switch {
	case res.Timeout:
		r.fail("mise node env-free probe timed out", "")
	case !res.Ran:
		r.warn("could not run the mise node probe: exec failed", "")
	case res.RC == 0 && strings.HasPrefix(strings.TrimSpace(res.Stdout), "v"):
		r.ok("mise node runs env-free: " + strings.TrimSpace(res.Stdout) + " (nix-ld OK)")
	default:
		detail := firstLine(strings.TrimSpace(res.Stderr))
		if detail == "" {
			detail = firstLine(strings.TrimSpace(res.Stdout))
		}
		r.fail("mise node fails under a scrubbed environment: "+detail,
			"The FHS loader (nix-ld) wiring has regressed — an FHS binary can no "+
				"longer find libstdc++ without LD_LIBRARY_PATH. Check the /lib64 "+
				"interpreter symlink and the baked /usr/share/nix-ld/lib dir "+
				"(flake.nix); see docs/design/mise-node-dynamic-linking.md.")
	}
	r.blank()
}

// realFirstMiseNode returns the path to a mise-installed node binary, or "" if
// none is present. It globs /mise/installs/node/*/bin/node and returns the first
// match — the probe only needs one FHS node to exercise the loader.
func realFirstMiseNode() string {
	matches, err := filepath.Glob("/mise/installs/node/*/bin/node")
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}
