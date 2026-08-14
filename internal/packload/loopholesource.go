package packload

// loopholesource.go resolves a `loophole` contribution's MODULE DIRECTORY — the
// pack-relative dir holding `manifest.jsonc` — and turns what that manifest declares
// into the pack's host-access claims.
//
// # Why the claims are produced HERE and not in packdecl
//
// `packdecl.Manifest.HostAccessClaims` is a pure walk over decoded `pack.json` bytes.
// packdecl has zero internal imports by design and a Manifest carries no root path, so
// it cannot open the module dir at all — a claim computed there would degrade to a bare
// `loophole acme`: a string that never changes no matter what the daemon becomes, which
// is content-blind consent (loophole-packaging.md §3.3).
//
// `*Pack` HAS a Root, and the precedent is already in this package:
// PluginHostAccessClaims (plugins.go) lives here for exactly this reason — a wrapped
// plugin's code-running components are declared in a file outside pack.json too. So this
// is the third producer of the same kind of string, and the reason
// Pack.HostAccessClaims (hostaccess.go) exists to merge all three.
//
// # Why it may import internal/loopholedecl and must not import internal/loopholes
//
// `internal/loopholes` → `internal/config` → `internal/packload`: importing the runtime
// registry here is a cycle, measured in loopholedecl's package doc. `internal/loopholedecl`
// is the schema extracted as a leaf precisely so this file can read a manifest
// (loophole-packaging.md §3.2, OQ-LP1). Everything it returns is RAW — `{loophole_dir}`
// unexpanded, `${XDG_RUNTIME_DIR}` unexpanded — which is what the claim strings need (see
// hostaccess.go's G2a note).

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// LoopholeModule is one loophole a pack ships: where it was declared, where it lives,
// and what its manifest says.
type LoopholeModule struct {
	// From is the pack-relative source as declared, e.g. "loopholes/acme-proxy". It is
	// the STABLE half — every claim string is built from this and the manifest, never
	// from Dir, because Dir is a staging path that differs per launch and per machine.
	From string
	// Name is the loophole's name: the module directory's basename. It is authoritative
	// without decoding anything, because loadManifest forces the manifest's own `name`
	// to equal it — which is what lets the footprint key a claim by loophole name even
	// when the manifest cannot be read.
	Name string
	// Dir is the absolute module directory in this pack's tree.
	Dir string
	// Decl is the decoded manifest, or nil when it could not be read (see Problem).
	Decl *loopholedecl.Manifest
	// Problem is why Decl is nil, phrased for a user, with NO absolute path in it — a
	// claim built from this has to compare equal across machines.
	Problem string
}

