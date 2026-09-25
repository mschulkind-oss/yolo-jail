package entrypoint

// nodefloor.go resolves ONE absolute Node interpreter for a `program` that declares a `node_floor`
// (docs/reference/agent-program-runtimes.md, "Resolution").
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
//     the node the MCP wrappers already target. On macos-user, which has no image, the package
//     floor's node on the sandbox PATH stands in for it (packageFloorNodes).
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
	"sort"
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
	return nodeVersionAt(imageNodePath)
}

// nodeVersionAt runs `<bin> --version` and returns the version without its `v`, or "" when it
// cannot be read. Shared by the image's node and the package floor's (packageFloorNodes), so both
// candidates are probed the same bounded way.
func nodeVersionAt(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
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

// miseNodeStore, when set, is the mise node store to read instead of the derived one — a test
// points it at a fixture tree. Empty in production, where miseNodeStoreDir derives it.
var miseNodeStore = ""

// defaultMiseNodeStore is the container's store: every container launch emits
// MISE_DATA_DIR=/mise (run.assemble), so this is also what the derivation below yields there.
const defaultMiseNodeStore = "/mise/installs/node"

// miseNodeStoreDir is where mise unpacks its node installs: $MISE_DATA_DIR/installs/node, which is
// the spelling docs/reference/agent-program-runtimes.md's "Resolution" gives candidate 2.
//
// ⚠ IT WAS A CONSTANT, /mise/installs/node, AND THAT WAS CONTAINER-ONLY. macos-user's store is
// <sandbox home>/.yolo/mise (macosuser.SandboxMiseData), which both its bootstrap process and its
// provisioning stage carry as MISE_DATA_DIR. With the constant, a node the stage had just
// installed there was invisible to the check that runs straight after, so once the floor check's
// failure became a real refusal (provision.RefusedStatus) every macos-user stage selecting a
// floor-declaring pack would have refused however the install went.
//
// THE PROCESS ENVIRONMENT, not an Env, because both callers are processes whose environment IS
// the jail's: the boot's launcher generator (the container's `-e MISE_DATA_DIR`, macos-user's
// bootstrap argv) and `yolo internal node-floor-satisfied` inside the stage. The one caller whose
// environment is NOT the jail's is `yolo check`'s host-side dry run; there MISE_DATA_DIR is
// normally unset and this falls back to the container's constant, exactly what it read before.
func miseNodeStoreDir() string {
	if miseNodeStore != "" {
		return miseNodeStore
	}
	if d := os.Getenv("MISE_DATA_DIR"); d != "" {
		return filepath.Join(d, "installs", "node")
	}
	return defaultMiseNodeStore
}

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
	for _, bin := range packageFloorNodes() {
		if v := nodeVersionAt(bin); v != "" && packdecl.SatisfiesNodeFloor(v, floor) {
			return bin
		}
	}
	return newestSatisfyingMiseNode(floor)
}

