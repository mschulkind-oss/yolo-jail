package config

// The §4.3a PLACEMENT rule of docs/design/loophole-packaging.md, which every
// other gate in that section leaves open:
//
//	G1 decides WHO may write command: ["python3", "/workspace/tool.py"].
//	G2 records the string in the lockfile. G3 asks where the pack came from.
//	G4 prints it at launch. NOTHING READS tool.py — and the workspace is
//	bind-mounted :rw, so the only artifact that actually executes is the only
//	one no gate inspects.
//
// The ruling was explicitly NOT content confirmation (no digest, no re-prompt
// loop): writing the user config already demands host access as the user, so a
// dialog guarding it protects nothing. What the permission argument does not
// cover is the SECOND actor — the agent that rewrites the named file has none of
// those permissions. Hence a path check: installed content may not live where an
// agent writes.
//
// Two trees qualify, and they are the two THIS launch hands to an agent: the
// workspace being mounted :rw, and paths.GlobalHome() (the shared /home/agent
// backing tree). The rule is deliberately incomplete — yolo knows those two, not
// that ~/code/other-project is agent-writable in some other jail — and §4.3a
// says so: it catches the shape that occurs (a daemon inside the repo being
// worked on) and the permission argument covers the rest.
//
// # THE FACES
//
// The rule is about a TARGET, so it has one face per place a target is named. The
// first batch landed the CONFIG faces (an inline `loopholes.<name>` entry's
// `command` and `doctor_cmd`, plus the spawn itself). §4.3a's landing item 1a also
// owes the MANIFEST faces, which is what LoopholeManifestPlacementProblems adds: a
// manifest's own `host_daemon.cmd` and `doctor_cmd`, and — the one that is not an
// argv at all — the loophole's MODULE DIR.
//
// The module dir is the face that subsumes the others, and that is why it is worth
// having. `{loophole_dir}` resolves to it, so a module dir inside an agent-writable
// tree means EVERY host-side field of that manifest names an agent-writable target,
// including the ones this rule cannot see (a Python daemon's imports, a binary's
// dlopen). Checking the dir is a statement about all of them at once — and it is
// why a refused module dir SUPPRESSES the argv refusals below it: one mistake, one
// message, the same rule the config faces follow against a scope violation.

