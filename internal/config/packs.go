package config

// packs.go implements the `packs` config key: agent-configuration packs, fetched
// host-side and staged into the jail.
//
// SCOPE, and it is the whole security model of the feature: `packs` is USER-SCOPE
// ONLY, read from paths.UserConfigPath() DIRECTLY rather than from the merged
// config. Workspace scope is therefore inexpressible by construction — not
// validated-against, which is a weaker guarantee that has to be maintained
// correctly forever.
//
// The reason is the same one that retired host_claude_files: a workspace config
// travels with the repo and is agent-editable, so it must not be able to name
// content that enters the jail. A pack can carry skills and briefing prose an agent
// then follows, and (later) surface fragments; that is influence a committed,
// agent-writable file may not have. This mirrors LoadHostFiles' source-bearing half
// exactly (hostfiles.go), which is the only shape of this boundary that has held
// here.
//
// A repo that wants to configure its own agents does not need this key: it already
// has a git repo and can lay out whatever it likes in the workspace. Packs solve
// cross-machine, cross-person distribution, which is inherently user-level. That
// ruling is why there is no workspace half, no `pack_requests`, and no approval
// verb to promote one.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs with packload
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// packsKey is the top-level config key.
const packsKey = "packs"

// NoPacksMessage / NoPacksGuidance are the empty-packs notice, shared by the two
// surfaces that report it: the launch-time warning (internal/cli/run) and the
// `yolo check` Packs section (internal/cli/check). It lives HERE, next to LoadPacks,
// because check cannot import the run pipeline — the dependency only ever goes the
// other way — and a copy in each was free to drift in wording with no test noticing.
// Both already import this package to load the packs it describes.
//
// NoPacksMessage carries NO trailing period so a `yolo check` badge can use it
// verbatim (no badge line in the reporter ends in one), while the launch notice, which
// is prose, appends one. That is the whole reason the punctuation is not baked in.
const (
	NoPacksMessage  = "No packs are configured, so this jail has no coding agent"
	NoPacksGuidance = "An agent arrives as a pack. The packs yolo ships are selected by " +
		"name — add `\"packs\": [\"claude\"]` to ~/.config/yolo-jail/config.jsonc, or run " +
		"`yolo pack --help` for what packs deliver and how to add one from elsewhere."
)

// knownPackKeys is the accepted key set of the object form.
var knownPackKeys = set(
	"source", "name", "only", "exclude",
)

// PackEntry is one validated `packs` entry, lowered from either the string (sugar)
// or object form.
//
// The json tags define the YOLO_PACKS wire form (see MarshalPacks): the host CLI
// resolves entries and hands them to the entrypoint through that env var, so the
// entrypoint never re-reads config — the same contract YOLO_HOST_FILES uses, and
// the reason the in-jail side cannot widen the set.
//
// A PACK APPLIES TO THE WHOLE JAIL. There used to be a per-entry `agents` filter
// ("stage this pack only for claude"), and it is gone: it presumed a fixed, known
// agent list, which is the assumption the pack model deletes — a pack that installs
// an agent is just a pack, and nothing in this machinery knows what an agent is.
// The filter was also redundant with where filtering actually happens: staging is
// per-agent at the DELIVERY end (jailcontent.PrepareSkills layers every pack's skills/
// into each agent that has a skills dir), so the config-side filter was a second,
// weaker copy of a decision already made downstream.
type PackEntry struct {
	// Source is the pack address. Always set. Either a `file://` URL (a local path,
	// the only form phase 0 fetches) or a `git+<transport>://` URL.
	Source string `json:"source"`

	// Name is the pack's short name, used for the staging dir, `yolo pack ls`, and
	// provenance. Defaults to a slug derived from Source when not given.
	Name string `json:"name"`

	// Only and Exclude filter the pack tree by glob, applied in that order. `only`
	// is a documented first-line ergonomic, not an escape hatch: "give me just
	// these three skills" is the dominant demand once a shared corpus is large
	// enough that a blanket import stops being trusted.
	Only    []string `json:"only,omitempty"`
	Exclude []string `json:"exclude,omitempty"`

	// IsEmbedded marks a pack shipped inside yolo. Set by the embedded-pack loader,
	// NEVER decoded from config: it grants privileges (see MayGrantHostFiles), so a
	// user-writable field would be the whole boundary undone. Hence json:"-".
	IsEmbedded bool `json:"-"`

	// There was an AllowExec here (wire name "allowExec", config key "allow_exec"),
	// the consumer's opt-in to staging a file with the exec bit. Removed 2026-08-30
	// along with the gate it fed — see internal/packstage's package doc for why the
	// gate was the wrong instrument. A config still carrying "allow_exec" now fails
	// the knownPackKeys check above as an unknown key, which is the intended outcome:
	// the key does nothing, and a key that does nothing must not be accepted quietly.
	// The wire form is tolerant in both skew directions (UnmarshalPacks uses plain
	// encoding/json, so an older host's "allowExec" is ignored by a newer entrypoint,
	// and the field was omitempty in the other direction).

	// Implicit marks an entry NO config line asked for — today only the conventional
	// local pack (LocalPackName). json:"-" like IsEmbedded, and for a stronger reason: a
	// config-settable "this entry is implicit" would be a lie a user could write.
	//
	// It exists because two surfaces ask "did the user configure any packs?" and mean
	// "has this jail got an agent" (NoPacksMessage). A local pack is content, never an
	// agent, so counting it there would silence a warning that is still true. See
	// HasConfiguredPack.
	Implicit bool `json:"-"`
}

