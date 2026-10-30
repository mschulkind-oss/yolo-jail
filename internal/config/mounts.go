package config

// mounts.go is the `mounts` key: its two element forms (docs/reference/context-mounts.md#the-config-shape),
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
	"encoding/json"
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
	// RW is true only for an object element that says "mode": "rw" or "write": true (the
	// readable config's spelling, docs/design/config-project.md §1; RC-D24).
	RW bool
	// Optional is true only for an object element that says "optional": true: a backend that
	// cannot deliver it skips it with one line and the launch goes on, where a required element
	// refuses (docs/reference/context-mounts.md#optional-mounts). It never softens a refusal
	// about safety rather than delivery (the read-write refusal set, the broker mount fence, a
	// destination collision): "optional" says "may be absent", never "may be unsafe".
	Optional bool
	// Spec renders the element as written — the string itself, or the object's compact
	// JSON — for messages that must name the entry the user wrote.
	Spec string
}

// OptionalSpelling is the element as an object with "optional": true: the exact JSON a refusal
// hands the user to paste in its place. It keeps the host as written, the destination when one
// was written, and the mode, so the pasted element validates and lands where this one would.
func (m ContextMount) OptionalSpelling() string {
	mode := "ro"
	if m.RW {
		mode = "rw"
	}
	if m.At != "" {
		return `{"host": ` + jsonString(m.Host) + `, "at": ` + jsonString(m.At) + `, "mode": "` + mode +
			`", "optional": true}`
	}
	return `{"host": ` + jsonString(m.Host) + `, "mode": "` + mode + `", "optional": true}`
}

