package render

// surface.go is THE renderer — the one implementation of "compose a surface at a
// target", and the only place in the tree that calls agentcfg.Compose /
// agentcfg.ComposeStateful (host-render-target.md §8 step 3, the load-bearing step).
//
// Before it there were two: internal/entrypoint's boot render and internal/cli's
// `yolo config` verbs, each with its own ~ expansion, its own ${workspace} handling and
// its own spelling of the file text a render produces. The three duplications were
// ADMITTED in the code — "mirrors internal/cli.expandHome but keyed on the Env",
// "entrypoint.surfaceText's rule, restated here because that one is unexported" — and
// every §6.1 data-loss probe was a drift between the pair, not an isolated bug. So the
// fix is not a third mirror with better comments; it is one renderer that takes the
// target as a parameter, which is what the rest of this package already describes.
//
// WHAT STAYS WITH THE CALLER, unchanged from the package header's list: resolving the
// host layer's bytes (a /ctx mount in a jail, the mirror file at a host notch), building
// the computed layer, deciding the selection, and writing the result. Those need the wide
// environment; composing does not. This file takes what they produce and returns what
// they write, and it touches no file of its own.

import (
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// Layers are the composition layers a surface declaration cannot carry, because each is
// read or derived from the live environment rather than declared: the user's own copy of
// the file, other packs' config-overlay contributions, and yolo's per-boot dynamic table.
//
// A struct rather than three parameters so a caller that has none passes the zero value
// and a caller that grows a fourth layer does not re-sign every call site. It deliberately
// does NOT carry agentcfg.Inputs' Overlay and LiteralNulls fields: both are ComposeStateful's
// to fill from the on-disk file (see agentcfg.Inputs.LiteralNulls), and a caller that could
// set them would be able to forge a capture.
type Layers struct {
	// HostBytes is the raw content of the user's own version of this file, or nil when
	// the surface declares no host layer or the target delivered none. RESOLVED BY THE
	// CALLER: in a jail it is a :ro /ctx mount whose path packload derives, host-side it
	// is the mirror file itself — both jail-environment questions this package does not
	// answer (see the package header).
	HostBytes []byte

	// Overlays are other packs' config-overlay contributions onto this surface, in
	// ascending precedence. Empty = none.
	Overlays []agentcfg.Overlay

	// Computed is yolo's per-boot dynamic layer (the MCP table, the LSP toggles), already
	// decoded. nil = none, which is every preview: the computed builders bake
	// target-absolute paths, so a caller without the environment that produced them
	// supplies nothing rather than something wrong.
	Computed any

	// ComputedInFull names the top-level keys of Computed whose table the derive declared
	// it regenerates in full (agentcfg.Inputs.ComputedInFull, the ctx.in_full sentinel).
	// It travels beside Computed rather than inside it, so no reader of the layer has a
	// marker to strip. nil = none: every computed table claims only the leaves it names.
	ComputedInFull []string

	// Lists are other packs' config-list contributions onto this surface
	// (agentcfg.ListContribution): entries appended to one array each, after every overlay
	// and below the capture. Empty = none. A capture that passes no layers passes none, and
	// learns the surface's list paths from the list-capture sidecar instead
	// (State.ListCaptureJSON).
	Lists []agentcfg.ListContribution
}

// State is the §5 capture state one stateful render reads: the file as it stands and the
// two sidecars that let ComposeStateful tell a user's edit from yolo's own last output.
// The caller resolves all four — Target.LastRenderPath and Target.OverlayPath say where the
// sidecars live, but reading them is the caller's, because absence, corruption and an
// unreadable file mean different things at different notches.
type State struct {
	// CurrentBytes is the file's current content, or nil when it is absent.
	CurrentBytes []byte
	// LastRenderPresent reports whether the last_render sidecar exists. Its absence is
	// the first-migration signal (agentcfg §3.2) — the one that ADOPTS rather than
	// discards — so it is a field of its own rather than len(LastRenderBytes) > 0.
	LastRenderPresent bool
	// LastRenderBytes is that sidecar's content, ignored when LastRenderPresent is false.
	LastRenderBytes []byte
	// OverlayJSON is the capture overlay sidecar's content, or nil when absent.
	OverlayJSON []byte
	// ListCaptureJSON is the list-capture sidecar's content (Target.ListCapturePath), or nil
	// when absent — the per-entry capture at the surface's list paths.
	ListCaptureJSON []byte
	// InsertRecordJSON is the config-list insert record's content (Target.ListRecordPath), or
	// nil when absent: which entries yolo itself put at each list path. Read only by a first
	// migration's adoption, so an entry yolo inserted under the host's other contract is never
	// adopted as the user's (agentcfg.StatefulInputs.InsertRecordJSON).
	InsertRecordJSON []byte
}

// Prepare resolves the ${workspace} placeholder in a surface's declared layers against
// THIS target's workspace, and is the one place that decision is made.
//
// A target with no workspace referent — the host notch, which has none by definition
// (see Host) — substitutes NOTHING. That is not a shortcut around the placeholder; it is
// the division of labour the host render already has: the host entry prunes every
// ${workspace}-KEYED branch out of the surface first (entrypoint.PruneWorkspaceKeyed) and
// names what it dropped, because a key the user's agent will never look at should not be
// written at all, let alone written under some arbitrary directory.
//
// SO THE GATE IS BEHAVIOR-PRESERVING, not a change smuggled into a refactor, and the
// reason is worth stating because it is not obvious: the boot render used to substitute
// unconditionally, including at the host notch, where Env.WorkspaceDir() handed it the
// container default "/workspace". That was already a no-op there —
// agentcfg.SubstituteWorkspace rewrites only keys that EQUAL the placeholder, and the
// prune has removed every key that so much as CONTAINS it before this is reached. What
// the gate removes is the appearance of a host render resolving a placeholder against a
// path no host has.
func (t Target) Prepare(s manifest.Surface) manifest.Surface {
	if t.Workspace == "" {
		return s
	}
	return agentcfg.SubstituteWorkspace(s, t.Workspace)
}

// Compose is the stateless render: prepare the surface for this target, fold the layers,
// return both. It writes nothing — the caller decides where the bytes go, and whether a
// failure is fatal (the package header's third exclusion).
//
// It returns the PREPARED surface alongside the result because every caller needs it
// afterwards: the path to write is Target.SurfacePath of it, and the file text is
// FileText of it. Returning it is what stops each caller keeping its own substituted
// copy, which is how the two paths came to substitute against different workspaces.
func (t Target) Compose(s manifest.Surface, l Layers) (manifest.Surface, *agentcfg.Result, error) {
	s = t.Prepare(s)
	res, err := agentcfg.Compose(agentcfg.Inputs{
		Surface:   s,
		HostBytes: l.HostBytes,
		Overlays:  l.Overlays,
		Computed:  l.Computed,
		Lists:     l.Lists,

		ComputedInFull: l.ComputedInFull,
	})
	if err != nil {
		return s, nil, err
	}
	return s, res, nil
}

// ComposeStateful is the stateful render: the same fold, plus the §5 capture that carries
// a user's in-jail edits across regeneration. Same contract as Compose — prepare, compose,
// return; write nothing.
//
// The two are separate entries rather than one with a flag because they answer different
// questions and their callers are different code: a preview and a reset want the layers
// alone, while a boot render and a capture want the layers reconciled against what is on
// disk. agentcfg keeps the same split for the same reason.
func (t Target) ComposeStateful(s manifest.Surface, l Layers, st State) (manifest.Surface, *agentcfg.StatefulOutput, error) {
	s = t.Prepare(s)
	out, err := agentcfg.ComposeStateful(agentcfg.StatefulInputs{
		Base: agentcfg.Inputs{
			Surface:   s,
			HostBytes: l.HostBytes,
			Overlays:  l.Overlays,
			Computed:  l.Computed,
			Lists:     l.Lists,

			ComputedInFull: l.ComputedInFull,
		},
		CurrentBytes:      st.CurrentBytes,
		LastRenderPresent: st.LastRenderPresent,
		LastRenderBytes:   st.LastRenderBytes,
		OverlayJSON:       st.OverlayJSON,
		ListCaptureJSON:   st.ListCaptureJSON,
		InsertRecordJSON:  st.InsertRecordJSON,
	})
	if err != nil {
		return s, nil, err
	}
	return s, out, nil
}

// ExpandHome resolves a "~"-relative surface path against THIS target's home — the seam
// that lets one renderer write into a jail home, a real home or a preview dir without
// consulting the process environment for any of them.
//
// It was two functions: internal/cli.expandHome, keyed on the process $HOME, and
// entrypoint.targetExpandHome, keyed on an Env. The first is why a host-side verb could
// silently act on the wrong home (§6.1); having one keyed on the Target is the fix, since
// a Target can only be built by a constructor that was told which home it means.
func (t Target) ExpandHome(p string) string {
	if p == "~" {
		return t.Home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(t.Home, p[2:])
	}
	return p
}

// SurfacePath is where this surface's file lands at this target: its declared path with
// "~" resolved. One expression, so a reader and a writer cannot disagree about which file
// a render is about.
func (t Target) SurfacePath(s manifest.Surface) string { return t.ExpandHome(s.Path) }

// SurfaceText renders a surface's encoded bytes as the exact file body to write, and is
// the same expression the §5 last_render baseline holds — which is what makes "the
// baseline matches the bytes yolo wrote" a fact rather than two hand-copied
// `if Kind() == KindObject` blocks agreeing by luck.
//
// Object codecs (json/toml) emit no trailing newline, so one is appended to make a
// well-formed text file. Keyless codecs must NOT get one: raw promises a byte-exact
// Decode→Encode round-trip (a stray "\n" corrupts it), and lines already terminates every
// line with "\n" (a second one decodes back as a spurious trailing empty element, which
// would also poison the baseline).
func SurfaceText(s manifest.Surface, encoded []byte) string {
	if s.Kind() == codec.KindObject {
		return string(encoded) + "\n"
	}
	return string(encoded)
}

// GeneratedHeader is the "yolo generated this" banner prepended to a composed surface
// FILE (A10). It exists so an agent that opens the file sees, in the file itself, that
// hand-editing it is the wrong move and where to look instead — steering, not enforcement
// (an agent is a directed writer, so a legible signal beats a permission bit it can work
// around).
//
// TOML ONLY, and the constraint is real, not conservatism:
//   - json has NO comment syntax, so a banner would make the file invalid;
//   - raw promises a byte-exact Decode→Encode round-trip and lines round-trips
//     element-for-element, so ANY inserted text corrupts the contract.
//
// It belongs to the SURFACE write alone, never to the last_render sidecar: that sidecar is
// the §5 capture baseline and must match what the ENGINE produced, or the next render's
// mergeDiff sees the banner itself as a user edit. That is why it is separate from
// SurfaceText rather than folded into it, and why FileText — the pair — is the expression
// a file write uses.
func GeneratedHeader(s manifest.Surface) string {
	if s.Codec != "toml" {
		return ""
	}
	return "# Generated by yolo-jail — composed at jail start; hand edits may be\n" +
		"# reverted or lost. Run `yolo config ls` to see how, and change the config\n" +
		"# input instead (`yolo config-ref`).\n"
}

// FileText is the complete text a render writes to a surface file: the banner plus the
// body. Named so a caller reads its intent at the write site, and so the one place that
// has to omit the banner (the baseline) is the one that calls SurfaceText directly.
func FileText(s manifest.Surface, encoded []byte) string {
	return GeneratedHeader(s) + SurfaceText(s, encoded)
}