// Slug returns a filesystem-safe identifier for this pack's staging dir. It reuses
// HostFileEntry.Slug's escaping so the two staging namespaces cannot collide by
// accident and one escaping bug cannot exist in two forms.
func (p PackEntry) Slug() string {
	return HostFileEntry{Path: p.Name}.Slug()
}

// IsLocal reports whether Source is a file:// address, which needs no fetch.
func (p PackEntry) IsLocal() bool {
	return strings.HasPrefix(p.Source, "file://")
}

// Origin classifies where a pack's CONTENT came from, which decides what that content
// is allowed to declare (D4).
type Origin int

const (
	// OriginFetched is content pulled from someone else's repository.
	OriginFetched Origin = iota
	// OriginLocal is content at a path on this machine — authored or vendored by the
	// user, and readable by them without yolo's help.
	OriginLocal
	// OriginEmbedded is content shipped inside yolo itself: reviewed with the release,
	// so a declaration from it IS a yolo-shipped decision.
	OriginEmbedded
)

// Embedded marks a pack as yolo-shipped. Set by the embedded-pack loader, never by
// config — an entry a user writes can only ever be local or fetched.
func (p PackEntry) Embedded() bool { return p.IsEmbedded }

// Origin returns the pack's content origin.
func (p PackEntry) Origin() Origin {
	switch {
	case p.IsEmbedded:
		return OriginEmbedded
	case p.IsLocal():
		return OriginLocal
	default:
		return OriginFetched
	}
}

// MayGrantHostFiles reports whether this pack's content may name a HOST FILE to cross
// into the jail (D4).
//
// The rule is about the CONTENT channel, not the config channel, and that distinction
// is the whole of it. Packs being user-scope already means a workspace cannot name a
// pack — but it does NOT mean a user who installed a third-party pack agreed to hand
// that repository their ~/.claude/settings.json. Installing a pack approves
// distributing skills and prose; a host-file grant is a materially stronger permission
// and no scope rule makes it not so.
//
// So: embedded (yolo-shipped, reviewed with the release) and local (the user's own
// files, which they can already read) may grant. FETCHED content may not, ever —
// that is the hole a84b11c closed for host_claude_files, and it would be reopened by
// letting a git ref widen the set.
func (p PackEntry) MayGrantHostFiles() bool {
	return p.Origin() != OriginFetched
}

