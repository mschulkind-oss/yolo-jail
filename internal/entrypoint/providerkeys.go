package entrypoint

// providerkeys.go is the jail boot's half of the PROVIDER KEYS (packdecl.ProviderKey): the
// settings in an agent's own config that its provider selection decides, so that no captured
// in-jail edit can put the agent on a provider yolo did not select (agentcfg/providerkeys.go
// states the fault, measured 2026-10-11: a captured CLAUDE_CODE_USE_BEDROCK kept Claude Code on
// Bedrock while yolo had selected its subscription and delivered no AWS credentials).
//
// These live here, all at the JAIL notch alone — the host notch writes the user's real file,
// where PP-D1 rules that yolo deletes nothing it did not write:
//
//   - providerKeyLeaves resolves the surface's declared keys against this render's selection
//     for the stateful render, which drops a captured value at each, and for the host layer the
//     jail imports, whose values at them it composes without (PP-D13, ruled 2026-10-11:
//     "Inside of JAIL, if we're not already managing the host config, I would say a profile
//     choice does override the host settings file being imported");
//   - noteProviderKeyDrops names every value it dropped, one line each, with the key and the
//     command that discards the rest of the captured edits (every stop names the next step),
//     and noteHostProviderKeyDrops each one the host layer lost, with the host file;
//   - notePlatformSwitchesOn is the PP-D1 conflict check run over the file THIS BOOT WROTE,
//     whatever layer the switch came from, so a switch the launcher's check cannot see — it
//     reads only the user's host copy — is still named before the agent starts.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/capturelog"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonptr"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// selectedPlatformOf is the `platform` of provider's row in the live providers table — the
// value a derive reads as ctx.selected_platform — or "" for no provider, no row, or no platform.
func selectedPlatformOf(tables map[string]map[string]any, provider string) string {
	if provider == "" {
		return ""
	}
	row, ok := tables[manifest.SourceProviders][provider].(map[string]any)
	if !ok {
		return ""
	}
	s, _ := row["platform"].(string)
	return s
}

// providerKeyLeaves is the provider keys p declares on surface s, resolved for a render whose
// selected provider is of platform: every unconditional key, and each value-conditional key
// (`matches`) whose platform is not the selected one. A key whose pattern does not compile is
// skipped — packdecl refuses it at decode, so only a pack read past validation reaches here.
func providerKeyLeaves(p *packload.Pack, s manifest.Surface, platform string) []agentcfg.OwnedLeaf {
	if p == nil || p.Decl == nil {
		return nil
	}
	var out []agentcfg.OwnedLeaf
	for _, k := range p.Decl.ProviderKeysFor(s.Agent + "/" + s.Name) {
		leaf := agentcfg.OwnedLeaf{Pointer: k.Pointer}
		if k.Matches != "" {
			if k.Platform == platform {
				continue // the platform that serves the value is the one selected
			}
			re, err := regexp.Compile(k.Matches)
			if err != nil {
				continue
			}
			leaf.Matches = re
		}
		out = append(out, leaf)
	}
	return out
}

// withProviderKeys is c carrying the surface's resolved provider keys, as a new value (a nil c
// is the common case).
func (c *surfaceContribs) withProviderKeys(keys []agentcfg.OwnedLeaf) *surfaceContribs {
	if len(keys) == 0 {
		return c
	}
	out := &surfaceContribs{providerKeys: keys}
	if c != nil {
		cp := *c
		cp.providerKeys = keys
		out = &cp
	}
	return out
}

// providerKeyLeaves is the provider-key half, nil for none.
func (c *surfaceContribs) providerKeyLeaves() []agentcfg.OwnedLeaf {
	if c == nil {
		return nil
	}
	return c.providerKeys
}

