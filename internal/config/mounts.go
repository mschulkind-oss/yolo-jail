package config

// mounts.go is the `mounts` key: its two element forms (docs/design/context-mounts.md §2.1),
// and the two gates the read-write form adds — WHERE one may be declared (§2.2's trust
// predicate) and WHICH host sources may be one (§2.3's refusal set).
//
// THE STRING FORM IS READ-ONLY ONLY, and unchanged: "host" lands at /ctx/<basename after
// resolution>, "host:/dest" at /dest. The writable form is an OBJECT element in the same
// list — {"host": ..., "mode": "rw", "at": ...} — with a REQUIRED mode, because a mode
// nobody wrote is how a writable mount gets handed out by accident. A `:rw` string suffix is
// refused with a message naming the object form rather than parsed as a mode: paths may
// contain colons, and the `:ro` suffix already mis-parses into a skipped mount.
//
// ⚠ EVERY READER OF THIS KEY GOES THROUGH ParseMounts. Three readers used to read string
// elements only (validateMounts, the broker mount fence's mountHostSources, and the run
// pipeline's loops), so an object element added to the list would have been skipped by the
// fence in silence — CX-D1's warning, and the reason there is one parser.

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// mountsKey is the one config key this file reads.
const mountsKey = "mounts"

// ContextMount is one well-formed `mounts` element.
type ContextMount struct {
	// Host is the host path as written: "~" unexpanded, symlinks unresolved. Each reader
	// resolves it on its own side of the boundary (a host launch against the host's home).
	Host string
	// At is the jail path as written, or "" for the default: /ctx/<basename of the RESOLVED
	// host path>, which DestFor computes.
	At string
	// RW is true only for an object element that says "mode": "rw".
	RW bool
	// Spec renders the element as written — the string itself, or the object's compact
	// JSON — for messages that must name the entry the user wrote.
	Spec string
}

// DestFor is the element's jail path, given its resolved host source.
func (m ContextMount) DestFor(resolvedSource string) string {
	if m.At != "" {
		return m.At
	}
	return paths.ContainerContextDir + "/" + filepath.Base(resolvedSource)
}