// jsonString is s as a JSON string literal. Not Go's %q, whose escapes (\x00,   left raw,
// \a) are not all JSON: a path with an odd byte would hand the user a line that does not parse.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// OptionalMountSkipLine is the one line every backend prints for an optional `mounts` element it
// does not deliver: the host path as written, the jail path, and why in a few words. Plain, no
// markup and no advice, by the maintainer's ruling: the user marked it optional, so the skip is
// the expected outcome rather than a problem to fix (docs/reference/context-mounts.md#optional-mounts).
func OptionalMountSkipLine(host, dest, why string) string {
	return "skipped optional mount: " + host + " → " + dest + " (" + why + ")"
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
var mountObjectKeys = map[string]struct{}{"host": {}, "at": {}, "mode": {}, "optional": {}}

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
		if strings.HasSuffix(s, ":optional") {
			// Refused as `:rw` is: a suffix is not an option, and parsed as a path it names a
			// directory that does not exist, so the mount would vanish with a warning.
			host, at := SplitMountString(strings.TrimSuffix(s, ":optional"))
			return m, []string{fmt.Sprintf("%s: %q ends in \":optional\", which is not an option — "+
				"a string entry is always required. An optional mount is the object form: %s",
				where, s, ContextMount{Host: host, At: at}.OptionalSpelling())}
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
		// `write` is the readable config's spelling (RC-D24), kept out of mountObjectKeys,
		// which is also the legacy JSONC schema census.
		if _, known := mountObjectKeys[k]; !known && k != "write" {
			problems = append(problems, fmt.Sprintf("%s: unknown key %q (known: at, host, mode, optional, write)", where, k))
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
	writeV, hasWrite := obj.Get("write")
	switch {
	case hasWrite && hasMode:
		problems = append(problems, where+`: "write" and "mode" both say the mode — keep one `+
			`("write = true" in a TOML config, "mode": "rw" in a JSONC one)`)
	case hasWrite:
		b, ok := writeV.(bool)
		if !ok {
			problems = append(problems, fmt.Sprintf(`%s: "write" must be true or false (got %s)`,
				where, reprAny(writeV)))
		}
		m.RW = b
		// A REQUEST'S HOST IS ABSOLUTE OR "~" (RC-D24): a relative one would resolve against
		// wherever yolo happened to be started, so the path approved would not be a path.
		if b && isStr && host != "" && !strings.HasPrefix(host, "/") && host != "~" && !strings.HasPrefix(host, "~/") {
			problems = append(problems, fmt.Sprintf(`%s: a writable mount's host %q must be absolute `+
				`or start with "~" — a relative path would resolve against wherever yolo was started`,
				where, host))
		}
	case !hasMode:
		problems = append(problems, where+`: "mode" is required in the object form — "ro" or "rw". `+
			"There is no default, so a writable mount is never one nobody wrote")
	case mode == "rw":
		m.RW = true
	case mode != "ro":
		problems = append(problems, fmt.Sprintf(`%s: "mode" must be "ro" or "rw" (got %s)`,
			where, reprAny(modeV)))
	}
	if optV, hasOpt := obj.Get("optional"); hasOpt {
		b, ok := optV.(bool)
		if !ok {
			problems = append(problems, fmt.Sprintf(`%s: "optional" must be true or false (got %s)`,
				where, reprAny(optV)))
		}
		m.Optional = b
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
	out, _ := parseMountsIndexed(cfg)
	return out
}

// parseMountsIndexed is ParseMounts plus each element's index in the list, which the
// elements it skips make differ from its index in the result.
func parseMountsIndexed(cfg *jsonx.OrderedMap) ([]ContextMount, []int) {
	if cfg == nil {
		return nil, nil
	}
	v, ok := cfg.Get(mountsKey)
	if !ok {
		return nil, nil
	}
	list, ok := asList(v)
	if !ok {
		return nil, nil
	}
	var out []ContextMount
	var indices []int
	for i, el := range list {
		m, problems := ParseMountElement(el, fmt.Sprintf("config.mounts[%d]", i))
		if len(problems) == 0 {
			out = append(out, m)
			indices = append(indices, i)
		}
	}
	return out, indices
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

// rwMountTrusted is THE TRUST PREDICATE of docs/reference/context-mounts.md#where-a-read-write-mount-may-be-declared: may a
// read-write element declared in this scope be honored?
//
// Its COMPUTATION is kept one function so that it can change in one place: user scope only,
// which is cache_relocations' precedent (a workspace config is agent-editable, so it cannot
// GRANT a read-write host mount). RC-D24 (docs/design/readable-config.md) supersedes OQ-WT1
// with one exception, judged beside this predicate rather than inside it because it needs the
// element's file and the user's ceiling: a TOML workspace file may REQUEST one when the user
// config allows it, and the config-change gate approves it (mountsrequest.go). Both halves read
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
// config and read-write ones from HERE and from the config-change gate's own read of RC-D24's
// allowed TOML requests (ApproveWorkspaceRead) (context-mounts.md#where-a-read-write-mount-may-be-declared), and validateMountScope's
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

// MountElementWhere is where the `mounts` element m was written, for a launch message that
// names it: its validator path ("config.mounts[2]", the spelling `yolo check` reports it by)
// and the file, line and column of each place that wrote it, highest precedence first
// (Sources.Locations). The element is found by its spelling (Spec) in the composed config read
// again with its record, so this belongs on a reporting path: a launch with nothing to report
// never reads the files twice.
//
// ("", nil) when no element of the composed config is spelled as m; a path and nil locations
// when the config came from no file the loader read (the in-jail copy of the host's config,
// a map a caller built by hand).
func MountElementWhere(workspace string, m ContextMount) (string, []string) {
	cfg, src, err := LoadConfigWithSources(workspace, false, func(string) {})
	if err != nil || m.Spec == "" {
		return "", nil
	}
	elements, indices := parseMountsIndexed(cfg)
	for i, e := range elements {
		if e.Spec == m.Spec && e.RW == m.RW {
			path := fmt.Sprintf("config.mounts[%d]", indices[i])
			return path, src.Locations(path)
		}
	}
	return "", nil
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

// rwMountRefusal is docs/reference/context-mounts.md#the-refusal-set's refusal set for a read-write
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
//     locks the workspace config file included.
//
// Clause 3 (macos-user: /var/yolo-jail and the sandbox home) is not here: it is a fact about
// one backend's layout, judged by that backend's siting at launch (macosuser.SiteContextLinks,
// through run's planMacosUserCtxMounts), which refuses either mode there.
//
// ~/.ssh, ~/.aws and the like are deliberately NOT named (CX-D2): whoever gets a USER-SCOPE
// element past the trust predicate already holds the host user's authority. That premise does
// not hold for a project's request (RC-D24), which the agent wrote: there the human's approval
// at the gate is the predicate, so the gate shows the destination the request truly resolves to,
// and a source written where the agent can plant a link is refused (mountPathRefusal,
// mountsrequestbind.go); the exemption is not relied on for it.
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
			"including the lock on the workspace config file"
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
// rule (docs/reference/context-mounts.md#the-config-shape).
func validateMounts(config *jsonx.OrderedMap, workspace string, errs, warns *[]string, edit ...*editValidationContext) {
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
		// An OPTIONAL element's missing source is no finding here: the launch prints its one
		// skip line and `yolo check` reports it as information (context-mounts.md#optional-mounts).
		if !pathExists(resolvedHost) && !m.Optional {
			add(warns, fmt.Sprintf("%s: host path does not exist and will be skipped: %s",
				where, resolvedHost))
		}
	}
	validateMountScope(config, workspace, errs, edit...)
	validateRWMountSources(config, workspace, errs)
	// A bare element's destination is the basename of its RESOLVED source, which only the
	// host can compute: in the jail's own workspace the sources are host paths that are not
	// here, so the comparison would be between names the host never uses. The host run has
	// already made it.
	if !(inJail() && jailOwnWorkspace(workspace)) {
		if len(edit) != 0 && edit[0] != nil {
			validateMountDestinations(config, errs, edit[0].packs)
		} else {
			validateMountDestinations(config, errs)
		}
	}
}

// validateMountScope judges every element the WORKSPACE config declares. ValidateConfig only
// ever sees the MERGED map, which carries no provenance, so — as validateCacheRelocations does —
// the workspace config is re-read to learn which elements came from it. Nothing is re-read when
// the merged map declares no mount.
//
//   - Read-only or writable, a source written inside the workspace or inside another mount is
//     refused (mountPathRefusal, oq-cx-symlink in docs/reference/context-mounts.md).
//   - A read-write element is the trust predicate's refusal half: declared in a scope
//     rwMountTrusted rejects, it is a `yolo check` error, and so a launch refusal (preflight
//     refuses on any validation error). THE ONE EXCEPTION IS RC-D24's REQUEST
//     (mountsrequest.go): an element every one of whose writers is a TOML workspace file is a
//     request, honored when the user config's `allow.mounts.on_request` lists "write" and
//     otherwise refused naming that line.
//
// In a jail, for the jail's own workspace, nothing is judged: the sources are host paths that
// are not here, and the host judged them against the host user's config.
func validateMountScope(config *jsonx.OrderedMap, workspace string, errs *[]string, edit ...*editValidationContext) {
	if len(ParseMounts(config)) == 0 || (inJail() && jailOwnWorkspace(workspace)) {
		return
	}
	wsCfg, wsNode, err := loadMountsWorkspaceConfig(workspace, edit...)
	if err != nil || wsCfg == nil {
		return
	}
	// Located in the workspace files' own record (sources.go), at the element itself: the
	// merged record could only say where the `mounts` key is, in every file that writes one.
	elements, indices := parseMountsIndexed(wsCfg)
	if len(elements) == 0 {
		return
	}
	user := validationUserScope(edit...)
	allowed := userAllowsWritableMountRequests(user)
	for n, m := range elements {
		node := wsNode.key(mountsKey).elem(indices[n])
		if m.RW && !rwMountTrusted(mountScopeWorkspace) {
			if !isWorkspaceRequest(node) {
				add(errs, locatedAt(node, workspaceReadWriteMountRefusal(m, node)))
				continue
			}
			if !allowed {
				add(errs, locatedAt(node, writableMountRequestRefusal(m)))
				continue
			}
		}
		others := make([]ContextMount, 0, len(elements))
		for k, o := range elements {
			if k != n {
				others = append(others, o)
			}
		}
		if user != nil {
			others = append(others, ParseMounts(user)...)
		}
		if why := mountPathRefusal(m, workspace, others); why != "" {
			add(errs, locatedAt(node, why))
		}
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
	// Named by its index in the merged list, so the location a caller adds (sources.go) is
	// the element's own line rather than the `mounts` key's in every file that writes one.
	elements, indices := parseMountsIndexed(config)
	for n, m := range elements {
		if !m.RW {
			continue
		}
		if why := rwMountRefusal(expandUser(m.Host), workspace); why != "" {
			add(errs, fmt.Sprintf("config.mounts[%d]: %s is refused: %s", indices[n], m.Spec, why))
		}
	}
}

// validateMountDestinations refuses two context mounts at one jail path: a `mounts` element
// against another, against a SELECTED pack's `mount` at /ctx/<into>, or at, inside or
// containing one of yolo's own children of /ctx (paths.ReservedContextPaths). Before this, a
// collision was a launch podman refused late ("duplicate mount destination",
// context-mounts.md#the-config-shape).
//
// Only collisions a `mounts` element is party to are this key's to report; the pack set is
// resolved only when there is an element to compare it with.
//
// ONLY WHAT THE LAUNCH WOULD BIND TAKES PART: an element or a pack grant whose source does
// not exist is skipped at launch with a warning (run.configCtxMounts, run.packCtxMounts),
// so it lands nowhere and collides with nothing. A config naming one path per machine —
// "~/src/lib" here, "/opt/lib" there, both /ctx/lib — started a jail on every machine before
// this check existed, and refusing it would refuse a launch podman would have made.
func validateMountDestinations(config *jsonx.OrderedMap, errs *[]string, selectedPacks ...[]*packload.Pack) {
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
	var selected []*packload.Pack
	if len(selectedPacks) != 0 {
		selected = selectedPacks[0]
	} else {
		selected, _ = resolveSelectedPacks()
	}
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