// LoadPacks returns the validated `packs` entries from the USER config only.
//
// It deliberately takes no merged config: reading the user file directly is what
// makes workspace scope inexpressible (see the file header). Callers pass the merged
// map to nothing here — if a future caller wants to, that is the change to refuse.
//
// A malformed user config is an ERROR, never a silently empty list: dropping a pack
// silently looks exactly like the feature not working, which is the failure this
// plumbing exists to avoid (the same call LoadHostFiles makes).
func LoadPacks(warn Warn) ([]PackEntry, error) {
	if warn == nil {
		warn = func(string) {}
	}
	userPath := paths.UserConfigPath()
	// loadUserScopeConfig, not LoadJSONCWithIncludes: the same direct read of the user file
	// (so workspace scope stays inexpressible — see the file header) PLUS any
	// --user-layer. `packs` is the key the layer exists for, since installing a loophole
	// from inside a jail means naming the pack that carries it.
	userCfg, err := loadUserScopeConfig(userPath, userPath, true, warn)
	if err != nil {
		return nil, err
	}
	var entries []PackEntry
	if v, present := userCfg.Get(packsKey); present && v != nil {
		var problems []string
		entries, problems = checkPacks(v)
		for _, p := range problems {
			warn(p + " — entry skipped")
		}
	}
	// THE CONVENTIONAL LOCAL PACK, appended LAST. See localPackEntry for why it is here at
	// all and why it composes last; it is appended after the configured entries — including
	// after an unparseable/absent `packs` key, which is exactly the config a user who only
	// ever wanted three personal skills has.
	//
	// A CONFIGURED pack already using the name wins the slot, because two entries with one
	// name share a staging dir and the second silently overwrites the first (the collision
	// checkPacks refuses among configured entries, which this one bypasses by being appended
	// rather than lowered). Yielding to the explicit entry is the honest direction: a config
	// line the user wrote outranks a convention yolo applied for them.
	if local, ok := localPackEntry(); ok && !hasPackNamed(entries, local.Name) {
		entries = append(entries, local)
	}
	return entries, nil
}

// hasPackNamed reports whether any entry already carries this name.
func hasPackNamed(entries []PackEntry, name string) bool {
	for _, e := range entries {
		if e.Name == name {
			return true
		}
	}
	return false
}

// LocalPackName is the pack name the conventional local pack carries. Exported because a
// report that names packs (`pack ls`, the apply report, a prune notice) needs to distinguish
// "the dir yolo included for you" from a pack the user wrote a config line for.
//
// A configured pack may take this name, and then it WINS: LoadPacks skips the implicit entry
// rather than appending a second pack with one name (see hasPackNamed). An explicit config
// line outranks a convention.
const LocalPackName = "local"

// localPackEntry is the implicitly-included entry for paths.LocalPackDir, or ok=false when
// there is no such directory.
//
// ORDER IS LOAD-BEARING, AND IT IS LAST. The caller appends this after every configured
// entry, which puts it last in the delivery order at both notches — the jail merges pack
// skills dirs in this order (jailcontent.PrepareSkills' packSkillDirs loop, later wins a
// same-named skill) and the host renders `loaded` in this order. Last therefore means a
// PERSONAL skill outranks a shared pack's, which preserves the precedence the jail already
// had when the user's tree was a separate layer written after the packs: the user's own copy
// wins. Moving it earlier would silently invert that.
//
// TRUST: the local pack MAY read the host, exactly like any other `file://` pack. It gets
// there by being one — Source is a file:// address, so Origin() is OriginLocal and
// MayGrantHostFiles() is true with no special case. The reasoning, since this is a
// trust-boundary decision: the fetched-pack gate exists because installing someone else's
// pack is not consent to hand THAT REPOSITORY your ~/.claude/settings.json (see
// MayGrantHostFiles). Here there is no third party at all — it is a directory the user
// created, inside the config dir yolo already reads their config from, holding files only
// they can write. A pack cannot gain access its author already has, and the author is the
// user. Gating it would also be theatre: anything it could declare, the user could declare
// in config.jsonc one directory up.
//
// ABSENT IS SILENT AND FREE. Most users will not have this directory; its absence yields no
// warning, no error, and one Stat. A NON-DIRECTORY at that path (a file, a dangling
// symlink) is treated as absent for the same reason — pointing it at a pack loader would
// produce a confusing failure about a path nothing asked for.
func localPackEntry() (PackEntry, bool) {
	dir := paths.LocalPackDir()
	fi, err := os.Stat(dir) // Stat, not Lstat: a symlinked local pack dir is legitimate
	if err != nil || !fi.IsDir() {
		return PackEntry{}, false
	}
	return PackEntry{
		Source:   "file://" + filepath.ToSlash(dir),
		Name:     LocalPackName,
		Implicit: true,
	}, true
}

