package config

// hostfiles.go is the config-side half of docs/plans/host-file-staging.md: the
// `host_files` key, which lets a user declare ANY host file (or a source-less
// file yolo brings into being) as a composed surface, rendered by the same engine
// that renders the builtin agent surfaces.
//
// It replaces the retired per-agent host_claude_files/host_pi_files keys. Those
// were a fixed, yolo-owned allow-list precisely because they crossed host files
// into the jail; this key reopens that with a PER-ENTRY scope rule instead (see
// SourceBearing and validateHostFiles), so a user config can widen the set while
// a workspace config — which travels with the repo and is agent-editable —
// cannot.

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/agents"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// hostFilesKey is the one config key this feature reads. Kept as a constant so
// the loader, the validator and the unknown-key set can't drift.
const hostFilesKey = "host_files"

// Host-file modes (the `mode` field). Each names a distinct answer to "what
// happens to this file across boots, and what happens when the agent edits it":
//
//   - HostFileModeReadonly renders 0o444 every boot. A host edit propagates on
//     the next launch; an in-jail edit fails loudly at the moment of the edit
//     rather than being silently reverted or silently made permanent. Honest
//     limit: this is DAC, not kernel enforcement — a root agent (Claude YOLO runs
//     UID 0) bypasses the mode bits, and anyone can chmod +w. It is a strong
//     signal and a speed bump, not a sandbox.
//   - HostFileModeOnce seeds the file when absent and then never touches it. In-jail
//     edits persist as ordinary file writes — no sidecar, no precedence puzzle —
//     and later host-side edits do NOT propagate. `yolo config reset` re-seeds.
//   - HostFileModeCopy overwrites the file every boot, 0o644. In-jail edits are
//     silently lost. This is the mode a directory entry implies.
//   - HostFileModeCapture is THE OVERLAY EXCEPTION: re-rendered every boot AND
//     in-jail edits captured into a sidecar that outranks the host layer. It is
//     never a default (see hostFileDefaultMode) because a captured edit wins over
//     the host file FOREVER — the sidecar never ages out — so implicit capture
//     would silently and permanently fork a host-mirrored file the first time a
//     tool rewrote its own config (`npm config set`, `git config --global`, any
//     CLI's first-run write).
const (
	HostFileModeReadonly = "readonly"
	HostFileModeOnce     = "once"
	HostFileModeCopy     = "copy"
	HostFileModeCapture  = "capture"
)

// hostFileModes is the closed set of accepted `mode` values, in the order the
// error message lists them (least to most machinery).
var hostFileModes = []string{
	HostFileModeReadonly, HostFileModeOnce, HostFileModeCopy, HostFileModeCapture,
}

// knownHostFileKeys is the accepted key set of the object form. Mirrors the
// per-key doc table in docs/plans/host-file-staging.md.
var knownHostFileKeys = set(
	"path", "source", "content", "codec", "managed", "defaults", "transform", "mode",
)

// HostFileEntry is one validated `host_files` entry, lowered from either the
// string (sugar) or object form. It is the config-side value; Phase 2 de-sugars
// it into a manifest.Surface with owner "user".
//
// The json tags define the YOLO_HOST_FILES wire form (see MarshalHostFiles): the
// host CLI is the single source of truth for the resolved entries and hands them
// to the entrypoint through that env var, so the entrypoint never re-reads config.
type HostFileEntry struct {
	// Path is the jail destination, home-relative and slash-separated with the
	// leading "~/" stripped (e.g. ".config/mytool/config.json"). Always set.
	Path string `json:"path"`

	// Source is the absolute host path to seed the `host` layer from, "~" already
	// expanded. Empty means source-less. Its presence is what makes an entry
	// user-scope only — see SourceBearing.
	Source string `json:"source,omitempty"`

	// Content is the inline literal seed for the `host` layer. Mutually exclusive
	// with Source. A source-less entry with neither Content nor Defaults/Managed
	// would compose to an empty file, which checkHostFiles rejects.
	Content string `json:"content,omitempty"`
	// HasContent distinguishes an absent `content` from an explicitly empty one
	// (`"content": ""` is a legitimate way to declare an empty file).
	HasContent bool `json:"hasContent,omitempty"`

	// Codec is the resolved codec name ("json" | "toml" | "lines" | "raw") — the
	// explicit `codec` when given, else auto-detected from Path's extension. Empty
	// for a directory entry (a directory is not a codec).
	Codec string `json:"codec,omitempty"`

	// Managed is the `managed` layer: keys yolo re-asserts after the Lua hook, so
	// they revert on edit. Lowered to the engine's plain value model.
	Managed any `json:"managed,omitempty"`
	// Defaults is the `defaults` layer: a user-overridable base. Also plain.
	Defaults any `json:"defaults,omitempty"`

	// Transform is the path to a Lua hook for this surface, "~" expanded. Empty
	// means the identity transform.
	Transform string `json:"transform,omitempty"`

	// Mode is the resolved mode: the explicit `mode` when given, else the per-kind
	// default (source-bearing → readonly, source-less → once, directory → copy).
	// Never resolves to capture implicitly.
	Mode string `json:"mode,omitempty"`

	// IsDir marks a directory entry, which routes to the recursive-copy path
	// rather than the compose engine (codec is strictly per-file).
	IsDir bool `json:"isDir,omitempty"`

	// Scope records where this entry was read from, for error messages and for
	// the Phase 3 `yolo config ls` listing. One of "user" or "workspace".
	Scope string `json:"scope,omitempty"`
}