import (
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// loopholeEntryPlacementProblems applies the rule to one MERGED
// `loopholes.<name>` entry.
//
// Only an INSTALL is checked — an entry with a `command`, on a name no manifest
// backs. An override installs nothing (its `command` is already refused as not
// overridable, and nothing reads a `doctor_cmd` off it), so checking it would
// report a second thing about a key that is inert anyway.
func loopholeEntryPlacementProblems(name string, spec *jsonx.OrderedMap, info *LoopholeInfo, workspace string) []string {
	if spec == nil || info != nil || !hasKey(spec, "command") {
		return nil
	}
	var out []string
	for _, key := range []string{"command", "doctor_cmd"} {
		out = append(out, LoopholePlacementProblems(
			"config.loopholes."+name+"."+key, specArgv(spec, key), workspace)...)
	}
	return out
}

// specArgv reads an argv-shaped config value as strings, keeping the INDEX of
// every element (a non-string becomes "" and is skipped by the check) so a
// message's [i] matches what the human wrote.
func specArgv(spec *jsonx.OrderedMap, key string) []string {
	v, present := spec.Get(key)
	if !present || v == nil {
		return nil
	}
	list, ok := asList(v)
	if !ok {
		return nil
	}
	out := make([]string, len(list))
	for i, e := range list {
		if s, isStr := asStr(e); isStr {
			out[i] = s
		}
	}
	return out
}

// agentWritableTree is one tree the placement rule refuses, plus the phrase that
// tells a reader why THAT tree.
type agentWritableTree struct {
	dir  string
	what string
}

// agentWritableTrees returns the trees this launch hands to an agent. workspace
// may be empty (a caller with no workspace in hand), in which case only the jail
// home tree is checked.
func agentWritableTrees(workspace string) []agentWritableTree {
	var out []agentWritableTree
	if ws := filepath.Clean(workspace); ws != "" && ws != "." && ws != "/" {
		out = append(out, agentWritableTree{ws,
			"the workspace this launch bind-mounts :rw"})
	}
	if home := filepath.Clean(paths.GlobalHome()); home != "" && home != "/" {
		out = append(out, agentWritableTree{home,
			"the jail home tree yolo hands the agent (" + home + ")"})
	}
	return out
}

// LoopholePlacementProblems applies the placement rule to ONE host execution's
// argv — a loophole's `command`, its `doctor_cmd`, or a manifest's
// `host_daemon.cmd` — and returns one message per element that resolves inside a
// tree an agent writes. label names the argv in config terms; each message
// appends the element's index.
//
// Elements are read the way the spawn reads them: a leading `~` expands, and a
// relative path resolves against the workspace, because the daemon is spawned
// with no explicit cwd and therefore inherits yolo's, which at launch is the
// workspace. Framework placeholders ({endpoint}, {socket}, {state},
// {loophole_dir}) name paths yolo owns and chooses, so they are skipped; so is
// anything starting with `-`, which is a flag rather than a target.
func LoopholePlacementProblems(label string, argv []string, workspace string) []string {
	trees := agentWritableTrees(workspace)
	if len(trees) == 0 {
		return nil
	}
	var out []string
	for i, a := range argv {
		target, ok := argvPathTarget(a, workspace)
		if !ok {
			continue
		}
		for _, tree := range trees {
			if !underTree(target, tree.dir) {
				continue
			}
			out = append(out, label+"["+itoa(i)+"]: "+target+" is inside "+tree.what+
				", where an agent can rewrite it between launches — installed content "+
				"may not live where an agent writes (loophole-packaging.md §4.3a). "+
				"Move the program outside that tree and name it there.")
			break
		}
	}
	return out
}

// LoopholeManifestPlacement is the input to the MANIFEST faces of the placement
// rule (§4.3a landing item 1a, "still owed"): a loophole's module dir plus the two
// host-side argvs its manifest declares. Every field is optional — a caller with
// only some of them in hand passes those.
type LoopholeManifestPlacement struct {
	// Name is the loophole's name. Every message leads with it, because §4.3a's
	// requirement is a refusal BY NAME and the reader's next action is to move that
	// loophole.
	Name string
	// ModuleDir is the module dir's HOST-side path — what {loophole_dir} resolves
	// to. Empty skips the dir face.
	ModuleDir string
	// HostDaemonCmd and DoctorCmd are the manifest's host-side argvs, with
	// {loophole_dir} ALREADY SUBSTITUTED. Passing them raw would let every element
	// carrying the token be skipped as a placeholder, which is exactly the case the
	// rule is here for.
	HostDaemonCmd []string
	DoctorCmd     []string
}

// LoopholeManifestPlacementProblems applies the placement rule to a manifest.
//
// workspace is the workspace this launch mounts :rw; "" means the caller has none
// in hand (the doctor path is the live example), which NARROWS the rule to the
// jail-home tree rather than disabling it. A narrower answer beats no answer, and
// the alternative — refusing to check at all without a workspace — is how the
// config faces came to miss the manifest ones for a batch.
//
// THE MODULE DIR IS CHECKED FIRST AND SUPPRESSES THE ARGV FACES, following the same
// no-double-message rule the config faces follow against a scope violation. A
// module dir inside an agent-writable tree already means every host-side field
// resolves there; reporting the dir and then each argv under it would be one
// mistake reported three times, and the fix for all three is the same move.
func LoopholeManifestPlacementProblems(p LoopholeManifestPlacement, workspace string) []string {
	if prob := loopholeModuleDirProblem(p.Name, p.ModuleDir, workspace); prob != "" {
		return []string{prob}
	}
	label := "loophole " + pytext.Repr(p.Name) + ": "
	var out []string
	out = append(out, LoopholePlacementProblems(label+"host_daemon.cmd", p.HostDaemonCmd, workspace)...)
	out = append(out, LoopholePlacementProblems(label+"doctor_cmd", p.DoctorCmd, workspace)...)
	return out
}

// loopholeModuleDirProblem is the dir face: the one target that is not an argv.
//
// It is the face that subsumes the others. `{loophole_dir}` resolves to this dir,
// so a module dir an agent writes means every host-side field of the manifest names
// an agent-writable target — including the ones no path check can see, since a
// Python daemon imports and a compiled one dlopens. §4.3a states that limit for the
// argv faces ("a tripwire against silent substitution, not a boundary"); checking
// the dir is how the tripwire covers the whole module rather than its entry point.
//
// Both the LEXICAL and the symlink-resolved spellings are tested, and the first hit
// wins. The trees themselves are deliberately not symlink-resolved (see underTree),
// so comparing only one spelling of the dir would make the answer depend on which
// side of the comparison happened to be canonical.
func loopholeModuleDirProblem(name, moduleDir, workspace string) string {
	if name == "" || moduleDir == "" {
		return ""
	}
	trees := agentWritableTrees(workspace)
	for _, dir := range []string{filepath.Clean(moduleDir), resolvePlacementPath(moduleDir)} {
		for _, tree := range trees {
			// Compare the resolved spelling against the RESOLVED tree too. On macOS
			// /var and /tmp are themselves symlinks (/private/var, /private/tmp), so a
			// resolved module dir under /private/var/... would otherwise never match a
			// lexically-spelled tree under /var/... — and a symlinked module dir pointing
			// into a workspace under /tmp would slip the tripwire.
			if !underTree(dir, tree.dir) && !underTree(dir, resolvePlacementPath(tree.dir)) {
				continue
			}
			return "loophole " + pytext.Repr(name) + ": module dir " + dir + " is inside " +
				tree.what + ", where an agent can rewrite it between launches — installed " +
				"content may not live where an agent writes (loophole-packaging.md §4.3a). " +
				"This covers the WHOLE module, not just its entry point: {loophole_dir} " +
				"resolves here, so every host-side field names something an agent can " +
				"replace, including what no path check can see (a Python daemon's imports, " +
				"a compiled one's dlopen). Install the loophole outside that tree."
		}
	}
	return ""
}

// resolvePlacementPath is the symlink-resolved absolute spelling of p, degrading to
// a lexical clean when the path does not exist — the same shape internal/loopholes
// uses to resolve a module dir, so the two cannot disagree about what {loophole_dir}
// became.
func resolvePlacementPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if evaled, err := filepath.EvalSymlinks(abs); err == nil {
		return evaled
	}
	return filepath.Clean(abs)
}

