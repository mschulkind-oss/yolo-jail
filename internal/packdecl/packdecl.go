// Package packdecl is the pack MANIFEST: what a pack declares about itself.
//
// This is the schema that replaces agents.AgentSpec. The core deliberately does not
// know what an "agent" is — a pack declares PATHS and CONTENT, and core mounts and
// stages them. A pack that installs a coding agent is just a pack whose declarations
// happen to describe one.
//
// That framing is the whole point, so it is worth stating what it buys: every
// per-agent loop in the mount assembler only ever needed paths (mount this staged
// tree there, make that dir writable, one host file may cross). None of them needed
// the concept. Keeping "agent" out of the core means adding a seventh tool is a pack
// file, not a Go change.
//
// It lives in its own package, dependency-free on the rest of the repo, because both
// the host CLI (mount assembly, `yolo pack lint`) and the in-jail entrypoint (surface
// rendering) read it, and neither should have to import the other's world.
package packdecl

import (
	"bytes"
	"encoding/json"
	"strings"
)

// ManifestName is the file a pack declares itself in, at its root.
const ManifestName = "pack.json"

// Manifest is a pack's self-declaration.
//
// Every field is OPTIONAL. A pack with only skills/ and an AGENTS.md needs no manifest
// at all — that is the common case and it must stay zero-ceremony. A manifest is how a
// pack asks for something more: a writable dir, a mount target, a host file.
type Manifest struct {
	// Name is the pack's own name for itself. The `packs` config entry may override
	// it; this is the default and what `yolo pack ls` shows.
	Name string `json:"name,omitempty"`

	// Description is one line, shown by `yolo pack ls`.
	Description string `json:"description,omitempty"`

	// Contributes is the pack's effects: one list of typed contributions, each with
	// an explicit `kind` from the closed set (see contributes.go / kinds.go). It
	// each with an explicit kind from the closed core-owned set
	// (docs/design/pack-system.md §2-§3). Read it through Contributions().
	Contributes []Contribution `json:"contributes,omitempty"`
}

// Hook is one requested imperative capability. Its extra fields are the parameters that
// hook needs; an unused one for a given hook name is an error rather than ignored, so a
// misplaced field is not a declaration that silently does nothing.
type Hook struct {
	// Name is the hook, from core's closed set (see internal/entrypoint/packhooks.go).
	Name string `json:"name"`
	// File is a home-relative file the hook acts on.
	File string `json:"file,omitempty"`
	// SharedDir is a home-relative directory from the pack's own sharedDirs, for a hook
	// that links into the machine-global tier.
	SharedDir string `json:"sharedDir,omitempty"`
}

// KnownHooks is the closed set of hook names, so a manifest can be validated on the HOST
// (where `yolo check` runs) without importing the entrypoint's implementation.
//
// Duplicating the names is the lesser evil versus a package dependency from the host CLI
// into the entrypoint; HookSetsAgree pins them together so the duplicate cannot drift.
var KnownHooks = []string{"shared_credentials", "per_jail_history", "claude_plugins"}

// Install declares a program the pack wants present in the jail.
type Install struct {
	// Kind is "npm" or "native".
	Kind string `json:"kind"`
	// Bin is the binary name on PATH, and the lazy-launcher filename.
	Bin string `json:"bin"`
	// Package is the npm package (kind == "npm").
	Package string `json:"package,omitempty"`
	// Flags are extra npm install flags.
	Flags []string `json:"flags,omitempty"`
	// InstallerURL is a curl-piped installer (kind == "native").
	//
	// This is the sharpest thing a manifest can name: a URL whose contents run as a
	// shell script. Honored only under the same origin rule as HostFiles — a fetched
	// pack cannot introduce one, because that would let a git ref execute arbitrary
	// code in the jail.
	InstallerURL string `json:"installerUrl,omitempty"`
}

// Mount stages one of the pack's own files or directories and mounts it read-only.
type Mount struct {
	// From is the pack-relative source path.
	From string `json:"from"`
	// To is the home-relative jail destination.
	To string `json:"to"`
	// HostOverlay is an optional host-home path whose content is PREPENDED to the
	// staged file (the "your own AGENTS.md first, then the pack's" case).
	//
	// Part of the credential boundary: it reads the host home, so it obeys the same
	// origin rule as HostFiles.
	HostOverlay string `json:"hostOverlay,omitempty"`
}