// SourceBearing reports whether this entry crosses a HOST FILE into the jail —
// the credential boundary of the whole feature.
//
// An entry that names a source can forward ~/.ssh/id_ed25519 or
// ~/.aws/credentials, so it is legal only in the USER config. A source-less entry
// (inline content, or only managed/defaults) copies nothing from the host — it
// just brings a yolo-managed file into being — so it is safe at any scope,
// including a repo's own yolo-jail.jsonc.
//
// User scope means "the human is trusted": nothing blocks a user from listing
// ~/.ssh/… in their OWN config — their machine, their call, and a blocklist is
// unenforceable anyway (symlinks). The boundary is that the REPO cannot make that
// choice on their behalf.
func (e HostFileEntry) SourceBearing() bool { return e.Source != "" }

// Slug is the surface Name this entry lowers to, derived from the destination
// Path. It must be INJECTIVE — the sidecar file names (`last_render`, `overlay`)
// and the `/ctx/host-user/<slug>` mount point derive from it, so two surfaces
// sharing a slug would share sidecars and one file's captured in-jail edit would
// be replayed onto the other.
//
// So this is an ESCAPING scheme, not a prettifier. A lossy flatten (mapping each
// of '/', '.', '_' to '-') reads nicer but collides: `.config/mytool/config.json`
// and `.config/mytool.config.json` both become `config-mytool-config-json`, and a
// path of only separators flattens to the EMPTY string, which manifest.New
// rejects. The scheme here is a percent-style escape with '_' as the SOLE
// sentinel:
//
//   - [A-Za-z0-9.-] pass through unchanged, so a dotfile path like
//     `.config/…/config.json` stays legible (dots and the extension survive);
//   - every other byte — '/', '_' itself, spaces, unicode — becomes "_hh" (its
//     value in two lowercase hex digits).
//
// Because a literal '_' is escaped to "_5f", a '_' in the output ALWAYS begins a
// two-hex-digit escape and can never be confused with a passed-through character.
// That single-role sentinel is what makes the map reversible and therefore
// injective — the trap the previous attempt fell into was letting '_' be both a
// substituted character AND an escape prefix, so `.x20` and a literal space both
// produced "_x20". The result is non-empty for any non-empty Path (every input
// byte emits at least one output byte, and checkHostFileDest already rejects the
// empty and "." destinations).
//
// Derived from Path rather than Source because Path is what the entry uniquely
// owns: checkHostFiles rejects two entries writing the same destination, so
// distinct entries have distinct Paths, and injectivity here carries that through
// to distinct slugs.
func (e HostFileEntry) Slug() string {
	var b strings.Builder
	b.Grow(len(e.Path) + 8)
	for i := 0; i < len(e.Path); i++ {
		c := e.Path[i]
		switch {
		case c == '.' || c == '-' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'):
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "_%02x", c)
		}
	}
	return b.String()
}

// hostFileCodecByExt is the extension→codec auto-detect map. Everything not
// listed — including no extension at all, .sh, .yaml, .yml, and .jsonc — is raw.
//
// .yaml/.yml are raw because there IS no yaml codec: the phantom name was removed
// from the accepted set (a real one needs a vendored dep + `go mod vendor` + a
// goSrc fileset update). .jsonc is raw on purpose: routing it through the json
// codec would sort keys and DROP COMMENTS, silently mangling a hand-written file.
var hostFileCodecByExt = map[string]string{
	".json": "json",
	".toml": "toml",
}

// hostFileCodecFor returns the auto-detected codec for a destination path.
func hostFileCodecFor(dest string) string {
	if c, ok := hostFileCodecByExt[strings.ToLower(path.Ext(dest))]; ok {
		return c
	}
	return "raw"
}

// hostFileDefaultMode is the mode an entry gets when it does not say. It turns on
// ONE question — does a host source of truth exist? — and it never answers
// "capture":
//
//   - a directory is a wholesale copy (there is no per-file composition to do);
//   - source-bearing → readonly: the host file is the source of truth, host edits
//     keep propagating, and an in-jail edit fails at the moment of the edit;
//   - source-less → once: the jail's copy is the only copy, so seed it and leave
//     it alone; edits persist as ordinary writes with no sidecar at all.
//
// Capture is the exception and must be written out, because overlay outranks host
// permanently (see the HostFileModeCapture doc).
func hostFileDefaultMode(sourceBearing, isDir bool) string {
	switch {
	case isDir:
		return HostFileModeCopy
	case sourceBearing:
		return HostFileModeReadonly
	default:
		return HostFileModeOnce
	}
}