// noteProviderKeyDrops prints one warning per captured value the render dropped at a provider
// key. A WARNING, like noteRepairedValues: yolo edited the user's captured state without asking,
// and the line is that edit's whole disclosure. The value is printed only when it is not
// credential-shaped (capturelog.go's rule), so a captured token never reaches the boot log.
func noteProviderKeyDrops(e *Env, surface manifest.Surface, drops []agentcfg.OwnedDrop) {
	for _, d := range drops {
		e.warn(fmt.Sprintf("%s: dropped the captured in-jail edit %s = %s from %s: %s's provider "+
			"selection decides that key, so no captured edit may set it (it is absent unless the "+
			"selected provider sets it). `yolo config diff %s` shows the captured edits that remain; "+
			"`yolo config reset %s` discards them all.", surface.Agent+"/"+surface.Name,
			pointerLabel(d.Pointer), logValue(pointerLabel(d.Pointer), d.Value), surface.Path,
			surface.Agent, surface.Agent, surface.Agent))
	}
}

// noteHostProviderKeyDrops prints one warning per provider key this jail's render took out of
// the user's own host copy of the surface (PP-D13): the key, the value when it is not
// credential-shaped, that the host file is unchanged, and how to get the key here — a profile.
// A key the selection itself sets to the same value (computed) changes nothing the agent reads,
// and is not named.
func noteHostProviderKeyDrops(e *Env, surface manifest.Surface, drops []agentcfg.OwnedDrop,
	computed map[string]any) {
	for _, d := range drops {
		if v, ok := lookupPointer(computed, d.Pointer); ok && fmt.Sprint(v) == fmt.Sprint(d.Value) {
			continue
		}
		e.warn(fmt.Sprintf("%s: your host %s sets %s = %s, and in a jail %s's provider selection "+
			"decides that key, so this jail's copy leaves it out (it is absent unless the selected "+
			"provider sets it). Your host file is unchanged; to use it here, choose a provider that "+
			"sets it with `yolo active-provider %s <provider>` on the host.", surface.Agent+"/"+surface.Name,
			surface.Path, pointerLabel(d.Pointer), logValue(pointerLabel(d.Pointer), d.Value),
			surface.Agent, surface.Agent))
	}
}

// pointerLabel spells an RFC 6901 pointer the way the rest of the boot log spells a key,
// dotted: "/env/CLAUDE_CODE_USE_BEDROCK" reads "env.CLAUDE_CODE_USE_BEDROCK".
func pointerLabel(p string) string {
	steps, err := jsonptr.Parse(p)
	if err != nil || len(steps) == 0 {
		return p
	}
	return strings.Join(steps, ".")
}

// lookupPointer is the value at pointer in m, and whether one is there.
func lookupPointer(m map[string]any, pointer string) (any, bool) {
	steps, err := jsonptr.Parse(pointer)
	if err != nil || len(steps) == 0 {
		return nil, false
	}
	var v any = m
	for _, step := range steps {
		obj, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = obj[step]; !ok {
			return nil, false
		}
	}
	return v, true
}

// notePlatformSwitchesOn is PP-D1's conflict line at the jail notch, over the file this boot
// RENDERED: for each platform switch p's programs declare on s, a switch that is on while the
// agent's selected provider is not of its platform is named, with the layer it came from and the
// fix. The launcher's own check (packload.PlatformSwitchConflicts) reads the user's host copy
// alone, so a switch any other layer wrote — a pack's config-overlay, a capture an older yolo
// kept, a file the agent rewrote — reached the agent unnamed; this one reads what the agent will
// read. rendered is the composed file; hostBytes the host layer this render folded (nil: none).
func notePlatformSwitchesOn(e *Env, p *packload.Pack, s manifest.Surface, platform string,
	rendered map[string]any, hostBytes []byte, contribs *surfaceContribs) {
	if p == nil || p.Decl == nil || rendered == nil {
		return
	}
	for _, bin := range p.InstallBins() {
		if bin != s.Agent {
			continue
		}
		for _, sw := range p.Decl.PlatformSwitches(bin) {
			if sw.Surface != s.Agent+"/"+s.Name || platform == sw.Platform {
				continue
			}
			v, ok := lookupPointer(rendered, sw.Pointer)
			if !ok || !packload.SwitchOn(v) {
				continue
			}
			e.warn(fmt.Sprintf("%s: %s, as this boot rendered it, sets %s (%s), which puts %s on its "+
				"own %s client, but no %s provider is selected for %s, so this launch delivered it "+
				"none of that platform's credentials and its requests will fail: select a provider "+
				"of that platform for %s, or %s.", bin, s.Path, sw.Key(),
				switchSource(s, sw.Pointer, hostBytes, contribs), bin, strconv.Quote(sw.Platform),
				strconv.Quote(sw.Platform), bin, bin, switchRemedy(s, sw.Pointer, hostBytes, contribs)))
		}
	}
}

