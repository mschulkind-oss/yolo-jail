package entrypoint

// nodefloor.go resolves ONE absolute Node interpreter for a `program` that declares a `node_floor`
// (docs/design/agent-program-runtimes.md §3.2).
//
// # The defect this exists for
//
// Which Node runs an npm-delivered agent CLI is decided by whichever `mise.toml` the WORKSPACE ships:
// the launcher execs a `#!/usr/bin/env node` script, and mise's shims precede /bin on BootPath. So a
// repo pinning Node 20 makes pi unrunnable, and the error names a missing module export rather than a
// version. That is the project's interpreter doing the agent's job.
//
// # Resolution ORDER, and why it is not a mise selector
//
//  1. The image's node, when it satisfies the floor — self-contained, always present, no install, and
//     the node the MCP wrappers already target.
//  2. The newest satisfying version in the mise store, resolved OFFLINE BY PATH: the store's
//     directory names ARE versions, so nothing is executed and nothing is fetched.
//
// ⚠ A MISE SELECTOR CANNOT BE USED HERE. Measured 2026-09-22: `mise install --dry-run node@22.19`
// reports "22.19.0 would install" with 22.20.0 and 22.23.2 already present. A selector is a PREFIX
// that FETCHES rather than a floor that ACCEPTS, so handing the floor to mise would download a new
// interpreter to satisfy a constraint an installed one already meets.
//
// # What this may NOT do
//
// It may not install, and it may not write. Launcher generation also runs HOST-SIDE as a dry run
// under `yolo check` (internal/cli/check/entrypoint.go), so a generator that installed would install
// on an observe verb. The eager install belongs to the provisioning stage, which is OQ-AR2's ruling.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// imageNodeVersion reports the version of the image's own node, or "" when it cannot be read.
//
// A SEAM, for two reasons rather than one. Tests need to drive the resolution table without a node on
// PATH; and the probe is an EXEC, which the host-side dry run should be able to stub rather than run
// against the host's own node — the generator is rendering content for a jail, and the host's version
// is not the jail's.
//
// The image publishes its node version nowhere: /etc/yolo-jail-image-identity holds a content hash
// only, and nothing bakes the version into the env. So a bounded exec is the available answer, and
// the precedent is established — the entrypoint already execs ldconfig, iptables, socat, supervise
// and `mise uninstall`.
var imageNodeVersion = func() string {
	// Bounded on purpose: this runs during launcher generation, on the boot path, and a hung probe
	// would hold up the launch for a value the resolution can do without.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, imageNodePath, "--version").Output()
	if err != nil {
		return ""
	}
	// `node --version` prints "v24.19.0\n"; CompareVersions tolerates the v, but trimming keeps the
	// value readable in a report.
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "v"))
}

// imageNodePath is the image's baked node. Not $PATH: the whole point is to bypass the mise shims
// that PATH resolution would hit first — reading the workspace's pin is the defect, not the fix.
const imageNodePath = "/bin/node"

// miseNodeStore is where mise unpacks its node installs. A package var so a test can point it at a
// fixture tree.
var miseNodeStore = "/mise/installs/node"

// ResolveNodeForFloor returns the absolute interpreter a program with this floor should be exec'd
// under, or "" when nothing available satisfies it.
//
// An empty floor returns "" AND that is not a failure: a program that declares nothing keeps today's
// behaviour byte-for-byte, which is load-bearing rather than tidy — `opencode-ai` ships a native ELF
// through the same `via: npm` route and must never be wrapped in an interpreter. The caller
// distinguishes the two cases by asking whether a floor was declared at all.
func ResolveNodeForFloor(floor string) string {
	if floor == "" {
		return ""
	}
	if v := imageNodeVersion(); v != "" && packdecl.SatisfiesNodeFloor(v, floor) {
		return imageNodePath
	}
	return newestSatisfyingMiseNode(floor)
}

// newestSatisfyingMiseNode picks the highest version in the mise store that meets the floor and has a
// runnable `bin/node`, or "" .
//
// # Why the directory NAME is the version, and why an alias is fine
//
// mise keeps alias directories beside real ones — `22`, `22.20`, `22.20.0`, `24` all exist in a live
// store — so a name that parses as a version IS its version, and CompareVersions treats a missing
// part as zero, which makes an alias compare as its own floor. Both are usable: an alias's `bin/node`
// resolves to the same binary.
//
// A more SPECIFIC name wins a tie, because "24.19.0" tells a reader what they got where "24" does
// not, and the baked path is read by a human debugging a launcher.
//
// ⚠ Entries whose `bin/node` is missing or not executable are skipped rather than returned: mise
// leaves a directory behind for a failed or partial install, and baking a path to a binary that is
// not there would turn a resolution success into an exec failure at the worst moment.
func newestSatisfyingMiseNode(floor string) string {
	entries, err := os.ReadDir(miseNodeStore)
	if err != nil {
		return ""
	}
	best, bestName := "", ""
	for _, e := range entries {
		name := e.Name()
		if !packdecl.ValidNodeFloor(name) || !packdecl.SatisfiesNodeFloor(name, floor) {
			continue
		}
		bin := filepath.Join(miseNodeStore, name, "bin", "node")
		if fi, err := os.Stat(bin); err != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
			continue
		}
		if bestName == "" {
			best, bestName = bin, name
			continue
		}
		switch packdecl.CompareVersions(name, bestName) {
		case 1:
			best, bestName = bin, name
		case 0:
			// Same version, two spellings: prefer the more specific one.
			if strings.Count(name, ".") > strings.Count(bestName, ".") {
				best, bestName = bin, name
			}
		}
	}
	return best
}