// shellish holds the characters that say "this element is not a plain path": a
// space or tab (a `sh -c` script body, or a flag and its value in one string), and
// the shell metacharacters that only ever appear in code. A path containing any of
// them is missed — which is the right trade for a tripwire, because the
// alternative is reading `sleep 300 & echo $! > /tmp/pid` as a relative path and
// refusing a working daemon at every launch.
const shellish = " \t\n;|&$><*?\"'`()"

// argvPathTarget resolves one argv element to the absolute path it would name,
// reporting false when the element does not denote a path at all (a bare program
// name resolved through PATH, a flag, a script body, or a framework placeholder).
func argvPathTarget(arg, workspace string) (string, bool) {
	if arg == "" || strings.HasPrefix(arg, "-") || strings.Contains(arg, "{") {
		return "", false
	}
	if strings.ContainsAny(arg, shellish) {
		return "", false
	}
	p := expandUser(arg)
	if !filepath.IsAbs(p) {
		// No separator means PATH lookup, not a path this rule can locate.
		if !strings.Contains(p, "/") || workspace == "" {
			return "", false
		}
		p = filepath.Join(workspace, p)
	}
	return filepath.Clean(p), true
}

// underTree reports whether p is dir itself or anything beneath it. Symlinks are
// deliberately not resolved: §4.3a calls the rule a tripwire against the shape
// that occurs, not a boundary, and an EvalSymlinks here would make the answer
// depend on which of the two trees happens to exist on this host.
func underTree(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+"/")
}