// switchSource names the layer a rendered switch came from, as far as the render's inputs say.
func switchSource(s manifest.Surface, pointer string, hostBytes []byte, contribs *surfaceContribs) string {
	if pack := overlaySetting(pointer, contribs); pack != "" {
		return "from pack " + strconv.Quote(pack) + "'s config-overlay"
	}
	if _, ok := lookupPointer(agentcfg.DecodeSurfaceObject(s.Codec, hostBytes), pointer); ok {
		return "from your own host copy of " + s.Path + ", which yolo leaves alone"
	}
	return "from a layer `yolo config diff " + s.Agent + "` names"
}

// switchRemedy is the second fix the line offers, worded for the layer the switch came from.
func switchRemedy(s manifest.Surface, pointer string, hostBytes []byte, contribs *surfaceContribs) string {
	if pack := overlaySetting(pointer, contribs); pack != "" {
		return "drop pack " + strconv.Quote(pack) + " (or its profile gate) from this launch"
	}
	if _, ok := lookupPointer(agentcfg.DecodeSurfaceObject(s.Codec, hostBytes), pointer); ok {
		return "remove " + pointerLabel(pointer) + " from your host " + s.Path
	}
	return "run `yolo config reset " + s.Agent + "` and restart the jail"
}

// overlaySetting is the first contributing pack whose config-overlay holds a value at pointer,
// at its top level or under its `managed` block, or "" when none does.
func overlaySetting(pointer string, contribs *surfaceContribs) string {
	for _, o := range contribs.overlayLayers() {
		data, ok := o.Data.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := lookupPointer(data, pointer); ok {
			return o.Pack
		}
		if managed, ok := data["managed"].(map[string]any); ok {
			if _, ok := lookupPointer(managed, pointer); ok {
				return o.Pack
			}
		}
	}
	return ""
}

// logValue is how a boot line prints a captured value: the capture log's rule, so a line and the
// log never disagree about what is safe to print.
func logValue(key string, v any) string { return capturelog.Value(key, v) }

// logOverlayChange appends to the capture log (capturelog) every change this render made to the
// surface's capture overlay: what the boot's capture took in, and what it dropped and why. Best
// effort, like the provenance record: the log is a record for the next investigation, and a boot
// that could not append to it has still rendered correctly.
func logOverlayChange(e *Env, target render.Target, r *statefulRender) {
	if r == nil || r.out == nil {
		return
	}
	surface := r.surface.Agent + "/" + r.surface.Name
	writer := "boot"
	if e.hostTarget {
		writer = "host apply"
	}
	if r.out.FirstMigration {
		writer += " (adopting the file: no render baseline to diff against)"
	}
	now := time.Now()
	why := "dropped: " + r.surface.Agent + "'s provider selection decides this key"
	reasons := capturelog.Reason{}
	var taken []string
	for _, d := range r.out.ProviderKeyDrops {
		key := pointerLabel(d.Pointer)
		reasons[key] = why
		if !capturelog.Held(r.overlayBefore, key) {
			// Captured by this very render and refused by it, so neither sidecar shows it.
			taken = append(taken, capturelog.Dropped(now, surface, writer, key, d.Value,
				"captured from the file and "+why))
		}
	}
	lines := capturelog.Lines(now, surface, writer, r.overlayBefore, r.out.OverlayJSON, reasons,
		"dropped: a higher layer sets it now, the layers render the same value, or it was repaired")
	lines = append(lines, taken...)
	_ = capturelog.AppendFile(prismSidecarDir(e), lines, target.SidecarFileMode())
}