// LoadHostFiles reads host_files with the per-entry scope rule enforced BY
// CONSTRUCTION:
//
//   - SOURCE-BEARING entries come only from paths.UserConfigPath() (plus its
//     include_if_found files, which are host-side too);
//   - SOURCE-LESS entries come from the merged config, so a repo may ship them.
//
// That split is the security boundary, not a convenience. Of the places a config
// key can come from, two are jail-writable: the workspace yolo-jail{,.local}.jsonc
// (/workspace is bind-mounted rw) and <workspace>/.yolo/config-snapshot.json (same
// mount, read verbatim in-jail by LoadConfig). Only the host user config is not.
// So a source-bearing entry is read from the user config DIRECTLY, making
// workspace scope inexpressible; validateHostFiles' workspace-scope error is
// defense-in-depth against a silent no-op, not the boundary itself.
//
// Entries are returned sorted by Path so the emitted mount argv and the render
// order are deterministic. A malformed entry is SKIPPED with a message through
// warn (nil warn discards) rather than failing the run: ValidateConfig makes the
// same problems loud and preflight blocks the run before assembly. The error
// return is reserved for a user config that cannot be read or parsed at all.
//
// probeSource controls whether a source-bearing entry's host path is stat'ed.
// Callers running in-jail must pass false: host paths are deliberately not in the
// jail's mount namespace, so probing them in here would turn a perfectly valid
// host config into a fatal error on every nested run — the bug cache_relocations
// already hit. A missing source is kept either way; the mount side skips a bind
// whose source does not exist (podman dies on a missing bind source) and the
// surface then falls back to its defaults layer.
func LoadHostFiles(merged *jsonx.OrderedMap, warn Warn, probeSource bool) ([]HostFileEntry, error) {
	if warn == nil {
		warn = func(string) {}
	}

	var entries []HostFileEntry

	// Source-bearing half: the USER config only, read directly.
	userPath := paths.UserConfigPath()
	// strict=true: a malformed user config is an error, never a silently empty
	// list. Silently dropping a host_files entry looks exactly like the feature
	// not working, which is the failure this whole key's plumbing exists to avoid.
	userCfg, err := LoadJSONCWithIncludes(userPath, userPath, true, warn, nil)
	if err != nil {
		return nil, err
	}
	if v, present := userCfg.Get(hostFilesKey); present && v != nil {
		userEntries, problems := checkHostFiles(v, "user", probeSource)
		for _, p := range problems {
			warn(p + " — entry skipped")
		}
		for _, e := range userEntries {
			if e.SourceBearing() {
				entries = append(entries, e)
			}
		}
	}

	// Source-less half: the merged config, so a workspace may contribute. The
	// user config's source-less entries are in here too (the merge includes it),
	// which is why the loop above takes only the source-bearing ones — otherwise
	// a user's source-less entry would be added twice.
	if merged != nil {
		if v, present := merged.Get(hostFilesKey); present && v != nil {
			mergedEntries, problems := checkHostFiles(v, "workspace", probeSource)
			for _, p := range problems {
				warn(p + " — entry skipped")
			}
			for _, e := range mergedEntries {
				if !e.SourceBearing() {
					entries = append(entries, e)
				}
			}
		}
	}

	// A source-bearing user entry and a source-less merged entry could name the
	// same destination; the source-bearing one wins (it is the more specific
	// declaration, and it is the one the user wrote in their own config).
	entries = dedupeHostFilesByPath(entries, warn)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

// dedupeHostFilesByPath keeps the first entry per destination Path and warns
// about the rest. checkHostFiles already rejects duplicates WITHIN one config
// value; this catches the cross-scope case, where a user's source-bearing entry
// and a workspace source-less entry name the same file.
func dedupeHostFilesByPath(entries []HostFileEntry, warn Warn) []HostFileEntry {
	seen := make(map[string]HostFileEntry, len(entries))
	out := make([]HostFileEntry, 0, len(entries))
	for _, e := range entries {
		if prev, dup := seen[e.Path]; dup {
			warn(fmt.Sprintf("config.%s: %s is declared at both %s and %s scope — "+
				"keeping the %s one", hostFilesKey, pytext.Repr("~/"+e.Path),
				prev.Scope, e.Scope, prev.Scope))
			continue
		}
		seen[e.Path] = e
		out = append(out, e)
	}
	return out
}

// checkHostFiles shape-validates a whole host_files value. It returns the
// accepted entries (fully resolved: codec auto-detected, mode defaulted, "~"
// expanded) and one message per rejected entry, already prefixed with its config
// path index.
//
// The loader and ValidateConfig both go through this, so what `yolo check`
// reports as an error is exactly what the loader drops, in exactly the same
// words.
//
// scope labels the entries for error messages and the ls listing. probeSource is
// the filesystem-probe gate — see LoadHostFiles.
func checkHostFiles(v any, scope string, probeSource bool) (entries []HostFileEntry, problems []string) {
	prefix := "config." + hostFilesKey
	list, ok := asList(v)
	if !ok {
		return nil, []string{prefix + ": expected a list of host-file entries " +
			"(a path string, or an object with a 'path')"}
	}
	reserved := hostFileReservedDests()
	byPath := make(map[string]int, len(list)) // dest -> first index claiming it
	for idx, raw := range list {
		itemPath := fmt.Sprintf("%s[%d]", prefix, idx)
		entry, problem := checkHostFileEntry(raw, itemPath, scope, reserved, probeSource)
		if problem != "" {
			problems = append(problems, problem)
			continue
		}
		if prev, dup := byPath[entry.Path]; dup {
			problems = append(problems, fmt.Sprintf(
				"%s: destination %s is already declared by entry [%d]",
				itemPath, pytext.Repr("~/"+entry.Path), prev))
			continue
		}
		byPath[entry.Path] = idx
		entries = append(entries, entry)
	}
	return entries, problems
}

// checkHostFileEntry validates and lowers ONE entry, string or object form. It
// returns either a resolved entry or a single problem message (already prefixed
// with itemPath) — never both.
func checkHostFileEntry(raw any, itemPath, scope string, reserved map[string]string, probeSource bool) (HostFileEntry, string) {
	if s, ok := asStr(raw); ok {
		return checkHostFileString(s, itemPath, scope, reserved)
	}
	m, ok := asMap(raw)
	if !ok {
		return HostFileEntry{}, itemPath + ": expected a path string or an object with a 'path'"
	}
	return checkHostFileObject(m, itemPath, scope, reserved, probeSource)
}

// checkHostFileString lowers the SUGAR form: a bare string means source == that
// host path, destination == the same home-relative path, codec auto-detected, and
// mode readonly (the source-bearing default — the host file stays the source of
// truth). It is therefore always source-bearing, hence user-scope only.
//
// A string ending in "/" is a directory entry, which routes to the recursive-copy
// path instead of the compose engine.
func checkHostFileString(s, itemPath, scope string, reserved map[string]string) (HostFileEntry, string) {
	isDir := strings.HasSuffix(s, "/")
	dest, msg := checkHostFileDest(s, reserved)
	if msg != "" {
		return HostFileEntry{}, fmt.Sprintf("%s: %s: %s", itemPath, pytext.Repr(s), msg)
	}
	// The sugar form's source IS the same path on the host. Expanded here so the
	// mount side never re-expands (and so a "~" in a user config resolves against
	// the HOST home, which is what the user meant).
	source := filepath.Clean(expandUser(s))
	if msg := checkHostFileSource(source); msg != "" {
		return HostFileEntry{}, fmt.Sprintf("%s: %s: %s", itemPath, pytext.Repr(s), msg)
	}
	entry := HostFileEntry{
		Path:   dest,
		Source: source,
		IsDir:  isDir,
		Mode:   hostFileDefaultMode(true, isDir),
		Scope:  scope,
	}
	if !isDir {
		entry.Codec = hostFileCodecFor(dest)
	}
	return entry, ""
}

// checkHostFileObject lowers the OBJECT form and enforces its field rules.
func checkHostFileObject(m *jsonx.OrderedMap, itemPath, scope string, reserved map[string]string, probeSource bool) (HostFileEntry, string) {
	for _, k := range m.Keys() {
		if _, known := knownHostFileKeys[k]; !known {
			return HostFileEntry{}, fmt.Sprintf("%s.%s: unknown key", itemPath, k)
		}
	}

	// path is optional WHEN a `source` is given: the overwhelmingly common case is
	// mirroring a host file at the same home-relative place (that is exactly what
	// the string-sugar form means), so requiring the caller to repeat the path
	// verbatim is noise that also invites the two halves drifting apart. An entry
	// with neither is unplaceable, so that stays an error.
	//
	// Only a "~/"-relative source can be defaulted. An absolute host path outside
	// $HOME (/etc/foo.conf) has no meaningful home-relative destination, so it must
	// say where it goes.
	rawPath, present := m.Get("path")
	if !present {
		inferred, msg := inferHostFileDest(m)
		if msg != "" {
			return HostFileEntry{}, itemPath + msg
		}
		rawPath = inferred
	}
	destRaw, ok := asStr(rawPath)
	if !ok {
		return HostFileEntry{}, itemPath + ".path: expected a home-relative path string"
	}
	isDir := strings.HasSuffix(destRaw, "/")
	dest, msg := checkHostFileDest(destRaw, reserved)
	if msg != "" {
		return HostFileEntry{}, fmt.Sprintf("%s.path: %s: %s", itemPath, pytext.Repr(destRaw), msg)
	}

	entry := HostFileEntry{Path: dest, IsDir: isDir, Scope: scope}

	// source ⊕ content. Both name the `host` layer's origin, so having both is
	// ambiguous rather than additive — and which one won would be invisible.
	rawSource, hasSource := m.Get("source")
	rawContent, hasContent := m.Get("content")
	if hasSource && hasContent {
		return HostFileEntry{}, itemPath + ": 'source' and 'content' are mutually exclusive " +
			"(both seed the host layer — use 'source' to copy a host file, 'content' for an inline literal)"
	}
	if hasSource {
		s, ok := asStr(rawSource)
		if !ok {
			return HostFileEntry{}, itemPath + ".source: expected a host path string"
		}
		src := filepath.Clean(expandUser(s))
		if msg := checkHostFileSource(src); msg != "" {
			return HostFileEntry{}, fmt.Sprintf("%s.source: %s: %s", itemPath, pytext.Repr(s), msg)
		}
		entry.Source = src
		// A source ending in "/" is a directory copy even when the destination
		// does not say so, since it is the source's shape that decides whether
		// there is a single file to run a codec over.
		if strings.HasSuffix(s, "/") {
			entry.IsDir = true
		}
	}
	if hasContent {
		s, ok := asStr(rawContent)
		if !ok {
			return HostFileEntry{}, itemPath + ".content: expected a string (the file's literal content)"
		}
		if entry.IsDir {
			return HostFileEntry{}, itemPath + ": 'content' cannot be used with a directory destination"
		}
		entry.Content, entry.HasContent = s, true
	}

	// codec: explicit wins over auto-detect. Rejected for a directory, where
	// there is no single file to decode.
	if rawCodec, has := m.Get("codec"); has {
		c, ok := asStr(rawCodec)
		if !ok {
			return HostFileEntry{}, fmt.Sprintf("%s.codec: expected one of %s",
				itemPath, strings.Join(codec.Names(), ", "))
		}
		if entry.IsDir {
			return HostFileEntry{}, itemPath + ": 'codec' cannot be used with a directory " +
				"(a directory is copied recursively, not decoded)"
		}
		// codec.Names() is the registry itself, so an accepted name is one
		// something can actually decode — the check cannot drift into validating a
		// name that dies at render (the old phantom 'yaml').
		if _, known := codec.LookupCodec(c); !known {
			return HostFileEntry{}, fmt.Sprintf("%s.codec: %s: unknown codec (want one of %s); "+
				"there is no 'yaml' codec — a .yaml file is handled as 'raw' bytes",
				itemPath, pytext.Repr(c), strings.Join(codec.Names(), ", "))
		}
		entry.Codec = c
	} else if !entry.IsDir {
		entry.Codec = hostFileCodecFor(dest)
	}

	// managed / defaults: the structured layers. Lowered to the engine's plain
	// value model here so the render side never sees a jsonx type (the engine
	// type-switches on map[string]any and would treat an *OrderedMap as an opaque
	// scalar).
	if rawManaged, has := m.Get("managed"); has {
		v, msg := checkHostFileLayer(rawManaged, entry, itemPath, "managed")
		if msg != "" {
			return HostFileEntry{}, msg
		}
		entry.Managed = v
	}
	if rawDefaults, has := m.Get("defaults"); has {
		v, msg := checkHostFileLayer(rawDefaults, entry, itemPath, "defaults")
		if msg != "" {
			return HostFileEntry{}, msg
		}
		entry.Defaults = v
	}

	if rawTransform, has := m.Get("transform"); has {
		s, ok := asStr(rawTransform)
		if !ok {
			return HostFileEntry{}, itemPath + ".transform: expected a path to a Lua hook file"
		}
		if entry.IsDir {
			return HostFileEntry{}, itemPath + ": 'transform' cannot be used with a directory " +
				"(a directory is copied recursively, not composed)"
		}
		entry.Transform = filepath.Clean(expandUser(s))
	}

	// mode: explicit wins; otherwise the per-kind default, which is never capture.
	if rawMode, has := m.Get("mode"); has {
		s, ok := asStr(rawMode)
		if !ok || !inStrList(hostFileModes, s) {
			return HostFileEntry{}, fmt.Sprintf("%s.mode: expected one of %s",
				itemPath, strings.Join(hostFileModes, ", "))
		}
		if entry.IsDir && s != HostFileModeCopy {
			return HostFileEntry{}, fmt.Sprintf(
				"%s.mode: a directory entry only supports %s (got %s) — "+
					"the other modes are per-file composition, and a directory is copied recursively",
				itemPath, pytext.Repr(HostFileModeCopy), pytext.Repr(s))
		}
		entry.Mode = s
	} else {
		entry.Mode = hostFileDefaultMode(entry.SourceBearing(), entry.IsDir)
	}

	// An entry with no layer at all composes to an empty file, which is almost
	// certainly a mistake (a typo'd key, or a half-written entry) — and it would
	// be a destructive one for a source-less `once` entry, since it seeds an empty
	// file and then never touches it again. Declare `"content": ""` to mean it.
	//
	// A DIRECTORY entry is the stricter case: it has no layers at all (every
	// composition key is rejected for a dir above), so a `source` is the only thing
	// it can possibly copy from. Without one there is nothing to stage, and Phase
	// 2's recursive-copy step would be handed an empty source path.
	switch {
	case entry.IsDir && entry.Source == "":
		return HostFileEntry{}, itemPath + ": a directory entry needs a 'source' to copy from " +
			"(a directory has no composition layers — use a file entry with 'content'/'defaults' " +
			"to bring a new file into being)"
	case !entry.IsDir && entry.Source == "" && !entry.HasContent &&
		entry.Managed == nil && entry.Defaults == nil:
		return HostFileEntry{}, itemPath + ": nothing to compose — an entry needs at least one of " +
			"'source', 'content', 'defaults', or 'managed' (use \"content\": \"\" to declare an empty file)"
	}

	if probeSource && entry.Source != "" {
		if msg := probeHostFileSource(entry); msg != "" {
			return HostFileEntry{}, fmt.Sprintf("%s.source: %s", itemPath, msg)
		}
	}
	return entry, ""
}

// inferHostFileDest derives a missing `path` from the entry's `source`, so
// mirroring a host file at the same place does not require writing it twice.
// Returns the destination string, or a message (suffixed onto the item path)
// explaining why it cannot be inferred.
//
// The inference is deliberately narrow: only a source written "~/…" yields a
// destination, because only then is the home-relative path unambiguous. An
// absolute host path outside $HOME has no home-relative counterpart to guess —
// "/etc/foo.conf" could reasonably mean ~/.config/foo.conf, ~/foo.conf, or
// nothing — and silently picking one would be worse than asking.
//
// The trailing "/" is preserved so a directory source stays a directory entry.
func inferHostFileDest(m *jsonx.OrderedMap) (string, string) {
	rawSource, hasSource := m.Get("source")
	if !hasSource {
		return "", ".path: required (the jail destination, e.g. " +
			"'~/.config/mytool/config.json') — it can only be omitted when 'source' " +
			"is a '~/…' path to mirror"
	}
	src, ok := asStr(rawSource)
	if !ok {
		// The source itself is malformed; let the source check report it.
		return "", ".source: expected a host path string"
	}
	if !strings.HasPrefix(src, "~/") {
		return "", fmt.Sprintf(".path: required — 'source' %s is not under $HOME, "+
			"so there is no home-relative destination to infer; name one explicitly",
			pytext.Repr(src))
	}
	return src, ""
}

// checkHostFileLayer validates a `managed`/`defaults` layer against the entry's
// codec and lowers it to the engine's plain value model.
//
// The shape must match the codec's KIND, because the engine folds layers by
// whole-value replacement for a keyless surface and deep-merge for an object one;
// a mistyped layer would either be silently dropped or blow up at render.
// Rejecting it here means the mistake is reported by `yolo check` with the key
// named, not discovered later as a mysteriously empty file.
//
// The kind comes from codec.KindOf — the same function the engine, the Lua
// boundary, and the absent-layer zero value use — so this check cannot disagree
// with what the renderer will accept.
func checkHostFileLayer(raw any, entry HostFileEntry, itemPath, field string) (any, string) {
	if entry.IsDir {
		return nil, fmt.Sprintf("%s: %s cannot be used with a directory "+
			"(a directory is copied recursively, not composed)", itemPath, pytext.Repr(field))
	}
	plain := jsonx.Plain(raw)
	kind, known := codec.KindOf(entry.Codec)
	if !known {
		// Unreachable: the codec was either auto-detected from a fixed table or
		// checked against the registry. Skip the shape check rather than reject a
		// value for an unknown reason.
		return plain, ""
	}
	if kind.Matches(plain) {
		return plain, ""
	}
	switch kind {
	case codec.KindScalar:
		return nil, fmt.Sprintf("%s.%s: expected a string for a %s surface (got %s) — "+
			"a %s file has no keys, so %s is its whole content",
			itemPath, field, pytext.Repr(entry.Codec), hostFileShapeName(plain),
			entry.Codec, pytext.Repr(field))
	case codec.KindArray:
		return nil, fmt.Sprintf("%s.%s: expected a list for a %s surface (got %s)",
			itemPath, field, pytext.Repr(entry.Codec), hostFileShapeName(plain))
	default:
		return nil, fmt.Sprintf("%s.%s: expected an object for a %s surface (got %s)",
			itemPath, field, pytext.Repr(entry.Codec), hostFileShapeName(plain))
	}
}

// hostFileShapeName names a value's JSON shape for an error message.
func hostFileShapeName(v any) string {
	switch v.(type) {
	case map[string]any:
		return "an object"
	case []any:
		return "a list"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case nil:
		return "null"
	default:
		return "a number"
	}
}

// builtinSurfacePaths are the destination paths of every yolo-composed surface — core's
// own plus every EMBEDDED PACK's. A host_files entry may not write one, because composing
// over a surface yolo also renders means two writers racing for one file, and whichever
// runs second wins silently. See hostFileReservedDests.
//
// READ FROM THE PACKS, not duplicated. It used to be a hand-maintained list, drift-checked
// by a test, because reading agentcfg.BuiltinManifest() would have pulled the Lua VM
// (agentcfg/luahook → gopher-lua) into internal/config and therefore into every binary
// that reads config. internal/packload has no such dependency — it reads manifests and
// decodes surfaces without the transform engine — so the real declarations are reachable
// now and the duplicate is gone.
//
// Embedded packs only, which is a real and deliberate limit: a CONFIGURED pack's surface
// path is not reserved here, because resolving one requires the pack store (a filesystem
// read, at config-validation time, that could fail for reasons having nothing to do with
// the config being validated). A user who declares a host_files entry at a configured
// pack's surface path gets two writers instead of an error.
func builtinSurfacePaths() []string {
	surfacePathsOnce.Do(func() {
		paths := []string{}
		dir, err := os.MkdirTemp("", "yolo-surface-paths-")
		if err == nil {
			packs, problems := packload.MaterializeEmbedded(officialpacks.FS, dir)
			if len(problems) == 0 {
				for _, p := range packs {
					surfaces, probs := p.Surfaces()
					if len(probs) > 0 {
						continue
					}
					for _, sf := range surfaces {
						paths = append(paths, sf.Path)
					}
				}
			}
			_ = os.RemoveAll(dir)
		}
		paths = append(paths, corePathsForReservation...)
		sort.Strings(paths)
		surfacePathsCache = paths
	})
	return surfacePathsCache
}

var (
	surfacePathsOnce  sync.Once
	surfacePathsCache []string
)

// corePathsForReservation are the surfaces CORE renders itself, which belong to no pack.
// Kept as a literal because there is exactly one and reading it back through agentcfg
// would reintroduce the Lua-VM dependency this function exists to avoid.
var corePathsForReservation = []string{
	"~/.config/mise/config.toml",
}

// hostFileReservedDests is the set of home-relative destinations a host_files
// entry may not write, as EXACT paths:
//
//   - every path yolo mounts as a single file or materializes as a symlink
//     (reservedHomeFiles) — composing over a bind mount silently writes into the
//     host's own file, and replacing a symlink breaks the atomic-write path;
//   - every builtin composed surface path — a user entry at
//     ~/.claude/settings.json would render the same file the prism renders, and
//     whichever ran last would win, quietly stripping yolo's managed block.
//
// Exact paths, not first segments: `~/.config/mytool/config.json` is the feature's
// central use case, so banning the `.config` segment (as writable_home_dirs
// rightly does — see reservedHomeSegments) would reject the main thing users want.
// Directory roots are NOT reserved here for the same reason: the overlay dirs are
// read-write binds, so composing a NEW file inside one is exactly what should
// work.
func hostFileReservedDests() map[string]string {
	dests := make(map[string]string, len(reservedHomeFiles)+len(builtinSurfacePaths()))
	for _, f := range reservedHomeFiles {
		dests[f] = "yolo mounts or materializes it directly"
	}
	for _, p := range builtinSurfacePaths() {
		dests[strings.TrimPrefix(p, "~/")] = "it is a yolo-composed agent surface"
	}
	return dests
}

// checkHostFileDest validates a jail DESTINATION and returns it home-relative
// (slash-separated, no leading "~/", no trailing slash), or a reason it is
// rejected.
//
// The path rules mirror writable_home_dirs' — no escaping $HOME, no ':' (podman
// would parse it as a mount option rather than part of the path) — because the
// destination becomes a path under /home/agent the same way. The RESERVATION rule
// differs: see hostFileReservedDests.
func checkHostFileDest(s string, reserved map[string]string) (string, string) {
	if s == "" {
		return "", "must not be empty"
	}
	rel := s
	switch {
	case rel == "~":
		return "", "must name a file under $HOME, not the home directory itself"
	case strings.HasPrefix(rel, "~/"):
		rel = rel[2:]
	case strings.HasPrefix(rel, "/"):
		return "", "must be a path under $HOME (write it '~/…'), not an absolute path — " +
			"an arbitrary container path is what 'mounts' is for"
	}
	if strings.ContainsRune(rel, ':') {
		return "", "must not contain ':' — it would be parsed as a podman mount option, not part of the path"
	}
	clean := path.Clean(rel)
	if clean == "." {
		return "", "must name a file under $HOME, not the home directory itself"
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", "must not escape $HOME with '..'"
	}
	if why, bad := reserved[clean]; bad {
		return "", fmt.Sprintf("%s is managed by yolo (%s) — writing it from config would "+
			"clobber yolo's own file; pick a different destination",
			pytext.Repr("~/"+clean), why)
	}
	return clean, ""
}

// checkHostFileSource validates a host SOURCE path. It must be absolute after "~"
// expansion (a relative host path has no meaningful base — the CLI's cwd is not
// where a config author is thinking) and ':'-free for the same podman reason as
// the destination.
func checkHostFileSource(src string) string {
	if src == "" {
		return "must not be empty"
	}
	if !filepath.IsAbs(src) {
		return "must be an absolute host path (or start with '~/'); a relative path has no defined base"
	}
	if strings.ContainsRune(src, ':') {
		return "must not contain ':' — it would be parsed as a podman mount option, not part of the path"
	}
	return ""
}

// probeHostFileSource stats a source-bearing entry's host path. It is host-only
// (see LoadHostFiles' probeSource) and NEVER rejects a missing file: a source that
// does not exist yet is a normal state (a dotfile the user has not created), and
// the surface simply falls back to its defaults layer. What it does catch is a
// file/directory MISMATCH, which is silently wrong rather than merely absent — a
// directory source composed as a single file, or a file entry expecting a tree.
func probeHostFileSource(entry HostFileEntry) string {
	st, err := os.Stat(entry.Source)
	if err != nil {
		return "" // absent is fine; the surface falls back to defaults
	}
	if st.IsDir() && !entry.IsDir {
		return fmt.Sprintf("%s is a directory but the entry composes a single file — "+
			"end the path with '/' to copy the tree, or point at a file",
			pytext.Repr(entry.Source))
	}
	if !st.IsDir() && entry.IsDir {
		return fmt.Sprintf("%s is a file but the entry is declared as a directory "+
			"(trailing '/') — drop the trailing slash", pytext.Repr(entry.Source))
	}
	return ""
}

// validateHostFiles enforces the host_files rules as `yolo check` errors: every
// shape rule, plus the per-entry scope rule.
//
// The scope half is the credential boundary's defense-in-depth. LoadHostFiles
// already ignores workspace-scope source-bearing entries entirely, so without
// this check such an entry is a silent no-op that looks like a broken feature.
// ValidateConfig only ever receives the MERGED map and the merge carries no
// provenance, so the only way to tell where an entry came from is to re-read the
// workspace config — one extra file read on a cold path, much cheaper than
// threading provenance through every caller of the merge (the same trade
// validateCacheRelocations makes).
//
// The filesystem probe is skipped inside a jail: the merged config in here is the
// host-written snapshot or the host user config bind-mounted read-only, either
// way carrying HOST paths that are deliberately not in the jail's mount namespace.
// Stat'ing them would turn a valid host config into a fatal error on every nested
// run. Shape and scope rules still apply everywhere.
func validateHostFiles(config *jsonx.OrderedMap, workspace string, errs *[]string) {
	v, present := config.Get(hostFilesKey)
	if !present {
		// Every workspace key survives into the merged map, so an absent key here
		// proves the workspace config has none either — no re-read needed.
		return
	}
	if v != nil {
		_, problems := checkHostFiles(v, "merged", !inJail())
		for _, p := range problems {
			add(errs, p)
		}
	}

	// Warnings from the re-read are discarded: this same file was already loaded
	// (and any parse problem already reported) by whoever produced the merged
	// config we were handed.
	wsCfg, err := LoadWorkspaceConfig(workspace, false, func(string) {})
	if err != nil || wsCfg == nil {
		return
	}
	wsValue, atWorkspace := wsCfg.Get(hostFilesKey)
	if !atWorkspace || wsValue == nil {
		return
	}
	// Re-check the WORKSPACE value on its own so the reported indices match what
	// the user wrote in their workspace config, not their position in the merge.
	wsEntries, _ := checkHostFiles(wsValue, "workspace", false)
	for i, e := range wsEntries {
		if !e.SourceBearing() {
			continue
		}
		add(errs, fmt.Sprintf("config.%s[%d]: an entry that names a host source is "+
			"user-scope only — move it to ~/.config/yolo-jail/config.jsonc (a workspace "+
			"config travels with the repo and is agent-editable, so it cannot decide which "+
			"host files cross into the jail). A source-less entry (inline 'content', or only "+
			"'managed'/'defaults') is allowed here.", hostFilesKey, i))
	}
}

// MarshalHostFiles renders resolved entries as the compact JSON that travels in
// the YOLO_HOST_FILES env var — the same single-source-of-truth pattern as
// YOLO_MCP_SERVERS / YOLO_AGENTS. The host CLI resolves host_files exactly once
// (LoadHostFiles) and hands the result to the entrypoint through this string, so
// the entrypoint never re-reads config and the slugs it derives are guaranteed to
// match the /ctx/host-user/<slug> mount points the CLI emitted.
//
// An empty slice marshals to "" (not "[]") so an unset feature leaves the env var
// empty, which UnmarshalHostFiles reads back as no entries.
func MarshalHostFiles(entries []HostFileEntry) (string, error) {
	if len(entries) == 0 {
		return "", nil
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("marshalling host_files for the entrypoint: %w", err)
	}
	return string(b), nil
}

// UnmarshalHostFiles decodes the YOLO_HOST_FILES env var back into resolved
// entries. An empty or whitespace-only value is no entries with no error — the
// feature simply being off — matching how the entrypoint treats an unset var.
//
// The Managed/Defaults layers round-trip through encoding/json, which re-lands
// them in exactly the plain value model (map[string]any, []any, float64) the
// compose engine type-switches on, so no jsonx types ever reach the renderer.
func UnmarshalHostFiles(s string) ([]HostFileEntry, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var entries []HostFileEntry
	if err := json.Unmarshal([]byte(s), &entries); err != nil {
		return nil, fmt.Errorf("decoding YOLO_HOST_FILES: %w", err)
	}
	return entries, nil
}

// SourceLessHostFiles returns the source-less entries — the ones that cross no
// host file into the jail. The macos-user backend passes only these through
// YOLO_HOST_FILES: it has no /ctx/host-user mount to carry a source into, so a
// source-bearing entry there would silently render with no host layer. Filtering
// them out (rather than letting them fall back to defaults) keeps the deficiency
// explicit — source-bearing host_files on macos-user are a known gap to revisit,
// not a half-working surprise.
func SourceLessHostFiles(entries []HostFileEntry) []HostFileEntry {
	out := make([]HostFileEntry, 0, len(entries))
	for _, e := range entries {
		if !e.SourceBearing() {
			out = append(out, e)
		}
	}
	return out
}

// SourceLessHostFilesFrom reads just the SOURCE-LESS entries out of an
// already-merged config map. It is the PURE counterpart of LoadHostFiles: no user
// config read, no filesystem probe, so a caller that must stay side-effect-free
// (macosuser.BuildRunPlan) can still stage the entries that cross nothing.
//
// Reading source-less entries from the merged map is correct, not a shortcut: they
// are legal at ANY scope precisely because they copy nothing from the host (see
// SourceBearing), so the merge is their proper source. The source-bearing half is
// deliberately unreachable here — it requires the user-config-only read that IS
// the credential boundary.
func SourceLessHostFilesFrom(merged *jsonx.OrderedMap) []HostFileEntry {
	if merged == nil {
		return nil
	}
	v, present := merged.Get(hostFilesKey)
	if !present || v == nil {
		return nil
	}
	entries, _ := checkHostFiles(v, "workspace", false)
	out := SourceLessHostFiles(entries)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// HostFileStaging names what the host CLI must provision so a destination is
// WRITABLE inside the jail. The jail home is a `:ro` bind of GlobalHome with
// read-write binds nested inside it, so where a destination lands decides whether
// the entrypoint's composed write succeeds at all — an uncovered path EROFS-fails
// (docs/design/composed-file-permissions.md §7.5).
type HostFileStaging int

const (
	// HostFileStagingNone: the destination already sits under a read-write bind
	// (`~/.config/…`, `~/.cache/…`, `~/.local/…`, `go/…`, or a selected agent's
	// overlay dir). The entrypoint's own MkdirAll+write just works; provisioning
	// anything here would shadow a yolo mount.
	HostFileStagingNone HostFileStaging = iota

	// HostFileStagingSymlink: a HOME-ROOT file (`~/.npmrc`) — it would land
	// directly in the `:ro` base. The CLI materializes a relative symlink in
	// GlobalHome pointing into the writable `~/.config` overlay, exactly as
	// storage.EnsureSymlink already does for .bashrc/.claude.json/.gitconfig.
	//
	// A DIRECTORY bind cannot serve this case (it would make the destination a
	// directory, so the composed write fails "is a directory"), and a
	// pre-created EMPTY backing file is worse: os.Stat on a bind-mounted empty
	// file SUCCEEDS, so HostFileModeOnce's seed-if-absent guard returns early on
	// the very first boot and the file stays empty forever. A DANGLING symlink is
	// the one shape that keeps `once` correct — Stat yields ENOENT until it is
	// seeded, then succeeds.
	HostFileStagingSymlink

	// HostFileStagingWritableDir: a NEW top-level directory (`~/foo/bar.json`) —
	// the parent does not exist in the `:ro` base at all, so the entrypoint's
	// MkdirAll EROFS-fails before any write. The CLI stages the destination's
	// parent as a writable subtree, reusing the writable_home_dirs recipe
	// (backing dir + GlobalHome mountpoint + a nested rw bind).
	HostFileStagingWritableDir
)

// hostFileWritableRoots are the home-relative first segments already covered by a
// read-write bind, so a destination under one needs no staging. Authority:
// podmanBaseMounts (internal/cli/run/assemble_parts.go) plus the per-agent overlay
// dirs (agents.AllOverlayDirs, emitted for SELECTED agents only).
//
// `.ssh` is deliberately absent: it is a rw bind, but composing a file into the
// jail's ssh dir from config is not a use case worth blessing implicitly.
var hostFileWritableRoots = func() map[string]struct{} {
	roots := map[string]struct{}{
		".config": {}, ".cache": {}, ".local": {}, "go": {}, ".npm-global": {},
	}
	for _, d := range agents.AllOverlayDirs {
		roots[firstHomeSegment(d)] = struct{}{}
	}
	return roots
}()

// StagingFor reports what the host CLI must provision for this entry's
// destination. It keys on the destination's FIRST SEGMENT, because that is what
// the mount table keys on: a nested rw bind covers its whole subtree, so
// `.config/mytool/config.json` is writable for the same reason `.config/x` is.
//
// A directory entry never needs symlink staging (its tree is copied wholesale
// into the destination, and copyTree does its own MkdirAll), but it does still
// need a writable parent, so the dir case falls through the same way.
func (e HostFileEntry) StagingFor() HostFileStaging {
	seg, rest := firstHomeSegment(e.Path), strings.Contains(e.Path, "/")
	if _, ok := hostFileWritableRoots[seg]; ok {
		return HostFileStagingNone
	}
	if !rest {
		// No slash at all: the destination IS a home-root leaf. A file needs the
		// symlink hatch; a bare directory entry needs a writable subtree.
		if e.IsDir {
			return HostFileStagingWritableDir
		}
		return HostFileStagingSymlink
	}
	return HostFileStagingWritableDir
}

// SymlinkTarget is the home-relative path a HostFileStagingSymlink entry's
// GlobalHome symlink points at: a private subtree of the writable `~/.config`
// overlay, keyed by the entry's injective slug so two entries can never collide.
//
// Kept under `.config/yolo-home/` rather than the overlay root so a composed
// home-root file is visibly yolo-provisioned and can never collide with a real
// `~/.config` entry a tool expects to own.
func (e HostFileEntry) SymlinkTarget() string {
	return path.Join(".config", "yolo-home", e.Slug())
}

// WritableParent is the home-relative directory a HostFileStagingWritableDir
// entry needs staged read-write: the destination's parent for a file, or the
// destination itself for a directory entry (whose tree is copied INTO it).
func (e HostFileEntry) WritableParent() string {
	if e.IsDir {
		return e.Path
	}
	if dir := path.Dir(e.Path); dir != "." {
		return dir
	}
	return e.Path
}