// HostFile is one host-home file to mount read-only into the jail.
type HostFile struct {
	// From is the host-home-relative source (e.g. ".claude/settings.json").
	From string `json:"from"`
	// To is the jail destination under /ctx. Empty means /ctx/host-<pack>/<basename>,
	// which is what the built-in agents used.
	To string `json:"to,omitempty"`
}

// Decode parses a manifest, reporting EVERY problem rather than the first so a pack
// author fixing one gets the whole list instead of one edit-check cycle per mistake.
//
// Strict about unknown fields: a misspelled key would otherwise be a declaration that
// silently does nothing, and the author would have no signal at all.
//
// Use DecodeTolerant instead when reading a manifest that some OTHER build wrote — see its
// doc for why the strictness has to stop at the version boundary.
func Decode(data []byte) (*Manifest, []string) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, []string{ManifestName + ": " + err.Error()}
	}
	return &m, m.Validate()
}

// DecodeTolerant parses a manifest, IGNORING fields this build does not know.
//
// The strictness in Decode is right for authoring — a misspelled key that silently does
// nothing is the worst outcome for a pack author — but it is wrong across a VERSION
// BOUNDARY, and the jail is exactly that. The host CLI and the in-jail `yolo-entrypoint`
// come from different places (the CLI is `go install`ed or freshly built; the entrypoint is
// baked into the image at the last `just load`), so a newer CLI staging a pack that uses a
// newer manifest field is a NORMAL state, not a corruption.
//
// Verified the hard way: adding the `tier` field to `skills` made every jail refuse to start
// against an older baked image —
//
//	yolo-entrypoint: refusing to start the jail: 2 config generator(s) failed:
//	  - load_packs: pack claude: pack.json: json: unknown field "tier"
//
// with no route to recovery except rebuilding the image, since the failing manifest is one
// yolo SHIPS. A field the entrypoint cannot use is a feature it cannot render, which is a
// degraded jail; a field it refuses to read is no jail at all. The first is recoverable and
// the second is not, so the version boundary reads tolerantly.
//
// Structural validation still runs, so a manifest that is malformed in a way BOTH builds
// understand (an unknown kind, a missing required field) still fails loudly here.
func DecodeTolerant(data []byte) (*Manifest, []string) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, []string{ManifestName + ": " + err.Error()}
	}
	return &m, m.Validate()
}

// Validate reports every structural problem — per-kind over contributes[].
func (m *Manifest) Validate() []string {
	return m.validateContributions()
}

// appendPathProblems rejects a path that escapes the tree it is relative to.
//
// Absolute paths and `..` are both refused: every path in a manifest is relative to
// either the pack root, the jail home, or the host home, and a pack must not be able
// to reach outside whichever one it was given. A fetched pack could otherwise name
// "../../etc/shadow" and have core mount it.
func appendPathProblems(problems []string, field, p string) []string {
	if p == "" {
		return problems
	}
	if strings.HasPrefix(p, "/") {
		return append(problems, field+": must be relative, not absolute ("+p+")")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return append(problems, field+": must not contain \"..\" ("+p+")")
		}
	}
	if strings.Contains(p, ":") {
		// A colon would be parsed as a mount-option separator by the container
		// runtime, silently turning part of the path into a flag.
		return append(problems, field+": must not contain \":\" ("+p+")")
	}
	return problems
}

// NeedsHostAccess reports whether honoring this manifest requires reading the host
// home or running a fetched installer — the declarations gated on pack ORIGIN.
//
// Collected in one predicate so a caller cannot check two of the three and believe it
// covered the boundary. That mistake already happened once, when "the credential
// boundary" was treated as AgentSpec.HostFiles alone while Briefing.HostSource and
// Skills read the host home too.
func (m *Manifest) NeedsHostAccess() []string {
	// Routes through the contributions (NeedsHostAccessContributions): the origin
	// gate is "any reads-host, program-via-installer, or host-prepending briefing"
	// (docs/design/pack-system.md §9).
	return m.NeedsHostAccessContributions()
}

func knownHook(name string) bool {
	for _, k := range KnownHooks {
		if k == name {
			return true
		}
	}
	return false
}