// LoopholeModules resolves every `loophole` contribution to a module directory and
// decodes its manifest, splitting what went wrong into REFUSALS and WARNINGS.
//
// Three outcomes per declaration, and there is deliberately no fourth:
//
//   - a directory with a readable manifest → a LoopholeModule with Decl set, nothing
//     reported;
//   - a `from` naming a directory the pack does NOT contain, one that escapes the pack
//     tree, or two modules resolving to one loophole NAME → a REFUSAL, and no module.
//     Refused by name: the author named a specific path and got nothing, and unlike
//     `skills` there is no conventional location to fall back to, so silence here would
//     be a declaration that does nothing;
//   - a directory whose manifest will not load → a WARNING, plus a module with Problem
//     set and Decl nil. Both halves matter: the report is the diagnostic, and the CLAIM
//     path needs a module to attach a fail-closed "declaration unreadable" claim to, or
//     an unreadable manifest would produce an EMPTY claim set — which is exactly
//     packMayAccessHost's grant-everything case.
//
// # Why the split, and why it is not an inconsistency (loophole-packaging.md §3.1)
//
// It is a LAYER split. A missing `from` is a `pack.json` error, decidable without loading
// any loophole, in a tree the user explicitly selected: refusing it is a fix, and it is
// the same call `skills` makes for a non-conventional `from` that stages nothing. An
// unloadable `manifest.jsonc` is the DISCOVERY layer's business, and discovery's contract
// across all four loophole sources is warn-and-continue — one bad third-party manifest in
// a shared directory must not take the others down with it. So the pack layer refuses what
// pack.json got wrong and warns about what the module got wrong, and nothing here changes
// discovery.
//
// The caller decides what a refusal costs. `pack lint` fails on either list (authoring:
// every problem is the author's). The launch path refuses on `refusals` and warns on
// `warnings`, which is what makes the split observable rather than decorative.
func (p *Pack) LoopholeModules() (mods []LoopholeModule, refusals, warnings []string) {
	root := filepath.Clean(p.Root)
	byName := map[string]string{}
	for _, rel := range p.Decl.LoopholeSources() {
		dir := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
		if root != "" && root != "." && !strings.HasPrefix(dir, root+string(filepath.Separator)) {
			refusals = append(refusals, fmt.Sprintf(
				"pack %s: loophole `from` %q escapes the pack tree — refused", p.Name, rel))
			continue
		}
		name := path.Base(path.Clean(filepath.ToSlash(rel)))
		// Two declarations whose dirs differ but whose BASENAMES agree are two loopholes
		// with one name, which is the self-collision the generic Collisions pass cannot
		// see (it skips single-pack groups) and which would silently share every claim
		// target. Caught here, where both declarations are in hand.
		if prev, dup := byName[name]; dup {
			refusals = append(refusals, fmt.Sprintf(
				"pack %s: loopholes %q and %q are both named %q — a loophole's name is its "+
					"directory basename and one name is one loophole, so one would shadow the "+
					"other; rename one module directory", p.Name, prev, rel, name))
			continue
		}
		byName[name] = rel

		fi, err := os.Stat(dir)
		switch {
		case err != nil || !fi.IsDir():
			refusals = append(refusals, fmt.Sprintf(
				"pack %s declares `loophole` from %q, which is not a directory in its content — "+
					"a loophole contribution points at a module dir holding %s (check the path, "+
					"and any only/exclude filters)", p.Name, rel, loopholedecl.ManifestName))
			continue
		}
		mod := LoopholeModule{From: rel, Name: name, Dir: dir}
		// TOLERANT, not strict, and the choice is the gate's rather than an author's. A key
		// only a NEWER build knows is version skew: refusing the manifest here would turn a
		// working loophole into an unreadable one, re-prompt for approval, and — since
		// promptYesNo fails closed on a non-TTY — refuse the loophole permanently. Tolerant
		// enumerates exactly what THIS build understands, which is exactly what it will
		// honor, so the claim set and the effect cannot disagree. An author hears about a
		// typo from the STRICT read `yolo pack lint` does (LoopholeDeclProblems).
		decl, _, derr := loopholedecl.LoadDirTolerant(dir)
		if derr != nil {
			mod.Problem = fmt.Sprintf("its %s could not be read", loopholedecl.ManifestName)
			warnings = append(warnings, fmt.Sprintf(
				"pack %s: loophole %q (from %q): %v — it will not be discovered, and its "+
					"host access is claimed as UNENUMERABLE rather than as nothing",
				p.Name, name, rel, derr))
		} else {
			mod.Decl = decl
		}
		mods = append(mods, mod)
	}
	return mods, refusals, warnings
}

// LoopholeDeclProblems is the AUTHORING read of every loophole module this pack ships:
// the resolution problems above, plus each manifest's problems under the STRICT decoder,
// one per line.
//
// Strict here and tolerant in LoopholeModules is the same asymmetry packdecl.Decode /
// DecodeTolerant already draws, for the same reason: an author must hear that a key
// declares nothing (today's loader had no unknown-key rejection at all, so `host_deamon`
// read as a loophole with no daemon), while a gate reading a manifest some other build
// wrote must not refuse the whole thing over a key it does not know.
func (p *Pack) LoopholeDeclProblems() []string {
	mods, refusals, warnings := p.LoopholeModules()
	problems := append(append([]string(nil), refusals...), warnings...)
	for _, mod := range mods {
		if _, err := loopholedecl.LoadDir(mod.Dir); err != nil {
			var probs []string
			if le, ok := err.(*loopholedecl.Error); ok {
				probs = le.Problems()
			} else {
				probs = []string{err.Error()}
			}
			for _, prob := range probs {
				problems = append(problems, fmt.Sprintf(
					"pack %s: loophole %q: %s", p.Name, mod.Name, prob))
			}
		}
	}
	return problems
}

