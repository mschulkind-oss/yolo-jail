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
	"fmt"
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

	// Install describes a program the pack wants available in the jail. It is a
	// DECLARATION, not a command: core decides how (and whether) to honor it.
	Install *Install `json:"install,omitempty"`

	// Mounts are the pack's own files, staged and mounted read-only into the jail.
	// This is how a pack delivers skills, briefing prose, or any other content that
	// must appear at a specific path.
	Mounts []Mount `json:"mounts,omitempty"`

	// WritableDirs are home-relative directories the pack needs to WRITE at runtime
	// (a tool's own config/state dir). Backed per-workspace, so two workspaces get
	// independent state — the default, and right for almost everything.
	WritableDirs []string `json:"writableDirs,omitempty"`

	// SharedDirs are home-relative directories shared across EVERY jail on the
	// machine, backed by GlobalHome instead of the per-workspace overlay.
	//
	// Reserved for IDENTITY/CREDENTIAL state, where re-authenticating in every
	// workspace would be wrong behavior rather than an inconvenience. Anything here
	// leaks between workspaces BY DESIGN, so declaring one is a real decision.
	SharedDirs []string `json:"sharedDirs,omitempty"`

	// HostFiles are host-home files the pack wants mounted read-only into the jail.
	//
	// THE CREDENTIAL BOUNDARY. A declaration here is only honored for a pack whose
	// content origin permits it (embedded or local — never fetched; see
	// config.PackEntry.MayGrantHostFiles). A fetched pack asking for one is refused,
	// because installing a third-party pack approves distributing content, not handing
	// that repository your host config.
	HostFiles []HostFile `json:"hostFiles,omitempty"`

	// Surfaces are composed config files, in the agentcfg surface schema. Decoded by
	// internal/agentcfg/manifest, not here — this package only carries the raw JSON so
	// packdecl stays free of an engine dependency.
	Surfaces json.RawMessage `json:"surfaces,omitempty"`

	// LaunchFlags are flags injected after the binary when the user runs it, keyed by
	// the binary name. This is how a tool's "don't prompt me" mode gets applied
	// without the user typing it every time.
	LaunchFlags map[string][]string `json:"launchFlags,omitempty"`

	// FlagAliases marks flags that mean the same thing, so an injected flag is skipped
	// when the user already passed an equivalent (e.g. -y for --yolo).
	FlagAliases map[string][]string `json:"flagAliases,omitempty"`

	// RetireMiseTools are mise tool tokens to strip from a workspace mise.toml,
	// for a tool that used to be installed that way and no longer is.
	RetireMiseTools []string `json:"retireMiseTools,omitempty"`
}

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
func Decode(data []byte) (*Manifest, []string) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, []string{ManifestName + ": " + err.Error()}
	}
	return &m, m.Validate()
}

// Validate reports every structural problem.
func (m *Manifest) Validate() []string {
	var problems []string

	if m.Install != nil {
		switch m.Install.Kind {
		case "npm":
			if m.Install.Package == "" {
				problems = append(problems, "install: kind \"npm\" needs a \"package\"")
			}
		case "native":
			if m.Install.InstallerURL == "" {
				problems = append(problems, "install: kind \"native\" needs an \"installerUrl\"")
			}
		case "":
			problems = append(problems, "install: missing \"kind\" (expected npm or native)")
		default:
			problems = append(problems, fmt.Sprintf(
				"install: unknown kind %q (expected npm or native)", m.Install.Kind))
		}
		if m.Install.Bin == "" {
			problems = append(problems, "install: missing \"bin\"")
		}
	}

	for i, mt := range m.Mounts {
		if mt.From == "" {
			problems = append(problems, fmt.Sprintf("mounts[%d]: missing \"from\"", i))
		}
		if mt.To == "" {
			problems = append(problems, fmt.Sprintf("mounts[%d]: missing \"to\"", i))
		}
		problems = appendPathProblems(problems, fmt.Sprintf("mounts[%d].from", i), mt.From)
		problems = appendPathProblems(problems, fmt.Sprintf("mounts[%d].to", i), mt.To)
		if mt.HostOverlay != "" {
			problems = appendPathProblems(problems,
				fmt.Sprintf("mounts[%d].hostOverlay", i), mt.HostOverlay)
		}
	}
	for i, hf := range m.HostFiles {
		if hf.From == "" {
			problems = append(problems, fmt.Sprintf("hostFiles[%d]: missing \"from\"", i))
		}
		problems = appendPathProblems(problems, fmt.Sprintf("hostFiles[%d].from", i), hf.From)
	}
	for i, d := range m.WritableDirs {
		problems = appendPathProblems(problems, fmt.Sprintf("writableDirs[%d]", i), d)
	}
	for i, d := range m.SharedDirs {
		problems = appendPathProblems(problems, fmt.Sprintf("sharedDirs[%d]", i), d)
	}
	return problems
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
	var reasons []string
	if len(m.HostFiles) > 0 {
		reasons = append(reasons, "hostFiles (reads the host home)")
	}
	for _, mt := range m.Mounts {
		if mt.HostOverlay != "" {
			reasons = append(reasons, "mounts[].hostOverlay (reads the host home)")
			break
		}
	}
	if m.Install != nil && m.Install.InstallerURL != "" {
		reasons = append(reasons, "install.installerUrl (runs a fetched script)")
	}
	return reasons
}