// SplitMountString is the string form's "host:container" split: the LAST colon followed by
// an absolute container path. A string with no such colon is a bare host path, whose
// destination is the default (at == "").
func SplitMountString(s string) (host, at string) {
	if i := strings.LastIndex(s, ":"); i > 0 && i+1 < len(s) && s[i+1] == '/' {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// mountObjectKeys are the object form's keys; any other key is a typo, reported as one.
var mountObjectKeys = map[string]struct{}{"host": {}, "at": {}, "mode": {}}

// ParseMountElement parses one `mounts` element. `where` names it in problems (e.g.
// "config.mounts[2]"); a non-empty problem list means the element is refused and m is
// meaningless.
func ParseMountElement(v any, where string) (m ContextMount, problems []string) {
	if s, ok := asStr(v); ok {
		m.Spec = s
		if strings.HasSuffix(s, ":rw") {
			// Not a mode, and never parsed as one: the object form is the only writable
			// spelling, so the refusal names it rather than guessing what was meant. The
			// suggestion splits a destination into `at`: the object form refuses a colon in
			// `host`, so suggesting "host:/ctx/d" as the host would answer one error with
			// another.
			host, at := SplitMountString(strings.TrimSuffix(s, ":rw"))
			suggestion := fmt.Sprintf(`{"host": %q, "mode": "rw"}`, host)
			if at != "" {
				suggestion = fmt.Sprintf(`{"host": %q, "at": %q, "mode": "rw"}`, host, at)
			}
			return m, []string{fmt.Sprintf("%s: %q ends in \":rw\", which is not a mode — "+
				"a string entry is always read-only. A read-write mount is the object form: %s",
				where, s, suggestion)}
		}
		m.Host, m.At = SplitMountString(s)
		if m.Host == "" {
			return m, []string{where + ": host mount path cannot be empty"}
		}
		return m, nil
	}
	obj, ok := asMap(v)
	if !ok {
		return m, []string{where + `: expected a string or an object {"host": ..., "mode": "ro"|"rw"}`}
	}
	m.Spec, _ = jsonx.DumpsCompact(obj)
	for _, k := range obj.Keys() {
		if _, known := mountObjectKeys[k]; !known {
			problems = append(problems, fmt.Sprintf("%s: unknown key %q (known: at, host, mode)", where, k))
		}
	}
	hostV, _ := obj.Get("host")
	host, isStr := asStr(hostV)
	switch {
	case !isStr:
		problems = append(problems, where+`: "host" is required and must be a string`)
	case host == "":
		problems = append(problems, where+": host mount path cannot be empty")
	case strings.Contains(host, ":"):
		// A bind is spelled `-v src:dest[:ro]`, and nothing in that grammar escapes a colon,
		// so a colon in either path would be read as the next field.
		problems = append(problems, fmt.Sprintf("%s: host path %q contains a colon, which a "+
			"bind mount cannot carry", where, host))
	}
	m.Host = host
	modeV, hasMode := obj.Get("mode")
	mode, _ := asStr(modeV)
	switch {
	case !hasMode:
		problems = append(problems, where+`: "mode" is required in the object form — "ro" or "rw". `+
			"There is no default, so a writable mount is never one nobody wrote")
	case mode == "rw":
		m.RW = true
	case mode != "ro":
		problems = append(problems, fmt.Sprintf(`%s: "mode" must be "ro" or "rw" (got %s)`,
			where, reprAny(modeV)))
	}
	if atV, hasAt := obj.Get("at"); hasAt {
		at, ok := asStr(atV)
		switch {
		case !ok || at == "":
			problems = append(problems, where+`: "at" must be a non-empty absolute jail path`)
		case !strings.HasPrefix(at, "/"):
			problems = append(problems, where+": container mount path must be absolute")
		case strings.Contains(at, ":"):
			problems = append(problems, fmt.Sprintf("%s: jail path %q contains a colon, which a "+
				"bind mount cannot carry", where, at))
		case containsDotDot(at):
			problems = append(problems, fmt.Sprintf("%s: jail path %q must not contain '..'", where, at))
		default:
			m.At = path.Clean(at)
		}
	}
	// A WRITABLE BIND ONLY UNDER /ctx. Anywhere else it could shadow a home path or a
	// composed surface with a host tree the jail can write, and writable_home_dirs and
	// cache_relocations are the shaped tools for those cases (§2.1).
	if m.RW && m.At != "" && !strings.HasPrefix(m.At, paths.ContainerContextDir+"/") {
		problems = append(problems, fmt.Sprintf("%s: a read-write mount must land under %s/ "+
			"(got \"at\": %q) — a writable bind anywhere else could shadow a home path or a "+
			"composed surface", where, paths.ContainerContextDir, m.At))
	}
	return m, problems
}

// reprAny renders a config value for a message.
func reprAny(v any) string {
	s, err := jsonx.DumpsCompact(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return s
}

// ParseMounts returns cfg's well-formed `mounts` elements in order. Malformed ones are
// dropped here and reported by validateMounts, which a launch runs first.
func ParseMounts(cfg *jsonx.OrderedMap) []ContextMount {
	if cfg == nil {
		return nil
	}
	v, ok := cfg.Get(mountsKey)
	if !ok {
		return nil
	}
	list, ok := asList(v)
	if !ok {
		return nil
	}
	var out []ContextMount
	for i, el := range list {
		m, problems := ParseMountElement(el, fmt.Sprintf("config.mounts[%d]", i))
		if len(problems) == 0 {
			out = append(out, m)
		}
	}
	return out
}

// mountScope is the set of config files a `mounts` element was declared in.
type mountScope int

const (
	// mountScopeUser is ~/.config/yolo-jail/config.jsonc, the files it includes, and a
	// --user-layer. None of them is writable from inside a jail.
	mountScopeUser mountScope = iota
	// mountScopeWorkspace is yolo-jail.jsonc, yolo-jail.local.jsonc and whatever either
	// includes — every one of them editable by the agent, through the /workspace bind.
	mountScopeWorkspace
)

// rwMountTrusted is THE TRUST PREDICATE of docs/design/context-mounts.md §2.2: may a
// read-write element declared in this scope be honored?
//
// Its COMPUTATION is not this doc's, and it is kept one function so that it can change in
// one place: workspace-config-trust.md owns it, and until its OQ-WT1 rules, that doc's rule
// D is the answer — user scope only, which is cache_relocations' precedent (a workspace
// config is agent-editable, so it cannot grant a read-write host mount). Both halves read
// it: rwElements (the loader, which decides what is MOUNTED) and validateMountScope (the
// `yolo check` error and launch refusal for an element it rejects). An element it rejects
// is never dropped silently and never downgraded to read-only, because a downgrade is a
// mount the user did not write.
func rwMountTrusted(scope mountScope) bool {
	return scope == mountScopeUser
}

// rwElements returns cfg's read-write elements, or none when the predicate does not trust
// the scope cfg was read from.
func rwElements(cfg *jsonx.OrderedMap, scope mountScope) []ContextMount {
	if !rwMountTrusted(scope) {
		return nil
	}
	var out []ContextMount
	for _, m := range ParseMounts(cfg) {
		if m.RW {
			out = append(out, m)
		}
	}
	return out
}

// LoadRWMounts returns the read-write `mounts` elements a TRUSTED source declared: today
// the user scope, read from paths.UserConfigPath() directly (with its includes and any
// --user-layer), never from the merged config.
//
// THIS IS THE BOUNDARY, for LoadCacheRelocations' reason: the merge records no provenance,
// so a builder reading rw elements out of it could not say whether one came from a file the
// agent can edit. The run pipeline therefore takes read-only elements from the merged
// config and read-write ones from HERE (context-mounts.md §2.2), and validateMountScope's
// refusal of a workspace-scope element is the loud half, not the boundary itself.
//
// Inside a jail the user scope is that jail's own (the generated config.jsonc, which never
// carries `mounts`, plus a --user-layer), so a nested launch honors exactly what its own
// user scope declares — trust flowing downward, and the paths are the jail's.
func LoadRWMounts(warn Warn) ([]ContextMount, error) {
	if warn == nil {
		warn = func(string) {}
	}
	p := paths.UserConfigPath()
	userCfg, err := loadUserScopeConfig(p, p, true, warn)
	if err != nil {
		return nil, err
	}
	return rwElements(userCfg, mountScopeUser), nil
}

// hasRWMount reports whether cfg declares any read-write element.
func hasRWMount(cfg *jsonx.OrderedMap) bool {
	for _, m := range ParseMounts(cfg) {
		if m.RW {
			return true
		}
	}
	return false
}

// rwBoundaryConsequence is what a read-write mount of each credential-boundary root would
// hand the jail — this file's half of the refusal; the collision itself is the shared
// predicate's (paths.WorkspaceScopeBreach).
var rwBoundaryConsequence = map[paths.ScopeRootKind]string{
	paths.RootHome: "every credential the jail is walled off from — ~/.ssh, ~/.gitconfig, " +
		"cloud and agent tokens — would be readable AND writable from inside it",
	paths.RootStateDir: "the jail could rewrite yolo's own state: every workspace's home " +
		"overlay, the approval records, the fetched pack trees and the flake bundle every " +
		"launch runs",
	paths.RootUserConfigDir: "the jail could rewrite the user-scope config that decides the " +
		"NEXT launch's packs, host files and mounts",
}

// rwMountRefusal is docs/design/context-mounts.md §2.3's refusal set for a read-write
// source, on RESOLVED paths: "" when the source may be mounted writable, else why not.
//
//  1. The credential-boundary predicate, SHARED rather than copied: the source is or
//     contains the home, ~/.config/yolo-jail or ~/.local/share/yolo-jail, or sits inside
//     either yolo directory. paths.WritableSourceScopeBreach is WorkspaceScopeBreach's own
//     rule body without the one exemption that is a workspace's alone — the capture store,
//     where `yolo capture` works in a scratch tree, and which a writable mount would open to
//     every other workspace's installers (CX-D16). It is also what keeps boundary-broker.md
//     BB-D34's premise true — no mount can write the approval record — now that a mount can
//     write.
//  2. Overlap with the workspace in either direction. Inside it, the mount is a second
//     name for bytes the jail already writes at /workspace; containing it, it is a second
//     writable path to the workspace that bypasses workspace_readonly, the overlay that
//     locks yolo-jail.jsonc included.
//
// Clause 3 (macos-user: /var/yolo-jail and the sandbox home) is not here: that backend
// delivers no context mount yet and refuses every declared one (run's
// refuseMacosUserCtxMounts), so there is no macos-user source for it to judge.
//
// ~/.ssh, ~/.aws and the like are deliberately NOT named (CX-D2): whoever gets an element
// past the trust predicate already holds the host user's authority.
func rwMountRefusal(source, workspace string) string {
	if b := paths.WritableSourceScopeBreach(source); b != nil {
		msg := b.WhatFor("the read-write source")
		if why := rwBoundaryConsequence[b.Kind]; why != "" {
			msg += ": " + why
		}
		return msg
	}
	src := expandAndResolve(source)
	ws := expandAndResolve(workspace)
	switch {
	case pathUnderOrEqual(src, ws):
		return "the read-write source " + src + " is inside the workspace " + ws +
			", which the jail already writes at /workspace — the mount would only be a " +
			"second name for the same bytes"
	case pathUnderOrEqual(ws, src):
		return "the read-write source " + src + " contains the workspace " + ws +
			": a second writable path to the workspace would bypass workspace_readonly, " +
			"including the lock on yolo-jail.jsonc"
	}
	return ""
}

// pathUnderOrEqual reports whether child is base or a descendant of base. Both must be
// resolved already.
func pathUnderOrEqual(child, base string) bool {
	if child == base {
		return true
	}
	rel, err := filepath.Rel(base, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// validateMounts checks the `mounts` key: each element's shape, the existence warning, and
// for the read-write form the trust predicate, the refusal set and the duplicate-destination
// rule (docs/design/context-mounts.md §2.1–§2.3).
func validateMounts(config *jsonx.OrderedMap, workspace string, errs, warns *[]string) {
	mountsV, present := config.Get(mountsKey)
	if !present || mountsV == nil {
		return
	}
	mounts, ok := asList(mountsV)
	if !ok {
		add(errs, "config.mounts: expected a list")
		return
	}
	for idx, mountV := range mounts {
		where := fmt.Sprintf("config.mounts[%d]", idx)
		m, problems := ParseMountElement(mountV, where)
		if len(problems) > 0 {
			for _, p := range problems {
				add(errs, p)
			}
			continue
		}
		resolvedHost := expandAndResolve(m.Host)
		if !pathExists(resolvedHost) {
			add(warns, fmt.Sprintf("%s: host path does not exist and will be skipped: %s",
				where, resolvedHost))
		}
	}
	validateMountScope(config, workspace, errs)
	validateRWMountSources(config, workspace, errs)
	// A bare element's destination is the basename of its RESOLVED source, which only the
	// host can compute: in the jail's own workspace the sources are host paths that are not
	// here, so the comparison would be between names the host never uses. The host run has
	// already made it.
	if !(inJail() && jailOwnWorkspace(workspace)) {
		validateMountDestinations(config, errs)
	}
}

// validateMountScope is the trust predicate's refusal half: a read-write element declared
// in a scope rwMountTrusted rejects is a `yolo check` error, and so a launch refusal
// (preflight refuses on any validation error). ValidateConfig only ever sees the MERGED map,
// which carries no provenance, so — as validateCacheRelocations does — the workspace config
// is re-read to learn which elements came from it. Nothing is re-read when the merged map
// declares no read-write element, which is every config today.
func validateMountScope(config *jsonx.OrderedMap, workspace string, errs *[]string) {
	if rwMountTrusted(mountScopeWorkspace) || !hasRWMount(config) {
		return
	}
	wsCfg, err := LoadWorkspaceConfig(workspace, false, func(string) {})
	if err != nil || wsCfg == nil {
		return
	}
	for _, m := range ParseMounts(wsCfg) {
		if !m.RW {
			continue
		}
		add(errs, fmt.Sprintf("config.mounts: %s is in the workspace config, and a read-write "+
			"mount is user-scope only — move it to %s. A workspace config is agent-editable, "+
			"so it cannot grant a read-write host mount (docs/design/context-mounts.md §2.2; "+
			"a read-only entry may stay where it is)", m.Spec, paths.UserConfigPath()))
	}
}

// validateRWMountSources runs the refusal set (rwMountRefusal) over every read-write
// element, on resolved host paths.
//
// SKIPPED ONLY FOR THE JAIL'S OWN WORKSPACE IN A JAIL, where the config being validated is
// the host's assembled one and its sources are host paths that do not exist in here (the
// in-jail `mounts` warning's reason). A nested launch from a jail validates its own user
// scope against its own paths, so the refusal runs there too.
func validateRWMountSources(config *jsonx.OrderedMap, workspace string, errs *[]string) {
	if inJail() && jailOwnWorkspace(workspace) {
		return
	}
	for _, m := range ParseMounts(config) {
		if !m.RW {
			continue
		}
		if why := rwMountRefusal(expandUser(m.Host), workspace); why != "" {
			add(errs, fmt.Sprintf("config.mounts: %s is refused: %s", m.Spec, why))
		}
	}
}

// validateMountDestinations refuses two context mounts at one jail path: a `mounts` element
// against another, against a SELECTED pack's `mount` at /ctx/<into>, or at, inside or
// containing one of yolo's own children of /ctx (paths.ReservedContextPaths). Before this, a
// collision was a launch podman refused late ("duplicate mount destination",
// context-mounts.md §2.1 and §3.2).
//
// Only collisions a `mounts` element is party to are this key's to report; the pack set is
// resolved only when there is an element to compare it with.
//
// ONLY WHAT THE LAUNCH WOULD BIND TAKES PART: an element or a pack grant whose source does
// not exist is skipped at launch with a warning (run.configCtxMounts, run.packCtxMounts),
// so it lands nowhere and collides with nothing. A config naming one path per machine —
// "~/src/lib" here, "/opt/lib" there, both /ctx/lib — started a jail on every machine before
// this check existed, and refusing it would refuse a launch podman would have made.
func validateMountDestinations(config *jsonx.OrderedMap, errs *[]string) {
	elements := ParseMounts(config)
	if len(elements) == 0 {
		return
	}
	type claim struct{ dest, who string }
	var claims []claim
	for _, m := range elements {
		source := expandAndResolve(m.Host)
		if !pathExists(source) {
			continue // skipped at launch, and warned about by validateMounts
		}
		claims = append(claims, claim{path.Clean(m.DestFor(source)), "the entry " + m.Spec})
	}
	nConfig := len(claims)
	if nConfig == 0 {
		return
	}
	// YOLO'S OWN CHILDREN OF /ctx (paths.ReservedContextPaths): the same namespace, §3.2.
	// Nesting counts here, where it does not between two declarations: podman refuses a
	// second bind at one path, and for a nested pair it creates the inner mountpoint inside
	// the outer bind's HOST directory — so an element inside one of these writes into a tree
	// yolo stages, and one containing them (an `at` of /ctx itself) has podman create yolo's
	// mountpoints inside the user's own source, `:ro` or not.
	for _, c := range claims {
		for _, r := range paths.ReservedContextPaths() {
			var where string
			switch {
			case c.dest == r.Path:
				where = "at " + r.Path
			case pathUnderOrEqual(c.dest, r.Path):
				where = "at " + c.dest + ", inside " + r.Path
			case pathUnderOrEqual(r.Path, c.dest):
				where = "at " + c.dest + ", which contains " + r.Path
			default:
				continue
			}
			add(errs, fmt.Sprintf("config.mounts: %s lands %s, where yolo binds %s itself — "+
				"give it a different \"at\" (or \"host:/ctx/<name>\")", c.who, where, r.Holds))
			break
		}
	}
	selected, _ := resolveSelectedPacks()
	for _, p := range selected {
		granted, _ := p.HonoredMounts()
		for _, mt := range granted {
			if !pathExists(expandUser("~/" + mt.From)) {
				continue // skipped at launch: the pack's content is not on this machine
			}
			claims = append(claims, claim{
				path.Clean(packload.MountCtxPath(mt)),
				"pack " + p.Name + "'s mount of ~/" + mt.From})
		}
	}
	first := map[string]int{}
	for i, c := range claims {
		j, seen := first[c.dest]
		if !seen {
			first[c.dest] = i
			continue
		}
		if i >= nConfig && j >= nConfig {
			continue // two packs: not this key's collision
		}
		add(errs, fmt.Sprintf("config.mounts: %s and %s both land at %s — one jail path can "+
			"carry only one mount; give one of them a different \"at\" (or \"host:/ctx/<name>\")",
			claims[j].who, c.who, c.dest))
	}
}