// LoopholeHostAccessClaims returns the approval strings every loophole this pack ships
// makes on the host — the third producer merged by Pack.HostAccessClaims.
//
// # The enumeration is TOTAL, and that is the load-bearing rule of the design
//
// Every declaration that crosses the host boundary emits its OWN claim
// (loophole-packaging.md §3.3). It has to, because `packMayAccessHost` reads an EMPTY
// claim set as "reads nothing from the host, runs nothing on it; the gate is moot" and
// returns TRUE — so a fetched pack shipping a loophole with only `host_bind_mounts` and
// `host_devices` used to get an arbitrary absolute host path into a UID-0 jail with no
// prompt, ever. A claim-free crossing must be unrepresentable:
//
//	host_daemon.cmd + doctor_cmd  → one base claim, keyed <name>       (host EXECUTION)
//	intercepts[]                  → one per host,  keyed <name>:intercept:<host>
//	host_bind_mounts[] (r/w or a  → one per bind,  keyed <name>:ipc:<container>
//	  socket)                                                          (read-write IPC)
//	host_bind_mounts[] (:ro)      → one per bind,  keyed <name>:mount:<container>
//	host_devices[]                → one per node,  keyed <name>:device:<path>
//
// `state_files` gets NO claim: it resolves under `StateDirFor(<name>)` inside yolo's own
// state tree, not a path a user would recognise as theirs. `jail_daemon` gets none
// either — it is a process inside the container, which is the one place a pack's code
// was always allowed to run.
//
// A loophole that declares none of the above crosses nothing and correctly claims
// nothing; what §3.3 forbids is a CROSSING that claims nothing.
//
// # The strings are RAW (G2a)
//
// These are LOCKFILE COMPARISON KEYS, walked for exact matches, not display text. Two
// consequences, both deliberate:
//
//   - Nothing is elided. An ellipsis would collapse two different daemons onto one
//     approved claim.
//   - Placeholders stay UNEXPANDED. `{loophole_dir}` resolves to a staging-specific
//     absolute path, so an expanded claim would be machine-specific: it could never
//     match an approval recorded elsewhere, would re-prompt forever, and since
//     promptYesNo fails closed on a non-TTY, `packMayAccessHost` would then refuse the
//     loophole permanently. For the same reason no claim is conditioned on the PLATFORM:
//     a manifest's `platforms` list decides whether the loophole runs HERE, and folding
//     that into the key would make one pack's approved set differ per machine.
//
// The footprint's Detail may abbreviate. The two are deliberately not the same string.
func (p *Pack) LoopholeHostAccessClaims() []string {
	var out []string
	for _, c := range p.loopholeClaims() {
		out = append(out, c.approval)
	}
	sort.Strings(out)
	return out
}

// loopholeClaim is one crossing a loophole declares, in both of its renderings: the
// approval string (raw, a comparison key) and the footprint's target+detail (display,
// may abbreviate). Built together so the two cannot describe different sets.
type loopholeClaim struct {
	// target is the footprint key: the loophole name for the base claim,
	// "<name>:<discriminator>" for every other, so a pack with three bind mounts emits
	// three separately-approvable strings.
	target   string
	detail   string
	approval string
	// runsHostCode is true for the base claim only — the one whose crossing is host
	// EXECUTION (host_daemon.cmd, doctor_cmd) rather than a host read or a passthrough. It
	// feeds Claim.RunsHostCode, which is what makes the footprint's ⚠ marker say so.
	runsHostCode bool
}

// loopholeClaims enumerates every crossing every loophole module declares.
//
// A REFUSED declaration (an absent module dir, a name collision) yields no module and so
// no claim, which is right: nothing crosses, because nothing will be discovered, and the
// refusal is reported by the paths that act on it. An UNREADABLE manifest does yield a
// module, and moduleClaims turns it into a fail-closed claim — see there.
func (p *Pack) loopholeClaims() []loopholeClaim {
	mods, _, _ := p.LoopholeModules()
	var out []loopholeClaim
	for _, mod := range mods {
		out = append(out, moduleClaims(mod)...)
	}
	return out
}