// HasConfiguredPack reports whether any entry came from the user's `packs` list — i.e.
// whether the empty-packs notice (NoPacksMessage) still applies.
//
// It exists because "no packs" means "no agent", and the local pack is content: a jail whose
// only pack is `~/.config/yolo-jail/local` has skills and prose and still nothing to run
// them. Answering that question with len(entries) would silence the notice for a user whose
// jail genuinely has no coding agent — the exact contradiction the opt-in ruling removed.
func HasConfiguredPack(entries []PackEntry) bool {
	for _, e := range entries {
		if !e.Implicit {
			return true
		}
	}
	return false
}

// checkPacks validates a raw `packs` value, returning the entries it could lower
// and a problem string per entry it could not. A non-list value yields one problem
// and no entries.
func checkPacks(v any) ([]PackEntry, []string) {
	list, ok := asList(v)
	if !ok {
		return nil, []string{"config." + packsKey + ": expected a list of pack entries"}
	}
	var entries []PackEntry
	var problems []string
	seenName := map[string]int{}
	for i, raw := range list {
		entry, problem := checkPackEntry(raw, fmt.Sprintf("config.%s[%d]", packsKey, i))
		if problem != "" {
			problems = append(problems, problem)
			continue
		}
		// A duplicate name would make two packs share a staging dir, so the second
		// would silently overwrite the first.
		if prev, dup := seenName[entry.Name]; dup {
			problems = append(problems, fmt.Sprintf(
				"config.%s[%d]: duplicate pack name %q (already used by entry %d) — "+
					"give one an explicit \"name\"", packsKey, i, entry.Name, prev))
			continue
		}
		seenName[entry.Name] = i
		entries = append(entries, entry)
	}
	return entries, problems
}

// checkPackEntry lowers and validates ONE entry in either form.
func checkPackEntry(raw any, itemPath string) (PackEntry, string) {
	if s, isStr := asStr(raw); isStr {
		return lowerPackSource(s, "", itemPath)
	}
	m, isMap := raw.(*jsonx.OrderedMap)
	if !isMap {
		return PackEntry{}, itemPath + ": expected a source string or an object"
	}
	for _, k := range m.Keys() {
		if _, known := knownPackKeys[k]; !known {
			return PackEntry{}, itemPath + "." + k + ": unknown key"
		}
	}
	rawSource, has := m.Get("source")
	if !has {
		return PackEntry{}, itemPath + ": missing required \"source\""
	}
	sourceStr, ok := asStr(rawSource)
	if !ok {
		return PackEntry{}, itemPath + ".source: expected a string address"
	}
	name := ""
	if rawName, hasName := m.Get("name"); hasName {
		if name, ok = asStr(rawName); !ok {
			return PackEntry{}, itemPath + ".name: expected a string"
		}
	}
	entry, problem := lowerPackSource(sourceStr, name, itemPath)
	if problem != "" {
		return PackEntry{}, problem
	}
	for _, field := range []struct {
		key string
		dst *[]string
	}{{"only", &entry.Only}, {"exclude", &entry.Exclude}} {
		rawVal, hasVal := m.Get(field.key)
		if !hasVal || rawVal == nil {
			continue
		}
		items, isList := asList(rawVal)
		if !isList {
			return PackEntry{}, itemPath + "." + field.key + ": expected a list of strings"
		}
		for _, it := range items {
			s, isStr := asStr(it)
			if !isStr {
				return PackEntry{}, itemPath + "." + field.key + ": expected a list of strings"
			}
			*field.dst = append(*field.dst, s)
		}
	}
	return entry, ""
}