// packageFloorNodes is candidate 1 on macos-user, which has no image and so no /bin/node: every
// executable `node` in the entries of $YOLO_DARWIN_LOGIN_PATH that do not lie inside $HOME, in PATH
// order. Empty on every other backend, where that variable is never set.
//
// WHAT IT FINDS is the node the native package floor puts on the sandbox PATH (`nodejs_24` in
// flake.nix's coreFloorNames, which macos-user's floor is derived from), plus one a `packages:`
// entry supplies. It is the same stand-in, filtered by the same rule, that imageProbePath
// (launchercollision.go) uses for "what the launch provides" on that backend: the variable is the
// sandbox's real PATH (macosuser.SandboxPath), and dropping every entry under the home drops the
// sandbox's own prefixes — the mise shims above all, whose node IS the workspace's pin, the
// defect this file exists to route around.
//
// ⚠ WITHOUT IT THE REFUSAL WAS WRONG ON macos-user, not merely incomplete: once an unsatisfiable
// floor became fatal (provision.RefusedStatus), a stage whose `mise install node@<floor>` failed
// (offline, a proxy) refused the launch while the floor's own node 24 sat on PATH, and named
// "none" as available. OQ-AR3 refuses only when NOTHING on the machine satisfies the floor.
//
// PROCESS ENVIRONMENT, for miseNodeStoreDir's reason: both macos-user callers — the bootstrap
// (DarwinBootstrapArgv bakes the variable and HOME) and the provisioning stage (sandboxEnvPairs
// does) — are processes whose environment IS the sandbox's. `yolo check`'s host-side dry run never
// has the variable, so it reads nothing here, as before.
func packageFloorNodes() []string {
	loginPath := os.Getenv(DarwinLoginPathEnv)
	if loginPath == "" {
		return nil
	}
	home := strings.TrimSuffix(os.Getenv("HOME"), "/")
	var out []string
	seen := map[string]bool{}
	for _, dir := range strings.Split(loginPath, ":") {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		if home != "" && (dir == home || strings.HasPrefix(dir, home+"/")) {
			continue
		}
		bin := filepath.Join(dir, "node")
		if seen[bin] || !runnableNode(bin) {
			continue
		}
		seen[bin] = true
		out = append(out, bin)
	}
	return out
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
	store := miseNodeStoreDir()
	entries, err := os.ReadDir(store)
	if err != nil {
		return ""
	}
	best, bestName := "", ""
	for _, e := range entries {
		name := e.Name()
		if !packdecl.ValidNodeFloor(name) || !packdecl.SatisfiesNodeFloor(name, floor) {
			continue
		}
		bin := filepath.Join(store, name, "bin", "node")
		if !runnableNode(bin) {
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

// runnableNode reports whether bin is an executable file — the test both the resolution and the
// availability report apply, so the two cannot disagree about what counts as "a node here".
func runnableNode(bin string) bool {
	fi, err := os.Stat(bin)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// AvailableNodes lists every interpreter the resolution can see, each as "<version> at <path>", in
// the resolution's own order: the image's node when its version can be read, then the package
// floor's (packageFloorNodes, macos-user only), then each distinct version in the mise store,
// newest first.
//
// IT IS THE "WHAT IS AVAILABLE" HALF OF OQ-AR3's REFUSAL, which must name it — a refusal that says
// only "nothing satisfies >=22.19" leaves the user to go and find out what does exist. It lists
// the SAME candidates, from the same sources, that ResolveNodeForFloor considers, so the
// sentence can never name an interpreter the resolution would not have looked at, nor leave out
// one it did.
//
// ONE LINE PER VERSION, not per directory: the store keeps alias dirs (`24`, `24.19`) beside the
// real `24.19.0`, all naming one binary, and listing each would read as three interpreters. Every
// directory is grouped by the binary it resolves to, and the most specific spelling names it —
// the same tie-break newestSatisfyingMiseNode applies.
func AvailableNodes() []string {
	var out []string
	if v := imageNodeVersion(); v != "" {
		out = append(out, v+" at "+imageNodePath)
	}
	for _, bin := range packageFloorNodes() {
		if v := nodeVersionAt(bin); v != "" {
			out = append(out, v+" at "+bin)
		}
	}
	store := miseNodeStoreDir()
	entries, err := os.ReadDir(store)
	if err != nil {
		return out
	}
	type found struct{ name, bin string }
	byTarget := map[string]found{}
	var order []string
	for _, e := range entries {
		name := e.Name()
		if !packdecl.ValidNodeFloor(name) {
			continue
		}
		bin := filepath.Join(store, name, "bin", "node")
		if !runnableNode(bin) {
			continue
		}
		target, err := filepath.EvalSymlinks(bin)
		if err != nil {
			target = bin
		}
		prev, seen := byTarget[target]
		if !seen {
			order = append(order, target)
			byTarget[target] = found{name, bin}
			continue
		}
		if strings.Count(name, ".") > strings.Count(prev.name, ".") {
			byTarget[target] = found{name, bin}
		}
	}
	var mise []found
	for _, target := range order {
		mise = append(mise, byTarget[target])
	}
	sort.SliceStable(mise, func(i, j int) bool {
		return packdecl.CompareVersions(mise[i].name, mise[j].name) > 0
	})
	for _, f := range mise {
		out = append(out, f.name+" at "+f.bin)
	}
	return out
}

// DescribeAvailableNodes is AvailableNodes as one line for the refusal, naming where it looked when
// it found nothing — "none" alone would not tell the reader whether the store was even read.
func DescribeAvailableNodes() string {
	if nodes := AvailableNodes(); len(nodes) > 0 {
		return strings.Join(nodes, "; ")
	}
	where := "no readable " + imageNodePath
	if os.Getenv(DarwinLoginPathEnv) != "" {
		where += ", no node outside $HOME on $" + DarwinLoginPathEnv
	}
	return "none (" + where + ", and no node in " + miseNodeStoreDir() + ")"
}