// moduleClaims is the per-module enumeration. Split out so it is testable from a
// manifest alone, and so the "one claim per crossing" table above has one place to be
// read against.
func moduleClaims(mod LoopholeModule) []loopholeClaim {
	name := mod.Name
	if mod.Decl == nil {
		// FAIL CLOSED. An unreadable declaration is not "no claims" — that is the empty
		// set the gate reads as consent, and it would let a fetched pack ship a loophole
		// whose manifest this build cannot parse and get it past the boundary. The string
		// names the module dir as DECLARED (never the staged absolute path) so it still
		// compares equal across machines.
		return []loopholeClaim{{
			target: name,
			detail: "declaration UNREADABLE — " + mod.Problem,
			approval: "loophole " + name + " DECLARATION UNREADABLE at " + mod.From +
				" — its claims cannot be enumerated",
			// An unenumerable declaration is treated as the sharpest thing it could be: a
			// manifest yolo cannot read may well declare a host daemon.
			runsHostCode: true,
		}}
	}
	m := mod.Decl
	var out []loopholeClaim

	// The base claim: HOST EXECUTION. `doctor_cmd` folds in here rather than getting its
	// own line because it is host execution too (RunDoctorChecks runs it from `yolo check`
	// and `yolo loopholes status`), and one claim per program is the honest unit.
	//
	// "RUNS" and "on your machine" are SPELLED OUT rather than left to the ⚠ marker,
	// following pluginClaimDetail's "RUNS CODE" precedent. ReviewWorthy is one boolean —
	// one severity — and it currently means "reads ~/.claude.json"; host execution has to
	// read differently from a host read, and the words are what does that.
	//
	// The argv is rendered with shquote.Join, and that is about INJECTIVITY rather than
	// about a shell: nothing execs this string (the spawn reads the argv list), but a
	// claim is a comparison key, so two DIFFERENT argvs must never render to one claim.
	// A bare space join is not injective — ["sh","-c","a b"] and ["sh","-c","a","b"]
	// collapse — which is the same failure an ellipsis would cause, arrived at by
	// accident. Quoting also keeps a whitespace-bearing argument legible in the prompt.
	// It is applied to the RAW argv, so the tokens survive: {loophole_dir} has no unsafe
	// bytes in shlex's set apart from the braces, and comes out `'{loophole_dir}/x.py'`.
	var runs []string
	if m.HostDaemon != nil && len(m.HostDaemon.Cmd) > 0 {
		runs = append(runs, shquote.Join(m.HostDaemon.Cmd))
	}
	if len(m.DoctorCmd) > 0 {
		runs = append(runs, shquote.Join(m.DoctorCmd))
	}
	if len(runs) > 0 {
		out = append(out, loopholeClaim{
			target:       name,
			detail:       "RUNS " + strings.Join(runs, " and ") + " on your machine",
			approval:     "loophole " + name + " RUNS " + strings.Join(runs, " and ") + " on your machine",
			runsHostCode: true,
		})
	}

	// One per intercept, and it EXISTS EVEN WITH transport:"none" AND NO DAEMON: an
	// intercept runs no host code and still installs a CA trusted by every TLS client in
	// the jail, which is a standing capability over every hostname the jail dials.
	for _, ic := range m.Intercepts {
		out = append(out, loopholeClaim{
			target: name + ":intercept:" + ic.Host,
			detail: "INTERCEPTS " + ic.Host + " — installs a CA trusted by every TLS client in the jail",
			approval: "loophole " + name + " INTERCEPTS " + ic.Host +
				" — installs a CA trusted by every TLS client in the jail",
		})
	}

	// One per bind mount, in one of two classes. See bindIsIPC for why the split is by
	// what the MANIFEST says rather than by what the path turns out to be.
	for _, bm := range m.HostBindMounts {
		if bindIsIPC(bm) {
			out = append(out, loopholeClaim{
				target: name + ":ipc:" + bm.Container,
				detail: "CONNECTS the jail to the host socket " + bm.Host + " — read-write host IPC",
				approval: "loophole " + name + " CONNECTS the jail to the host socket " + bm.Host +
					" at " + bm.Container + " — read-write host IPC",
			})
			continue
		}
		out = append(out, loopholeClaim{
			target: name + ":mount:" + bm.Container,
			detail: "MOUNTS " + bm.Host + " -> " + bm.Container + " (read-only for a file or " +
				"directory; an AF_UNIX SOCKET here is read-write host IPC regardless of `:ro`)",
			approval: "loophole " + name + " MOUNTS " + bm.Host + " -> " + bm.Container +
				" (read-only for a file or directory; an AF_UNIX SOCKET here is read-write " +
				"host IPC regardless of `:ro`)",
		})
	}

	// One per device. A device node is NOT weaker than a read-write bind mount: `audio`'s
	// own manifest describes `--device` as passing a node "so the cgroup device-allow
	// rules permit reads/writes". Same objection, so the same claim — and the
	// home-relative constraint a pack-shipped bind mount gets does not apply to a device
	// node, which is precisely why it needs the claim.
	for _, dev := range m.HostDevices {
		out = append(out, loopholeClaim{
			target:   name + ":device:" + dev,
			detail:   "PASSES THROUGH the host device " + dev + " (reads and writes)",
			approval: "loophole " + name + " PASSES THROUGH the host device " + dev + " (reads and writes)",
		})
	}
	return out
}