// lowerPackSource validates an address and derives the default name.
func lowerPackSource(source, name, itemPath string) (PackEntry, string) {
	source = strings.TrimSpace(source)
	if source == "" {
		return PackEntry{}, itemPath + ".source: must not be empty"
	}
	scheme, _, hasScheme := strings.Cut(source, "://")
	if !hasScheme {
		// A BARE NAME selects an EMBEDDED pack — `packs: ["claude"]`. This is the whole
		// opt-in surface for the packs yolo ships, and it is deliberately the shortest
		// thing a user can write: naming the tool you want is the entire configuration.
		//
		// Embedded packs are NOT active by default, which is the ruling that makes the
		// launch warning honest. A jail with an empty config really has no agent, and says
		// so; activating six of them unconditionally while printing "no packs are
		// configured" was a contradiction the user would only discover by looking in
		// ~/.yolo/bin/block.
		if name, ok := embeddedPackName(source); ok {
			return PackEntry{Source: embeddedSourceFor(name), Name: name, IsEmbedded: true}, ""
		}
		return PackEntry{}, itemPath + ".source: " + unknownEmbeddedMessage(source)
	}
	switch {
	case scheme == "file":
	case strings.HasPrefix(scheme, "git+"):
	default:
		return PackEntry{}, itemPath + ".source: unsupported scheme " + scheme +
			":// (expected file:// or git+ssh:// / git+https://)"
	}
	if _, err := url.Parse(source); err != nil {
		return PackEntry{}, itemPath + ".source: not a valid URL: " + err.Error()
	}
	if name == "" {
		name = defaultPackName(source)
	}
	if problem := checkPackName(name, itemPath); problem != "" {
		return PackEntry{}, problem
	}
	return PackEntry{Source: source, Name: name}, ""
}

// defaultPackName derives a short name from an address: the last non-empty path
// segment, with any `?ref=` query and a trailing `.git` stripped.
func defaultPackName(source string) string {
	s := source
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSuffix(strings.TrimRight(s, "/"), ".git")
	// A git subpath uses `//` to separate repo from directory; the segment after it
	// is the more specific (and more useful) name.
	if i := strings.LastIndex(s, "//"); i > 0 && !strings.HasSuffix(s[:i], ":") {
		if sub := strings.Trim(s[i+2:], "/"); sub != "" {
			s = sub
		}
	}
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// checkPackName rejects a name that would escape or collide in the staging dir.
func checkPackName(name, itemPath string) string {
	switch {
	case name == "":
		return itemPath + ": could not derive a pack name from the source — add an explicit \"name\""
	case name == "." || name == "..":
		return itemPath + ".name: must not be \".\" or \"..\""
	case strings.ContainsAny(name, "/\\:"):
		return itemPath + ".name: must not contain a path separator or ':'"
	}
	return ""
}

// validatePacks reports `packs` problems for `yolo check`.
//
// It reads the USER config, not the merged map, for the same reason LoadPacks does.
// A `packs` key found in the WORKSPACE config is a hard error naming the fix: it is
// not merely ignored, because a silently-inert key looks exactly like a broken
// feature.
func validatePacks(workspace string, errs *[]string) {
	userPath := paths.UserConfigPath()
	if userCfg, err := loadUserScopeConfig(userPath, userPath, false, func(string) {}); err == nil && userCfg != nil {
		if v, present := userCfg.Get(packsKey); present && v != nil {
			_, problems := checkPacks(v)
			for _, p := range problems {
				add(errs, p)
			}
		}
	}
	wsCfg, err := LoadWorkspaceConfig(workspace, false, func(string) {})
	if err != nil || wsCfg == nil {
		return
	}
	if _, atWorkspace := wsCfg.Get(packsKey); atWorkspace {
		add(errs, "config."+packsKey+": user-scope only — move it to "+
			"~/.config/yolo-jail/config.jsonc. A workspace config travels with the repo "+
			"and is agent-editable, so it cannot decide which packs stage content "+
			"(skills and briefing prose an agent then follows) into the jail. A repo "+
			"that wants to configure its own agents can just commit the files.")
	}
}

// PackProfileCLINames is the CLI-name namespace a pack_profiles key resolves in
// (profiles-as-pack-variants.md §2.5, §8): every binary a `program` contribution of a
// RESOLVABLE pack installs — the packs yolo ships plus whatever the user configured,
// selected or not. §8's split is why selection is deliberately not consulted here:
// whether a key names a real CLI is answered against the universe, always, and
// selection only decides whether the chosen profile renders.
//
// `program` contributions only. A `requires` entry ASSERTS a binary exists and
// installs nothing, so it puts no name in the namespace a user can select a profile
// by — and a bin yolo does not install is not a bin a profile can gate.
//
// known=false when the universe cannot be enumerated: a configured pack that cannot
// be resolved from the store (never installed, offline, moved). The caller then steps
// ASIDE rather than refusing keys it cannot check — an unresolvable pack is its own
// failure, reported louder and first by `yolo check`'s Packs section and by the
// launch's staging, and reporting it here too would dress a broken install up as a
// typo'd profile key. Same contract as resolvePackLoopholeModules on the run side,
// whose silent-and-empty answer is this one's twin.
func PackProfileCLINames() ([]string, bool) {
	entries, err := LoadPacks(func(string) {})
	if err != nil {
		return nil, false
	}
	embedded := packload.Embedded()
	// EmbeddedNames reads the embed.FS directly, so a non-empty name list beside an
	// empty materialized set means materialization failed — a yolo bug, not a user
	// condition. Reporting "nothing is installed" from it would turn every key into a
	// false fatal, so it is an unknowable universe instead.
	if len(embedded) == 0 && len(packload.EmbeddedNames()) > 0 {
		return nil, false
	}
	seen := map[string]bool{}
	for _, p := range embedded {
		for _, bin := range p.InstallBins() {
			seen[bin] = true
		}
	}
	store := &packsrc.Store{Dir: paths.PacksDir()}
	for _, entry := range entries {
		if entry.Embedded() {
			continue // already in the set above
		}
		// nil Getenv: the store falls back to the real environment, which is what a
		// resolver running behind a read-only surface wants (the staged-tree fallback
		// is how a nested launch's local packs resolve). See packRoot on the run side.
		addr, err := packsrc.Parse(entry.Source)
		if err != nil {
			return nil, false
		}
		res, err := store.Resolve(addr, entry.Slug())
		if err != nil {
			return nil, false
		}
		p, problems := packload.LoadDir(res.Root, entry.Name, false)
		if len(problems) > 0 || p == nil {
			return nil, false
		}
		for _, bin := range p.InstallBins() {
			seen[bin] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, true
}

// MarshalPacks renders resolved entries as the compact JSON that travels in
// YOLO_PACKS. Deterministic (entries are already in config order) so an unchanged
// config yields an unchanged argv.
func MarshalPacks(entries []PackEntry) (string, error) {
	if len(entries) == 0 {
		return "", nil
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("marshalling packs for the entrypoint: %w", err)
	}
	return string(b), nil
}

// UnmarshalPacks decodes the YOLO_PACKS wire form. The entrypoint uses this; it
// never reads config, so it cannot widen the set.
func UnmarshalPacks(wire string) ([]PackEntry, error) {
	if strings.TrimSpace(wire) == "" {
		return nil, nil
	}
	var entries []PackEntry
	if err := json.Unmarshal([]byte(wire), &entries); err != nil {
		return nil, fmt.Errorf("decoding YOLO_PACKS: %w", err)
	}
	return entries, nil
}

// embeddedPackName resolves a bare entry to an embedded pack name.
func embeddedPackName(s string) (string, bool) {
	for _, n := range packload.EmbeddedNames() {
		if n == s {
			return n, true
		}
	}
	return "", false
}

// embeddedSourceFor is the synthetic Source an embedded entry carries.
//
// Non-empty because Source is documented as always set and several call sites (Slug,
// provenance, `yolo pack ls`) read it. The scheme is deliberately NOT file:// — an embedded
// pack is never resolved through the pack store, so a URL suggesting a path would invite a
// reader to look for one.
func embeddedSourceFor(name string) string { return "embedded:" + name }

// unknownEmbeddedMessage explains a bare entry that matched no embedded pack.
//
// It lists what IS available, because the failure is otherwise indistinguishable from a
// malformed URL — and the most likely cause is a typo in a tool name, where showing the
// real list is the whole fix.
func unknownEmbeddedMessage(s string) string {
	names := packload.EmbeddedNames()
	// A PATH-SHAPED entry is a forgotten scheme, not a misspelled pack name. Offering a
	// list of tool names to someone who wrote "/no/scheme" or "./my-pack" answers a
	// question they did not ask; the plausible names are what a bare word is.
	pathShaped := len(names) == 0 ||
		strings.ContainsAny(s, "/\\.") || strings.Contains(s, ":")
	if pathShaped {
		return "expected a URL with a scheme, e.g. file:///path/to/pack or " +
			"git+ssh://git@host/org/repo//sub?ref=main"
	}
	return fmt.Sprintf("no pack named %q ships with yolo (available: %s); for a pack from "+
		"elsewhere, give a full address like file:///path/to/pack or "+
		"git+ssh://git@host/org/repo//sub?ref=main", s, strings.Join(names, ", "))
}