// bindIsIPC reports whether a bind mount is claimed as read-write host IPC rather than
// as a read.
//
// # `:ro` IS NOT A BOUNDARY FOR A UNIX SOCKET — measured, twice
//
//	$ podman run --rm -v /tmp/s.sock:/ro.sock:ro -v /tmp/s.sock:/rw.sock … connect to both
//	/ro.sock CONNECT_OK b'HELLO'
//	/rw.sock CONNECT_OK b'HELLO'
//
// The kernel's read-only check exempts non-REG/DIR/LNK inodes; this is the well-known
// `docker.sock:ro` result. So a socket bound `:ro` gives the jail unrestricted
// bidirectional access to whatever is behind it (a container socket, `ssh-agent`,
// `gpg-agent`, the PipeWire daemon), and a claim calling that "read-only" would be false.
//
// # Why the test is the manifest's `readonly` bit and not the inode
//
// Nothing here may stat the host path, for two independent reasons, and this is the one
// place the design's "a socket bind is its own claim class" cannot be implemented as
// written:
//
//   - the path is RAW. `{loophole_dir}/asound.conf`, `${XDG_RUNTIME_DIR}/pulse/native` —
//     resolving either needs the module's real path and the current environment, which is
//     what makes a claim string machine-specific (G2a) and is exactly what this producer
//     must not do;
//   - a stat is a fact about THIS MACHINE at THIS MOMENT. A claim that changes class when
//     a socket is absent would re-prompt on the machine where the socket is missing.
//
// So the split is by the only static evidence available: `readonly: false` — the manifest
// itself saying the bind is bidirectional, which is what every socket bind in-tree
// declares (`audio` sets it on both of its sockets, and the design's own count of
// audio-shaped claims reads them as the IPC class), or a basename that names a socket.
// A `:ro` bind of a socket with a non-obvious name therefore lands in the MOUNT class —
// which is why that class's text carries the socket caveat verbatim rather than claiming
// "read-only" and stopping. Nothing is understated; only the discriminator is coarser
// than a stat would be.
//
// The precise fix is a DECLARED socket bit in the manifest schema
// (`internal/loopholedecl`), which would make the class a fact the author states rather
// than one yolo infers.
func bindIsIPC(bm loopholedecl.HostBindMount) bool {
	if !bm.Readonly {
		return true
	}
	base := path.Base(path.Clean(filepath.ToSlash(bm.Host)))
	return strings.HasSuffix(base, ".sock") || strings.HasSuffix(base, ".socket")
}
